//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) sendMaterializedContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string, source string) error {
	if approved, err := r.maybeAutoApproveContinuationOffer(ctx, key, msg, state, source); approved || err != nil {
		return err
	}
	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, text)
}

func (r *Runtime) repairInvalidPendingPhaseApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, state session.ContinuationState, now time.Time) (session.OperationState, bool) {
	repairedOpState, repaired, err := r.repairInvalidPendingPhaseApprovalState(ctx, key, msg.ChatID, opState, state, now, true, "materialization_repair")
	if err != nil {
		return session.NormalizeOperationState(opState), false
	}
	return repairedOpState, repaired
}

func (r *Runtime) repairInvalidPendingContinuationApprovals(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	records, err := r.store.ContinuationStates()
	if err != nil {
		return 0, fmt.Errorf("load continuation states for approval repair: %w", err)
	}
	repaired := 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return repaired, err
		}
		state := session.NormalizeContinuationState(record.State)
		if state.Status != session.ContinuationStatusPending {
			continue
		}
		opState, err := r.store.OperationState(record.Key)
		if err != nil {
			return repaired, fmt.Errorf("load operation state chat_id=%d: %w", record.Key.ChatID, err)
		}
		_, ok, err := r.repairInvalidPendingPhaseApprovalState(ctx, record.Key, record.Key.ChatID, opState, state, now, true, "startup_repair")
		if err != nil {
			return repaired, err
		}
		if ok {
			repaired++
			continue
		}
		opState, ok, err = r.repairStaleContinuationDerivedOrganicProposalState(ctx, record.Key, record.Key.ChatID, opState, state, true, now, true, "startup_repair")
		if err != nil {
			return repaired, err
		}
		if ok {
			repaired++
		}
	}
	operationRecords, err := r.store.OperationStates()
	if err != nil {
		return repaired, fmt.Errorf("load operation states for approval repair: %w", err)
	}
	for _, record := range operationRecords {
		if err := ctx.Err(); err != nil {
			return repaired, err
		}
		if staleContinuationDerivedOrganicProposalReason(record.State) == "" {
			continue
		}
		state, exists, err := r.store.ContinuationStateIfExists(record.Key)
		if err != nil {
			return repaired, fmt.Errorf("load continuation state chat_id=%d: %w", record.Key.ChatID, err)
		}
		_, ok, err := r.repairStaleContinuationDerivedOrganicProposalState(ctx, record.Key, record.Key.ChatID, record.State, state, exists, now, false, "startup_repair")
		if err != nil {
			return repaired, err
		}
		if ok {
			repaired++
		}
	}
	return repaired, nil
}

func (r *Runtime) repairInvalidPendingPhaseApprovalState(ctx context.Context, key session.SessionKey, chatID int64, opState session.OperationState, state session.ContinuationState, now time.Time, notify bool, surface string) (session.OperationState, bool, error) {
	if r == nil || r.store == nil {
		return session.NormalizeOperationState(opState), false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return opState, false, nil
	}
	reason := continuationApprovalBundleInvalidReason(opState.PhasePlan, state.ApprovalBundle)
	if reason == "" {
		return opState, false, nil
	}
	state.Status = session.ContinuationStatusRevoked
	state.ActionProposal.Status = session.ProposalStatusSuperseded
	state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	state.ContinuationLease.UpdatedAt = now
	state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
	for i := range state.ApprovalBundle.Phases {
		state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusRevoked
	}
	state.ApprovalBundle.UpdatedAt = now
	state.UpdatedAt = now
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return opState, false, fmt.Errorf("revoke invalid pending continuation chat_id=%d: %w", key.ChatID, err)
	}

	opState = operationStateWithInvalidApprovalCleared(opState, state, now)
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return opState, false, fmt.Errorf("clear invalid pending operation approval chat_id=%d: %w", key.ChatID, err)
	}

	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "materialization_repair"
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           surface,
		"subject_id":        strings.TrimSpace(state.DecisionID),
		"operator_label":    "Invalid continuation approval repaired",
		"visible_action":    "repair_invalid_pending_approval",
		"decision":          "revoked_invalid_pending_approval",
		"findings": []core.RuntimeFinding{{
			Kind:             "invalid_pending_approval",
			EvidenceStatus:   "detected_from_phase_contract",
			Detail:           reason,
			RequiredBehavior: "Do not execute old approval buttons; re-adjudicate the next eligible action.",
		}},
	}, now)
	if notify && r.outbound != nil && chatID != 0 {
		text := r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), "Stopped stale approval.\n\nI will create a fresh narrower proposal for the next eligible action.")
		_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   text,
		})
	}
	return opState, true, nil
}

