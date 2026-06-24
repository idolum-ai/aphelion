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
	NoContentProbe     bool
	DiagnosticOnly     bool
}

type durableWakeChildBlockerSpec struct {
	Kind               string
	State              session.NextActionState
	NextAction         string
	RequiredAuthority  string
	ResourceBlocker    string
	RetryPolicy        string
	OperationKind      string
	OperationTool      string
	OperatorProjection string
	ReviewLocalActions []string
	ReviewQuestions    []string
	ReviewRiskFlags    []string
	NoContentProbe     bool
	DiagnosticOnly     bool
	Markers            []string
}

var durableWakeBlockedChildBlockerSpecs = []durableWakeChildBlockerSpec{
	{
		Kind:               "tool_runtime_not_executable",
		State:              session.NextActionBlockedNeedsResourceRepair,
		NextAction:         "materialize or repair the child-local tool runtime, then run one no-content readiness probe",
		ResourceBlocker:    "tool_runtime_not_executable",
		RetryPolicy:        "retry_after_tool_runtime_repair",
		OperationKind:      "child_tool_runtime_repair",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child-local tool runtime is missing or not executable; repair the wrapper/materialization, then run one no-content readiness probe.",
		ReviewLocalActions: []string{"Child verified grants/config, then found the child-local tool runtime missing or not executable."},
		ReviewQuestions:    []string{"Repair the child-local tool runtime materialization, then rerun exactly one no-content readiness probe."},
		ReviewRiskFlags:    []string{"durable_child", "tool_runtime", "no_content_probe_required"},
		NoContentProbe:     true,
		DiagnosticOnly:     true,
		Markers:            []string{"missing_or_not_executable", "not executable", "missing executable", "wrapper_missing"},
	},
	{
		Kind:               "tool_lifecycle_unregistered",
		State:              session.NextActionBlockedNeedsResourceRepair,
		NextAction:         "register, install, audit, and probe the child-local tool lifecycle before retrying",
		ResourceBlocker:    "tool_lifecycle_unregistered",
		RetryPolicy:        "retry_after_tool_lifecycle_repair",
		OperationKind:      "child_tool_lifecycle_repair",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child tool lifecycle is not registered or verified; repair lifecycle records before retrying the child wake.",
		ReviewLocalActions: []string{"Child wake was blocked by tool lifecycle readiness, not by mailbox credentials."},
		ReviewQuestions:    []string{"Repair the tool lifecycle records, then rerun a bounded no-content readiness probe."},
		ReviewRiskFlags:    []string{"durable_child", "tool_lifecycle"},
		DiagnosticOnly:     true,
		Markers:            []string{externalChannelReadinessFailureLife, "lifecycle_unregistered", "install/audit/probe", "tool lifecycle"},
	},
	{
		Kind:               "grant_missing_or_stale",
		State:              session.NextActionBlockedNeedsAuthority,
		NextAction:         "approve or repair the exact grant needed for the child task",
		RequiredAuthority:  "grant_missing_or_stale",
		RetryPolicy:        "retry_after_authority_repair",
		OperationKind:      "child_authority_repair",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child task needs an exact live grant before it can continue.",
		ReviewLocalActions: []string{"Child task stopped before executing because required authority was missing or stale."},
		ReviewQuestions:    []string{"Approve, renew, or reject the exact child grant before retrying."},
		ReviewRiskFlags:    []string{"durable_child", "authority"},
		DiagnosticOnly:     true,
		Markers:            []string{"grant_missing", "grant missing", "grant_expired", "grant expired", "grant_revoked", "grant revoked", "missing_grant"},
	},
	{
		Kind:               "resource_permission_denied",
		State:              session.NextActionBlockedNeedsResourceRepair,
		NextAction:         "repair the child-local resource permission boundary before retrying",
		ResourceBlocker:    "resource_permission_denied",
		RetryPolicy:        "retry_after_resource_repair",
		OperationKind:      "child_resource_repair",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child task has authority but the resource boundary denied the operation; repair the child-local resource path before retry.",
		ReviewLocalActions: []string{"Child task reached the resource boundary and stopped without widening authority."},
		ReviewQuestions:    []string{"Repair the child-local resource boundary, then retry only the bounded child task."},
		ReviewRiskFlags:    []string{"durable_child", "resource_boundary"},
		DiagnosticOnly:     true,
		Markers:            []string{"permission denied", "not writable", "read-only", "readonly", "host_permission_denied"},
	},
	{
		Kind:               "credential_unverified",
		State:              session.NextActionWaitingForOperator,
		NextAction:         "run or review a no-content credential/account-status probe before mailbox work",
		RequiredAuthority:  "credential_status_probe",
		ResourceBlocker:    "credential_unverified",
		RetryPolicy:        "retry_after_credential_verification",
		OperationKind:      "child_credential_probe",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Credential state is not proven; run a no-content status probe before any mailbox action.",
		ReviewLocalActions: []string{"Child task stopped before content access because credential status is not verified."},
		ReviewQuestions:    []string{"Run exactly one no-content credential/account-status probe, then continue only if it passes."},
		ReviewRiskFlags:    []string{"durable_child", "credential_status", "no_content_probe_required"},
		NoContentProbe:     true,
		DiagnosticOnly:     true,
		Markers:            []string{"credential", "oauth", "auth_status", "account_status"},
	},
	{
		Kind:               "external_transient",
		State:              session.NextActionScheduledRetry,
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      "child_retry",
		OperationTool:      "durable_child_repair",
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		ReviewLocalActions: []string{"Child task stopped on a transient external condition; no authority was widened."},
		ReviewQuestions:    []string{"Retry after the bounded backoff if the work is still current."},
		ReviewRiskFlags:    []string{"durable_child", "external_transient"},
		DiagnosticOnly:     true,
		Markers:            []string{"timeout", "temporarily unavailable", "transient"},
	},
}

