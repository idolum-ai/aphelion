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
		if inboundOriginDetailLabel(msg) != string(session.TurnAuthorizationKindContinuation) {
			return false, nil, nil
		}
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
			if handled, result, err := r.maybeMaterializePendingContinuationApproval(ctx, key, msg, actor, true); handled {
				return true, result, err
			}
		}
		return false, nil, nil
	}
	if childWakeRepairRetryTextNegated(normalizeContinuationControlText(msg.Text)) {
		return false, nil, nil
	}
	if positiveReactionShouldSurfacePendingApproval(msg) {
		if handled, result, err := r.maybeMaterializePendingContinuationApproval(ctx, key, msg, actor, true); handled {
			return true, result, err
		}
	}
	if isChildWakeRepairRetryApprovalText(msg.Text) || isChildWakeRepairAdvanceText(msg.Text) {
		if handled, result, err := r.maybeMaterializeChildWakeRepairRetryApproval(ctx, key, msg, actor, true, false); handled {
			return true, result, err
		}
	}
	if isPendingContinuationApprovalSurfaceText(msg.Text) {
		if handled, result, err := r.maybeMaterializePendingContinuationApproval(ctx, key, msg, actor, false); handled {
			return true, result, err
		}
	}
	return false, nil, nil
}

func positiveReactionShouldSurfacePendingApproval(msg core.InboundMessage) bool {
	if msg.Reaction == nil || len(msg.Reaction.New) == 0 {
		return false
	}
	for _, reaction := range msg.Reaction.New {
		switch strings.TrimSpace(reaction) {
		case "👍", "+1":
			return true
		}
	}
	return false
}

func isPendingContinuationApprovalSurfaceText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || childWakeRepairRetryTextNegated(value) || pendingApprovalTextNegated(value) {
		return false
	}
	if !strings.Contains(value, "approval") && !strings.Contains(value, "approve") {
		return false
	}
	if strings.Contains(value, "approve") || strings.Contains(value, "approved") {
		return true
	}
	if strings.Contains(value, "card") || strings.Contains(value, "prompt") {
		return true
	}
	for _, marker := range []string{"surface", "show", "send", "materialize", "display"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func pendingApprovalTextNegated(value string) bool {
	for _, phrase := range []string{
		"do not approve",
		"don't approve",
		"dont approve",
		"do not show approval",
		"don't show approval",
		"dont show approval",
		"no approval",
		"not approved",
	} {
		if value == phrase || strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func (r *Runtime) maybeMaterializePendingContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, allowTypedActionWithoutText bool) (bool, *core.TurnResult, error) {
	if handled, result, err := r.maybeMaterializeChildWakeRepairRetryApproval(ctx, key, msg, actor, true, allowTypedActionWithoutText); handled {
		return true, result, err
	}
	if handled, result, err := r.maybeMaterializeReferencedAuthorityBundleApproval(ctx, key, msg, actor); handled {
		return true, result, err
	}
	materializable, err := r.hasMaterializablePendingContinuationApproval(key, time.Now().UTC())
	if err != nil {
		return true, nil, err
	}
	if !materializable {
		return false, nil, nil
	}
	materialized, err := r.MaterializeRequestedApproval(ctx, key, msg, msg.Text)
	if err != nil {
		return true, nil, err
	}
	if materialized {
		return true, &core.TurnResult{Text: "I surfaced the pending continuation approval."}, nil
	}
	return false, nil, nil
}

func (r *Runtime) maybeMaterializeReferencedAuthorityBundleApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || actor.Role != principal.RoleAdmin {
		return false, nil, nil
	}
	bundleID, ok := authorityBundleIDFromApprovalText(msg.Text)
	if !ok {
		return false, nil, nil
	}
	bundle, found, err := r.store.AuthorityBundleContract(bundleID)
	if err != nil {
		return true, nil, err
	}
	if !found {
		return true, &core.TurnResult{Text: "I could not find that recorded authority bundle."}, nil
	}
	bundle = session.NormalizeAuthorityBundleContract(bundle)
	if bundle.SessionID != "" && bundle.SessionID != session.SessionIDForKey(key) {
		return true, &core.TurnResult{Text: "That authority bundle belongs to a different session."}, nil
	}
	now := time.Now().UTC()
	if !bundle.ExpiresAt.IsZero() && !now.Before(bundle.ExpiresAt.UTC()) {
		return true, &core.TurnResult{Text: "That authority bundle expired; ask me to prepare a fresh bounded approval."}, nil
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		return true, nil, err
	}
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		RecordID:           session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, now),
		Key:                key,
		Owner:              "runtime",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"authority_bundle:" + bundle.BundleID},
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review bounded authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		return true, nil, fmt.Errorf("record referenced authority bundle approval handoff: %w", err)
	}
	materialized, err := r.MaterializeRequestedApproval(ctx, key, msg, msg.Text)
	if err != nil {
		return true, nil, err
	}
	if materialized {
		return true, &core.TurnResult{Text: "I surfaced the referenced authority bundle approval."}, nil
	}
	return true, &core.TurnResult{Text: "I recorded the referenced authority bundle approval request."}, nil
}