func operationStateWithInvalidApprovalCleared(opState session.OperationState, state session.ContinuationState, now time.Time) session.OperationState {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	actionOpID := strings.TrimSpace(state.ActionProposal.OperationID)
	actionID := strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	decisionID := strings.TrimSpace(state.DecisionID)
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	if opState.Proposal.Status == session.ProposalStatusPending {
		proposalID := strings.TrimSpace(opState.Proposal.ID)
		if proposalID != "" && (proposalID == actionOpID || proposalID == actionID || proposalID == decisionID) {
			opState.Proposal.Status = session.ProposalStatusSuperseded
			opState.Proposal.UpdatedAt = now
		}
	}
	if opState.PlanLease.Status == session.PlanLeaseStatusProposed || opState.PlanLease.Status == session.PlanLeaseStatusActive || opState.PlanLease.Status == session.PlanLeaseStatusApproved {
		planID := strings.TrimSpace(opState.PlanLease.ID)
		if planID != "" && (planID == actionOpID || planID == actionID || planID == decisionID) {
			opState.PlanLease.Status = session.PlanLeaseStatusRevoked
			opState.PlanLease.UpdatedAt = now
		}
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	bundleIDs := make(map[string]struct{}, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		if id := strings.TrimSpace(phase.OperationPhaseID); id != "" {
			bundleIDs[id] = struct{}{}
		}
	}
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		_, inBundle := bundleIDs[phaseID]
		leaseMatches := leaseID != "" && strings.TrimSpace(opState.PhasePlan.Phases[i].LeaseID) == leaseID
		if !inBundle && !leaseMatches {
			continue
		}
		if opState.PhasePlan.Phases[i].Status == session.PlanStatusInProgress {
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		}
		opState.PhasePlan.Phases[i].LeaseID = ""
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "phase_approval_adjudicated"
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func (r *Runtime) repairStaleContinuationDerivedOrganicProposalState(
	ctx context.Context,
	key session.SessionKey,
	chatID int64,
	opState session.OperationState,
	state session.ContinuationState,
	stateExists bool,
	now time.Time,
	notify bool,
	surface string,
) (session.OperationState, bool, error) {
	if r == nil || r.store == nil {
		return session.NormalizeOperationState(opState), false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	reason := staleContinuationDerivedOrganicProposalReason(opState)
	if reason == "" {
		return opState, false, nil
	}

	continuationMatches := stateExists && staleContinuationStateMatchesOrganicProposal(state, opState)
	if continuationMatches {
		state.Status = session.ContinuationStatusRevoked
		state.RemainingTurns = 0
		state.HandshakeBlockedReason = "stale_continuation_projection"
		state.ActionProposal.Status = session.ProposalStatusSuperseded
		state.ActionProposal.UpdatedAt = now
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RevokedAt = now
		state.ContinuationLease.UpdatedAt = now
		state.UpdatedAt = now
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return opState, false, fmt.Errorf("revoke stale continuation-derived approval chat_id=%d: %w", key.ChatID, err)
		}
	}

	opState.Proposal.Status = session.ProposalStatusSuperseded
	opState.Proposal.UpdatedAt = now
	opState.Status = session.OperationStatusIdle
	opState.Stage = "organic_proposal_repaired"
	opState.Summary = "Stale continuation-derived organic proposal superseded before execution."
	opState.UpdatedAt = now
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return opState, false, fmt.Errorf("supersede stale continuation-derived organic proposal chat_id=%d: %w", key.ChatID, err)
	}

	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "continuation_repair"
	}
	subjectID := firstNonEmptyContinuation(state.DecisionID, opState.Proposal.ID, opState.ID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           surface,
		"subject_id":        subjectID,
		"operator_label":    "Stale continuation-derived approval repaired",
		"visible_action":    "repair_stale_continuation_projection",
		"decision":          "revoked_stale_continuation_projection",
		"findings": []core.RuntimeFinding{{
			Kind:             "stale_continuation_projection",
			EvidenceStatus:   "detected_from_operation_finding",
			Detail:           reason,
			RequiredBehavior: "Do not materialize old inactive continuation projection as new work; require current typed remaining work.",
		}},
	}, now)
	if notify && continuationMatches && r.outbound != nil && chatID != 0 {
		text := r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), "Stopped stale approval.\n\nThat prompt was based on older continuation state, not current remaining work.")
		_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   text,
		})
	}
	return session.NormalizeOperationState(opState), true, nil
}

