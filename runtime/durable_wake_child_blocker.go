//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

type durableWakeChildBlockerClassification struct {
	Kind               string
	State              session.NextActionState
	NextAction         string
	RequiredAuthority  string
	ResourceBlocker    string
	Verifier           string
	RetryPolicy        string
	OperationKind      string
	OperationTool      string
	OperationInputJSON string
	OperatorProjection string
	ReviewSummary      string
	ReviewLocalActions []string
	ReviewQuestions    []string
	ReviewRiskFlags    []string
	ReviewMetadata     map[string]string
}

func durableWakeChildTaskBlockerClassification(agent core.DurableAgent, result session.ChildTaskResultInput) durableWakeChildBlockerClassification {
	agentID := strings.TrimSpace(agent.AgentID)
	blockerKind := normalizeDurableWakeChildBlockerKind(result.BlockerKind)
	text := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(result.BlockerKind),
		strings.TrimSpace(result.ErrorText),
		strings.TrimSpace(result.Summary),
	}, "\n"))
	toolName := durableWakeChildBlockerToolName(agent, text)
	adapterName := strings.TrimSpace(externalChannelAdapter(agent))
	if toolName == "" {
		toolName = adapterName
	}
	if blockerKind == "" {
		blockerKind = "child_reported_blocked"
	}
	classification := durableWakeChildBlockerClassification{
		Kind:               blockerKind,
		State:              result.NextState,
		NextAction:         "review the child task blocker and choose the next bounded repair",
		ResourceBlocker:    blockerKind,
		RetryPolicy:        "retry_after_blocker_resolution",
		OperationKind:      "child_task_blocker_review",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child task stopped with a blocker; review the exact child result and choose a bounded repair before retrying.",
		ReviewLocalActions: []string{"Child task stopped before a terminal completion; the blocker was recorded as a durable next action."},
		ReviewQuestions:    []string{"Review the child blocker and choose the next bounded repair before retrying the child task."},
		ReviewRiskFlags:    []string{"durable_child", "blocked_child_task"},
	}
	switch result.Status {
	case session.ChildTaskResultUpdate:
		classification.Kind = firstNonEmpty(blockerKind, "child_task_update")
		classification.State = session.NextActionWaitingForChild
		classification.NextAction = "continue the bounded child task from the latest reported update"
		classification.ResourceBlocker = classification.Kind
		classification.RetryPolicy = "continue_after_child_update"
		classification.OperatorProjection = "Child task reported an intermediate update; continue only through the bounded child task packet."
		classification.ReviewLocalActions = []string{"Child task reported an intermediate update and remains open for bounded continuation."}
		classification.ReviewQuestions = []string{"Continue the child task only if the latest update still matches current intent."}
	case session.ChildTaskResultBlocked:
		classification = durableWakeBlockedChildClassification(classification, text)
	}
	classification.ReviewSummary = durableWakeChildBlockerReviewSummary(agentID, classification, result)
	classification.ReviewMetadata = durableWakeChildBlockerReviewMetadata(agentID, adapterName, toolName, classification, result)
	classification.OperationInputJSON = durableWakeChildBlockerOperationInputJSON(agentID, adapterName, toolName, classification, result)
	return classification
}

