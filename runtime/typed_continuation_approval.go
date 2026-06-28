//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) maybeHandleTypedContinuationApproval(ctx context.Context, msg core.InboundMessage, actor principal.Principal) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || msg.ChatID == 0 || msg.Origin == core.InboundOriginTurnAuthorization {
		return false, nil, nil
	}
	if !isTypedContinuationApprovalText(msg.Text) {
		return false, nil, nil
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, nil, err
	}
	state = session.NormalizeContinuationState(state)
	if !exists || state.Status != session.ContinuationStatusPending || state.RemainingTurns <= 0 {
		return false, nil, nil
	}
	approved, err := r.ApproveContinuationForKey(key, actor.TelegramUserID)
	if err != nil {
		if errors.Is(err, core.ErrContinuationExpired) {
			if _, _, refreshErr := r.RefreshContinuationProposalForKey(ctx, key, "expired typed approval"); refreshErr != nil {
				return true, nil, refreshErr
			}
			return true, &core.TurnResult{Text: "The prior approval expired; I sent a fresh approval prompt."}, nil
		}
		return true, nil, err
	}
	if approved.Status == session.ContinuationStatusApproved && approved.RemainingTurns > 0 {
		if err := r.TriggerContinuationForKey(ctx, key); err != nil {
			return true, nil, err
		}
	}
	return true, &core.TurnResult{Text: "Approved continuation."}, nil
}

func (r *Runtime) maybeHandleApprovedContinuationRunIntent(ctx context.Context, msg core.InboundMessage, actor principal.Principal) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || msg.ChatID == 0 {
		return false, nil, nil
	}
	if actor.Role != principal.RoleAdmin {
		return false, nil, nil
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
	if msg.Origin == core.InboundOriginTurnAuthorization {
		state, exists, err := r.store.ContinuationStateIfExists(key)
		if err != nil {
			return false, nil, err
		}
		state = session.NormalizeContinuationState(state)
		if exists && state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
			retry := session.NormalizeContinuationRetryOperation(state.ContinuationLease.RetryOperation)
			if retry.Active() {
				result, err := r.triggerContinuationLoopWithResult(ctx, key)
				if err != nil {
					return true, nil, err
				}
				if !result.Ran {
					return true, &core.TurnResult{Text: approvedContinuationRunNoopText(result.State)}, nil
				}
				return true, &core.TurnResult{Text: "Running approved continuation."}, nil
			}
		}
		if !exists || state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
			if handled, result, err := r.maybeMaterializeChildWakeRepairRetryApproval(ctx, key, msg, actor, true, true); handled {
				return true, result, err
			}
		}
		return false, nil, nil
	}
	if isChildWakeRepairRetryApprovalText(msg.Text) {
		if handled, result, err := r.maybeMaterializeChildWakeRepairRetryApproval(ctx, key, msg, actor, true, false); handled {
			return true, result, err
		}
	}
	if !isApprovedContinuationRunText(msg.Text) && !isChildWakeRepairAdvanceText(msg.Text) {
		return false, nil, nil
	}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, nil, err
	}
	state = session.NormalizeContinuationState(state)
	if !exists || state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		if handled, result, err := r.maybeMaterializeChildWakeRepairRetryApproval(ctx, key, msg, actor, true, false); handled {
			return true, result, err
		}
		return false, nil, nil
	}
	result, err := r.triggerContinuationLoopWithResult(ctx, key)
	if err != nil {
		return true, nil, err
	}
	if !result.Ran {
		return true, &core.TurnResult{Text: approvedContinuationRunNoopText(result.State)}, nil
	}
	return true, &core.TurnResult{Text: "Running approved continuation."}, nil
}