func staleContinuationDerivedOrganicProposalReason(opState session.OperationState) string {
	opState = session.NormalizeOperationState(opState)
	if opState.Status != session.OperationStatusBlocked || opState.Stage != "organic_proposal" || !pendingOperationProposalNeedsButton(opState.Proposal) {
		return ""
	}
	if !strings.HasPrefix(strings.TrimSpace(opState.ID), "organic-proposal-") && !strings.HasPrefix(strings.TrimSpace(opState.Proposal.ID), "organic-proposal-") {
		return ""
	}
	for _, finding := range opState.Findings {
		basis := strings.ToLower(strings.TrimSpace(finding.Basis))
		if strings.Contains(basis, "persisted continuation state carried") {
			return "Organic proposal was inferred only from inactive continuation projection."
		}
	}
	return ""
}

func staleContinuationStateMatchesOrganicProposal(state session.ContinuationState, opState session.OperationState) bool {
	state = session.NormalizeContinuationState(state)
	opState = session.NormalizeOperationState(opState)
	if state.Status != session.ContinuationStatusPending {
		return false
	}
	proposalID := strings.TrimSpace(opState.Proposal.ID)
	operationID := strings.TrimSpace(opState.ID)
	actionOperationID := strings.TrimSpace(state.ActionProposal.OperationID)
	actionID := strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	decisionID := strings.TrimSpace(state.DecisionID)
	for _, candidate := range []string{proposalID, operationID} {
		if candidate == "" {
			continue
		}
		if candidate == actionOperationID || candidate == actionID || candidate == decisionID {
			return true
		}
	}
	return false
}

func continuationApprovalBundleInvalidReason(plan session.OperationPhasePlan, bundle session.ContinuationApprovalBundle) string {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if len(bundle.Phases) == 0 {
		return ""
	}
	phaseByID := make(map[string]session.OperationPhase, len(plan.Phases))
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if id := strings.TrimSpace(phase.ID); id != "" {
			phaseByID[id] = phase
		}
	}
	family := ""
	for _, bundlePhase := range bundle.Phases {
		phaseID := strings.TrimSpace(bundlePhase.OperationPhaseID)
		phase, ok := phaseByID[phaseID]
		if !ok {
			phase = session.OperationPhase{
				ID:               phaseID,
				Summary:          bundlePhase.Summary,
				AuthorityClass:   bundlePhase.AuthorityClass,
				WhyNow:           bundlePhase.WhyNow,
				BoundedEffect:    bundlePhase.BoundedEffect,
				AllowedActions:   append([]string(nil), bundlePhase.AllowedActions...),
				ForbiddenActions: append([]string(nil), bundlePhase.ForbiddenActions...),
				ValidationPlan:   append([]string(nil), bundlePhase.ValidationPlan...),
				Status:           session.PlanStatusPending,
			}
		}
		if reason := operationPhaseApprovalExcludedReason(plan, phase); reason != "" {
			return reason
		}
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			return reason
		}
		phaseFamily := operationPhaseApprovalFamily(phase)
		if family == "" {
			family = phaseFamily
		} else if family != phaseFamily {
			return "mixed authority classes require separate approvals"
		}
		if operationPhaseRequiresFreshApprovalGate(phase) && len(bundle.Phases) > 1 {
			return "fresh approval gate cannot be bundled"
		}
	}
	return ""
}

func (r *Runtime) recordPlanningOnlyOperationPhaseBlocked(key session.SessionKey, opState session.OperationState, phase session.OperationPhase, now time.Time) {
	if r == nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", map[string]any{
		"blocked_reason": "planning_only_phase_requires_plan_lease",
		"phase_plan_id":  strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":       strings.TrimSpace(phase.ID),
		"phase_summary":  strings.TrimSpace(phase.Summary),
		"operation_id":   strings.TrimSpace(opState.ID),
	}, now)
}