func authorityBundleIDFromApprovalText(text string) (string, bool) {
	value := strings.TrimSpace(text)
	if value == "" || !isPendingContinuationApprovalSurfaceText(value) {
		return "", false
	}
	found := ""
	for _, field := range strings.Fields(value) {
		token := strings.Trim(field, "`'\".,:;()[]{}<>")
		if !strings.HasPrefix(token, "authbundle-") {
			continue
		}
		if found != "" && found != token {
			return "", false
		}
		found = token
	}
	return found, found != ""
}

func (r *Runtime) hasMaterializablePendingContinuationApproval(key session.SessionKey, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, operationKind := range []string{"continuation_lease_request", "authority_bundle_request"} {
		if actions, err := r.store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", operationKind, 1); err != nil {
			return false, err
		} else if len(actions) > 0 {
			return true, nil
		}
	}
	_, opState, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	opState = session.NormalizeOperationState(opState)
	if pendingOperationProposalNeedsButton(opState.Proposal) || pendingOperationPlanLeaseNeedsButton(opState.PlanLease) || operationHasMaterializablePhaseApproval(opState) {
		return true, nil
	}
	return false, nil
}

func (r *Runtime) maybeMaterializeChildWakeRepairRetryApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, allowRunIntent bool, allowTypedActionWithoutText bool) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || actor.Role != principal.RoleAdmin {
		return false, nil, nil
	}
	if !allowTypedActionWithoutText && !isChildWakeRepairRetryApprovalText(msg.Text) && !(allowRunIntent && isChildWakeRepairAdvanceText(msg.Text)) {
		return false, nil, nil
	}
	action, ok, err := r.currentChildWakeRetryableAction(key, msg)
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
	payload["materialized_from"] = session.NextActionOperationKindDurableChildRecovery + "_retry"
	payload["repair_next_action_id"] = strings.TrimSpace(repairAction.RecordID)
	payload["handoff_next_action_id"] = strings.TrimSpace(handoff.RecordID)
	if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), session.NextActionOperationKindDurableChildRecovery+"_retry", payload, now); err != nil {
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
	repairActionKey := nextActionRecordSessionKey(repairAction, key)
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		RecordID:    repairAction.RecordID,
		Key:         repairActionKey,
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

func nextActionRecordSessionKey(action session.NextActionRecord, fallback session.SessionKey) session.SessionKey {
	key := session.SessionKey{
		ChatID: action.ChatID,
		UserID: action.UserID,
		Scope:  action.Scope,
	}
	if !key.Scope.IsZero() || key.ChatID != 0 || key.UserID != 0 {
		return key
	}
	return fallback
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
		Stage:     session.NextActionOperationKindDurableChildRecovery + "_approval_request",
		Summary:   "Fresh bounded child_wake approval needed after wake runtime repair.",
		UpdatedAt: now,
	})
}

func (r *Runtime) currentChildWakeRetryableAction(key session.SessionKey, msg core.InboundMessage) (session.NextActionRecord, bool, error) {
	actions, err := r.store.OpenNextActionsBySession(key, 100)
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	if action, ok := firstChildWakeRetryableAction(actions, msg); ok {
		return action, true, nil
	}
	action, ok, err := r.currentReviewedChildWakeRetryableAction(key, msg)
	if err != nil || ok {
		return action, ok, err
	}
	return session.NextActionRecord{}, false, nil
}

func (r *Runtime) currentReviewedChildWakeRetryableAction(key session.SessionKey, msg core.InboundMessage) (session.NextActionRecord, bool, error) {
	if r == nil || r.store == nil || key.ChatID == 0 {
		return session.NextActionRecord{}, false, nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return session.NextActionRecord{}, false, err
	}
	wantedAgent := childWakeRetryMentionedAgentID(msg.Text, agents)
	var candidates []session.NextActionRecord
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" || (strings.TrimSpace(agent.Status) != "" && strings.TrimSpace(agent.Status) != "active") {
			continue
		}
		if agent.ReviewTargetChatID != key.ChatID {
			continue
		}
		if wantedAgent != "" && wantedAgent != agentID {
			continue
		}
		childKey := r.durableAgentExecutionKey(agentID)
		actions, err := r.store.OpenNextActionsBySession(childKey, 100)
		if err != nil {
			return session.NextActionRecord{}, false, err
		}
		for _, action := range actions {
			if childWakeActionCanRequestRetry(action) {
				candidates = append(candidates, action)
			}
		}
	}
	if len(candidates) == 0 {
		return session.NextActionRecord{}, false, nil
	}
	if wantedAgent == "" && len(candidates) > 1 {
		return session.NextActionRecord{}, false, nil
	}
	return newestNextActionRecord(candidates), true, nil
}

