//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

var errWorkExecutorNoCompletionEvidence = errors.New("work executor returned no completion evidence")

func (r *Runtime) persistWorkResult(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error) session.OperationArtifact {
	if r == nil || r.store == nil {
		return session.OperationArtifact{}
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return session.OperationArtifact{}
	}
	opState = session.NormalizeOperationState(opState)
	if strings.TrimSpace(opState.ID) == "" {
		opState.ID = strings.TrimSpace(req.OperationID)
	}
	opState.Work.Executor = firstRuntimeWorkNonEmpty(result.ExecutorName, status.Active)
	opState.Work.ConfiguredExecutor = status.Configured
	opState.Work.PreferredExecutor = status.Preferred
	opState.Work.FallbackReason = status.FallbackReason
	opState.Work.LastOperationID = strings.TrimSpace(req.OperationID)
	opState.Work.LastActionProposalID = strings.TrimSpace(req.State.ActionProposal.ID)
	opState.Work.LastActionOperationID = strings.TrimSpace(req.State.ActionProposal.OperationID)
	opState.Work.LastLeaseID = strings.TrimSpace(req.LeaseID)
	opState.Work.LastWorkMode = strings.TrimSpace(string(req.Mode))
	opState.Work.CodexThreadID = firstRuntimeWorkNonEmpty(result.ThreadID, opState.Work.CodexThreadID)
	opState.Work.CodexLastTurnID = firstRuntimeWorkNonEmpty(result.TurnID, opState.Work.CodexLastTurnID)
	opState.Work.CodexLaneMode = string(req.Mode)
	opState.Work.RepoRoot = firstRuntimeWorkNonEmpty(req.RepoRoot, opState.Work.RepoRoot)
	opState.Work.Workdir = firstRuntimeWorkNonEmpty(req.Workdir, opState.Work.Workdir)
	opState.Work.ChangedFiles = append([]string(nil), result.ChangedFiles...)
	opState.Work.Commands = append([]string(nil), result.Commands...)
	opState.Work.CodexEvents = append([]session.WorkCodexEvent(nil), result.CodexEvents...)
	opState.Work.PatchPreview = strings.TrimSpace(result.PatchPreview)
	opState.Work.CommitLaneStatus = strings.TrimSpace(result.CommitLaneStatus)
	opState.Work.LastSummary = strings.TrimSpace(result.Summary)
	opState.Work.LastError = ""
	if cause != nil {
		opState.Work.LastError = cause.Error()
	} else if result.Recovery != nil {
		opState.Work.LastError = workResultRecoverySummary(result)
	}
	opState.Work.LastExecutorUpdatedAt = time.Now().UTC()
	if cause == nil && result.Recovery == nil && workResultHasSubstantiveCompletionEvidenceForRequest(req, result) {
		opState.Work.LastCompletedAt = opState.Work.LastExecutorUpdatedAt
	} else {
		opState.Work.LastCompletedAt = time.Time{}
		if cause == nil && result.Recovery == nil {
			opState.Work.LastError = errWorkExecutorNoCompletionEvidence.Error()
		}
	}
	artifact := r.writeWorkResultArtifact(key, req, result, status, cause, opState.Work.LastExecutorUpdatedAt)
	if artifact.Ref != "" {
		opState.Artifacts = appendOperationArtifact(opState.Artifacts, artifact)
	}
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		log.Printf("WARN persist work result failed chat_id=%d err=%v", key.ChatID, err)
	}
	return artifact
}

func workResultHasSubstantiveCompletionEvidence(result WorkResult) bool {
	return strings.TrimSpace(result.Summary) != "" ||
		len(result.ChangedFiles) > 0 ||
		len(result.Commands) > 0 ||
		len(result.CodexEvents) > 0 ||
		strings.TrimSpace(result.PatchPreview) != "" ||
		strings.TrimSpace(result.CommitLaneStatus) != ""
}

func workResultHasSubstantiveCompletionEvidenceForRequest(req WorkRequest, result WorkResult) bool {
	if workRequestRequiresMaterialCompletionEvidence(req) {
		if !workResultHasMaterialCompletionEvidence(req, result) {
			return false
		}
		return !workResultFailureInvalidatesMaterialCompletion(result)
	}
	if workResultHasFailedToolEvidence(result) {
		return false
	}
	return workResultHasSubstantiveCompletionEvidence(result)
}