func (r *Runtime) recordAndSendBlockedOperationPhaseApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, phase session.OperationPhase, reason string, now time.Time) {
	if r == nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "approval is blocked"
	}
	payload := map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           "phase_materialization",
		"subject_id":        strings.TrimSpace(phase.ID),
		"operator_label":    "Continuation approval blocked",
		"visible_action":    "blocked_status",
		"phase_plan_id":     strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":          strings.TrimSpace(phase.ID),
		"phase_summary":     strings.TrimSpace(phase.Summary),
		"operation_id":      strings.TrimSpace(opState.ID),
		"decision":          "blocked",
		"debug_breadcrumb": core.ContinuationDebugBreadcrumb(
			key.ChatID,
			strings.TrimSpace(phase.ID),
			"runtime.renderOperationPhaseApprovalBlockedStatus",
			"runtime/continuation_materialize.go",
			"inspect /health trace for phase plan, continuation state, and TES adjudication events",
		),
		"findings": []core.RuntimeFinding{{
			Kind:             "approval_blocked",
			EvidenceStatus:   "declared_by_phase_contract",
			Detail:           reason,
			RequiredBehavior: "Do not show approval buttons until a fresh eligible proposal exists.",
		}},
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", payload, now)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", map[string]any{
		"blocked_reason": reason,
		"phase_plan_id":  strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":       strings.TrimSpace(phase.ID),
		"phase_summary":  strings.TrimSpace(phase.Summary),
		"operation_id":   strings.TrimSpace(opState.ID),
		"debug_breadcrumb": core.ContinuationDebugBreadcrumb(
			key.ChatID,
			strings.TrimSpace(phase.ID),
			"runtime.renderOperationPhaseApprovalBlockedStatus",
			"runtime/continuation_materialize.go",
			"inspect /health trace for phase plan, continuation state, and TES blocked events",
		),
	}, now)
	if r.outbound == nil || msg.ChatID == 0 {
		return
	}
	replyTo := msg.MessageID
	var replyToPtr *int64
	if replyTo != 0 {
		replyToPtr = &replyTo
	}
	text := r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), renderOperationPhaseApprovalBlockedStatus(opState, phase, reason))
	_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    text,
		ReplyTo: replyToPtr,
	})
}

func renderOperationPhaseApprovalBlockedStatus(opState session.OperationState, phase session.OperationPhase, reason string) string {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	title := firstNonEmptyContinuation(phase.Summary, opState.PhasePlan.Goal, opState.Objective, "Next phase")
	lines := []string{"I can't continue that step yet.", "", "Plan: " + truncatePreview(title, 96)}
	if explanation := operationBlockedApprovalExplanation(phase, reason); explanation != "" {
		lines = append(lines, "", "Reason:", explanation)
	}
	if next := operationBlockedApprovalNextStep(phase, reason); next != "" {
		lines = append(lines, "", "Next:", next)
	}
	lines = append(lines, "", "Use /status for the current state.")
	return strings.Join(lines, "\n")
}

func operationBlockedApprovalExplanation(phase session.OperationPhase, reason string) string {
	switch operationBlockedApprovalKindFor(phase, reason) {
	case operationBlockedApprovalOptIn:
		return "The person who owns this data has not opted in yet."
	case operationBlockedApprovalConsent:
		return "This needs explicit consent from the right person before I can touch it."
	default:
		if strings.TrimSpace(reason) != "" && !operationBlockedReasonLooksInternal(reason) {
			return truncatePreview(strings.TrimSpace(reason), 180)
		}
		return "The current proposal does not give a clear enough boundary for this step."
	}
}

func operationBlockedApprovalNextStep(phase session.OperationPhase, reason string) string {
	switch operationBlockedApprovalKindFor(phase, reason) {
	case operationBlockedApprovalOptIn:
		return "Get explicit opt-in from the resource owner, then ask me to continue."
	case operationBlockedApprovalConsent:
		return "Get explicit consent from the resource owner, then approve a narrower step."
	default:
		return "Send a narrower request that names the resource, action, and stopping point."
	}
}

func operationBlockedApprovalKindFor(phase session.OperationPhase, reason string) operationBlockedApprovalKind {
	phase = normalizeSingleOperationPhase(phase)
	if phase.RequiresOptIn || operationPhaseReasonCodeRequiresOptIn(phase.BlockedReasonCode) || operationPhaseReasonCodeRequiresOptIn(phase.GateReasonCode) {
		return operationBlockedApprovalOptIn
	}
	if phase.RequiresConsent || operationPhaseReasonCodeRequiresConsent(phase.BlockedReasonCode) || operationPhaseReasonCodeRequiresConsent(phase.GateReasonCode) {
		return operationBlockedApprovalConsent
	}
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "waiting for explicit opt-in", "waiting for explicit opt in":
		return operationBlockedApprovalOptIn
	case "waiting for explicit consent", "blocked on consent":
		return operationBlockedApprovalConsent
	default:
		return operationBlockedApprovalUnknown
	}
}

func operationBlockedReasonLooksInternal(reason string) bool {
	reason = strings.TrimSpace(strings.ToLower(reason))
	return reason == "" ||
		strings.Contains(reason, "_") ||
		strings.Contains(reason, "blocked:") ||
		strings.Contains(reason, "phase") ||
		strings.Contains(reason, "lease") ||
		strings.Contains(reason, "proposal")
}