func (r *Runtime) maybeMaterializeChildWakeRepairRetryApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, allowRunIntent bool, allowTypedActionWithoutText bool) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || actor.Role != principal.RoleAdmin {
		return false, nil, nil
	}
	if !allowTypedActionWithoutText && !isChildWakeRepairRetryApprovalText(msg.Text) && !(allowRunIntent && isChildWakeRepairAdvanceText(msg.Text)) {
		return false, nil, nil
	}
	action, ok, err := r.currentChildWakeRetryableAction(key)
	if err != nil || !ok {
		return false, nil, err
	}
	contract, err := r.childWakeRepairRetryRecoveryContract(key, msg, action)
	if err != nil {
		return true, nil, err
	}
	contract, err = r.store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		return true, nil, fmt.Errorf("store child_wake repair retry contract: %w", err)
	}
	now := time.Now().UTC()
	if err := r.ensureChildWakeRepairRetryOperationState(key, contract, now); err != nil {
		return true, nil, err
	}
	handoff, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "approved_retry",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         contract.SubjectRef,
		CausalRefs:         []string{"next_action:" + strings.TrimSpace(action.RecordID), "contract:" + contract.ContractID},
		NextAction:         "approve one fresh bounded child_wake continuation before retrying wake_once",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: session.ContinuationRecoveryContractProjectionInput(contract.ContractID),
		OperatorProjection: "The prior child_wake runtime blocker has been repaired. Approve one fresh bounded child_wake continuation before retrying durable_agent wake_once.",
		CreatedAt:          now,
	})
	if err != nil {
		return true, nil, fmt.Errorf("record child_wake repair retry approval handoff: %w", err)
	}
	if err := r.materializeChildWakeRepairRetryApprovalHandoff(ctx, key, msg, actor, action, handoff, now); err != nil {
		return true, nil, err
	}
	return true, &core.TurnResult{Text: "I sent a fresh bounded child_wake approval prompt."}, nil
}

func (r *Runtime) materializeChildWakeRepairRetryApprovalHandoff(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, repairAction session.NextActionRecord, handoff session.NextActionRecord, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, ok := r.continuationApprovalPromptSender(); !ok {
		return fmt.Errorf("runtime outbound does not support inline continuation prompts")
	}
	tools := r.toolsForPrincipal(actor, key)
	if tools == nil {
		return fmt.Errorf("request_approval tool is unavailable for child_wake repair retry")
	}
	unlock := r.lockSession(key)
	defer unlock()
	if _, err := tools.Execute(ctx, "request_approval", json.RawMessage(handoff.OperationInputJSON)); err != nil {
		return fmt.Errorf("materialize child_wake repair retry handoff %s: %w", strings.TrimSpace(handoff.RecordID), err)
	}
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return fmt.Errorf("load child_wake repair retry continuation: %w", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending || state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		return fmt.Errorf("child_wake repair retry produced non-pending child_wake continuation")
	}
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = "child_wake_repair_retry"
	payload["repair_next_action_id"] = strings.TrimSpace(repairAction.RecordID)
	payload["handoff_next_action_id"] = strings.TrimSpace(handoff.RecordID)
	if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "child_wake_repair_retry", payload, now); err != nil {
		return err
	}
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		RecordID:    handoff.RecordID,
		Key:         key,
		Owner:       "runtime",
		SubjectKind: handoff.SubjectKind,
		SubjectRef:  handoff.SubjectRef,
		Reason:      "recovery_handoff_materialized",
		ResolvedAt:  now,
	}); err != nil {
		return fmt.Errorf("resolve child_wake repair retry handoff after approval request: %w", err)
	}
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		RecordID:    repairAction.RecordID,
		Key:         key,
		Owner:       "runtime",
		SubjectKind: repairAction.SubjectKind,
		SubjectRef:  repairAction.SubjectRef,
		Reason:      "repair_retry_approval_requested",
		ResolvedAt:  now,
	}); err != nil {
		return fmt.Errorf("resolve child_wake repair blocker after approval request: %w", err)
	}
	return nil
}

func (r *Runtime) ensureChildWakeRepairRetryOperationState(key session.SessionKey, contract session.ContinuationRecoveryContract, now time.Time) error {
	if _, err := r.store.OperationState(key); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load child_wake repair retry operation state: %w", err)
	}
	agentID := strings.TrimSpace(contract.AgentID)
	objective := "Retry child wake once through the approved continuation path."
	if agentID != "" {
		objective = "Retry " + agentID + " wake once through the approved continuation path."
	}
	return r.store.UpdateOperationState(key, session.OperationState{
		ID:        "op-child-wake-repair-retry-" + session.EffectAttemptCommandHash(contract.ContractID)[7:23],
		Objective: objective,
		Status:    session.OperationStatusBlocked,
		Stage:     "child_wake_repair_approval_request",
		Summary:   "Fresh bounded child_wake approval needed after wake runtime repair.",
		UpdatedAt: now,
	})
}

func (r *Runtime) currentChildWakeRetryableAction(key session.SessionKey) (session.NextActionRecord, bool, error) {
	actions, err := r.store.OpenNextActionsBySession(key, 100)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	for _, action := range actions {
		if childWakeActionCanRequestRetry(action) {
			return action, true, nil
		}
	}
	return session.NextActionRecord{}, false, nil
}