func workResultHasFailedToolEvidence(result WorkResult) bool {
	return result.ToolFailures > 0 || strings.TrimSpace(result.ToolFailure) != ""
}

func workResultFailureInvalidatesMaterialCompletion(result WorkResult) bool {
	for _, failure := range append([]string{result.ToolFailure}, result.ToolFailureTexts...) {
		if workResultFailureTextInvalidatesMaterialCompletion(failure) {
			return true
		}
	}
	return false
}

func workResultFailureTextInvalidatesMaterialCompletion(failure string) bool {
	failure = strings.ToLower(strings.TrimSpace(failure))
	if failure == "" {
		return false
	}
	for _, marker := range []string{
		"authority_rejected",
		"approval required",
		"authorization required",
		"permission denied",
		"not authorized",
		"unauthorized",
		"no active grant",
		"grant required",
		"capability required",
	} {
		if strings.Contains(failure, marker) {
			return true
		}
	}
	return false
}

func workRequestRequiresMaterialCompletionEvidence(req WorkRequest) bool {
	if workModeRank(req.Mode) > workModeRank(WorkModeReadOnly) {
		return true
	}
	state := session.NormalizeContinuationState(req.State)
	if len(state.ContinuationLease.RequiredCapabilityGrants) > 0 {
		return true
	}
	if session.InferContinuationLeaseClass(state.ActionProposal.RiskClass, state.ActionProposal.AllowedActions, state.ActionProposal.BoundedEffect) == session.ContinuationLeaseClassCapabilityGrant {
		return true
	}
	if class := session.InferContinuationLeaseClass(string(state.ContinuationLease.LeaseClass), state.ContinuationLease.AllowedActions, ""); class == session.ContinuationLeaseClassCapabilityGrant {
		return true
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if len(phase.RequiredCapabilityGrants) > 0 {
			return true
		}
		if session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect) == session.ContinuationLeaseClassCapabilityGrant {
			return true
		}
	}
	return false
}

func workResultHasMaterialCompletionEvidence(req WorkRequest, result WorkResult) bool {
	if len(result.ChangedFiles) > 0 ||
		len(result.CodexEvents) > 0 ||
		strings.TrimSpace(result.PatchPreview) != "" ||
		strings.TrimSpace(result.CommitLaneStatus) != "" {
		return true
	}
	for _, command := range result.Commands {
		effect := commandeffect.Classify(command)
		if workCommandEffectCompletesRequest(req, command, effect) {
			return true
		}
	}
	return false
}

func workCommandEffectCompletesRequest(req WorkRequest, command string, effect commandeffect.Effect) bool {
	if !effect.SideEffects && effect.Kind == commandeffect.KindReadOnlyInspection {
		return false
	}
	switch req.Mode {
	case WorkModeDeploy:
		return effect.Kind == commandeffect.KindService ||
			effect.Kind == commandeffect.KindRemoteHost ||
			effect.Kind == commandeffect.KindBuildArtifact ||
			effect.Kind == commandeffect.KindRepoHistory ||
			effect.Kind == commandeffect.KindExternalAccount
	case WorkModeCommit:
		return effect.Kind == commandeffect.KindRepoHistory
	case WorkModeWorkspaceWrite:
		return effect.Kind == commandeffect.KindWorkspaceMutation ||
			effect.Kind == commandeffect.KindBuildArtifact ||
			effect.Kind == commandeffect.KindValidation
	case WorkModeReadOnly, "":
		state := session.NormalizeContinuationState(req.State)
		if session.InferContinuationLeaseClass(state.ActionProposal.RiskClass, state.ActionProposal.AllowedActions, state.ActionProposal.BoundedEffect) == session.ContinuationLeaseClassCapabilityGrant ||
			len(state.ContinuationLease.RequiredCapabilityGrants) > 0 {
			return workExternalAccountCommandCompletesRequest(state, command, effect)
		}
		return effect.Kind != commandeffect.KindUnknown
	default:
		return effect.Kind != commandeffect.KindUnknown
	}
}

