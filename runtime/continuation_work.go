//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) ContinuationState(chatID int64) (session.ContinuationState, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.ContinuationStateForKey(key)
}

func (r *Runtime) ContinuationStateForKey(key session.SessionKey) (session.ContinuationState, error) {
	return r.store.ContinuationState(key)
}

func (r *Runtime) ClearChatSessionContext(chatID int64) (bool, error) {
	if r == nil {
		return false, nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.ClearSessionContextForKey(key)
}

func (r *Runtime) ClearSessionContextForKey(key session.SessionKey) (bool, error) {
	if r == nil {
		return false, nil
	}
	removed, err := r.store.DeleteSession(key)
	if err != nil {
		return false, err
	}
	return removed > 0, nil
}

func (r *Runtime) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.ApproveContinuationForKey(key, approverID)
}

func (r *Runtime) ApproveContinuationForKey(key session.SessionKey, approverID int64) (session.ContinuationState, error) {
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return session.ContinuationState{}, err
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return state, core.ErrContinuationNotPending
	}
	if state.RemainingTurns <= 0 {
		return state, core.ErrContinuationNoTurns
	}
	now := time.Now().UTC()
	state, err = continuationStateWithLeaseApproved(state, approverID, now)
	if err != nil {
		if updateErr := r.store.UpdateContinuationState(key, state); updateErr != nil {
			return session.ContinuationState{}, updateErr
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, err
	}
	if err := r.approveRequiredCapabilityGrantsForContinuation(key, state, approverID, now); err != nil {
		state.Status = session.ContinuationStatusPending
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusPending
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return session.ContinuationState{}, err
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, fmt.Errorf("approve required capability grants: %w", err)
	}
	if compilation := continuationAuthorityCompilation(state); compilation.Invalid() {
		blocked, _, blockErr := r.blockInvalidContinuationAuthorityContract(context.Background(), key, core.InboundMessage{ChatID: key.ChatID}, state, "approval", now, false)
		if blockErr != nil {
			return session.ContinuationState{}, blockErr
		}
		return blocked, fmt.Errorf("continuation authority contract invalid: %s", continuationAuthorityContractInvalidReason(compilation))
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		state = continuationStateWithPlanLeaseApprovalConsumed(state, now)
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return session.ContinuationState{}, err
	}
	r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusApproved)
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approverID
	r.recordExecutionEvent(key, core.ExecutionEventContinuationApproved, "continuation", "approved", payload, now)
	return state, nil
}

func (r *Runtime) approveRequiredCapabilityGrantsForContinuation(key session.SessionKey, state session.ContinuationState, approverID int64, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	specs := continuationRequiredCapabilityGrantSpecs(state)
	if len(specs) == 0 {
		return nil
	}
	approver := fmt.Sprintf("telegram:%d", approverID)
	for _, spec := range specs {
		if err := r.approveRequiredCapabilityGrantSpec(key, spec, approver, now); err != nil {
			return err
		}
	}
	return nil
}

func continuationRequiredCapabilityGrantSpecs(state session.ContinuationState) []session.CapabilityGrantSpec {
	state = session.NormalizeContinuationState(state)
	specs := append([]session.CapabilityGrantSpec(nil), state.ContinuationLease.RequiredCapabilityGrants...)
	if state.ApprovalBundle.Active() {
		current := strings.TrimSpace(state.ApprovalBundle.CurrentPhaseID)
		for _, phase := range state.ApprovalBundle.Phases {
			phase = session.NormalizeContinuationApprovalBundlePhase(phase)
			if current != "" && strings.TrimSpace(phase.ID) != current {
				continue
			}
			specs = append(specs, phase.RequiredCapabilityGrants...)
			if current != "" {
				break
			}
		}
	}
	return session.NormalizeCapabilityGrantSpecs(specs)
}

