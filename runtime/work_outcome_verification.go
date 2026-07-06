//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	workOutcomeVerificationKind         = "work_outcome"
	workOutcomeVerificationRiskClass    = "read_only_review"
	workOutcomeVerificationClockSlack   = 2 * time.Second
	workOutcomeVerificationCandidateMax = 12
)

type workOutcomeVerificationResult struct {
	Verified     bool
	ReasonCode   string
	Summary      string
	ChangedFiles []string
}

func (r *Runtime) offerWorkOutcomeVerificationApproval(ctx context.Context, key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error, artifact session.OperationArtifact, resolution workOutcomeResolution) error {
	if r == nil || r.store == nil {
		if cause != nil {
			return cause
		}
		return nil
	}
	now := time.Now().UTC()
	target := session.NormalizeContinuationVerificationTarget(resolution.VerificationTarget)
	if target == nil {
		if cause != nil {
			return cause
		}
		return nil
	}
	if artifact.Ref != "" {
		copyTarget := *target
		copyTarget.EvidenceRefs = appendUniqueRuntimeString(copyTarget.EvidenceRefs, artifact.Ref)
		target = session.NormalizeContinuationVerificationTarget(&copyTarget)
	}
	state := workOutcomeVerificationContinuationState(req, *target, now)
	unlock := r.lockSession(key)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		unlock()
		return fmt.Errorf("persist work outcome verification continuation: %w", err)
	}
	payload := workResultPayload(req, result, status, cause)
	for k, v := range resolution.Payload {
		payload[k] = v
	}
	payload["reason"] = "work_outcome_verification_required"
	payload["verification_target"] = target.ReasonCode
	payload["verification_candidate_paths"] = target.CandidatePaths
	if artifact.Ref != "" {
		payload["artifact_ref"] = artifact.Ref
	}
	r.recordExecutionEvent(key, core.ExecutionEventWorkOutcomeVerificationOffered, "work", "verification_offered", payload, now)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", continuationExecutionPayload(state), now)
	unlock()
	msg := core.InboundMessage{ChatID: key.ChatID, SenderID: req.State.ApprovedBy, TelegramThreadID: continuationCallbackThreadIDForKey(key)}
	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, r.renderContinuationPrompt(ctx, key, msg, state))
}

func workOutcomeVerificationTargetForResult(req WorkRequest, result WorkResult, reason string, windowStart time.Time, windowEnd time.Time) *session.ContinuationVerificationTarget {
	candidates := candidatePathsForWorkOutcome(req, result)
	if len(candidates) == 0 {
		return nil
	}
	opState := session.NormalizeOperationState(req.Operation)
	phaseID := workOutcomeVerificationPhaseID(opState, req.State)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "side_effects_outcome_unverified"
	}
	target := &session.ContinuationVerificationTarget{
		Kind:                      workOutcomeVerificationKind,
		ReasonCode:                reason,
		OperationID:               strings.TrimSpace(req.OperationID),
		PhaseID:                   phaseID,
		OriginalLeaseID:           strings.TrimSpace(req.LeaseID),
		OriginalActionProposalID:  strings.TrimSpace(req.State.ActionProposal.ID),
		OriginalActionOperationID: strings.TrimSpace(req.State.ActionProposal.OperationID),
		OriginalWorkMode:          strings.TrimSpace(string(req.Mode)),
		RepoRoot:                  strings.TrimSpace(req.RepoRoot),
		Workdir:                   strings.TrimSpace(req.Workdir),
		WindowStart:               windowStart,
		WindowEnd:                 windowEnd,
		ClaimedSummary:            strings.TrimSpace(result.Summary),
		CandidatePaths:            candidates,
	}
	return session.NormalizeContinuationVerificationTarget(target)
}