var durableWakeFailedChildBlockerSpec = durableWakeChildBlockerSpec{
	Kind:               "wake_failed",
	State:              session.NextActionBlockedNeedsResourceRepair,
	NextAction:         "repair the child wake runtime or dependency failure before retrying",
	ResourceBlocker:    "wake_failed",
	RetryPolicy:        "retry_after_wake_repair",
	OperationKind:      "child_wake_repair",
	OperationTool:      "durable_child_repair",
	OperatorProjection: "Child wake failed before a child-authored completion; repair the wake/runtime failure before retrying.",
	ReviewLocalActions: []string{"Durable wake failed before a child-authored terminal result was produced."},
	ReviewQuestions:    []string{"Repair the wake/runtime dependency, then retry the bounded child task."},
	ReviewRiskFlags:    []string{"durable_child", "wake_failed"},
	DiagnosticOnly:     true,
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
	if result.Status == session.ChildTaskResultCompleted {
		return durableWakeChildBlockerClassification{State: session.NextActionTerminal}
	}
	if blockerKind == "" && result.Status == session.ChildTaskResultUpdate {
		blockerKind = "child_task_update"
	}
	if blockerKind == "" && result.Status == session.ChildTaskResultFailed {
		blockerKind = "wake_failed"
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
		DiagnosticOnly:     true,
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
		classification.OperationKind = "child_task_continue"
		classification.OperationTool = "durable_child_continuation"
		classification.DiagnosticOnly = false
		classification.OperatorProjection = "Child task reported an intermediate update; continue only through the bounded child task packet."
		classification.ReviewLocalActions = []string{"Child task reported an intermediate update and remains open for bounded continuation."}
		classification.ReviewQuestions = []string{"Continue the child task only if the latest update still matches current intent."}
	case session.ChildTaskResultBlocked:
		classification = durableWakeBlockedChildClassification(classification, text)
	case session.ChildTaskResultFailed:
		classification = durableWakeApplyChildBlockerSpec(classification, durableWakeFailedChildBlockerSpec)
	}
	if classification.Kind != "" {
		classification.ReviewSummary = durableWakeChildBlockerReviewSummary(agentID, classification, result)
		classification.ReviewMetadata = durableWakeChildBlockerReviewMetadata(agentID, adapterName, toolName, classification, result)
		classification.OperationInputJSON = durableWakeChildBlockerOperationInputJSON(agentID, adapterName, toolName, classification, result)
	}
	return classification
}

func durableWakeBlockedChildClassification(base durableWakeChildBlockerClassification, text string) durableWakeChildBlockerClassification {
	for _, spec := range durableWakeBlockedChildBlockerSpecs {
		if durableWakeTextMatchesAny(text, spec.Markers) {
			return durableWakeApplyChildBlockerSpec(base, spec)
		}
	}
	base.Kind = firstNonEmpty(base.Kind, "child_reported_blocked")
	base.State = session.NextActionWaitingForOperator
	base.NextAction = "review the child-authored blocker and choose an exact repair"
	base.ResourceBlocker = base.Kind
	base.RetryPolicy = "operator_disambiguation_required"
	base.OperationKind = "child_blocker_disambiguation"
	base.OperationTool = "durable_child_repair"
	base.DiagnosticOnly = true
	base.OperatorProjection = "Child reported a blocker that does not compile to a known repair class; inspect the child result and choose an exact repair."
	return base
}

func durableWakeApplyChildBlockerSpec(base durableWakeChildBlockerClassification, spec durableWakeChildBlockerSpec) durableWakeChildBlockerClassification {
	base.Kind = spec.Kind
	base.State = spec.State
	base.NextAction = spec.NextAction
	base.RequiredAuthority = spec.RequiredAuthority
	base.ResourceBlocker = spec.ResourceBlocker
	base.RetryPolicy = spec.RetryPolicy
	base.OperationKind = spec.OperationKind
	base.OperationTool = spec.OperationTool
	base.OperatorProjection = spec.OperatorProjection
	base.ReviewLocalActions = spec.ReviewLocalActions
	base.ReviewQuestions = spec.ReviewQuestions
	base.ReviewRiskFlags = spec.ReviewRiskFlags
	base.NoContentProbe = spec.NoContentProbe
	base.DiagnosticOnly = spec.DiagnosticOnly
	return base
}

func durableWakeTextMatchesAny(text string, markers []string) bool {
	for _, marker := range markers {
		if marker != "" && strings.Contains(text, marker) {
			return true
		}
	}
	return false
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
		"diagnostic_only":  classification.DiagnosticOnly,
		"no_content_probe": classification.NoContentProbe,
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