func childWakeActionCanRequestRetry(action session.NextActionRecord) bool {
	if strings.TrimSpace(action.SubjectKind) != "continuation_lease_request" ||
		childWakeRepairAgentIDFromSubjectRef(action.SubjectRef) == "" {
		return false
	}
	operationKind := strings.TrimSpace(action.OperationKind)
	operationTool := strings.TrimSpace(action.OperationTool)
	switch action.State {
	case session.NextActionBlockedNeedsResourceRepair:
		switch operationKind {
		case "child_wake_runtime_repair":
			return operationTool == "durable_agent"
		case "child_tool_runtime_repair":
			return operationTool == "update_operation" && strings.TrimSpace(action.ResourceBlocker) == "tool_runtime_not_executable"
		default:
			return false
		}
	case session.NextActionWaitingForOperator:
		return childWakeOperatorFollowupActionCanRequestRetry(action)
	default:
		return false
	}
}

func childWakeOperatorFollowupActionCanRequestRetry(action session.NextActionRecord) bool {
	if strings.TrimSpace(action.OperationTool) != "update_operation" {
		return false
	}
	if strings.TrimSpace(action.OperationKind) != "child_credential_probe" {
		return false
	}
	if strings.TrimSpace(action.ResourceBlocker) != "credential_unverified" {
		return false
	}
	var input struct {
		RecoveryContract      string `json:"recovery_contract"`
		RecoveryOperationKind string `json:"recovery_operation_kind"`
		AgentID               string `json:"agent_id"`
		DurableAgentID        string `json:"durable_agent_id"`
		NoContentProbe        bool   `json:"no_content_probe"`
		DiagnosticOnly        bool   `json:"diagnostic_only"`
		RecoveryHandoff       struct {
			Contract          string `json:"contract"`
			OperationKind     string `json:"operation_kind"`
			AgentID           string `json:"agent_id"`
			DurableAgentID    string `json:"durable_agent_id"`
			NoContentProbe    bool   `json:"no_content_probe"`
			DiagnosticOnly    bool   `json:"diagnostic_only"`
			ResourceBlocker   string `json:"resource_blocker"`
			BlockerKind       string `json:"blocker_kind"`
			RequiredAuthority string `json:"required_authority"`
		} `json:"recovery_handoff"`
	}
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return false
	}
	if strings.TrimSpace(input.RecoveryContract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(input.RecoveryOperationKind) != strings.TrimSpace(action.OperationKind) {
		return false
	}
	if strings.TrimSpace(input.RecoveryHandoff.Contract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(input.RecoveryHandoff.OperationKind) != strings.TrimSpace(action.OperationKind) {
		return false
	}
	if !input.NoContentProbe || !input.RecoveryHandoff.NoContentProbe {
		return false
	}
	if !input.DiagnosticOnly || !input.RecoveryHandoff.DiagnosticOnly {
		return false
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(input.DurableAgentID)
	}
	handoffAgentID := strings.TrimSpace(input.RecoveryHandoff.AgentID)
	if handoffAgentID == "" {
		handoffAgentID = strings.TrimSpace(input.RecoveryHandoff.DurableAgentID)
	}
	subjectAgentID := childWakeRepairAgentIDFromSubjectRef(action.SubjectRef)
	if agentID == "" || handoffAgentID == "" || subjectAgentID == "" {
		return false
	}
	if agentID != subjectAgentID || handoffAgentID != subjectAgentID {
		return false
	}
	blocker := firstNonEmptyContinuation(strings.TrimSpace(input.RecoveryHandoff.ResourceBlocker), strings.TrimSpace(input.RecoveryHandoff.BlockerKind), strings.TrimSpace(action.ResourceBlocker))
	if blocker != "credential_unverified" {
		return false
	}
	return true
}

func (r *Runtime) childWakeRepairRetryRecoveryContract(key session.SessionKey, msg core.InboundMessage, action session.NextActionRecord) (session.ContinuationRecoveryContract, error) {
	var payload struct {
		Action            string `json:"action"`
		AgentID           string `json:"agent_id"`
		DurableAgentID    string `json:"durable_agent_id"`
		RequestInstanceID string `json:"request_instance_id"`
	}
	_ = json.Unmarshal([]byte(action.OperationInputJSON), &payload)
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(payload.DurableAgentID)
	}
	if agentID == "" {
		agentID = childWakeRepairAgentIDFromSubjectRef(action.SubjectRef)
	}
	if agentID == "" {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("child_wake repair retry requires agent_id")
	}
	grantID := childWakeRepairGrantIDFromSubjectRef(action.SubjectRef)
	targetResource := "durable_agent:" + agentID + ":wake_once"
	requestToken := session.EffectAttemptCommandHash(strings.Join([]string{
		strings.TrimSpace(action.RecordID),
		strings.TrimSpace(payload.RequestInstanceID),
		strings.TrimSpace(msg.Text),
		fmt.Sprintf("%d", msg.MessageID),
	}, "\x00"))[7:31]
	principalID := operationPhaseRecoveryPrincipal(msg, key)
	if principalID == "" {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("child_wake repair retry requires operator principal")
	}
	return session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "repair-retry-" + requestToken,
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, grantID, "durable_agent", "wake_once", ""),
		Principal:           principalID,
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": agentID},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             grantID,
		GrantTargetResource: targetResource,
		CreatedAt:           time.Now().UTC(),
	})
}