func (r *Runtime) approveRequiredCapabilityGrantSpec(key session.SessionKey, spec session.CapabilityGrantSpec, approver string, now time.Time) error {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	request := session.CapabilityRequest{}
	if spec.RequestID != "" {
		stored, ok, err := r.store.CapabilityRequest(spec.RequestID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("required capability request %q not found", spec.RequestID)
		}
		request = session.NormalizeCapabilityRequest(stored)
	}
	kind := spec.Kind
	if kind == "" {
		kind = request.Kind
	}
	target := firstNonEmptyContinuation(spec.TargetResource, request.TargetResource)
	grantedTo := firstNonEmptyContinuation(spec.GrantedTo, request.RequestedFor, request.RequestedBy)
	if kind == "" || target == "" || grantedTo == "" {
		return fmt.Errorf("required capability grant spec for request %q is incomplete", spec.RequestID)
	}
	actions := session.NormalizeCapabilityActions(spec.AllowedActions)
	if len(actions) == 0 {
		actions = []string{"invoke"}
	}
	if existing, ok, err := r.store.ActiveCapabilityGrant(kind, target, grantedTo, actions[0]); err != nil {
		return err
	} else if ok && existing.GrantID != "" {
		return nil
	}
	if spec.RequestID != "" && request.ReviewStatus != session.CapabilityReviewStatusApproved {
		if _, err := r.store.AppendCapabilityReview(session.CapabilityReview{ReviewID: fmt.Sprintf("review-%s-%d", safeContinuationIDPart(spec.RequestID), now.UnixNano()), RequestID: spec.RequestID, Reviewer: approver, ReviewerRole: string(principal.RoleAdmin), Status: session.CapabilityReviewStatusApproved, Rationale: "bundled plan/capability approval", CreatedAt: now}); err != nil {
			return err
		}
	}
	grantID := spec.GrantID
	if grantID == "" {
		grantID = fmt.Sprintf("capg-%s-%d", safeContinuationIDPart(firstNonEmptyContinuation(spec.RequestID, string(kind)+"-"+target)), now.UnixNano())
	}
	_, err := r.store.UpsertCapabilityGrant(session.CapabilityGrant{GrantID: grantID, RequestID: spec.RequestID, GrantedBy: approver, GrantedTo: grantedTo, Kind: kind, TargetResource: target, AllowedActions: actions, Contract: firstNonEmptyContinuation(spec.Contract, request.Contract), Constraints: firstNonEmptyContinuation(spec.Constraints, request.Constraints), Status: session.CapabilityGrantStatusActive, GrantedAt: now, ExpiresAt: spec.ExpiresAt, CreatedAt: now, UpdatedAt: now})
	return err
}

func safeContinuationIDPart(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "required-capability"
	}
	return out
}

type ContinuationRevokeResult struct {
	State             session.ContinuationState
	Revoked           bool
	ContinuationLabel string
}

func (r *Runtime) RevokeContinuation(chatID int64) (ContinuationRevokeResult, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.RevokeContinuationForKey(key)
}

func (r *Runtime) RevokeContinuationForKey(key session.SessionKey) (ContinuationRevokeResult, error) {
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return ContinuationRevokeResult{}, err
	}
	state = session.NormalizeContinuationState(state)
	revoked := state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	if revoked {
		state = continuationStateWithLeaseRevoked(state, time.Now().UTC())
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return ContinuationRevokeResult{}, err
		}
		r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationRevoked, "continuation", "revoked", continuationExecutionPayload(state), time.Now().UTC())
	}
	return ContinuationRevokeResult{State: state, Revoked: revoked, ContinuationLabel: continuationUserFacingPlanLabel(state)}, nil
}