func workExternalAccountCommandCompletesRequest(state session.ContinuationState, command string, effect commandeffect.Effect) bool {
	switch effect.Kind {
	case commandeffect.KindExternalAccount, commandeffect.KindCredential, commandeffect.KindCapability:
	default:
		return false
	}
	allowed := append([]string(nil), state.ActionProposal.AllowedActions...)
	allowed = append(allowed, state.ContinuationLease.AllowedActions...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		allowed = append(allowed, phase.AllowedActions...)
	}
	commandToken := normalizeContinuationAuthorityAction(command)
	for _, action := range allowed {
		switch normalizeContinuationAuthorityAction(action) {
		case "github_pr_create", "github_pr_open", "pull_request_create", "pull_request_open", "open_pull_request", "create_github_pr":
			if strings.Contains(commandToken, "gh_pr_create") ||
				strings.Contains(commandToken, "gh_pr_open") ||
				strings.Contains(commandToken, "pull_request_create") ||
				strings.Contains(commandToken, "create_github_pr") {
				return true
			}
		case "github_pr_update", "github_pr_metadata_update", "pull_request_update", "pull_request_metadata_update", "update_pull_request_title", "update_pull_request_body":
			if strings.Contains(commandToken, "gh_pr_edit") ||
				strings.Contains(commandToken, "pull_request_update") ||
				strings.Contains(commandToken, "update_pull_request") {
				return true
			}
		default:
			if normalizeContinuationAuthorityAction(action) == "invoke_active_capability_grant" {
				return effect.Kind == commandeffect.KindExternalAccount && strings.Contains(commandToken, "gh_")
			}
		}
	}
	return false
}

func (r *Runtime) deliverWorkResult(ctx context.Context, key session.SessionKey, result WorkResult, artifact session.OperationArtifact) error {
	if r == nil || r.outbound == nil || key.ChatID == 0 {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(result.ExecutorName), "native") {
		return nil
	}
	text := renderWorkResultMessage(result, artifact)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	text = r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), text)
	if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: key.ChatID, Text: text}); err != nil {
		return fmt.Errorf("send work executor result: %w", err)
	}
	return nil
}

func renderWorkResultMessage(result WorkResult, artifact session.OperationArtifact) string {
	executor := firstRuntimeWorkNonEmpty(result.ExecutorName, "work executor")
	lines := []string{"Work executor finished via " + executor + "."}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "", truncatePreview(summary, 900))
	}
	if len(result.ChangedFiles) > 0 {
		lines = append(lines, "", "Changed files:")
		for _, file := range result.ChangedFiles {
			lines = append(lines, "- "+strings.TrimSpace(file))
		}
	}
	if len(result.Commands) > 0 {
		lines = append(lines, "", "Commands:")
		for _, command := range result.Commands {
			lines = append(lines, "- "+strings.TrimSpace(command))
		}
	}
	if status := strings.TrimSpace(result.CommitLaneStatus); status != "" {
		lines = append(lines, "", "Commit lane: "+status)
	}
	if preview := strings.TrimSpace(result.PatchPreview); preview != "" {
		lines = append(lines, "", "Patch preview:", truncatePreview(preview, 900))
	}
	if artifact.Ref != "" {
		lines = append(lines, "", "Full evidence artifact:", artifact.Ref)
	}
	if strings.TrimSpace(result.Summary) == "" && len(result.ChangedFiles) == 0 && len(result.Commands) == 0 {
		lines = append(lines, "", "No detailed summary was returned.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (r *Runtime) offerWorkFailureRetry(ctx context.Context, key session.SessionKey, chatID int64, cause error) {
	if r == nil || cause == nil || chatID == 0 {
		return
	}
	if r.isShuttingDown() || errors.Is(cause, context.Canceled) {
		return
	}
	reason := "work_executor_failed_before_completion"
	if _, sent, refreshErr := r.refreshContinuationProposal(ctx, key, reason, "work_executor_failure", false); refreshErr != nil {
		log.Printf("WARN refresh continuation after work failure failed chat_id=%d err=%v", chatID, refreshErr)
		fallbackSent := r.sendWorkFailureRetryFallback(ctx, key, chatID, cause, refreshErr)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "retry_offer_failed", map[string]any{
			"reason":        "work_executor_failure_retry_offer_failed",
			"error":         trimError(refreshErr.Error()),
			"fallback_sent": fallbackSent,
		}, time.Now().UTC())
	} else if sent {
		r.recordExecutionEvent(key, core.ExecutionEventRecoveryIssued, "work", "retry_offered", map[string]any{
			"reason": "work_executor_failure",
			"error":  trimError(cause.Error()),
		}, time.Now().UTC())
	}
}