func firstChildWakeRetryableAction(actions []session.NextActionRecord, msg core.InboundMessage) (session.NextActionRecord, bool) {
	wantedAgent := childWakeRetryMentionedAgentIDFromActions(msg.Text, actions)
	var candidates []session.NextActionRecord
	for _, action := range actions {
		if !childWakeActionCanRequestRetry(action) {
			continue
		}
		if wantedAgent != "" && childWakeRetryActionAgentID(action) != wantedAgent {
			continue
		}
		candidates = append(candidates, action)
	}
	if len(candidates) == 0 {
		return session.NextActionRecord{}, false
	}
	return newestNextActionRecord(candidates), true
}

func newestNextActionRecord(actions []session.NextActionRecord) session.NextActionRecord {
	if len(actions) == 0 {
		return session.NextActionRecord{}
	}
	newest := actions[0]
	for _, action := range actions[1:] {
		if action.CreatedAt.After(newest.CreatedAt) {
			newest = action
		}
	}
	return newest
}

func childWakeRetryMentionedAgentID(text string, agents []core.DurableAgent) string {
	value := normalizeContinuationControlText(text)
	if value == "" {
		return ""
	}
	matched := ""
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" || !strings.Contains(value, strings.ToLower(agentID)) {
			continue
		}
		if matched != "" && matched != agentID {
			return ""
		}
		matched = agentID
	}
	return matched
}

func childWakeRetryMentionedAgentIDFromActions(text string, actions []session.NextActionRecord) string {
	value := normalizeContinuationControlText(text)
	if value == "" {
		return ""
	}
	matched := ""
	for _, action := range actions {
		agentID := childWakeRetryActionAgentID(action)
		if agentID == "" || !strings.Contains(value, strings.ToLower(agentID)) {
			continue
		}
		if matched != "" && matched != agentID {
			return ""
		}
		matched = agentID
	}
	return matched
}

func childWakeActionCanRequestRetry(action session.NextActionRecord) bool {
	operationKind := strings.TrimSpace(action.OperationKind)
	operationTool := strings.TrimSpace(action.OperationTool)
	if operationKind != session.NextActionOperationKindDurableChildRecovery || operationTool != "update_operation" {
		return false
	}
	if strings.TrimSpace(action.SubjectKind) == "task_packet" {
		return childWakeTaskPacketRetryActionCanRequestRetry(action)
	}
	if strings.TrimSpace(action.SubjectKind) != "continuation_lease_request" ||
		childWakeRepairAgentIDFromSubjectRef(action.SubjectRef) == "" {
		return false
	}
	switch action.State {
	case session.NextActionBlockedNeedsResourceRepair:
		switch strings.TrimSpace(action.ResourceBlocker) {
		case "tool_runtime_not_executable", "child_task_attempt_claim_failed", "wake_failed":
			return true
		}
	case session.NextActionWaitingForOperator:
		return childWakeOperatorFollowupActionCanRequestRetry(action)
	}
	return false
}

func childWakeTaskPacketRetryActionCanRequestRetry(action session.NextActionRecord) bool {
	if action.State != session.NextActionScheduledRetry ||
		strings.TrimSpace(action.ResourceBlocker) != "external_transient" ||
		strings.TrimSpace(action.RetryPolicy) != "bounded_backoff" {
		return false
	}
	return childWakeRetryActionAgentID(action) != ""
}