func isTypedContinuationApprovalText(text string) bool {
	value := strings.ToLower(strings.TrimSpace(text))
	value = strings.Trim(value, ".! \t\r\n")
	switch value {
	case "approve", "approved", "yes approve", "yes approved", "approved yes", "ok approve", "ok approved":
		return true
	default:
		return false
	}
}

func isApprovedContinuationRunText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || approvedContinuationRunTextNegated(value) {
		return false
	}
	switch value {
	case "continue", "please continue", "continue please",
		"run", "run it", "please run", "run please",
		"resume", "resume it", "please resume", "resume please",
		"proceed", "please proceed", "proceed please",
		"go ahead", "yes continue", "ok continue", "yes run", "ok run":
		return true
	}
	for _, prefix := range []string{
		"continue approved",
		"continue the approved",
		"continue with approved",
		"run approved",
		"run the approved",
		"run with approved",
		"resume approved",
		"resume the approved",
		"resume with approved",
	} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isChildWakeRepairRetryApprovalText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || childWakeRepairRetryTextNegated(value) {
		return false
	}
	return strings.Contains(value, "retry") &&
		strings.Contains(value, "wake") &&
		(strings.Contains(value, "approved continuation") ||
			strings.Contains(value, "approval") ||
			strings.Contains(value, "approve"))
}

func isChildWakeRepairAdvanceText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || childWakeRepairRetryTextNegated(value) {
		return false
	}
	if isApprovedContinuationRunText(text) || isChildWakeRepairRetryApprovalText(text) {
		return true
	}
	if !strings.Contains(value, "continue") && !strings.Contains(value, "proceed") && !strings.Contains(value, "resume") {
		return false
	}
	for _, marker := range []string{
		"child tool runtime repair",
		"child_tool_runtime_repair",
		"tool runtime",
		"runtime bin",
		"runtime material",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func childWakeRepairRetryTextNegated(value string) bool {
	for _, phrase := range []string{
		"do not retry",
		"don't retry",
		"dont retry",
		"do not wake",
		"don't wake",
		"dont wake",
		"no retry",
		"no wake",
		"not now",
		"pause",
	} {
		if value == phrase || strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func childWakeRepairAgentIDFromSubjectRef(subjectRef string) string {
	parts := strings.Split(strings.TrimSpace(subjectRef), ":")
	if len(parts) >= 2 && parts[0] == string(session.ContinuationLeaseClassChildWake) {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func childWakeRepairGrantIDFromSubjectRef(subjectRef string) string {
	parts := strings.Split(strings.TrimSpace(subjectRef), ":")
	if len(parts) >= 3 && parts[0] == string(session.ContinuationLeaseClassChildWake) {
		return strings.TrimSpace(parts[2])
	}
	return ""
}

func normalizeContinuationControlText(text string) string {
	value := strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		"\t", " ",
		"\n", " ",
		"\r", " ",
		".", " ",
		",", " ",
		"!", " ",
		"?", " ",
		";", " ",
		":", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(value)), " ")
}

func approvedContinuationRunTextNegated(value string) bool {
	for _, phrase := range []string{
		"do not continue",
		"don't continue",
		"dont continue",
		"do not run",
		"don't run",
		"dont run",
		"do not resume",
		"don't resume",
		"dont resume",
		"do not proceed",
		"don't proceed",
		"dont proceed",
		"not now",
		"no continue",
		"no run",
		"pause",
		"stop",
	} {
		if value == phrase || strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func approvedContinuationRunNoopText(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired ||
		state.ActionProposal.Status == session.ProposalStatusExpired {
		return "The approved continuation expired before it could run."
	}
	if state.Status != session.ContinuationStatusApproved {
		return "No approved continuation is currently runnable."
	}
	if state.RemainingTurns <= 0 || state.ContinuationLease.RemainingTurns <= 0 ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusConsumed {
		return "The approved continuation has no remaining turns."
	}
	return "The approved continuation is not currently runnable."
}
