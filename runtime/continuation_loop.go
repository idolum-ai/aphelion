//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type continuationLoopDecision struct {
	Continue bool
	Reason   string
	Boundary string
	Mission  missionLoopAssessment
}

type missionLoopAssessment struct {
	MissionID string
	Status    string
	Summary   string
	Continue  bool
}

func (r *Runtime) triggerContinuationLoop(ctx context.Context, key session.SessionKey) error {
	if r == nil {
		return nil
	}
	initial, err := r.ContinuationStateForKey(key)
	if err != nil {
		return err
	}
	initial = session.NormalizeContinuationState(initial)
	wasRunnable := initial.Status == session.ContinuationStatusApproved && initial.RemainingTurns > 0
	loopBudget := continuationLoopBudget(initial)

	state, err := r.triggerApprovedContinuationOnce(ctx, key)
	if err != nil {
		return err
	}
	if !wasRunnable {
		return nil
	}
	turnsRun := 1
	decision := r.continuationLoopDecisionForState(key, state, time.Now().UTC())
	r.recordContinuationLoopAssessment(key, state, decision, turnsRun)
	for {
		if !decision.Continue {
			r.recordContinuationLoopBoundary(key, state, decision, turnsRun)
			return r.maybeOfferNextOperationPhaseAfterContinuationBoundary(ctx, key, state, decision)
		}
		if turnsRun >= loopBudget {
			decision.Continue = false
			decision.Reason = "loop_budget_exhausted"
			decision.Boundary = fmt.Sprintf("automatic loop stopped after %d approved turn(s); remaining work requires a fresh trigger or approval boundary", turnsRun)
			r.recordContinuationLoopBoundary(key, state, decision, turnsRun)
			return r.maybeOfferNextOperationPhaseAfterContinuationBoundary(ctx, key, state, decision)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.sendContinuationLoopProgress(ctx, key, state, decision); err != nil {
			log.Printf("WARN continuation loop progress send failed chat_id=%d err=%v", key.ChatID, err)
		}
		state, err = r.triggerApprovedContinuationOnce(ctx, key)
		if err != nil {
			return err
		}
		turnsRun++
		decision = r.continuationLoopDecisionForState(key, state, time.Now().UTC())
		r.recordContinuationLoopAssessment(key, state, decision, turnsRun)
	}
}

func (r *Runtime) maybeOfferNextOperationPhaseAfterContinuationBoundary(ctx context.Context, key session.SessionKey, state session.ContinuationState, decision continuationLoopDecision) error {
	if r == nil || r.store == nil || key.ChatID == 0 {
		return nil
	}
	if !continuationBoundaryCanOfferNextOperationPhase(state, decision) {
		return nil
	}
	now := time.Now().UTC()
	opState, err := r.store.OperationState(key)
	if err != nil {
		return nil
	}
	opState, completed := operationStateWithConsumedWorkContinuationPhaseCompleted(opState, state, now)
	if completed {
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return fmt.Errorf("persist completed consumed operation phase: %w", err)
		}
	}
	if operationStatusIsTerminal(opState.Status) {
		return nil
	}
	if operationPhasePlanHasBlockingInProgress(opState.PhasePlan) {
		return nil
	}
	prompt := continuationNextPhasePromptText(opState, state)
	msg := core.InboundMessage{
		ChatID:   key.ChatID,
		SenderID: firstNonZeroInt64(state.ContinuationLease.ApprovedBy, state.ApprovedBy, key.UserID),
		Text:     prompt,
		Origin:   core.InboundOriginTurnAuthorization,
	}
	_, err = r.materializePendingOperationProposalApproval(ctx, key, msg, prompt, nil)
	return err
}

func continuationBoundaryCanOfferNextOperationPhase(state session.ContinuationState, decision continuationLoopDecision) bool {
	state = session.NormalizeContinuationState(state)
	if decision.Continue {
		return false
	}
	switch strings.TrimSpace(decision.Reason) {
	case "not_approved", "no_remaining_turns":
	default:
		return false
	}
	return state.ContinuationLease.Status == session.ContinuationLeaseStatusConsumed &&
		state.ContinuationLease.RemainingTurns <= 0 &&
		strings.TrimSpace(state.ContinuationLease.ID) != ""
}

func continuationNextPhasePromptText(opState session.OperationState, state session.ContinuationState) string {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	next := "Offer the next bounded approval for the remaining operation phase."
	if phase, ok := nextOperationPhaseForApproval(opState); ok {
		next = firstNonEmptyContinuation(phase.Summary, phase.ID, next)
	} else if bundle, ok := nextOperationPhaseBundleForApproval(opState); ok && len(bundle) > 0 {
		next = operationPhaseBundleSummary(continuationApprovalBundlePhasesFromOperation(opState, bundle))
	} else if lease, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
		next = firstNonEmptyContinuation(lease.Summary, lease.Objective, next)
	}
	return strings.TrimSpace(strings.Join([]string{
		"The previous approved continuation lease was consumed.",
		"Do not execute new work without approval.",
		"Next pending operation phase: " + next,
		firstNonEmptyContinuation(opState.Objective, state.Objective),
	}, "\n"))
}