func childWakeOperatorFollowupActionCanRequestRetry(action session.NextActionRecord) bool {
	if strings.TrimSpace(action.OperationTool) != "update_operation" {
		return false
	}
	if strings.TrimSpace(action.OperationKind) != session.NextActionOperationKindDurableChildRecovery {
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
		strings.TrimSpace(input.RecoveryOperationKind) != session.NextActionOperationKindDurableChildRecovery {
		return false
	}
	if strings.TrimSpace(input.RecoveryHandoff.Contract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(input.RecoveryHandoff.OperationKind) != session.NextActionOperationKindDurableChildRecovery {
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
		agentID = childWakeRetryActionAgentID(action)
	}
	if agentID == "" {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("child_wake repair retry requires agent_id")
	}
	principalID := operationPhaseRecoveryPrincipal(msg, key)
	if principalID == "" {
		return session.ContinuationRecoveryContract{}, fmt.Errorf("child_wake repair retry requires operator principal")
	}
	grantID := childWakeRepairGrantIDFromSubjectRef(action.SubjectRef)
	targetResource := "durable_agent:" + agentID + ":wake_once"
	if grantID == "" {
		grant, ok, err := r.activeChildWakeRetryGrant(agentID, principalID)
		if err != nil {
			return session.ContinuationRecoveryContract{}, err
		}
		if !ok {
			return session.ContinuationRecoveryContract{}, fmt.Errorf("child_wake repair retry requires an active exact durable_agent wake_once grant for %s", agentID)
		}
		grantID = strings.TrimSpace(grant.GrantID)
		targetResource = strings.TrimSpace(grant.TargetResource)
	}
	requestToken := session.EffectAttemptCommandHash(strings.Join([]string{
		strings.TrimSpace(action.RecordID),
		strings.TrimSpace(payload.RequestInstanceID),
		strings.TrimSpace(msg.Text),
		fmt.Sprintf("%d", msg.MessageID),
	}, "\x00"))[7:31]
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

func (r *Runtime) activeChildWakeRetryGrant(agentID string, principalID string) (session.CapabilityGrant, bool, error) {
	if r == nil || r.store == nil {
		return session.CapabilityGrant{}, false, nil
	}
	agentID = strings.TrimSpace(agentID)
	principalID = strings.TrimSpace(principalID)
	if agentID == "" || principalID == "" {
		return session.CapabilityGrant{}, false, nil
	}
	targetResource := "durable_agent:" + agentID + ":wake_once"
	grants, err := r.store.ActiveCapabilityGrants(session.CapabilityKindGenericDelegation, targetResource, principalID, "invoke")
	if err != nil {
		return session.CapabilityGrant{}, false, err
	}
	for _, grant := range grants {
		if grantedAgent := childWakeAgentIDFromGrantConstraints(grant.Constraints); grantedAgent != "" && grantedAgent != agentID {
			continue
		}
		return grant, true, nil
	}
	return session.CapabilityGrant{}, false, nil
}

func childWakeAgentIDFromGrantConstraints(raw string) string {
	var constraints map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &constraints); err != nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(constraints["agent_id"]))
}

func childWakeRetryActionAgentID(action session.NextActionRecord) string {
	agentID := childWakeRepairAgentIDFromSubjectRef(action.SubjectRef)
	if agentID != "" {
		return agentID
	}
	if strings.TrimSpace(action.Scope.DurableAgentID) != "" {
		return strings.TrimSpace(action.Scope.DurableAgentID)
	}
	var payload struct {
		AgentID         string `json:"agent_id"`
		DurableAgentID  string `json:"durable_agent_id"`
		RecoveryAction  string `json:"recovery_action"`
		RecoveryFamily  string `json:"recovery_family"`
		RecoveryHandoff struct {
			AgentID         string `json:"agent_id"`
			DurableAgentID  string `json:"durable_agent_id"`
			BlockerKind     string `json:"blocker_kind"`
			ResourceBlocker string `json:"resource_blocker"`
		} `json:"recovery_handoff"`
	}
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.RecoveryFamily) != "" &&
		strings.TrimSpace(payload.RecoveryFamily) != session.NextActionOperationKindDurableChildRecovery {
		return ""
	}
	blocker := firstNonEmptyContinuation(strings.TrimSpace(payload.RecoveryAction), strings.TrimSpace(payload.RecoveryHandoff.BlockerKind), strings.TrimSpace(payload.RecoveryHandoff.ResourceBlocker), strings.TrimSpace(action.ResourceBlocker))
	if strings.TrimSpace(action.SubjectKind) == "task_packet" && blocker != "external_transient" {
		return ""
	}
	agentID = firstNonEmptyContinuation(strings.TrimSpace(payload.AgentID), strings.TrimSpace(payload.DurableAgentID), strings.TrimSpace(payload.RecoveryHandoff.AgentID), strings.TrimSpace(payload.RecoveryHandoff.DurableAgentID))
	return agentID
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

func isChildWakeRepairRetryApprovalText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || childWakeRepairRetryTextNegated(value) {
		return false
	}
	return strings.Contains(value, "retry") && strings.Contains(value, "wake")
}

func isChildWakeRepairAdvanceText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || childWakeRepairRetryTextNegated(value) {
		return false
	}
	if isChildWakeRepairRetryApprovalText(text) {
		return true
	}
	if !strings.Contains(value, "continue") && !strings.Contains(value, "proceed") && !strings.Contains(value, "resume") {
		return false
	}
	for _, marker := range []string{
		"child recovery",
		"durable child recovery",
		"durable_child_recovery",
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
		"do not continue",
		"don't continue",
		"dont continue",
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