func workOutcomeVerificationContinuationState(req WorkRequest, target session.ContinuationVerificationTarget, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	phaseLabel := firstNonEmptyContinuation(target.PhaseID, req.State.ActionProposal.OperationID, req.OperationID, "prior work")
	candidates := strings.Join(target.CandidatePaths, ", ")
	if candidates == "" {
		candidates = "the recorded work artifact and typed ledger evidence"
	}
	decisionID := "verify-" + sanitizeOperationPhaseProposalID(firstNonEmptyContinuation(phaseLabel, newContinuationDecisionID()))
	if len(decisionID) > 96 {
		decisionID = decisionID[:96]
	}
	auto := false
	action := session.ActionProposal{
		ID:                  "aprop-" + decisionID,
		OperationID:         decisionID,
		OperatorTitle:       "Verify prior outcome",
		PlanTitle:           "Verify prior outcome",
		Summary:             "Verify prior outcome for " + phaseLabel,
		WhyNow:              "The previous approved work reported side effects, but completion evidence was not strong enough to close the phase.",
		BoundedEffect:       "Read-only verification of prior work outcome only. Candidate evidence: " + candidates + ". Do not write files, rerun mutation, commit, push, deploy, restart, contact external accounts, or grant capabilities.",
		RiskClass:           workOutcomeVerificationRiskClass,
		AllowedActions:      []string{"read_workspace_evidence", "inspect_candidate_artifacts", "verify_prior_work_outcome", "report_verification_result"},
		ForbiddenActions:    []string{"write_files", "rerun_side_effecting_work", "commit", "push_remote", "deploy", "restart_service", "external_account_action", "credential_access", "grant_capability"},
		ValidationPlan:      []string{"inspect only the bounded candidate evidence", "confirm whether the original claimed outcome exists", "record a typed verification verdict before any broader continuation resumes"},
		AutoApproveEligible: &auto,
		ExpiresAt:           now.Add(continuationLeaseDefaultTTL),
		Status:              session.ProposalStatusPending,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	state := session.ContinuationState{
		Kind:               session.TurnAuthorizationKindContinuation,
		Status:             session.ContinuationStatusPending,
		DecisionID:         decisionID,
		Objective:          firstNonEmptyContinuation(req.Operation.Objective, req.State.Objective, "Verify prior work outcome before continuing."),
		StageSummary:       action.Summary,
		RemainingTurns:     1,
		ActionProposal:     session.NormalizeActionProposal(action),
		VerificationTarget: session.NormalizeContinuationVerificationTarget(&target),
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A prior bounded phase needs evidence verification before the operation can move on.",
			NextStep:   action.Summary,
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   action.WhyNow,
			NextStep:    action.Summary,
			Constraints: action.BoundedEffect,
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		UpdatedAt: now,
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	return session.NormalizeContinuationState(state)
}

func (r *Runtime) runReservedWorkOutcomeVerification(ctx context.Context, key session.SessionKey, reservation approvedContinuationReservation) error {
	if r == nil || r.store == nil || reservation.State.VerificationTarget == nil {
		return nil
	}
	target := session.NormalizeContinuationVerificationTarget(reservation.State.VerificationTarget)
	if target == nil {
		return nil
	}
	result := verifyWorkOutcomeTarget(*target)
	now := time.Now().UTC()
	payload := continuationExecutionPayload(reservation.State)
	payload["verification_reason"] = result.ReasonCode
	payload["verified"] = result.Verified
	payload["changed_files"] = result.ChangedFiles
	if result.Verified {
		if err := r.persistVerifiedWorkOutcome(key, *target, result, now); err != nil {
			return err
		}
		r.recordExecutionEvent(key, core.ExecutionEventWorkOutcomeVerificationCompleted, "work", "verified", payload, now)
		if _, err := r.materializePendingOperationProposalApproval(ctx, key, core.InboundMessage{ChatID: key.ChatID, SenderID: reservation.ApprovedBy, TelegramThreadID: continuationCallbackThreadIDForKey(key)}, "continue after verified outcome", nil); err != nil {
			return err
		}
		return nil
	}
	r.recordExecutionEvent(key, core.ExecutionEventWorkOutcomeVerificationInconclusive, "work", "inconclusive", payload, now)
	return r.offerInconclusiveWorkOutcomeReconciliation(ctx, key, reservation, *target, result, payload, now)
}

func (r *Runtime) offerInconclusiveWorkOutcomeReconciliation(ctx context.Context, key session.SessionKey, reservation approvedContinuationReservation, target session.ContinuationVerificationTarget, verdict workOutcomeVerificationResult, payload map[string]any, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := workOutcomeReconciliationContinuationState(reservation.State, target, verdict, now)
	unlock := r.lockSession(key)
	if err := r.persistInconclusiveWorkOutcome(key, target, verdict, state, now); err != nil {
		unlock()
		return err
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		unlock()
		return fmt.Errorf("persist work outcome reconciliation continuation: %w", err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["reason"] = "work_outcome_reconciliation_required"
	payload["reconciliation_reason"] = verdict.ReasonCode
	payload["verification_target"] = target.ReasonCode
	payload["verification_candidate_paths"] = target.CandidatePaths
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending_reconciliation", payload, now)
	unlock()
	msg := core.InboundMessage{ChatID: key.ChatID, SenderID: reservation.ApprovedBy, TelegramThreadID: continuationCallbackThreadIDForKey(key)}
	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, renderInconclusiveWorkOutcomeReconciliationPrompt(state, verdict))
}

func workOutcomeReconciliationContinuationState(prior session.ContinuationState, target session.ContinuationVerificationTarget, verdict workOutcomeVerificationResult, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	prior = session.NormalizeContinuationState(prior)
	if normalizedTarget := session.NormalizeContinuationVerificationTarget(&target); normalizedTarget != nil {
		target = *normalizedTarget
	}
	phaseLabel := firstNonEmptyContinuation(target.PhaseID, target.OriginalActionOperationID, target.OperationID, "prior work")
	reason := strings.TrimSpace(verdict.ReasonCode)
	if reason == "" {
		reason = "verification_inconclusive"
	}
	decisionID := "reconcile-" + sanitizeOperationPhaseProposalID(firstNonEmptyContinuation(phaseLabel, newContinuationDecisionID()))
	if len(decisionID) > 96 {
		decisionID = decisionID[:96]
	}
	candidates := strings.Join(target.CandidatePaths, ", ")
	if candidates == "" {
		candidates = "the recorded work artifact and typed ledger evidence"
	}
	summary := "Reconcile verification outcome for " + phaseLabel
	bounded := "Read-only outcome reconciliation only. Inspect the bounded candidate evidence (" + candidates + "), the operation ledger, and workspace state needed to determine whether the prior work can be verified or must be redone. Do not write files, rerun mutation, commit, push, deploy, restart, contact external accounts, or grant capabilities."
	auto := false
	action := session.ActionProposal{
		ID:                  "aprop-" + decisionID,
		OperationID:         decisionID,
		OperatorTitle:       "Reconcile prior outcome",
		PlanTitle:           "Reconcile prior outcome",
		Summary:             summary,
		WhyNow:              "The deterministic artifact check was inconclusive (" + reason + "), so the runtime needs a bounded read-only reconciliation before moving to the next phase.",
		BoundedEffect:       bounded,
		RiskClass:           workOutcomeVerificationRiskClass,
		AllowedActions:      []string{"read_workspace_evidence", "inspect_operation_ledger", "compare_candidate_artifacts", "report_verification_result", "propose_next_bounded_phase"},
		ForbiddenActions:    []string{"write_files", "rerun_side_effecting_work", "commit", "push_remote", "deploy", "restart_service", "external_account_action", "credential_access", "grant_capability"},
		ValidationPlan:      []string{"inspect only existing bounded evidence", "report whether the prior outcome is verified, unverifiable, or needs rerun", "propose the next bounded phase without claiming completion unless evidence is present"},
		AutoApproveEligible: &auto,
		ExpiresAt:           now.Add(continuationLeaseDefaultTTL),
		Status:              session.ProposalStatusPending,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	state := session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        decisionID,
		Objective:         firstNonEmptyContinuation(prior.Objective, "Reconcile prior work outcome before continuing."),
		StageSummary:      summary,
		RemainingTurns:    1,
		ActionProposal:    session.NormalizeActionProposal(action),
		PersonaIntent:     session.ContinuationIntent{Decision: session.ContinuationIntentDecisionContinue, Rationale: action.WhyNow, NextStep: summary, Confidence: "high", UpdatedAt: now},
		GovernorIntent:    session.ContinuationIntent{Decision: session.ContinuationIntentDecisionContinue, Rationale: action.WhyNow, NextStep: summary, Constraints: bounded, Confidence: "high", Ratified: true, UpdatedAt: now},
		ApprovedBy:        0,
		DecisionMessageID: 0,
		UpdatedAt:         now,
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	return session.NormalizeContinuationState(state)
}

func renderInconclusiveWorkOutcomeReconciliationPrompt(state session.ContinuationState, verdict workOutcomeVerificationResult) string {
	state = session.NormalizeContinuationState(state)
	reason := strings.TrimSpace(verdict.ReasonCode)
	if reason == "" {
		reason = "verification_inconclusive"
	}
	card := newContinuationApprovalPromptCard("Approve", state.ActionProposal.Summary, state.RemainingTurns)
	card.addSection("Why", "The approved verification could not prove the prior work outcome: "+reason+".")
	card.addSection("Scope", state.ActionProposal.BoundedEffect)
	card.addListSection("Covers", continuationApprovalPromptIncludedLines(state))
	card.addListSection("Stops before", continuationApprovalPromptStops(state))
	return card.String()
}

func (r *Runtime) persistInconclusiveWorkOutcome(key session.SessionKey, target session.ContinuationVerificationTarget, verdict workOutcomeVerificationResult, state session.ContinuationState, now time.Time) error {
	opState, err := r.store.OperationState(key)
	if err != nil {
		return fmt.Errorf("read operation for inconclusive work outcome: %w", err)
	}
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	opState.Work.LastOperationID = firstNonEmptyContinuation(target.OperationID, opState.ID)
	opState.Work.LastActionProposalID = strings.TrimSpace(target.OriginalActionProposalID)
	opState.Work.LastActionOperationID = strings.TrimSpace(target.OriginalActionOperationID)
	opState.Work.LastLeaseID = strings.TrimSpace(target.OriginalLeaseID)
	opState.Work.LastWorkMode = strings.TrimSpace(target.OriginalWorkMode)
	opState.Work.RepoRoot = firstNonEmptyContinuation(target.RepoRoot, opState.Work.RepoRoot)
	opState.Work.Workdir = firstNonEmptyContinuation(target.Workdir, opState.Work.Workdir)
	opState.Work.LastSummary = strings.TrimSpace(verdict.Summary)
	opState.Work.LastError = "work outcome verification inconclusive: " + firstNonEmptyContinuation(verdict.ReasonCode, "verification_inconclusive")
	opState.Work.LastCompletedAt = time.Time{}
	opState.Work.LastExecutorUpdatedAt = now
	opState.Status = session.OperationStatusActive
	opState.Stage = "verification_inconclusive"
	opState.Proposal = session.OperationProposal{
		ID:            strings.TrimSpace(state.ActionProposal.OperationID),
		Kind:          strings.TrimSpace(state.ActionProposal.RiskClass),
		OperatorTitle: strings.TrimSpace(state.ActionProposal.OperatorTitle),
		PlanTitle:     strings.TrimSpace(state.ActionProposal.PlanTitle),
		Summary:       strings.TrimSpace(state.ActionProposal.Summary),
		WhyNow:        strings.TrimSpace(state.ActionProposal.WhyNow),
		BoundedEffect: strings.TrimSpace(state.ActionProposal.BoundedEffect),
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	opState.UpdatedAt = now
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return fmt.Errorf("persist inconclusive work outcome: %w", err)
	}
	r.markEffectAttemptsForVerificationTarget(key, target, session.EffectAttemptStatusUncertain, "work outcome verification inconclusive: "+firstNonEmptyContinuation(verdict.ReasonCode, "verification_inconclusive"), now)
	return nil
}

func (r *Runtime) persistVerifiedWorkOutcome(key session.SessionKey, target session.ContinuationVerificationTarget, verdict workOutcomeVerificationResult, now time.Time) error {
	unlock := r.lockSession(key)
	defer unlock()
	opState, err := r.store.OperationState(key)
	if err != nil {
		return fmt.Errorf("read operation for verified work outcome: %w", err)
	}
	opState = session.NormalizeOperationState(opState)
	opState.Work.LastOperationID = firstNonEmptyContinuation(target.OperationID, opState.ID)
	opState.Work.LastActionProposalID = strings.TrimSpace(target.OriginalActionProposalID)
	opState.Work.LastActionOperationID = strings.TrimSpace(target.OriginalActionOperationID)
	opState.Work.LastLeaseID = strings.TrimSpace(target.OriginalLeaseID)
	opState.Work.LastWorkMode = strings.TrimSpace(target.OriginalWorkMode)
	opState.Work.RepoRoot = firstNonEmptyContinuation(target.RepoRoot, opState.Work.RepoRoot)
	opState.Work.Workdir = firstNonEmptyContinuation(target.Workdir, opState.Work.Workdir)
	opState.Work.ChangedFiles = append([]string(nil), verdict.ChangedFiles...)
	opState.Work.LastSummary = strings.TrimSpace(verdict.Summary)
	opState.Work.LastError = ""
	opState.Work.LastCompletedAt = now
	opState.Work.LastExecutorUpdatedAt = now
	r.markEffectAttemptsForVerificationTarget(key, target, session.EffectAttemptStatusVerified, "", now)
	opState, _ = operationStateWithVerifiedWorkOutcomePhaseCompleted(opState, target, now)
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return fmt.Errorf("persist verified work outcome: %w", err)
	}
	return nil
}

func (r *Runtime) markEffectAttemptsForVerificationTarget(key session.SessionKey, target session.ContinuationVerificationTarget, status session.EffectAttemptStatus, errorText string, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	attempts, err := r.store.EffectAttemptsForWork(key, target.OperationID, target.PhaseID, target.OriginalLeaseID, target.OriginalActionProposalID)
	if err != nil {
		return
	}
	for _, attempt := range attempts {
		if !session.EffectAttemptHasSideEffects(attempt) {
			continue
		}
		if _, err := r.store.UpsertEffectAttempt(session.EffectAttemptInput{
			AttemptID:    attempt.AttemptID,
			Key:          key,
			TurnRunID:    attempt.TurnRunID,
			OperationID:  attempt.OperationID,
			PhaseID:      attempt.PhaseID,
			LeaseID:      attempt.LeaseID,
			ProposalID:   attempt.ProposalID,
			WorkMode:     attempt.WorkMode,
			Executor:     attempt.Executor,
			Tool:         attempt.Tool,
			Command:      attempt.Command,
			EffectKind:   attempt.EffectKind,
			EffectReason: attempt.EffectReason,
			BoundaryKind: attempt.BoundaryKind,
			SubjectJSON:  attempt.SubjectJSON,
			Status:       status,
			ErrorText:    errorText,
			EvidenceRefs: append(attempt.EvidenceRefs, "verification:"+target.ReasonCode),
			StartedAt:    attempt.StartedAt,
			CompletedAt:  now,
			UpdatedAt:    now,
		}); err != nil {
			continue
		}
	}
}

func operationStateWithVerifiedWorkOutcomePhaseCompleted(opState session.OperationState, target session.ContinuationVerificationTarget, now time.Time) (session.OperationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	normalizedTarget := session.NormalizeContinuationVerificationTarget(&target)
	if normalizedTarget == nil {
		return opState, false
	}
	target = *normalizedTarget
	updated := false
	for i := range opState.PhasePlan.Phases {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if phase.Status != session.PlanStatusInProgress {
			continue
		}
		if target.PhaseID != "" && strings.TrimSpace(phase.ID) != target.PhaseID {
			continue
		}
		if target.OriginalLeaseID != "" && strings.TrimSpace(phase.LeaseID) != target.OriginalLeaseID {
			continue
		}
		if target.OriginalActionOperationID != "" && operationPhaseProposalID(opState, phase) != target.OriginalActionOperationID {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusCompleted
		if opState.PhasePlan.Phases[i].CompletedAt.IsZero() {
			opState.PhasePlan.Phases[i].CompletedAt = now
		}
		updated = true
		break
	}
	if !updated {
		return opState, false
	}
	if reconciled, reconciledDuplicates := operationStateWithCompletedPhaseDuplicatesReconciled(opState, now); reconciledDuplicates {
		opState = reconciled
	}
	if reconciled, clearedStaleLease := operationStateWithStalePlanLeaseCleared(opState, now); clearedStaleLease {
		opState = reconciled
	}
	if closed, completed := operationStateWithCompletedPhasePlanClosed(opState, now); completed {
		return session.NormalizeOperationState(closed), true
	}
	opState.Status = session.OperationStatusActive
	opState.Stage = firstNonEmptyContinuation(strings.TrimSpace(opState.Stage), "phase_completed")
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState), true
}

func verifyWorkOutcomeTarget(target session.ContinuationVerificationTarget) workOutcomeVerificationResult {
	normalizedTarget := session.NormalizeContinuationVerificationTarget(&target)
	if normalizedTarget == nil {
		return workOutcomeVerificationResult{ReasonCode: "verification_target_missing", Summary: "No verification target was recorded."}
	}
	target = *normalizedTarget
	switch WorkMode(target.OriginalWorkMode) {
	case WorkModeWorkspaceWrite:
		return verifyWorkspaceWriteOutcome(target)
	case WorkModeCommit:
		return workOutcomeVerificationResult{ReasonCode: "commit_outcome_requires_existing_reconciliation", Summary: "Commit outcome verification is handled by local git reconciliation or remains inconclusive."}
	default:
		return workOutcomeVerificationResult{ReasonCode: "typed_evidence_required", Summary: "No deterministic local verifier exists for this authority class without existing typed evidence."}
	}
}

func verifyWorkspaceWriteOutcome(target session.ContinuationVerificationTarget) workOutcomeVerificationResult {
	base := firstNonEmptyContinuation(target.Workdir, target.RepoRoot)
	if strings.TrimSpace(base) == "" {
		return workOutcomeVerificationResult{ReasonCode: "verification_workdir_missing", Summary: "No approved workspace root was recorded for verification."}
	}
	var verified []string
	for _, candidate := range target.CandidatePaths {
		rel, full, ok := safeWorkspaceVerificationPath(base, candidate)
		if !ok {
			continue
		}
		info, err := os.Lstat(full)
		if err != nil || info.IsDir() {
			continue
		}
		if !verificationModTimeMatchesWindow(info.ModTime(), target.WindowStart, target.WindowEnd) {
			continue
		}
		verified = appendUniqueRuntimeString(verified, rel)
	}
	if len(verified) == 0 {
		return workOutcomeVerificationResult{ReasonCode: "candidate_artifacts_not_verified", Summary: "No bounded candidate artifact could be verified inside the approved workspace."}
	}
	return workOutcomeVerificationResult{
		Verified:     true,
		ReasonCode:   "workspace_artifacts_verified",
		Summary:      "Verified prior workspace-write outcome from candidate artifact evidence.",
		ChangedFiles: verified,
	}
}

func safeWorkspaceVerificationPath(base string, candidate string) (string, string, bool) {
	base = strings.TrimSpace(base)
	candidate = strings.TrimSpace(candidate)
	if base == "" || candidate == "" {
		return "", "", false
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", "", false
	}
	var full string
	if filepath.IsAbs(candidate) {
		full = filepath.Clean(candidate)
	} else {
		full = filepath.Join(baseAbs, filepath.Clean(candidate))
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", "", false
	}
	rel, err := filepath.Rel(baseAbs, fullAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", "", false
	}
	return filepath.ToSlash(rel), fullAbs, true
}

func verificationModTimeMatchesWindow(modTime time.Time, windowStart time.Time, windowEnd time.Time) bool {
	if modTime.IsZero() || windowStart.IsZero() {
		return true
	}
	modTime = modTime.UTC()
	if modTime.Before(windowStart.UTC().Add(-workOutcomeVerificationClockSlack)) {
		return false
	}
	if !windowEnd.IsZero() && modTime.After(windowEnd.UTC().Add(workOutcomeVerificationClockSlack)) {
		return false
	}
	return true
}

func workOutcomeVerificationPhaseID(opState session.OperationState, state session.ContinuationState) string {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if strings.TrimSpace(phase.LeaseID) != "" && strings.TrimSpace(phase.LeaseID) == strings.TrimSpace(state.ContinuationLease.ID) {
			return strings.TrimSpace(phase.ID)
		}
		if operationPhaseMatchesConsumedContinuation(opState, phase, state) {
			return strings.TrimSpace(phase.ID)
		}
	}
	return ""
}

var workOutcomePathCandidateRE = regexp.MustCompile(`(?i)(?:^|[\s"'(:=])((?:[./A-Za-z0-9_-]+/)+[A-Za-z0-9_.-]+\.(?:md|txt|json|yaml|yml|toml|go|py|sh|sql|csv|html|css|js|ts|tsx|jsx|png|jpg|jpeg|gif|svg|pdf))`)

func candidatePathsForWorkOutcome(req WorkRequest, result WorkResult) []string {
	var candidates []string
	for _, path := range result.ChangedFiles {
		candidates = appendUniqueRuntimeString(candidates, path)
	}
	text := strings.Join(append([]string{result.Summary, result.PatchPreview, result.CommitLaneStatus}, result.Commands...), "\n")
	for _, match := range workOutcomePathCandidateRE.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 {
			continue
		}
		value := strings.Trim(match[1], " \t\r\n\"'`)")
		if rel, _, ok := safeWorkspaceVerificationPath(firstNonEmptyContinuation(req.Workdir, req.RepoRoot), value); ok {
			candidates = appendUniqueRuntimeString(candidates, rel)
		}
		if len(candidates) >= workOutcomeVerificationCandidateMax {
			break
		}
	}
	return candidates
}