func (r *Runtime) sendWorkFailureRetryFallback(ctx context.Context, key session.SessionKey, chatID int64, cause error, refreshErr error) bool {
	if r == nil || r.outbound == nil || chatID == 0 {
		return false
	}
	lines := []string{
		"I could not show the retry approval buttons.",
		"",
		"The approved work did not finish cleanly, so the next step needs a fresh manual approval for one bounded retry.",
	}
	if cause != nil {
		lines = append(lines, "", "Work failure: "+trimError(cause.Error()))
	}
	if refreshErr != nil {
		lines = append(lines, "Approval prompt failure: "+trimError(refreshErr.Error()))
	}
	text := r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), strings.Join(lines, "\n"))
	if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: chatID, Text: text}); err != nil {
		log.Printf("WARN send work failure retry fallback failed chat_id=%d err=%v", chatID, err)
		return false
	}
	return true
}

func appendOperationArtifact(values []session.OperationArtifact, artifact session.OperationArtifact) []session.OperationArtifact {
	artifact.Ref = strings.TrimSpace(artifact.Ref)
	if artifact.Ref == "" {
		return values
	}
	artifact.Label = strings.TrimSpace(artifact.Label)
	out := make([]session.OperationArtifact, 0, len(values)+1)
	seen := false
	for _, value := range values {
		if strings.TrimSpace(value.Ref) == artifact.Ref {
			out = append(out, artifact)
			seen = true
			continue
		}
		out = append(out, value)
	}
	if !seen {
		out = append(out, artifact)
	}
	return out
}