func (r *Runtime) TriggerContinuation(ctx context.Context, chatID int64) error {
	if r == nil {
		return nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.TriggerContinuationForKey(ctx, key)
}

func (r *Runtime) TriggerContinuationForKey(ctx context.Context, key session.SessionKey) error {
	if r == nil {
		return nil
	}
	state, err := r.ContinuationStateForKey(key)
	if err != nil {
		return err
	}
	if continuationLeaseExpired(state, time.Now().UTC()) {
		state = continuationStateWithLeaseExpired(state, time.Now().UTC())
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return err
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "approval_only", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	approverID := state.ApprovedBy
	if approverID <= 0 {
		return fmt.Errorf("continuation approver is not recorded")
	}
	actor, ok := r.resolver.ResolveTelegramUser(approverID)
	if !ok {
		return fmt.Errorf("continuation approver %d is not admitted", approverID)
	}
	return r.runApprovedContinuation(ctx, actor, key, state)
}

func (r *Runtime) runApprovedContinuation(ctx context.Context, actor principal.Principal, key session.SessionKey, state session.ContinuationState) error {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return nil
	}
	if r.shouldRouteContinuationThroughWorkExecutor(state) {
		return r.runApprovedWorkContinuation(ctx, actor, key, state)
	}
	return r.runApprovedContinuationNative(ctx, actor, key, state)
}

func (r *Runtime) runApprovedContinuationNative(ctx context.Context, actor principal.Principal, key session.SessionKey, state session.ContinuationState) error {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return nil
	}
	sandboxRequired := continuationRequiresApprovedUserSandbox(state)
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	continuationEventText := approvedContinuationEventTextForState(state)
	state = continuationStateAfterLeaseTurnConsumed(state, time.Now().UTC())
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approvedBy
	payload["execution_principal_role"] = string(executionActor.Role)
	if sandboxRequired {
		payload["sandbox_profile"] = organicProposalSandboxProfile
	}
	if executionActor.Role != actor.Role {
		payload["sandboxed_from_role"] = string(actor.Role)
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "consumed", payload, time.Now().UTC())
	msg := continuationInboundForKey(key, executionActor, continuationEventText, core.InboundOriginTurnAuthorization, string(session.TurnAuthorizationKindContinuation))
	_, err := r.handleInternalContinuation(ctx, executionActor, msg)
	return err
}

func (r *Runtime) shouldRouteContinuationThroughWorkExecutor(state session.ContinuationState) bool {
	if r == nil || r.workExecutor == nil {
		return false
	}
	if continuationRequiresApprovedUserSandbox(state) {
		return false
	}
	return continuationWorkMode(state) != ""
}

func (r *Runtime) runApprovedWorkContinuation(ctx context.Context, actor principal.Principal, key session.SessionKey, state session.ContinuationState) error {
	if r == nil || r.store == nil || r.workExecutor == nil {
		return r.runApprovedContinuationNative(ctx, actor, key, state)
	}
	state = session.NormalizeContinuationState(state)
	mode := continuationWorkMode(state)
	leaseDecision := continuationWorkModeAccessCheck(state, mode, time.Now().UTC())
	if !leaseDecision.Allowed {
		return r.blockContinuationForLeaseAccessDenied(key, state, leaseDecision)
	}
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	opState, _ := r.store.OperationState(key)
	opState = session.NormalizeOperationState(opState)
	req := r.workRequestForContinuation(key, key.ChatID, executionActor, state, opState)
	state = continuationStateAfterLeaseTurnConsumed(state, time.Now().UTC())
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approvedBy
	payload["execution_principal_role"] = string(executionActor.Role)
	payload["work_executor_requested"] = true
	payload["work_mode"] = string(req.Mode)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "consumed", payload, time.Now().UTC())
	r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorStarted, "work", "started", map[string]any{
		"operation_id": strings.TrimSpace(req.OperationID),
		"lease_id":     strings.TrimSpace(req.LeaseID),
		"mode":         strings.TrimSpace(string(req.Mode)),
	}, time.Now().UTC())
	result, err := r.workExecutor.Run(ctx, req)
	status := r.workExecutor.Status()
	if err != nil {
		artifact := r.persistWorkResult(key, req, result, status, err)
		payload := workResultPayload(req, result, status, err)
		if artifact.Ref != "" {
			payload["artifact_ref"] = artifact.Ref
		}
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFailed, "work", "failed", payload, time.Now().UTC())
		r.offerWorkFailureRetry(ctx, key, key.ChatID, err)
		return err
	}
	if strings.TrimSpace(status.FallbackReason) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFallback, "work", "fallback", map[string]any{
			"operation_id":     strings.TrimSpace(req.OperationID),
			"lease_id":         strings.TrimSpace(req.LeaseID),
			"active_executor":  strings.TrimSpace(status.Active),
			"fallback_reason":  strings.TrimSpace(status.FallbackReason),
			"last_attempted":   strings.TrimSpace(status.LastAttempted),
			"configured":       strings.TrimSpace(status.Configured),
			"preferred":        strings.TrimSpace(status.Preferred),
			"executor_warning": workExecutorFallbackWarning(status),
		}, time.Now().UTC())
		if err := r.warnWorkExecutorFallback(ctx, key, status); err != nil {
			log.Printf("WARN send work executor fallback warning failed chat_id=%d err=%v", key.ChatID, err)
		}
	}
	artifact := r.persistWorkResult(key, req, result, status, nil)
	payload = workResultPayload(req, result, status, nil)
	if artifact.Ref != "" {
		payload["artifact_ref"] = artifact.Ref
	}
	r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorSucceeded, "work", "succeeded", payload, time.Now().UTC())
	if err := r.deliverWorkResult(ctx, key, result, artifact); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) warnWorkExecutorFallback(ctx context.Context, key session.SessionKey, status WorkExecutorStatus) error {
	if r == nil || r.outbound == nil || key.ChatID == 0 {
		return nil
	}
	text := workExecutorFallbackWarning(status)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	text = r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), text)
	_, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: key.ChatID, Text: text})
	return err
}