func durableWakeBlockedChildClassification(base durableWakeChildBlockerClassification, text string) durableWakeChildBlockerClassification {
	switch {
	case strings.Contains(text, "missing_or_not_executable") ||
		strings.Contains(text, "not executable") ||
		strings.Contains(text, "missing executable") ||
		strings.Contains(text, "wrapper_missing"):
		base.Kind = "tool_runtime_not_executable"
		base.State = session.NextActionBlockedNeedsResourceRepair
		base.NextAction = "materialize or repair the child-local tool runtime, then run one no-content readiness probe"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "retry_after_tool_runtime_repair"
		base.OperationKind = "child_tool_runtime_repair"
		base.OperatorProjection = "Child-local tool runtime is missing or not executable; repair the wrapper/materialization, then run one no-content readiness probe."
		base.ReviewLocalActions = []string{"Child verified grants/config, then found the child-local tool runtime missing or not executable."}
		base.ReviewQuestions = []string{"Repair the child-local tool runtime materialization, then rerun exactly one no-content readiness probe."}
		base.ReviewRiskFlags = []string{"durable_child", "tool_runtime", "no_content_probe_required"}
	case strings.Contains(text, externalChannelReadinessFailureLife) ||
		strings.Contains(text, "lifecycle_unregistered") ||
		strings.Contains(text, "install/audit/probe") ||
		strings.Contains(text, "tool lifecycle"):
		base.Kind = "tool_lifecycle_unregistered"
		base.State = session.NextActionBlockedNeedsResourceRepair
		base.NextAction = "register, install, audit, and probe the child-local tool lifecycle before retrying"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "retry_after_tool_lifecycle_repair"
		base.OperationKind = "child_tool_lifecycle_repair"
		base.OperatorProjection = "Child tool lifecycle is not registered or verified; repair lifecycle records before retrying the child wake."
		base.ReviewLocalActions = []string{"Child wake was blocked by tool lifecycle readiness, not by mailbox credentials."}
		base.ReviewQuestions = []string{"Repair the tool lifecycle records, then rerun a bounded no-content readiness probe."}
		base.ReviewRiskFlags = []string{"durable_child", "tool_lifecycle"}
	case strings.Contains(text, "grant_missing") ||
		strings.Contains(text, "grant missing") ||
		strings.Contains(text, "grant_expired") ||
		strings.Contains(text, "grant expired") ||
		strings.Contains(text, "grant_revoked") ||
		strings.Contains(text, "grant revoked") ||
		strings.Contains(text, "missing_grant"):
		base.Kind = "grant_missing_or_stale"
		base.State = session.NextActionBlockedNeedsAuthority
		base.NextAction = "approve or repair the exact grant needed for the child task"
		base.RequiredAuthority = base.Kind
		base.ResourceBlocker = ""
		base.RetryPolicy = "retry_after_authority_repair"
		base.OperationKind = "child_authority_repair"
		base.OperatorProjection = "Child task needs an exact live grant before it can continue."
		base.ReviewLocalActions = []string{"Child task stopped before executing because required authority was missing or stale."}
		base.ReviewQuestions = []string{"Approve, renew, or reject the exact child grant before retrying."}
		base.ReviewRiskFlags = []string{"durable_child", "authority"}
	case strings.Contains(text, "permission denied") ||
		strings.Contains(text, "not writable") ||
		strings.Contains(text, "read-only") ||
		strings.Contains(text, "readonly") ||
		strings.Contains(text, "host_permission_denied"):
		base.Kind = "resource_permission_denied"
		base.State = session.NextActionBlockedNeedsResourceRepair
		base.NextAction = "repair the child-local resource permission boundary before retrying"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "retry_after_resource_repair"
		base.OperationKind = "child_resource_repair"
		base.OperatorProjection = "Child task has authority but the resource boundary denied the operation; repair the child-local resource path before retry."
		base.ReviewLocalActions = []string{"Child task reached the resource boundary and stopped without widening authority."}
		base.ReviewQuestions = []string{"Repair the child-local resource boundary, then retry only the bounded child task."}
		base.ReviewRiskFlags = []string{"durable_child", "resource_boundary"}
	case strings.Contains(text, "credential") ||
		strings.Contains(text, "oauth") ||
		strings.Contains(text, "auth_status") ||
		strings.Contains(text, "account_status"):
		base.Kind = "credential_unverified"
		base.State = session.NextActionWaitingForOperator
		base.NextAction = "run or review a no-content credential/account-status probe before mailbox work"
		base.RequiredAuthority = "credential_status_probe"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "retry_after_credential_verification"
		base.OperationKind = "child_credential_probe"
		base.OperatorProjection = "Credential state is not proven; run a no-content status probe before any mailbox action."
		base.ReviewLocalActions = []string{"Child task stopped before content access because credential status is not verified."}
		base.ReviewQuestions = []string{"Run exactly one no-content credential/account-status probe, then continue only if it passes."}
		base.ReviewRiskFlags = []string{"durable_child", "credential_status", "no_content_probe_required"}
	case strings.Contains(text, "timeout") ||
		strings.Contains(text, "temporarily unavailable") ||
		strings.Contains(text, "transient"):
		base.Kind = "external_transient"
		base.State = session.NextActionScheduledRetry
		base.NextAction = "wait for the bounded retry window before retrying the child task"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "bounded_backoff"
		base.OperationKind = "child_retry"
		base.OperatorProjection = "Child task hit a transient external blocker; retry only after bounded backoff."
		base.ReviewLocalActions = []string{"Child task stopped on a transient external condition; no authority was widened."}
		base.ReviewQuestions = []string{"Retry after the bounded backoff if the work is still current."}
		base.ReviewRiskFlags = []string{"durable_child", "external_transient"}
	default:
		base.Kind = firstNonEmpty(base.Kind, "child_reported_blocked")
		base.State = session.NextActionWaitingForOperator
		base.NextAction = "review the child-authored blocker and choose an exact repair"
		base.ResourceBlocker = base.Kind
		base.RetryPolicy = "operator_disambiguation_required"
		base.OperationKind = "child_blocker_disambiguation"
		base.OperatorProjection = "Child reported a blocker that does not compile to a known repair class; inspect the child result and choose an exact repair."
	}
	return base
}