func (r *Runtime) writeWorkResultArtifact(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error, now time.Time) session.OperationArtifact {
	if r == nil || r.cfg == nil {
		return session.OperationArtifact{}
	}
	body := workResultArtifactMarkdown(key, req, result, status, cause, now)
	if strings.TrimSpace(body) == "" {
		return session.OperationArtifact{}
	}
	root := firstRuntimeWorkNonEmpty(r.cfg.Agent.SharedMemoryRoot, r.cfg.Agent.ExecRoot)
	if strings.TrimSpace(root) == "" {
		return session.OperationArtifact{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	dir := filepath.Join(root, "memory", "work-evidence", now.Format("2006-01-02"), fmt.Sprintf("chat-%d", key.ChatID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("WARN write work evidence artifact mkdir failed chat_id=%d err=%v", key.ChatID, err)
		return session.OperationArtifact{}
	}
	base := sanitizeWorkArtifactName(firstRuntimeWorkNonEmpty(req.OperationID, req.LeaseID, "work"))
	if base == "" {
		base = "work"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, now.UnixNano()))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		log.Printf("WARN write work evidence artifact failed chat_id=%d err=%v", key.ChatID, err)
		return session.OperationArtifact{}
	}
	return session.OperationArtifact{Label: "Work evidence", Ref: path}
}

func workResultArtifactMarkdown(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if strings.TrimSpace(result.Summary) == "" &&
		strings.TrimSpace(result.ProviderFailure) == "" &&
		len(result.ProviderEvents) == 0 &&
		len(result.ChangedFiles) == 0 &&
		len(result.Commands) == 0 &&
		len(result.CodexEvents) == 0 &&
		strings.TrimSpace(result.PatchPreview) == "" &&
		len(result.ApprovalLog) == 0 &&
		result.TurnRunID == 0 &&
		result.ToolSuccesses == 0 &&
		result.ToolFailures == 0 &&
		strings.TrimSpace(result.ToolFailure) == "" &&
		cause == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Work Evidence\n\n")
	fmt.Fprintf(&b, "- captured_at: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "- chat_id: %d\n", key.ChatID)
	if req.OperationID != "" {
		fmt.Fprintf(&b, "- operation_id: %s\n", req.OperationID)
	}
	if req.LeaseID != "" {
		fmt.Fprintf(&b, "- lease_id: %s\n", req.LeaseID)
	}
	if result.ExecutorName != "" {
		fmt.Fprintf(&b, "- executor: %s\n", result.ExecutorName)
	}
	if status.Configured != "" {
		fmt.Fprintf(&b, "- configured_executor: %s\n", status.Configured)
	}
	if status.FallbackReason != "" {
		fmt.Fprintf(&b, "- fallback_reason: %s\n", status.FallbackReason)
	}
	if result.ProviderFailure != "" {
		fmt.Fprintf(&b, "- provider_failure: %s\n", trimError(result.ProviderFailure))
	}
	if result.TurnRunID != 0 {
		fmt.Fprintf(&b, "- turn_run_id: %d\n", result.TurnRunID)
	}
	if result.ToolSuccesses > 0 {
		fmt.Fprintf(&b, "- tool_successes: %d\n", result.ToolSuccesses)
	}
	if result.ToolFailures > 0 {
		fmt.Fprintf(&b, "- tool_failures: %d\n", result.ToolFailures)
	}
	if result.ToolFailure != "" {
		fmt.Fprintf(&b, "- tool_failure: %s\n", trimError(result.ToolFailure))
	}
	if result.Recovery != nil {
		fmt.Fprintf(&b, "- recovery_kind: %s\n", strings.TrimSpace(string(result.Recovery.Kind)))
		if strings.TrimSpace(result.Recovery.Summary) != "" {
			fmt.Fprintf(&b, "- recovery_summary: %s\n", trimError(result.Recovery.Summary))
		}
	} else {
		if result.RecoveryKind != "" {
			fmt.Fprintf(&b, "- recovery_kind: %s\n", strings.TrimSpace(result.RecoveryKind))
		}
		if result.RecoverySummary != "" {
			fmt.Fprintf(&b, "- recovery_summary: %s\n", trimError(result.RecoverySummary))
		}
	}
	if cause != nil {
		fmt.Fprintf(&b, "- error: %s\n", trimError(cause.Error()))
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		b.WriteString("\n## Summary\n\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if len(result.ChangedFiles) > 0 {
		b.WriteString("\n## Changed Files\n\n")
		for _, file := range result.ChangedFiles {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(file))
		}
	}
	if len(result.Commands) > 0 {
		b.WriteString("\n## Commands\n\n")
		for _, command := range result.Commands {
			fmt.Fprintf(&b, "- `%s`\n", strings.TrimSpace(command))
		}
	}
	if len(result.CodexEvents) > 0 {
		b.WriteString("\n## Codex Events\n\n")
		for _, event := range result.CodexEvents {
			parts := []string{}
			for _, part := range []string{event.Kind, event.Method, event.Status, event.Subject, event.Path, event.Command, event.Server, event.Tool, event.ThreadID, event.TurnID} {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			if len(parts) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.Join(parts, " | "))
			if preview := strings.TrimSpace(event.Preview); preview != "" {
				b.WriteString("\n```text\n")
				b.WriteString(preview)
				b.WriteString("\n```\n")
			}
		}
	}
	if len(result.ProviderEvents) > 0 {
		b.WriteString("\n## Provider Events\n\n")
		for _, event := range result.ProviderEvents {
			parts := []string{}
			for _, part := range []string{event.EventType, event.Provider, event.FromProvider, event.ToProvider, event.Reason, event.ResponseID} {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			if event.Attempt > 0 {
				parts = append(parts, fmt.Sprintf("attempt=%d", event.Attempt))
			}
			if event.MaxRetries > 0 {
				parts = append(parts, fmt.Sprintf("max_retries=%d", event.MaxRetries))
			}
			if event.PartialContentChars > 0 {
				parts = append(parts, fmt.Sprintf("partial_content_chars=%d", event.PartialContentChars))
			}
			if event.PartialToolCalls > 0 {
				parts = append(parts, fmt.Sprintf("partial_tool_calls=%d", event.PartialToolCalls))
			}
			if len(parts) == 0 && strings.TrimSpace(event.Error) == "" {
				continue
			}
			if len(parts) > 0 {
				fmt.Fprintf(&b, "- %s\n", strings.Join(parts, " | "))
			}
			if errText := strings.TrimSpace(event.Error); errText != "" {
				fmt.Fprintf(&b, "  error: %s\n", trimError(errText))
			}
		}
	}
	if len(result.ApprovalLog) > 0 {
		b.WriteString("\n## Approval Log\n\n")
		for _, item := range result.ApprovalLog {
			fmt.Fprintf(&b, "- %s: %s", strings.TrimSpace(item.Method), strings.TrimSpace(item.Decision))
			if item.Command != "" {
				fmt.Fprintf(&b, " `%s`", item.Command)
			}
			if item.Reason != "" {
				fmt.Fprintf(&b, " (%s)", item.Reason)
			}
			b.WriteString("\n")
		}
	}
	if preview := strings.TrimSpace(result.PatchPreview); preview != "" {
		b.WriteString("\n## Patch Preview\n\n```diff\n")
		b.WriteString(preview)
		b.WriteString("\n```\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func sanitizeWorkArtifactName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func workResultPayload(req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error) map[string]any {
	payload := map[string]any{
		"operation_id":          strings.TrimSpace(req.OperationID),
		"lease_id":              strings.TrimSpace(req.LeaseID),
		"mode":                  strings.TrimSpace(string(req.Mode)),
		"executor":              strings.TrimSpace(result.ExecutorName),
		"configured_executor":   strings.TrimSpace(status.Configured),
		"preferred_executor":    strings.TrimSpace(status.Preferred),
		"active_executor":       strings.TrimSpace(status.Active),
		"fallback_reason":       strings.TrimSpace(status.FallbackReason),
		"provider_events_count": len(result.ProviderEvents),
		"side_effects":          result.SideEffects,
		"changed_files_count":   len(result.ChangedFiles),
		"commands_count":        len(result.Commands),
		"codex_events_count":    len(result.CodexEvents),
		"approval_events_count": len(result.ApprovalLog),
		"turn_run_id":           result.TurnRunID,
		"tool_successes":        result.ToolSuccesses,
		"tool_failures":         result.ToolFailures,
	}
	if result.Recovery != nil {
		payload["recovery_kind"] = strings.TrimSpace(string(result.Recovery.Kind))
		payload["recovery_recoverable"] = result.Recovery.Recoverable
		payload["recovery_replan_required"] = result.Recovery.ReplanRequired
	}
	if strings.TrimSpace(result.ThreadID) != "" {
		payload["thread_id"] = strings.TrimSpace(result.ThreadID)
	}
	if strings.TrimSpace(result.TurnID) != "" {
		payload["turn_id"] = strings.TrimSpace(result.TurnID)
	}
	if strings.TrimSpace(result.CommitLaneStatus) != "" {
		payload["commit_lane_status"] = strings.TrimSpace(result.CommitLaneStatus)
	}
	if strings.TrimSpace(result.CompletionKind) != "" {
		payload["completion_kind"] = strings.TrimSpace(result.CompletionKind)
	}
	if strings.TrimSpace(result.RecoveryKind) != "" {
		payload["recovery_kind"] = strings.TrimSpace(result.RecoveryKind)
	}
	if strings.TrimSpace(result.RecoveryDelivery) != "" {
		payload["recovery_delivery"] = strings.TrimSpace(result.RecoveryDelivery)
	}
	if strings.TrimSpace(result.RecoverySummary) != "" {
		payload["recovery_summary"] = trimError(result.RecoverySummary)
	}
	if strings.TrimSpace(result.ProviderFailure) != "" {
		payload["provider_failure"] = trimError(result.ProviderFailure)
	}
	if strings.TrimSpace(result.ToolFailure) != "" {
		payload["tool_failure"] = trimError(result.ToolFailure)
	}
	if len(result.ToolFailureTexts) > 0 {
		payload["tool_failure_texts"] = result.ToolFailureTexts
	}
	if cause != nil {
		payload["error"] = trimError(cause.Error())
	}
	return payload
}

func workResultRecoverySummary(result WorkResult) string {
	if result.Recovery == nil {
		return ""
	}
	kind := strings.TrimSpace(string(result.Recovery.Kind))
	if kind == "" {
		kind = "turn_recovery"
	}
	summary := strings.TrimSpace(result.Recovery.Summary)
	if summary == "" {
		return "turn recovery handoff: " + kind
	}
	return "turn recovery handoff: " + kind + ": " + summary
}

func actorLabel(actor principal.Principal) string {
	if actor.Role == principal.RoleAdmin {
		return "admin"
	}
	if actor.Role == principal.RoleApprovedUser {
		return "approved_user"
	}
	if strings.TrimSpace(actor.DurableAgentID) != "" {
		return strings.TrimSpace(actor.DurableAgentID)
	}
	return "machine"
}