func workExecutorFallbackWarning(status WorkExecutorStatus) string {
	if strings.TrimSpace(status.FallbackReason) == "" {
		return ""
	}
	active := firstRuntimeWorkNonEmpty(status.Active, "native")
	preferred := firstRuntimeWorkNonEmpty(status.Preferred, status.LastAttempted, "preferred executor")
	if active == preferred {
		return ""
	}
	return fmt.Sprintf("Work executor fallback: %s unavailable; using %s.", preferred, active)
}

func (r *Runtime) blockContinuationForLeaseAccessDenied(key session.SessionKey, state session.ContinuationState, decision session.ContinuationLeaseAccessDecision) error {
	if r == nil || r.store == nil {
		return nil
	}
	now := time.Now().UTC()
	prior := session.NormalizeContinuationState(state)
	state = continuationStateWithLeaseRevoked(state, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied)
	payload := continuationExecutionPayload(state)
	payload["reason"] = "lease_action_denied"
	payload["lease_action"] = strings.TrimSpace(decision.Action)
	payload["lease_access_reason"] = strings.TrimSpace(decision.Reason)
	payload["lease_id"] = strings.TrimSpace(decision.LeaseID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, now)
	r.offerLeaseActionDeniedRepair(context.Background(), key, key.ChatID, prior, decision, now)
	return nil
}

func (r *Runtime) workRequestForContinuation(key session.SessionKey, chatID int64, actor principal.Principal, state session.ContinuationState, opState session.OperationState) WorkRequest {
	mode := continuationWorkMode(state)
	if mode == "" {
		mode = WorkModeReadOnly
	}
	repoRoot := firstNonEmptyContinuation(opState.Work.RepoRoot, opState.Work.Workdir)
	if repoRoot == "" && r != nil && r.cfg != nil {
		repoRoot = r.cfg.Agent.ExecRoot
	}
	workdir := firstNonEmptyContinuation(opState.Work.Workdir, repoRoot)
	threadID := strings.TrimSpace(opState.Work.CodexThreadID)
	return WorkRequest{
		OperationID: firstNonEmptyContinuation(opState.ID, state.ActionProposal.OperationID),
		RepoRoot:    repoRoot,
		Workdir:     workdir,
		Prompt:      workPromptForContinuation(state, opState),
		Mode:        mode,
		LeaseID:     state.ContinuationLease.ID,
		ThreadID:    threadID,
		Key:         key,
		ChatID:      chatID,
		Actor:       actor,
		State:       state,
		Operation:   opState,
	}
}