func continuationLoopBudget(state session.ContinuationState) int {
	state = session.NormalizeContinuationState(state)
	budget := state.RemainingTurns
	if budget <= 0 {
		budget = 1
	}
	if budget > operationPlanBudgetMaxLanes {
		budget = operationPlanBudgetMaxLanes
	}
	return budget
}

func (r *Runtime) continuationLoopDecisionForState(key session.SessionKey, state session.ContinuationState, now time.Time) continuationLoopDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state = session.NormalizeContinuationState(state)
	decision := continuationLoopDecision{
		Continue: false,
		Reason:   "not_approved",
		Boundary: "no active approved continuation remains",
		Mission:  r.assessContinuationLoopMission(key, state, now),
	}
	if state.Status != session.ContinuationStatusApproved {
		return decision
	}
	if state.RemainingTurns <= 0 {
		decision.Reason = "no_remaining_turns"
		decision.Boundary = "approved continuation turns are exhausted"
		return decision
	}
	if !state.ContinuationLease.ActiveAt(now) {
		decision.Reason = "lease_inactive_or_expired"
		decision.Boundary = "approved lease is inactive or expired"
		return decision
	}
	if r.continuationBudgetRecoveryPending(key, state, now) {
		decision.Reason = "recovery_pending"
		decision.Boundary = "automatic recovery turn is already scheduled for this approved continuation"
		return decision
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		decision.Reason = "approval_only"
		decision.Boundary = "plan lease approval has no active executable bundle"
		return decision
	}
	if err := r.validateContinuationApprovalBundleFingerprints(key, state); err != nil {
		decision.Reason = "stale_bundle"
		decision.Boundary = "approved bundle fingerprint no longer matches durable operation state"
		return decision
	}
	if !decision.Mission.Continue {
		decision.Reason = "mission_boundary"
		decision.Boundary = decision.Mission.Summary
		if strings.TrimSpace(decision.Boundary) == "" {
			decision.Boundary = "mission state requires a stop before continuing"
		}
		return decision
	}
	decision.Continue = true
	decision.Reason = "lease_active"
	decision.Boundary = ""
	return decision
}

func (r *Runtime) assessContinuationLoopMission(key session.SessionKey, state session.ContinuationState, now time.Time) missionLoopAssessment {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	assessment := missionLoopAssessment{Status: "not_bound", Continue: true}
	if r == nil || r.store == nil {
		return assessment
	}
	state = session.NormalizeContinuationState(state)
	missionID := firstNonEmptyContinuation(state.ActionProposal.MissionID, state.ContinuationLease.MissionID)
	if missionID == "" {
		if opState, err := r.store.OperationState(key); err == nil {
			opState = session.NormalizeOperationState(opState)
			switch opState.Status {
			case session.OperationStatusCompleted:
				return missionLoopAssessment{Status: "operation_completed", Summary: "operation is already completed", Continue: false}
			case session.OperationStatusFailed:
				return missionLoopAssessment{Status: "operation_failed", Summary: "operation failed and needs review", Continue: false}
			}
		}
		return assessment
	}
	assessment.MissionID = missionID
	mission, ok, err := r.store.Mission(missionID)
	if err != nil {
		assessment.Status = "mission_read_failed"
		assessment.Summary = err.Error()
		return assessment
	}
	if !ok {
		assessment.Status = "mission_not_found"
		assessment.Summary = "mission record was not found; lease remains the active boundary"
		return assessment
	}
	mission = session.NormalizeMissionState(mission)
	assessment.Status = string(mission.Status)
	assessment.Summary = firstNonEmptyContinuation(mission.BlockedReason, mission.Objective, mission.Title)
	switch mission.Status {
	case session.MissionStatusCompleted:
		assessment.Continue = false
		assessment.Summary = firstNonEmptyContinuation(assessment.Summary, "mission is complete")
	case session.MissionStatusBlocked:
		assessment.Continue = false
		assessment.Summary = firstNonEmptyContinuation(mission.BlockedReason, "mission is blocked")
	case session.MissionStatusArchived, session.MissionStatusExpired, session.MissionStatusDormant:
		assessment.Continue = false
		assessment.Summary = "mission status is " + string(mission.Status)
	default:
		assessment.Continue = true
	}
	return assessment
}

