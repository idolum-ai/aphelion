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

func (r *Runtime) ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.ApproveContinuationBundleForKey(key, approverID, phaseIDs)
}

func (r *Runtime) ApproveContinuationForKey(key session.SessionKey, approverID int64) (session.ContinuationState, error) {
	return r.ApproveContinuationBundleForKey(key, approverID, nil)
}

func (r *Runtime) ApproveContinuationBundleForKey(key session.SessionKey, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	unlock := r.lockSession(key)
	defer unlock()
	return r.approveContinuationBundleForKeyLocked(key, approverID, phaseIDs)
}

func (r *Runtime) approveContinuationBundleForKeyLocked(key session.SessionKey, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
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
	if err := r.validateContinuationApprovalBundleFingerprints(key, state); err != nil {
		return state, err
	}
	now := time.Now().UTC()
	state, err = continuationStateWithLeaseApprovedForBundlePhases(state, approverID, phaseIDs, now)
	if err != nil {
		if updateErr := r.store.UpdateContinuationState(key, state); updateErr != nil {
			return session.ContinuationState{}, updateErr
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, err
	}
	if state, err = r.refreshContinuationApprovalBundleFingerprint(key, state); err != nil {
		return session.ContinuationState{}, err
	}
	if compilation := continuationAuthorityCompilation(state); compilation.Invalid() {
		blocked, _, blockErr := r.blockInvalidContinuationAuthorityContract(context.Background(), key, core.InboundMessage{ChatID: key.ChatID}, state, "approval", now, false)
		if blockErr != nil {
			return session.ContinuationState{}, blockErr
		}
		return blocked, fmt.Errorf("continuation authority contract invalid: %s", continuationAuthorityContractInvalidReason(compilation))
	}
	state, err = r.approveRequiredCapabilityGrantsForContinuation(key, state, approverID, now)
	if err != nil {
		state.Status = session.ContinuationStatusPending
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusPending
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return session.ContinuationState{}, err
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, fmt.Errorf("approve required capability grants: %w", err)
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		state = continuationStateWithPlanLeaseApprovalConsumed(state, now)
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return session.ContinuationState{}, err
	}
	if err := r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusApproved); err != nil {
		return session.ContinuationState{}, fmt.Errorf("sync approved operation proposal status: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approverID
	r.recordExecutionEvent(key, core.ExecutionEventContinuationApproved, "continuation", "approved", payload, now)
	return state, nil
}

func (r *Runtime) approveRequiredCapabilityGrantsForContinuation(key session.SessionKey, state session.ContinuationState, approverID int64, now time.Time) (session.ContinuationState, error) {
	state = session.NormalizeContinuationState(state)
	if r == nil || r.store == nil {
		return state, nil
	}
	specs := continuationRequiredCapabilityGrantSpecs(state)
	if len(specs) == 0 {
		return state, nil
	}
	resolved, err := r.validateRequiredCapabilityGrantSpecs(specs)
	if err != nil {
		return state, err
	}
	approver := fmt.Sprintf("telegram:%d", approverID)
	for _, item := range resolved {
		grantID, err := r.approveResolvedRequiredCapabilityGrant(item, approver, continuationRequiredCapabilityGrantExpiry(state, item.spec, now), now)
		if err != nil {
			return state, err
		}
		if grantID != "" && !stringSliceContains(state.ContinuationLease.CapabilityGrantIDs, grantID) {
			state.ContinuationLease.CapabilityGrantIDs = append(state.ContinuationLease.CapabilityGrantIDs, grantID)
		}
	}
	return session.NormalizeContinuationState(state), nil
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

type resolvedRequiredCapabilityGrant struct {
	spec      session.CapabilityGrantSpec
	request   session.CapabilityRequest
	kind      session.CapabilityKind
	target    string
	grantedTo string
	actions   []string
	existing  bool
}

func (r *Runtime) validateRequiredCapabilityGrantSpecs(specs []session.CapabilityGrantSpec) ([]resolvedRequiredCapabilityGrant, error) {
	resolved := make([]resolvedRequiredCapabilityGrant, 0, len(specs))
	for _, spec := range specs {
		spec = session.NormalizeCapabilityGrantSpec(spec)
		request := session.CapabilityRequest{}
		if spec.RequestID != "" {
			stored, ok, err := r.store.CapabilityRequest(spec.RequestID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("required capability request %q not found", spec.RequestID)
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
			return nil, fmt.Errorf("required capability grant spec for request %q is incomplete", spec.RequestID)
		}
		actions := session.NormalizeCapabilityActions(spec.AllowedActions)
		if len(actions) == 0 {
			actions = []string{"invoke"}
		}
		existing, err := r.requiredCapabilityGrantActionsCovered(kind, target, grantedTo, actions)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedRequiredCapabilityGrant{spec: spec, request: request, kind: kind, target: target, grantedTo: grantedTo, actions: actions, existing: existing})
	}
	return resolved, nil
}

func (r *Runtime) requiredCapabilityGrantActionsCovered(kind session.CapabilityKind, target string, grantedTo string, actions []string) (bool, error) {
	for _, action := range session.NormalizeCapabilityActions(actions) {
		grant, ok, err := r.store.ActiveCapabilityGrant(kind, target, grantedTo, action)
		if err != nil {
			return false, err
		}
		if !ok || grant.GrantID == "" {
			return false, nil
		}
	}
	return true, nil
}

func continuationRequiredCapabilityGrantExpiry(state session.ContinuationState, spec session.CapabilityGrantSpec, now time.Time) time.Time {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	if !spec.ExpiresAt.IsZero() {
		return spec.ExpiresAt.UTC()
	}
	state = session.NormalizeContinuationState(state)
	if !state.ContinuationLease.ExpiresAt.IsZero() {
		return state.ContinuationLease.ExpiresAt.UTC()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.UTC().Add(continuationLeaseDefaultTTL)
}

func (r *Runtime) approveResolvedRequiredCapabilityGrant(item resolvedRequiredCapabilityGrant, approver string, expiresAt time.Time, now time.Time) (string, error) {
	if item.existing {
		return "", nil
	}
	if item.spec.RequestID != "" && item.request.ReviewStatus != session.CapabilityReviewStatusApproved {
		if _, err := r.store.AppendCapabilityReview(session.CapabilityReview{ReviewID: fmt.Sprintf("review-%s-%d", safeContinuationIDPart(item.spec.RequestID), now.UnixNano()), RequestID: item.spec.RequestID, Reviewer: approver, ReviewerRole: string(principal.RoleAdmin), Status: session.CapabilityReviewStatusApproved, Rationale: "bundled plan/capability approval", CreatedAt: now}); err != nil {
			return "", err
		}
	}
	grantID := item.spec.GrantID
	if grantID == "" {
		grantID = fmt.Sprintf("capg-%s-%d", safeContinuationIDPart(firstNonEmptyContinuation(item.spec.RequestID, string(item.kind)+"-"+item.target)), now.UnixNano())
	}
	_, err := r.store.UpsertCapabilityGrant(session.CapabilityGrant{GrantID: grantID, RequestID: item.spec.RequestID, GrantedBy: approver, GrantedTo: item.grantedTo, Kind: item.kind, TargetResource: item.target, AllowedActions: item.actions, Contract: firstNonEmptyContinuation(item.spec.Contract, item.request.Contract), Constraints: firstNonEmptyContinuation(item.spec.Constraints, item.request.Constraints), Status: session.CapabilityGrantStatusActive, GrantedAt: now, ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return "", err
	}
	return grantID, nil
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
	unlock := r.lockSession(key)
	defer unlock()
	return r.revokeContinuationForKeyLocked(key)
}

func (r *Runtime) revokeContinuationForKeyLocked(key session.SessionKey) (ContinuationRevokeResult, error) {
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return ContinuationRevokeResult{}, err
	}
	state = session.NormalizeContinuationState(state)
	revoked := state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	if revoked {
		now := time.Now().UTC()
		state = continuationStateWithLeaseRevoked(state, now)
		if err := r.revokeContinuationMintedCapabilityGrants(state, now); err != nil {
			return ContinuationRevokeResult{}, err
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return ContinuationRevokeResult{}, err
		}
		if err := r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied); err != nil {
			return ContinuationRevokeResult{}, fmt.Errorf("sync revoked operation proposal status: %w", err)
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationRevoked, "continuation", "revoked", continuationExecutionPayload(state), now)
	}
	return ContinuationRevokeResult{State: state, Revoked: revoked, ContinuationLabel: continuationUserFacingPlanLabel(state)}, nil
}

func (r *Runtime) revokeContinuationMintedCapabilityGrants(state session.ContinuationState, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	for _, grantID := range state.ContinuationLease.CapabilityGrantIDs {
		grantID = strings.TrimSpace(grantID)
		if grantID == "" {
			continue
		}
		grant, ok, err := r.store.CapabilityGrant(grantID)
		if err != nil {
			return fmt.Errorf("load minted capability grant %q: %w", grantID, err)
		}
		if !ok {
			continue
		}
		grant = session.NormalizeCapabilityGrant(grant)
		if grant.Status == session.CapabilityGrantStatusRevoked {
			continue
		}
		grant.Status = session.CapabilityGrantStatusRevoked
		grant.RevokedAt = now
		grant.UpdatedAt = now
		if _, err := r.store.UpsertCapabilityGrant(grant); err != nil {
			return fmt.Errorf("revoke minted capability grant %q: %w", grantID, err)
		}
	}
	return nil
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
	return r.triggerContinuationLoop(ctx, key)
}

type approvedContinuationReservation struct {
	State           session.ContinuationState
	Actor           principal.Principal
	ExecutionActor  principal.Principal
	ApprovedBy      int64
	EventText       string
	SandboxRequired bool
	WorkRequest     *WorkRequest
	WorkMode        WorkMode
}

type leaseAccessDeniedRepair struct {
	Prior    session.ContinuationState
	Decision session.ContinuationLeaseAccessDecision
	At       time.Time
}

func (r *Runtime) triggerApprovedContinuationOnce(ctx context.Context, key session.SessionKey) (session.ContinuationState, bool, int, error) {
	state, reservation, loopBudget, repair, err := r.reserveApprovedContinuationTurn(key)
	if repair != nil {
		r.offerLeaseActionDeniedRepair(context.Background(), key, key.ChatID, repair.Prior, repair.Decision, repair.At)
	}
	if err != nil {
		return state, false, loopBudget, err
	}
	if reservation == nil {
		if !session.NormalizeContinuationState(state).Active() {
			r.retireStaleContinuationApprovalCards(ctx, key, key.ChatID, continuationCallbackThreadIDForKey(key), 0, "continuation_inactive", time.Now().UTC())
		}
		return state, false, loopBudget, nil
	}
	r.retireStaleContinuationApprovalCards(ctx, key, key.ChatID, continuationCallbackThreadIDForKey(key), 0, "lease_consumed", time.Now().UTC())
	if err := r.runReservedApprovedContinuation(ctx, key, *reservation); err != nil {
		return state, true, loopBudget, err
	}
	updated, err := r.ContinuationStateForKey(key)
	if err != nil {
		return session.ContinuationState{}, true, loopBudget, err
	}
	return updated, true, loopBudget, nil
}

func (r *Runtime) reserveApprovedContinuationTurn(key session.SessionKey) (session.ContinuationState, *approvedContinuationReservation, int, *leaseAccessDeniedRepair, error) {
	unlock := r.lockSession(key)
	defer unlock()
	return r.reserveApprovedContinuationTurnLocked(key)
}

func (r *Runtime) reserveApprovedContinuationTurnLocked(key session.SessionKey) (session.ContinuationState, *approvedContinuationReservation, int, *leaseAccessDeniedRepair, error) {
	now := time.Now().UTC()
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return session.ContinuationState{}, nil, 0, nil, err
	}
	if continuationLeaseExpired(state, now) {
		state = continuationStateWithLeaseExpired(state, now)
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return session.ContinuationState{}, nil, 0, nil, err
		}
		if err := r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusExpired); err != nil {
			return session.ContinuationState{}, nil, 0, nil, fmt.Errorf("sync expired operation proposal status: %w", err)
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, nil, 0, nil, nil
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "approval_only", continuationExecutionPayload(state), now)
		return state, nil, 0, nil, nil
	}
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, nil, 0, nil, nil
	}
	if r.continuationBudgetRecoveryPending(key, state, now) {
		payload := continuationExecutionPayload(state)
		payload["reason"] = "recovery_pending"
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "recovery_pending", payload, now)
		return state, nil, 0, nil, nil
	}
	if err := r.validateContinuationApprovalBundleFingerprints(key, state); err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "stale_bundle", continuationExecutionPayload(state), now)
		return state, nil, 0, nil, err
	}
	approverID := state.ApprovedBy
	if approverID <= 0 {
		return state, nil, 0, nil, fmt.Errorf("continuation approver is not recorded")
	}
	actor, ok := r.resolver.ResolveTelegramUser(approverID)
	if !ok {
		return state, nil, 0, nil, fmt.Errorf("continuation approver %d is not admitted", approverID)
	}
	state = session.NormalizeContinuationState(state)
	loopBudget := continuationLoopBudget(state)
	if r.shouldRouteContinuationThroughWorkExecutor(state) {
		return r.reserveApprovedWorkContinuationTurnLocked(key, actor, state, loopBudget)
	}

	sandboxRequired := continuationRequiresApprovedUserSandbox(state)
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	continuationEventText := approvedContinuationEventTextForState(state)
	state = continuationStateAfterLeaseTurnConsumed(state, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return state, nil, loopBudget, nil, err
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
	r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "consumed", payload, now)
	return state, &approvedContinuationReservation{
		State:           state,
		Actor:           actor,
		ExecutionActor:  executionActor,
		ApprovedBy:      approvedBy,
		EventText:       continuationEventText,
		SandboxRequired: sandboxRequired,
	}, loopBudget, nil, nil
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