func durableWakeChildTaskNextActionInput(key session.SessionKey, agent core.DurableAgent, result session.ChildTaskResultInput, taskPacketID string, now time.Time) *session.NextActionInput {
	if result.Status == session.ChildTaskResultCompleted {
		return nil
	}
	if strings.TrimSpace(agent.AgentID) == "" {
		agent.AgentID = key.Scope.DurableAgentID
	}
	classification := durableWakeChildTaskBlockerClassification(agent, result)
	nextAction := classification.NextAction
	requiredAuthority := classification.RequiredAuthority
	retryPolicy := classification.RetryPolicy
	if result.Status == session.ChildTaskResultUpdate && nextAction == "" {
		nextAction = "continue the bounded child task from the latest reported update"
		retryPolicy = "continue_after_child_update"
	}
	if nextAction == "" {
		nextAction = "repair the child task blocker before retrying"
	}
	if retryPolicy == "" {
		retryPolicy = "retry_after_blocker_resolution"
	}
	state := classification.State
	if state == "" {
		state = result.NextState
	}
	if state == "" {
		state = session.NextActionWaitingForOperator
	}
	return &session.NextActionInput{
		Key:                key,
		Owner:              "durable_wake",
		State:              state,
		SubjectKind:        "task_packet",
		SubjectRef:         taskPacketID,
		CausalRefs:         []string{"task_packet:" + taskPacketID, "child_task_attempt:" + result.AttemptID, "child_task_result:" + result.ResultID},
		NextAction:         nextAction,
		RequiredAuthority:  requiredAuthority,
		ResourceBlocker:    classification.ResourceBlocker,
		Verifier:           classification.Verifier,
		RetryPolicy:        retryPolicy,
		OperationKind:      classification.OperationKind,
		OperationTool:      classification.OperationTool,
		OperationInputJSON: classification.OperationInputJSON,
		OperatorProjection: firstNonEmpty(strings.TrimSpace(classification.OperatorProjection), strings.TrimSpace(result.Summary), nextAction),
		CreatedAt:          now,
	}
}

func durableWakeChildBlockerReviewIntent(agent core.DurableAgent, result session.ChildTaskResultInput, nextAction *session.NextActionInput, now time.Time) (session.ChildTaskOutcomeIntentInput, bool) {
	if nextAction == nil || result.Status != session.ChildTaskResultBlocked {
		return session.ChildTaskOutcomeIntentInput{}, false
	}
	if strings.TrimSpace(agent.AgentID) == "" || agent.ReviewTargetChatID == 0 {
		return session.ChildTaskOutcomeIntentInput{}, false
	}
	classification := durableWakeChildTaskBlockerClassification(agent, result)
	metadata := classification.ReviewMetadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["next_action_record_subject"] = strings.TrimSpace(nextAction.SubjectRef)
	payloadRaw, _ := json.Marshal(map[string]any{
		"agent_id":       strings.TrimSpace(agent.AgentID),
		"summary":        classification.ReviewSummary,
		"interval_label": now.UTC().Format(time.RFC3339),
		"local_actions":  classification.ReviewLocalActions,
		"questions":      classification.ReviewQuestions,
		"risk_flags":     classification.ReviewRiskFlags,
		"artifact_refs":  []string{"child_task://" + strings.TrimSpace(result.PacketID), "child_result://" + strings.TrimSpace(result.ResultID)},
		"metadata":       metadata,
	})
	return session.ChildTaskOutcomeIntentInput{
		Kind:           session.ChildTaskOutcomeIntentChildBlockerReview,
		Sequence:       10,
		PayloadJSON:    string(payloadRaw),
		ResultRef:      "child_task_blocker_review:" + strings.TrimSpace(result.ResultID),
		IdempotencyKey: "child_task_blocker_review:" + strings.TrimSpace(result.ResultID) + ":" + classification.Kind,
		CreatedAt:      now,
	}, true
}