func (r *Runtime) continuationBudgetRecoveryPending(key session.SessionKey, state session.ContinuationState, now time.Time) bool {
	if r == nil || r.store == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	scope, _ := r.turnBudgetRecoveryScope(key, core.InboundMessage{ChatID: key.ChatID, SenderID: firstNonZeroInt64(state.ApprovedBy, state.ContinuationLease.ApprovedBy, key.UserID)}, nil)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 100)
	if err != nil {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if strings.TrimSpace(event.EventType) != core.ExecutionEventTurnBudgetRecovery {
			continue
		}
		if !event.CreatedAt.IsZero() && now.Sub(event.CreatedAt) > turnBudgetRecoveryTimeout {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(payload["recovery_scope"])) != scope {
			continue
		}
		switch strings.TrimSpace(event.Status) {
		case "scheduled", "resuming":
			return true
		default:
			return false
		}
	}
	return false
}

func (r *Runtime) recordContinuationLoopAssessment(key session.SessionKey, state session.ContinuationState, decision continuationLoopDecision, turnsRun int) {
	if r == nil || r.store == nil {
		return
	}
	now := time.Now().UTC()
	payload := continuationExecutionPayload(state)
	payload["loop_turns_run"] = turnsRun
	payload["loop_continue"] = decision.Continue
	payload["loop_reason"] = strings.TrimSpace(decision.Reason)
	if missionID := strings.TrimSpace(decision.Mission.MissionID); missionID != "" {
		payload["mission_id"] = missionID
	}
	if status := strings.TrimSpace(decision.Mission.Status); status != "" {
		payload["mission_status"] = status
	}
	if summary := strings.TrimSpace(decision.Mission.Summary); summary != "" {
		payload["mission_summary"] = summary
	}
	r.recordExecutionEvent(key, core.ExecutionEventMissionProgressAssessed, "mission", "assessed", payload, now)
	if strings.TrimSpace(decision.Mission.MissionID) != "" {
		r.appendContinuationMissionEvent(decision.Mission.MissionID, core.ExecutionEventMissionProgressAssessed, "continuation loop assessed mission progress", payload, now)
	}
	if !decision.Mission.Continue && strings.TrimSpace(decision.Mission.Status) == string(session.MissionStatusCompleted) {
		r.recordExecutionEvent(key, core.ExecutionEventMissionCompletionDeclared, "mission", "completed", payload, now)
		r.appendContinuationMissionEvent(decision.Mission.MissionID, core.ExecutionEventMissionCompletionDeclared, "continuation loop observed mission completion", payload, now)
	}
}

func (r *Runtime) recordContinuationLoopBoundary(key session.SessionKey, state session.ContinuationState, decision continuationLoopDecision, turnsRun int) {
	if r == nil || r.store == nil {
		return
	}
	payload := continuationExecutionPayload(state)
	payload["loop_turns_run"] = turnsRun
	payload["boundary_reason"] = strings.TrimSpace(decision.Reason)
	payload["boundary"] = strings.TrimSpace(decision.Boundary)
	if missionID := strings.TrimSpace(decision.Mission.MissionID); missionID != "" {
		payload["mission_id"] = missionID
	}
	if status := strings.TrimSpace(decision.Mission.Status); status != "" {
		payload["mission_status"] = status
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBoundaryReached, "continuation", "boundary", payload, time.Now().UTC())
}

func (r *Runtime) appendContinuationMissionEvent(missionID string, eventType string, summary string, payload map[string]any, now time.Time) {
	if r == nil || r.store == nil || strings.TrimSpace(missionID) == "" {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	raw, _ := json.Marshal(payload)
	if _, err := r.store.AppendMissionEvent(session.MissionEvent{
		MissionID: strings.TrimSpace(missionID),
		EventType: strings.TrimSpace(eventType),
		Actor:     "runtime:continuation_loop",
		Summary:   strings.TrimSpace(summary),
		Payload:   string(raw),
		CreatedAt: now.UTC(),
	}); err != nil {
		log.Printf("WARN append continuation mission event failed mission_id=%s type=%s err=%v", strings.TrimSpace(missionID), strings.TrimSpace(eventType), err)
	}
}

func (r *Runtime) sendContinuationLoopProgress(ctx context.Context, key session.SessionKey, state session.ContinuationState, decision continuationLoopDecision) error {
	if r == nil || r.outbound == nil || key.ChatID == 0 {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	label := strings.TrimPrefix(continuationUserFacingPlanLabel(state), "Plan: ")
	if label == "" {
		label = firstNonEmptyContinuation(state.ActionProposal.Summary, state.StageSummary, "approved continuation")
	}
	parts := []string{
		fmt.Sprintf("Continuing approved step: %s.", label),
		fmt.Sprintf("Approved steps remaining: %d.", state.RemainingTurns),
	}
	if status := strings.TrimSpace(decision.Mission.Status); status != "" && status != "not_bound" {
		parts = append(parts, "Mission: "+status+".")
	}
	text := strings.Join(parts, " ")
	text = r.prefixTelegramPresentedText(r.telegramPresentationForKey(key), text)
	_, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: key.ChatID, Text: text})
	return err
}