func (r *Runtime) reserveApprovedWorkContinuationTurnLocked(key session.SessionKey, actor principal.Principal, state session.ContinuationState, loopBudget int) (session.ContinuationState, *approvedContinuationReservation, int, *leaseAccessDeniedRepair, error) {
	mode := continuationWorkMode(state)
	leaseDecision := continuationWorkModeAccessCheck(state, mode, time.Now().UTC())
	if !leaseDecision.Allowed {
		blocked, repair, err := r.blockContinuationForLeaseAccessDeniedLocked(key, state, leaseDecision, time.Now().UTC())
		return blocked, nil, 0, repair, err
	}
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return state, nil, loopBudget, nil, err
	}
	opState = session.NormalizeOperationState(opState)
	req := r.workRequestForContinuation(key, key.ChatID, executionActor, state, opState)
	state = continuationStateAfterLeaseTurnConsumed(state, time.Now().UTC())
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return state, nil, loopBudget, nil, err
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
	return state, &approvedContinuationReservation{
		State:          state,
		Actor:          actor,
		ExecutionActor: executionActor,
		ApprovedBy:     approvedBy,
		WorkRequest:    &req,
		WorkMode:       mode,
	}, loopBudget, nil, nil
}

func (r *Runtime) runReservedApprovedContinuation(ctx context.Context, key session.SessionKey, reservation approvedContinuationReservation) error {
	if reservation.WorkRequest != nil {
		return r.runReservedApprovedWorkContinuation(ctx, key, reservation)
	}
	msg := continuationInboundForKey(key, reservation.ExecutionActor, reservation.EventText, core.InboundOriginTurnAuthorization, string(session.TurnAuthorizationKindContinuation))
	_, err := r.handleInternalContinuation(ctx, reservation.ExecutionActor, msg)
	return err
}