func (r *Runtime) applyDurableWakeChildBlockerReviewIntent(agent core.DurableAgent, intent session.ChildTaskOutcomeIntent) error {
	var payload struct {
		AgentID       string            `json:"agent_id"`
		Summary       string            `json:"summary"`
		IntervalLabel string            `json:"interval_label"`
		LocalActions  []string          `json:"local_actions"`
		Questions     []string          `json:"questions"`
		RiskFlags     []string          `json:"risk_flags"`
		ArtifactRefs  []string          `json:"artifact_refs"`
		Metadata      map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(intent.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parse child blocker review intent payload: %w", err)
	}
	agentID := firstNonEmpty(strings.TrimSpace(agent.AgentID), strings.TrimSpace(payload.AgentID))
	if agentID == "" {
		return fmt.Errorf("child blocker review intent missing agent_id")
	}
	loaded, err := r.store.DurableAgent(agentID)
	if err != nil {
		return err
	}
	artifact := core.DurableReviewArtifact{
		AgentID:       agentID,
		Summary:       strings.TrimSpace(payload.Summary),
		IntervalLabel: strings.TrimSpace(payload.IntervalLabel),
		LocalActions:  payload.LocalActions,
		Questions:     payload.Questions,
		RiskFlags:     payload.RiskFlags,
		ArtifactRefs:  payload.ArtifactRefs,
		Metadata:      payload.Metadata,
	}
	if _, err := durableagent.NewRuntime(r.store).QueueReviewArtifactWithIdempotencyKey(*loaded, artifact, intent.IdempotencyKey); err != nil {
		return fmt.Errorf("queue child blocker review artifact: %w", err)
	}
	return nil
}

func durableWakeChildBlockerReviewSummary(agentID string, classification durableWakeChildBlockerClassification, result session.ChildTaskResultInput) string {
	agentName := durableAgentDisplayName(agentID)
	if agentName == "" {
		agentName = "Child"
	}
	summary := strings.TrimSpace(classification.OperatorProjection)
	if summary == "" {
		summary = strings.TrimSpace(result.Summary)
	}
	if summary == "" {
		summary = classification.NextAction
	}
	return truncateRunes(agentName+" stopped on "+classification.Kind+". "+summary, 700)
}

func durableWakeChildBlockerReviewMetadata(agentID string, adapterName string, toolName string, classification durableWakeChildBlockerClassification, result session.ChildTaskResultInput) map[string]string {
	metadata := map[string]string{
		"durable_agent_id":     strings.TrimSpace(agentID),
		"child_blocker_kind":   strings.TrimSpace(classification.Kind),
		"operator_status":      "blocked",
		"operator_title":       durableAgentDisplayName(agentID) + " child task blocked",
		"operator_summary":     strings.TrimSpace(classification.OperatorProjection),
		"operator_action":      strings.TrimSpace(classification.OperationKind),
		"operator_next_action": strings.TrimSpace(classification.NextAction),
		"retry_policy":         strings.TrimSpace(classification.RetryPolicy),
		"child_task_result_id": strings.TrimSpace(result.ResultID),
		"child_task_packet_id": strings.TrimSpace(result.PacketID),
		"child_task_status":    string(result.Status),
		"child_next_state":     string(classification.State),
		"child_result_kind":    strings.TrimSpace(result.ResultKind),
		"child_local_subject":  "false",
		"status":               "blocked",
		"status_source":        "child_task_result",
		"trigger_kinds":        "durable_child,child_task_blocker",
	}
	if adapterName != "" {
		metadata["channel_adapter"] = adapterName
	}
	if toolName != "" {
		metadata["tool_name"] = toolName
	}
	if result.BlockerKind != "" {
		metadata["raw_child_blocker_kind"] = strings.TrimSpace(result.BlockerKind)
	}
	return metadata
}

func durableWakeChildBlockerOperationInputJSON(agentID string, adapterName string, toolName string, classification durableWakeChildBlockerClassification, result session.ChildTaskResultInput) string {
	payload := map[string]any{
		"agent_id":         strings.TrimSpace(agentID),
		"blocker_kind":     strings.TrimSpace(classification.Kind),
		"task_packet_id":   strings.TrimSpace(result.PacketID),
		"child_result_id":  strings.TrimSpace(result.ResultID),
		"diagnostic_only":  true,
		"no_content_probe": classification.Kind == "tool_runtime_not_executable" || classification.Kind == "credential_unverified",
	}
	if adapterName != "" {
		payload["adapter"] = adapterName
	}
	if toolName != "" {
		payload["tool"] = toolName
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func normalizeDurableWakeChildBlockerKind(kind string) string {
	kind = normalizeExternalChannelWakeOutcomeReason(kind)
	if kind == "child_reported_needs_review" {
		return "child_reported_blocked"
	}
	return kind
}

func durableWakeChildBlockerToolName(agent core.DurableAgent, lowerText string) string {
	if token := tokenBeforeMarker(lowerText, "=missing_or_not_executable"); token != "" {
		return token
	}
	if token := tokenAfterMarker(lowerText, "adapter="); token != "" {
		return token
	}
	return strings.TrimSpace(externalChannelAdapter(agent))
}

func tokenBeforeMarker(text string, marker string) string {
	idx := strings.Index(text, marker)
	if idx <= 0 {
		return ""
	}
	start := idx - 1
	for start >= 0 && durableWakeTokenRune(rune(text[start])) {
		start--
	}
	return strings.Trim(text[start+1:idx], " _.-")
}

func tokenAfterMarker(text string, marker string) string {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := start
	for end < len(text) && durableWakeTokenRune(rune(text[end])) {
		end++
	}
	return strings.Trim(text[start:end], " _.-")
}

func durableWakeTokenRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
}