func (r *Runtime) runReservedApprovedWorkContinuation(ctx context.Context, key session.SessionKey, reservation approvedContinuationReservation) error {
	if r == nil || r.store == nil || reservation.WorkRequest == nil {
		return nil
	}
	if reservation.State.VerificationTarget != nil {
		return r.runReservedWorkOutcomeVerification(ctx, key, reservation)
	}
	if r.workExecutor == nil {
		return nil
	}
	req := *reservation.WorkRequest
	workStartedAt := time.Now().UTC()
	result, err := r.workExecutor.Run(ctx, req)
	workFinishedAt := time.Now().UTC()
	status := r.workExecutor.Status()
	if err == nil && workResultBudgetRecoveryScheduled(result) {
		artifact := r.persistWorkResultForContinuation(key, req, result, status, nil)
		payload := workResultPayload(req, result, status, nil)
		if artifact.Ref != "" {
			payload["artifact_ref"] = artifact.Ref
		}
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorRecovering, "work", "recovering", payload, time.Now().UTC())
		return nil
	}
	if err == nil && workResultBudgetRecoveryBlocked(result) {
		cause := nativeWorkRecoveryError{Kind: result.RecoveryKind, Summary: result.RecoverySummary}
		artifact := r.persistWorkResultForContinuation(key, req, result, status, cause)
		payload := workResultPayload(req, result, status, cause)
		if artifact.Ref != "" {
			payload["artifact_ref"] = artifact.Ref
		}
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFailed, "work", "recovery_blocked", payload, time.Now().UTC())
		return nil
	}
	if err == nil && result.Recovery == nil && !workResultHasSubstantiveCompletionEvidenceForRequest(req, result) {
		resolved, resolution := r.resolveWorkOutcomeAfterMissingEvidence(ctx, key, req, result, workStartedAt, workFinishedAt)
		result = resolved
		switch resolution.Kind {
		case workOutcomeResolutionAutoVerified:
			// The resolved result carries typed evidence; let the normal success path persist and record it once.
		case workOutcomeResolutionVerificationOfferable, workOutcomeResolutionBlockedUnverified:
			cause := resolution.Err
			if cause == nil {
				cause = errWorkExecutorOutcomeUnverified
			}
			artifact := r.persistWorkResultForContinuation(key, req, result, status, cause)
			payload := workResultPayload(req, result, status, cause)
			for k, v := range resolution.Payload {
				payload[k] = v
			}
			if artifact.Ref != "" {
				payload["artifact_ref"] = artifact.Ref
			}
			r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFailed, "work", "outcome_unverified", payload, time.Now().UTC())
			r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "outcome_unverified", payload, time.Now().UTC())
			if resolution.VerificationOfferable() {
				return r.offerWorkOutcomeVerificationApproval(ctx, key, req, result, status, cause, artifact, resolution)
			}
			return cause
		default:
			err = errWorkExecutorNoCompletionEvidence
		}
	}
	if err != nil {
		artifact := r.persistWorkResultForContinuation(key, req, result, status, err)
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
	artifact := r.persistWorkResultForContinuation(key, req, result, status, nil)
	payload := workResultPayload(req, result, status, nil)
	if artifact.Ref != "" {
		payload["artifact_ref"] = artifact.Ref
	}
	r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorSucceeded, "work", "succeeded", payload, time.Now().UTC())
	if err := r.deliverWorkResult(ctx, key, result, artifact); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) persistWorkResultForContinuation(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error) session.OperationArtifact {
	unlock := r.lockSession(key)
	defer unlock()
	return r.persistWorkResult(key, req, result, status, cause)
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
	unlock := r.lockSession(key)
	_, repair, err := r.blockContinuationForLeaseAccessDeniedLocked(key, state, decision, time.Now().UTC())
	unlock()
	if repair != nil {
		r.offerLeaseActionDeniedRepair(context.Background(), key, key.ChatID, repair.Prior, repair.Decision, repair.At)
	}
	return err
}

func (r *Runtime) blockContinuationForLeaseAccessDeniedLocked(key session.SessionKey, state session.ContinuationState, decision session.ContinuationLeaseAccessDecision, now time.Time) (session.ContinuationState, *leaseAccessDeniedRepair, error) {
	if r == nil || r.store == nil {
		return session.NormalizeContinuationState(state), nil, nil
	}
	prior := session.NormalizeContinuationState(state)
	state = continuationStateWithLeaseRevoked(state, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return session.ContinuationState{}, nil, err
	}
	if err := r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied); err != nil {
		return session.ContinuationState{}, nil, fmt.Errorf("sync denied operation proposal status: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["reason"] = "lease_action_denied"
	payload["lease_action"] = strings.TrimSpace(decision.Action)
	payload["lease_access_reason"] = strings.TrimSpace(decision.Reason)
	payload["lease_id"] = strings.TrimSpace(decision.LeaseID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, now)
	return state, &leaseAccessDeniedRepair{Prior: prior, Decision: decision, At: now}, nil
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
