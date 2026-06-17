//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	reentryRecommendationDelay   = 5 * time.Minute
	reentryRecommendationCadence = time.Minute
	reentryRecommendationLimit   = 96
	reentryCandidateLimit        = 3
	reentryBodyTextRuneLimit     = 700
	reentryIgnoredDampeningTTL   = 12 * time.Hour
	reentryStaleDampeningTTL     = 6 * time.Hour
)

func (r *Runtime) StartReentryRecommendationLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || r.store == nil || r.outbound == nil {
		return
	}
	if logger == nil {
		logger = log.Printf
	}
	r.startBackgroundLoop("reentry_recommendation", func() {
		runPeriodic(ctx, reentryRecommendationCadence, func(runCtx context.Context) {
			if err := r.runReentryRecommendationSweepOnce(runCtx, time.Now().UTC()); err != nil {
				logger("WARN reentry recommendation sweep failed: %v", err)
				r.reportOperationalIssue(runCtx, "reentry_recommendation", err)
			}
		})
	})
}

func (r *Runtime) runReentryRecommendationSweepOnce(ctx context.Context, now time.Time) error {
	if r == nil || r.store == nil || r.outbound == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	runs, err := r.store.LatestTurnRunsBySession(reentryRecommendationLimit)
	if err != nil {
		return err
	}
	for _, run := range runs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := r.maybeSurfaceReentryRecommendation(ctx, run, now); err != nil {
			key := session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: run.Scope}
			r.recordExecutionEvent(key, core.ExecutionEventReentryRecommendationFailed, "reentry_recommendation", "failed", map[string]any{
				"turn_run_id": run.ID,
				"error":       trimError(err.Error()),
			}, now)
			log.Printf("WARN reentry recommendation failed chat_id=%d session_id=%s run_id=%d err=%v", run.ChatID, run.SessionID, run.ID, err)
		}
	}
	return nil
}

func (r *Runtime) maybeSurfaceReentryRecommendation(ctx context.Context, run session.TurnRun, now time.Time) error {
	key := session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: run.Scope}
	state, ok, reason, err := r.reentryRecommendationState(ctx, key, run, now)
	if err != nil || !ok {
		_ = reason
		return err
	}
	exists, err := r.reentryRecommendationTerminalFingerprintExists(state.Run.SessionID, state.Fingerprint)
	if err != nil || exists {
		return err
	}
	state.Evidence = r.reentryRecommendationEvidence(ctx, key, state, now)
	candidates := r.reentryRecommendationCandidates(ctx, state)
	if len(candidates) == 0 {
		return nil
	}
	if reentryRecommendationLowValueOnly(candidates) {
		r.recordReentryRecommendationJudgment(key, "suppressed_low_value", map[string]any{
			"recommendation_id":    reentryRecommendationID(state.Fingerprint),
			"turn_run_id":          state.Run.ID,
			"terminal_fingerprint": state.Fingerprint,
			"candidate_count":      len(candidates),
			"candidate_order":      reentryRecommendationCandidateIDs(candidates),
			"candidates":           reentryRecommendationAuditCandidates(candidates),
		}, now)
		return nil
	}
	r.recordReentryRecommendationJudgment(key, "deterministic_ranked", map[string]any{
		"recommendation_id":    reentryRecommendationID(state.Fingerprint),
		"turn_run_id":          state.Run.ID,
		"terminal_fingerprint": state.Fingerprint,
		"candidate_count":      len(candidates),
		"candidate_order":      reentryRecommendationCandidateIDs(candidates),
		"candidates":           reentryRecommendationAuditCandidates(candidates),
	}, now)
	candidates = r.rankReentryRecommendationCandidates(ctx, state, candidates)
	if len(candidates) > reentryCandidateLimit {
		candidates = candidates[:reentryCandidateLimit]
	}
	record := session.ReentryRecommendation{
		ID:                  reentryRecommendationID(state.Fingerprint),
		Owner:               reentryRecommendationOwner(run),
		ChatID:              run.ChatID,
		SessionID:           run.SessionID,
		Scope:               run.Scope,
		SourceTurnRunID:     run.ID,
		TerminalFingerprint: state.Fingerprint,
		Candidates:          candidates,
	}
	record, allowed, reason, err := r.store.CreateReentryRecommendationIfAllowed(record, now)
	if err != nil || !allowed {
		_ = reason
		return err
	}
	return r.deliverReentryRecommendation(ctx, key, record, now)
}

type reentryRecommendationState struct {
	Key          session.SessionKey
	Run          session.TurnRun
	Operation    session.OperationState
	Plan         session.PlanState
	Continuation session.ContinuationState
	Missions     []session.MissionState
	MemoryNotes  []string
	Threads      []session.TelegramThread
	Signals      []session.InteriorSignalState
	Evidence     session.EvidenceHydrationResult
	Now          time.Time
	Fingerprint  string
}

func (r *Runtime) reentryRecommendationState(ctx context.Context, key session.SessionKey, run session.TurnRun, now time.Time) (reentryRecommendationState, bool, string, error) {
	_ = ctx
	if run.ChatID == 0 {
		return reentryRecommendationState{}, false, "missing_chat", nil
	}
	if run.Kind != session.TurnRunKindInteractive && run.Kind != session.TurnRunKindRecovery {
		return reentryRecommendationState{}, false, "non_interactive_run", nil
	}
	if run.Status == session.TurnRunStatusRunning {
		return reentryRecommendationState{}, false, "running", nil
	}
	terminalAt := run.CompletedAt
	if terminalAt.IsZero() {
		terminalAt = run.LastActivityAt
	}
	if terminalAt.IsZero() || now.Before(terminalAt.Add(reentryRecommendationDelay)) {
		return reentryRecommendationState{}, false, "quiet_window", nil
	}
	latest, err := r.store.LatestTurnRun(key)
	if err != nil {
		return reentryRecommendationState{}, false, "", err
	}
	if latest == nil || latest.ID != run.ID || latest.Status == session.TurnRunStatusRunning {
		return reentryRecommendationState{}, false, "newer_turn", nil
	}

	continuation, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return reentryRecommendationState{}, false, "", err
	}
	continuation = session.NormalizeContinuationState(continuation)
	if exists && (continuation.Status == session.ContinuationStatusPending || continuation.Status == session.ContinuationStatusApproved || continuation.Active()) {
		return reentryRecommendationState{}, false, "continuation_active", nil
	}
	op, err := r.store.OperationState(key)
	if err != nil {
		return reentryRecommendationState{}, false, "", err
	}
	op = session.NormalizeOperationState(op)
	if op.Proposal.Status == session.ProposalStatusPending {
		return reentryRecommendationState{}, false, "operation_proposal_pending", nil
	}
	plan, _ := r.store.PlanState(key)
	plan = session.NormalizePlanState(plan)
	missions := r.reentryRecommendationMissions(run)
	state := reentryRecommendationState{
		Key:          key,
		Run:          run,
		Operation:    op,
		Plan:         plan,
		Continuation: continuation,
		Missions:     missions,
		MemoryNotes:  r.reentryRecommendationMemoryNotes(now),
		Threads:      r.reentryRecommendationThreads(run),
		Signals:      r.reentryRecommendationInteriorSignals(key, now),
		Now:          now,
	}
	state.Fingerprint = r.reentryRecommendationFingerprint(state)
	return state, true, "", nil
}

func (r *Runtime) reentryRecommendationTerminalFingerprintExists(sessionID string, fingerprint string) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	return r.store.ReentryRecommendationTerminalFingerprintExists(sessionID, fingerprint)
}

func (r *Runtime) reentryRecommendationMissions(run session.TurnRun) []session.MissionState {
	if r == nil || r.store == nil {
		return nil
	}
	owner := reentryRecommendationOwner(run)
	if owner == "" {
		return nil
	}
	out := make([]session.MissionState, 0, 4)
	for _, status := range []session.MissionStatus{session.MissionStatusBlocked, session.MissionStatusActive, session.MissionStatusCandidate} {
		missions, err := r.store.Missions(session.MissionFilter{Owner: owner, Status: status, Limit: 4})
		if err != nil {
			continue
		}
		out = append(out, missions...)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func (r *Runtime) reentryRecommendationMemoryNotes(now time.Time) []string {
	if r == nil || r.cfg == nil {
		return nil
	}
	root := strings.TrimSpace(r.cfg.Agent.SharedMemoryRoot)
	if root == "" {
		root = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}
	notes := r.loadRecentDailyNotes(root, now)
	if len(notes) > 2 {
		return notes[:2]
	}
	return notes
}

func (r *Runtime) reentryRecommendationThreads(run session.TurnRun) []session.TelegramThread {
	if r == nil || r.store == nil || run.ChatID == 0 {
		return nil
	}
	threads, err := r.store.ListTelegramThreadsByView(run.ChatID, "open", 8)
	if err != nil {
		return nil
	}
	return threads
}

func (r *Runtime) reentryRecommendationInteriorSignals(key session.SessionKey, now time.Time) []session.InteriorSignalState {
	if r == nil || r.store == nil {
		return nil
	}
	out := make([]session.InteriorSignalState, 0, 8)
	if states, err := r.store.InteriorSignalStates(key, now); err == nil {
		out = append(out, states...)
	}
	if states, err := r.store.InteriorSignalStates(heartbeatSignalKey(), now); err == nil {
		out = append(out, states...)
	}
	return compactReentryInteriorSignals(out)
}

func compactReentryInteriorSignals(states []session.InteriorSignalState) []session.InteriorSignalState {
	seen := map[string]struct{}{}
	out := make([]session.InteriorSignalState, 0, len(states))
	for _, state := range states {
		if state.Category == "" || state.SubjectKey == "" || state.Intensity <= 0.05 {
			continue
		}
		key := state.SessionID + "\x00" + state.Category + "\x00" + state.SubjectKey
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, state)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Intensity == out[j].Intensity {
			return out[i].LastObservedAt.After(out[j].LastObservedAt)
		}
		return out[i].Intensity > out[j].Intensity
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func (r *Runtime) reentryRecommendationEvidence(ctx context.Context, key session.SessionKey, state reentryRecommendationState, now time.Time) session.EvidenceHydrationResult {
	if r == nil || r.store == nil {
		return session.EvidenceHydrationResult{}
	}
	query := reentryRecommendationSourceText(state)
	if strings.TrimSpace(query) == "" {
		query = strings.TrimSpace(state.Run.RequestText)
	}
	if strings.TrimSpace(query) == "" && strings.TrimSpace(state.Operation.ID) == "" {
		return session.EvidenceHydrationResult{}
	}
	result, err := r.store.HydrateEvidence(session.EvidenceHydrationQuery{
		Key:         key,
		OperationID: strings.TrimSpace(state.Operation.ID),
		Query:       query,
		Limit:       8,
		Now:         now,
	})
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventReentryRecommendationJudged, "reentry_recommendation", "evidence_failed", map[string]any{
			"turn_run_id": state.Run.ID,
			"error":       trimError(err.Error()),
		}, now)
		return session.EvidenceHydrationResult{}
	}
	_ = ctx
	return result
}

func (r *Runtime) reentryRecommendationFingerprint(state reentryRecommendationState) string {
	op := session.NormalizeOperationState(state.Operation)
	cont := session.NormalizeContinuationState(state.Continuation)
	fields := []string{
		state.Run.SessionID,
		fmt.Sprintf("run:%d", state.Run.ID),
		string(state.Run.Kind),
		string(state.Run.Status),
		state.Run.CompletedAt.UTC().Format(time.RFC3339Nano),
		state.Run.ErrorText,
		op.ID,
		string(op.Status),
		op.Stage,
		op.Proposal.ID,
		string(op.Proposal.Status),
		cont.DecisionID,
		string(cont.Status),
		cont.ContinuationLease.ID,
		string(cont.ContinuationLease.Status),
	}
	for _, mission := range state.Missions {
		fields = append(fields, "mission:"+strings.TrimSpace(mission.ID), string(mission.Status), mission.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	for _, thread := range state.Threads {
		fields = append(fields, fmt.Sprintf("thread:%d:%d", thread.ChatID, thread.ThreadID), string(thread.Status), thread.LastActivityAt.UTC().Format(time.RFC3339Nano), thread.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	for _, signal := range state.Signals {
		fields = append(fields, "signal:"+strings.TrimSpace(signal.SessionID)+":"+strings.TrimSpace(signal.Category)+":"+strings.TrimSpace(signal.SubjectKey), signal.LastObservedAt.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Runtime) reentryRecommendationCandidates(ctx context.Context, state reentryRecommendationState) []session.ReentryRecommendationCandidate {
	_ = ctx
	source := strings.ToLower(reentryRecommendationSourceText(state))
	op := session.NormalizeOperationState(state.Operation)
	candidates := make([]session.ReentryRecommendationCandidate, 0, 8)
	if reentryLooksReleaseRelated(source) {
		subject := reentryOperationSubject(op)
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               "c1",
			Kind:             session.ReentryCandidateReviewReleaseReadiness,
			Label:            reentryConcreteLabel("Check release", subject),
			BodyText:         reentryConcreteBodyText("Check release", reentryOperationBodySubject(op)),
			Summary:          reentryConcreteSummary("Review release-oriented state and identify whether a deploy/release proposal is actually warranted.", subject),
			PromptText:       reentryPromptForCandidate("Review the selected release path from saved state: " + subject + ". If release, deploy, or restart action is needed, ask for the exact bounded approval before acting."),
			IntentClass:      "review_release_readiness",
			TemporalFit:      "now",
			WhyNow:           "release/deploy state is explicit in the current saved work surface",
			AuthorityClass:   "read_only",
			RequiresApproval: true,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID), "operation_state"},
			SourceKind:       "operation_state",
			SourceRef:        reentryOperationSourceRef(state.Operation),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     4.5,
				"user_intent_fit":   4.0,
				"evidence_strength": reentryEvidenceStrengthScore(state.Evidence),
				"resurfacing_value": 2.0,
				"authority_cost":    1.5,
				"staleness_risk":    reentryOperationStalenessRisk(state.Operation),
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Release/deploy signals are explicit in current saved state; review readiness before proposing action.",
		})
	}
	if op.Active() && (op.Status == session.OperationStatusCompleted || op.Status == session.OperationStatusBlocked || op.Status == session.OperationStatusActive) {
		kind := session.ReentryCandidateRequestNextLease
		intentClass := "continue_operation"
		labelPrefix := "Continue"
		if op.Status == session.OperationStatusBlocked {
			intentClass = "repair_blocker"
			labelPrefix = "Repair"
		} else if op.Status == session.OperationStatusCompleted {
			intentClass = "follow_up_operation"
			labelPrefix = "Follow up"
		}
		subject := reentryOperationSubject(op)
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             kind,
			Label:            reentryConcreteLabel(labelPrefix, subject),
			BodyText:         reentryConcreteBodyText(labelPrefix, reentryOperationBodySubject(op)),
			Summary:          reentryConcreteSummary("Reconstruct the operation state and choose the smallest useful next approval.", subject),
			PromptText:       reentryPromptForCandidate("Continue the selected operation path: " + subject + ". Take the next safe non-boundary step now. If boundary authority is required, ask for that exact bounded approval before acting."),
			IntentClass:      intentClass,
			TemporalFit:      reentryOperationTemporalFit(op),
			WhyNow:           "current operation state is the nearest durable work surface",
			AuthorityClass:   firstNonEmpty(op.PhasePlan.CurrentPhaseID, "operation"),
			RequiresApproval: true,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID), "operation_state"},
			SourceKind:       "operation_state",
			SourceRef:        reentryOperationSourceRef(op),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     reentryOperationRelevanceScore(op),
				"user_intent_fit":   4.5,
				"evidence_strength": reentryEvidenceStrengthScore(state.Evidence),
				"resurfacing_value": 1.0,
				"authority_cost":    reentryOperationAuthorityCost(op),
				"staleness_risk":    reentryOperationStalenessRisk(op),
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Current operation state is the nearest durable work surface; ask only for the smallest bounded next lease.",
		})
	}
	for _, mission := range state.Missions {
		subject := reentryMissionSubject(mission)
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateResumeMission,
			Label:            reentryMissionCandidateLabel(mission),
			BodyText:         reentryMissionCandidateBodyText(mission),
			Summary:          reentryConcreteSummary("Review the selected mission without turning memory into hidden authority.", subject),
			PromptText:       reentryPromptForCandidate("Review the selected mission path: " + subject + ". Decide whether it deserves attention now; if action is needed, ask for the smallest bounded approval, otherwise say why it remains parked."),
			IntentClass:      "resume_mission",
			TemporalFit:      reentryMissionTemporalFit(mission),
			WhyNow:           "mission ledger has a durable remembered objective",
			AuthorityClass:   "mission_review",
			RequiresApproval: true,
			BasisRefs:        []string{"mission"},
			SourceKind:       "mission",
			SourceRef:        strings.TrimSpace(mission.ID),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     reentryMissionRelevanceScore(mission),
				"user_intent_fit":   reentryMissionIntentFitScore(mission, source),
				"evidence_strength": 2.5,
				"resurfacing_value": reentryMissionResurfacingScore(mission, state.Now),
				"authority_cost":    1.0,
				"staleness_risk":    reentryMissionStalenessRisk(mission, state.Now),
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Mission ledger has a durable remembered objective; review it before treating it as live work.",
		})
		if len(candidates) >= 8 {
			break
		}
	}
	for _, thread := range state.Threads {
		if thread.ThreadID <= 0 || telegramThreadIDFromScope(state.Run.ChatID, state.Run.Scope) == thread.ThreadID {
			continue
		}
		displaySlot := reentryThreadDisplaySlot(thread)
		subject := reentryThreadSubject(thread)
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReflectWithOperator,
			Label:            reentryThreadCandidateLabel(displaySlot, subject),
			BodyText:         reentryThreadCandidateBodyText(displaySlot, reentryThreadBodySubject(thread)),
			Summary:          reentryConcreteSummary("Review an open same-chat side thread as a possible resurfacing path.", subject),
			PromptText:       reentryPromptForCandidate(fmt.Sprintf("Review Thread %d from saved state: %s. Explain whether it deserves attention now or should remain parked; do not absorb, close, or act without approval.", displaySlot, subject)),
			IntentClass:      "revisit_thread",
			TemporalFit:      reentryThreadTemporalFit(thread, state.Now),
			WhyNow:           "open same-chat side thread may be worth revisiting",
			AuthorityClass:   "conversation",
			RequiresApproval: false,
			BasisRefs:        []string{fmt.Sprintf("telegram_thread:%d:%d", thread.ChatID, thread.ThreadID)},
			SourceKind:       "telegram_thread",
			SourceRef:        fmt.Sprintf("%d:%d", thread.ChatID, thread.ThreadID),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     reentryThreadRelevanceScore(thread, source),
				"user_intent_fit":   reentryThreadIntentFitScore(thread, source),
				"evidence_strength": 2.0,
				"resurfacing_value": reentryThreadResurfacingScore(thread, state.Now),
				"authority_cost":    0.5,
				"staleness_risk":    reentryThreadStalenessRisk(thread, state.Now),
				"cross_thread_risk": 1.0,
			}),
			JudgmentReason: "Open same-chat thread may be worth revisiting, but remains a resurfacing candidate rather than active context.",
		})
		if len(candidates) >= 8 {
			break
		}
	}
	for _, signal := range state.Signals {
		subject := reentrySignalSubject(signal)
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReviewMemoryHealth,
			Label:            reentryConcreteLabel("Inspect pressure", subject),
			BodyText:         reentryConcreteBodyText("Inspect pressure", reentrySignalBodySubject(signal)),
			Summary:          reentryConcreteSummary("Inspect accumulated interior pressure as a low-authority attention signal.", subject),
			PromptText:       reentryPromptForCandidate("Inspect the selected recurring pressure signal: " + subject + ". Explain whether it should influence the next step now; ask approval before making changes."),
			IntentClass:      "inspect_pressure",
			TemporalFit:      "soon",
			WhyNow:           "interior pressure has accumulated enough to become an attention candidate",
			AuthorityClass:   "read_only",
			RequiresApproval: true,
			BasisRefs:        []string{fmt.Sprintf("interior_signal:%s:%s", signal.Category, signal.SubjectKey)},
			SourceKind:       "interior_signal",
			SourceRef:        fmt.Sprintf("%s:%s", signal.Category, signal.SubjectKey),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     reentrySignalRelevanceScore(signal),
				"user_intent_fit":   2.0,
				"evidence_strength": 1.5,
				"resurfacing_value": reentrySignalResurfacingScore(signal),
				"authority_cost":    1.0,
				"staleness_risk":    1.0,
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Interior pressure is useful for attention, but it remains low-authority and cannot become permission.",
		})
		if len(candidates) >= 8 {
			break
		}
	}
	if len(state.MemoryNotes) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReviewMemoryHealth,
			Label:            "Inspect memory: recent continuity notes",
			BodyText:         "Inspect memory: recent continuity notes",
			Summary:          "Check memory and continuity notes for stale, noisy, or unresolved state before starting more work.",
			PromptText:       reentryPromptForCandidate("Inspect memory and continuity health from saved state. Summarize whether anything looks stale, noisy, or unresolved; ask approval before making changes."),
			IntentClass:      "inspect_memory_health",
			TemporalFit:      "later",
			WhyNow:           "recent memory notes may affect future work",
			AuthorityClass:   "read_only",
			RequiresApproval: true,
			BasisRefs:        []string{"memory_state"},
			SourceKind:       "memory_state",
			SourceRef:        "recent_daily_notes",
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     2.0,
				"user_intent_fit":   2.0,
				"evidence_strength": 1.5,
				"resurfacing_value": 2.0,
				"authority_cost":    1.0,
				"staleness_risk":    2.0,
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Recent memory notes exist; inspect health before letting stale memory steer work.",
		})
	}
	if state.Evidence.FallbackUsed || len(state.Evidence.MissingEvidenceIDs) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateClarifyGoal,
			Label:            "Repair evidence context before choosing a path",
			BodyText:         "Repair evidence context before choosing a path",
			Summary:          "Hydration found gaps or fallback state; ask a concise clarification instead of pretending context is complete.",
			PromptText:       reentryPromptForCandidate("Evidence context is incomplete or fallback-only. Ask one concise clarification question before choosing a work path."),
			IntentClass:      "repair_context",
			TemporalFit:      "now",
			WhyNow:           "evidence hydration reported gaps",
			AuthorityClass:   "clarification",
			RequiresApproval: false,
			BasisRefs:        []string{"evidence_hydration"},
			SourceKind:       "evidence_hydration",
			SourceRef:        strings.TrimSpace(state.Evidence.RunID),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     3.5,
				"user_intent_fit":   3.0,
				"evidence_strength": 1.0,
				"resurfacing_value": 1.0,
				"authority_cost":    0.0,
				"staleness_risk":    0.5,
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Evidence hydration reported gaps; repair context before recommending old work.",
		})
	}
	if strings.TrimSpace(state.Run.RequestText) != "" || op.Active() || len(state.Missions) > 0 || len(state.MemoryNotes) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReflectWithOperator,
			Label:            "Ask: choose useful path",
			BodyText:         "Ask: choose useful path",
			Summary:          "Offer a reflective reorientation option so productivity and system wellbeing stay connected.",
			PromptText:       reentryPromptForCandidate("Ask the operator whether the next move should be work, repair, conversation, or rest; do not start external actions without approval."),
			IntentClass:      "clarify_goal",
			TemporalFit:      "later",
			WhyNow:           "multiple possible paths compete without one concrete winner",
			AuthorityClass:   "conversation",
			RequiresApproval: false,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID)},
			SourceKind:       "turn_run",
			SourceRef:        fmt.Sprintf("%d", state.Run.ID),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     3.0,
				"user_intent_fit":   3.0,
				"evidence_strength": reentryEvidenceStrengthScore(state.Evidence),
				"resurfacing_value": 1.0,
				"authority_cost":    0.0,
				"staleness_risk":    0.5,
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Reflection is the safe default when multiple possible paths compete.",
		})
	}
	if len(candidates) == 0 && (op.Active() || len(state.MemoryNotes) > 0 || strings.TrimSpace(state.Run.RequestText) != "") {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               "c1",
			Kind:             session.ReentryCandidateClarifyGoal,
			Label:            "Ask what would be genuinely useful next",
			BodyText:         "Ask what would be genuinely useful next",
			Summary:          "Ask the operator for the missing next objective instead of inventing authority.",
			PromptText:       reentryPromptForCandidate("Saved state does not show a concrete next step. Ask one concise clarification question about the next objective."),
			IntentClass:      "clarify_goal",
			TemporalFit:      "later",
			WhyNow:           "saved state lacks a concrete next path",
			AuthorityClass:   "clarification",
			RequiresApproval: false,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID)},
			SourceKind:       "turn_run",
			SourceRef:        fmt.Sprintf("%d", state.Run.ID),
			EvidenceRefs:     reentryEvidenceRefs(state.Evidence),
			Scores: reentryCandidateScores(map[string]float64{
				"relevance_now":     2.0,
				"user_intent_fit":   2.5,
				"evidence_strength": 1.0,
				"resurfacing_value": 0.0,
				"authority_cost":    0.0,
				"staleness_risk":    0.0,
				"cross_thread_risk": 0.0,
			}),
			JudgmentReason: "Saved state lacks a concrete next path; ask instead of inventing one.",
		})
	}
	candidates = normalizeReentryCandidates(candidates)
	candidates = removeReentryFallbackPadding(candidates)
	return rankReentryCandidatesDeterministically(r.applyReentryRecommendationDampening(state, candidates))
}

func nextReentryCandidateID(existing []session.ReentryRecommendationCandidate) string {
	return fmt.Sprintf("c%d", len(existing)+1)
}

func rankReentryCandidatesDeterministically(candidates []session.ReentryRecommendationCandidate) []session.ReentryRecommendationCandidate {
	candidates = normalizeReentryCandidates(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := reentryCandidateWeightedScore(candidates[i])
		right := reentryCandidateWeightedScore(candidates[j])
		if left == right {
			return reentryCandidateTieBreaker(candidates[i]) < reentryCandidateTieBreaker(candidates[j])
		}
		return left > right
	})
	return candidates
}

func reentryCandidateWeightedScore(candidate session.ReentryRecommendationCandidate) float64 {
	scores := candidate.Scores
	return 3*scores["relevance_now"] +
		3*scores["user_intent_fit"] +
		2*scores["evidence_strength"] +
		scores["resurfacing_value"] -
		scores["authority_cost"] -
		2*scores["staleness_risk"] -
		3*scores["cross_thread_risk"]
}

func reentryCandidateTieBreaker(candidate session.ReentryRecommendationCandidate) string {
	return strings.Join([]string{
		string(candidate.Kind),
		strings.TrimSpace(candidate.SourceKind),
		strings.TrimSpace(candidate.SourceRef),
		strings.TrimSpace(candidate.ID),
	}, "\x00")
}

func (r *Runtime) applyReentryRecommendationDampening(state reentryRecommendationState, candidates []session.ReentryRecommendationCandidate) []session.ReentryRecommendationCandidate {
	candidates = normalizeReentryCandidates(candidates)
	if r == nil || r.store == nil || strings.TrimSpace(state.Run.SessionID) == "" || len(candidates) == 0 {
		return candidates
	}
	ignored, stale := r.reentryRecommendationDampeningKeys(state)
	if len(ignored) == 0 && len(stale) == 0 {
		return candidates
	}
	out := make([]session.ReentryRecommendationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := reentryCandidateDampeningKey(candidate)
		candidate.DampeningKey = key
		if _, ok := ignored[key]; ok {
			continue
		}
		if _, ok := stale[key]; ok {
			candidate.Scores = cloneReentryCandidateScores(candidate.Scores)
			candidate.Scores["staleness_risk"] = 5
			candidate.JudgmentReason = strings.TrimSpace(candidate.JudgmentReason + " Similar recommendation went stale recently.")
		}
		out = append(out, candidate)
	}
	return out
}

func (r *Runtime) reentryRecommendationDampeningKeys(state reentryRecommendationState) (map[string]time.Time, map[string]time.Time) {
	ignored := map[string]time.Time{}
	stale := map[string]time.Time{}
	records, err := r.store.ReentryRecommendations(session.ReentryRecommendationFilter{SessionID: state.Run.SessionID, Limit: 100})
	if err != nil {
		return ignored, stale
	}
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, record := range records {
		record = session.NormalizeReentryRecommendation(record)
		updatedAt := record.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = record.CreatedAt
		}
		if updatedAt.IsZero() {
			continue
		}
		age := now.Sub(updatedAt)
		var target map[string]time.Time
		switch record.Status {
		case session.ReentryRecommendationStatusIgnored:
			if age < 0 || age > reentryIgnoredDampeningTTL {
				continue
			}
			target = ignored
		case session.ReentryRecommendationStatusStale:
			if age < 0 || age > reentryStaleDampeningTTL {
				continue
			}
			target = stale
		default:
			continue
		}
		for _, candidate := range record.Candidates {
			if key := reentryCandidateDampeningKey(candidate); key != "" {
				target[key] = updatedAt
			}
		}
	}
	return ignored, stale
}

func cloneReentryCandidateScores(scores map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(scores)+1)
	for key, value := range scores {
		out[key] = value
	}
	return out
}

func reentryCandidateDampeningKey(candidate session.ReentryRecommendationCandidate) string {
	candidate = session.NormalizeReentryRecommendationCandidate(candidate)
	key := strings.TrimSpace(candidate.DampeningKey)
	if key != "" {
		return key
	}
	parts := []string{
		strings.TrimSpace(candidate.IntentClass),
		string(candidate.Kind),
		strings.TrimSpace(candidate.SourceKind),
		strings.TrimSpace(candidate.SourceRef),
	}
	for i, part := range parts {
		parts[i] = strings.ToLower(strings.Join(strings.Fields(part), "_"))
	}
	return strings.Trim(strings.Join(parts, ":"), ":")
}

func reentryRecommendationLowValueOnly(candidates []session.ReentryRecommendationCandidate) bool {
	candidates = normalizeReentryCandidates(candidates)
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if reentryCandidateSpecificEnough(candidate) {
			return false
		}
	}
	return true
}

func removeReentryFallbackPadding(candidates []session.ReentryRecommendationCandidate) []session.ReentryRecommendationCandidate {
	candidates = normalizeReentryCandidates(candidates)
	if reentryRecommendationLowValueOnly(candidates) {
		return candidates
	}
	out := make([]session.ReentryRecommendationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if reentryCandidateSpecificEnough(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func reentryCandidateSpecificEnough(candidate session.ReentryRecommendationCandidate) bool {
	candidate = session.NormalizeReentryRecommendationCandidate(candidate)
	switch candidate.SourceKind {
	case "operation_state", "mission", "telegram_thread", "interior_signal", "memory_state", "evidence_hydration":
		return true
	default:
		return false
	}
}

func reentryCandidateScores(scores map[string]float64) map[string]float64 {
	return session.NormalizeReentryRecommendationCandidate(session.ReentryRecommendationCandidate{Scores: scores}).Scores
}

func reentryEvidenceRefs(result session.EvidenceHydrationResult) []string {
	refs := make([]string, 0, len(result.Required)+len(result.Selected))
	seen := map[string]struct{}{}
	for _, obj := range append(append([]session.EvidenceObject(nil), result.Required...), result.Selected...) {
		id := strings.TrimSpace(obj.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, id)
		if len(refs) >= 6 {
			break
		}
	}
	return refs
}

func reentryEvidenceStrengthScore(result session.EvidenceHydrationResult) float64 {
	if len(result.Selected) == 0 {
		return 0.5
	}
	score := 1.0
	for _, obj := range result.Selected {
		switch obj.EpistemicStatus {
		case session.EvidenceStatusAttested:
			score += 0.8
		case session.EvidenceStatusObserved:
			score += 0.6
		case session.EvidenceStatusProjection:
			score += 0.35
		case session.EvidenceStatusClaimed:
			score += 0.1
		}
		if obj.SourceKind == session.EvidenceSourceOperationState || obj.SourceKind == session.EvidenceSourceExecutionEvent || obj.SourceKind == session.EvidenceSourceTurnRun {
			score += 0.4
		}
		if score >= 5 {
			return 5
		}
	}
	if result.FallbackUsed {
		score -= 0.75
	}
	if len(result.MissingEvidenceIDs) > 0 {
		score -= 0.75
	}
	if score < 0 {
		return 0
	}
	return score
}

func reentryOperationSourceRef(op session.OperationState) string {
	op = session.NormalizeOperationState(op)
	if strings.TrimSpace(op.ID) != "" {
		return strings.TrimSpace(op.ID)
	}
	return "current"
}

func reentryOperationSubject(op session.OperationState) string {
	return reentryVisibleSubject(reentryOperationSubjectRaw(op), "current operation")
}

func reentryOperationBodySubject(op session.OperationState) string {
	return reentryBodySubject(reentryOperationSubjectRaw(op), "current operation")
}

func reentryOperationSubjectRaw(op session.OperationState) string {
	op = session.NormalizeOperationState(op)
	phase, ok := reentryCurrentOperationPhase(op.PhasePlan)
	values := []string{
		op.PlanLease.EvidenceDigest.SuggestedNextLease,
		op.Proposal.Summary,
		op.Proposal.BoundedEffect,
		op.PhasePlan.Goal,
		op.Objective,
		op.PlanLease.Summary,
		op.Summary,
		op.ID,
	}
	if ok {
		values = append([]string{
			phase.OperatorTitle,
			phase.PlanTitle,
			phase.Summary,
			phase.BoundedEffect,
			phase.ApprovalSubject,
			phase.ID,
		}, values...)
	}
	return firstNonEmpty(values...)
}

func reentryCurrentOperationPhase(plan session.OperationPhasePlan) (session.OperationPhase, bool) {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	currentPhaseID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentPhaseID != "" {
		for _, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentPhaseID {
				return phase, true
			}
		}
	}
	for _, phase := range plan.Phases {
		status := session.NormalizePlanStatus(phase.Status)
		if status == session.PlanStatusInProgress || status == session.PlanStatusPending || strings.TrimSpace(phase.BlockedReasonCode) != "" {
			return phase, true
		}
	}
	return session.OperationPhase{}, false
}

func reentryOperationTemporalFit(op session.OperationState) string {
	op = session.NormalizeOperationState(op)
	switch op.Status {
	case session.OperationStatusActive, session.OperationStatusBlocked:
		return "now"
	case session.OperationStatusCompleted:
		return "soon"
	default:
		return "later"
	}
}

func reentryOperationRelevanceScore(op session.OperationState) float64 {
	op = session.NormalizeOperationState(op)
	switch op.Status {
	case session.OperationStatusActive:
		return 5
	case session.OperationStatusBlocked:
		return 4.5
	case session.OperationStatusCompleted:
		if op.PlanLease.EvidenceDigest.Active() {
			return 3
		}
		return 2.5
	default:
		if op.Active() {
			return 3
		}
		return 0
	}
}

func reentryOperationAuthorityCost(op session.OperationState) float64 {
	text := strings.ToLower(strings.Join([]string{op.Proposal.Kind, op.Proposal.Summary, op.Proposal.BoundedEffect, op.PhasePlan.CurrentPhaseID, reentryCurrentPhaseAuthorityClass(op.PhasePlan)}, " "))
	switch {
	case strings.Contains(text, "external") || strings.Contains(text, "deploy") || strings.Contains(text, "restart") || strings.Contains(text, "commit") || strings.Contains(text, "write"):
		return 3
	case strings.Contains(text, "read") || strings.Contains(text, "review"):
		return 1
	default:
		if op.Proposal.Active() || op.PhasePlan.Active() {
			return 1.5
		}
		return 0.5
	}
}

func reentryOperationStalenessRisk(op session.OperationState) float64 {
	op = session.NormalizeOperationState(op)
	switch op.Status {
	case session.OperationStatusActive:
		return 0.5
	case session.OperationStatusBlocked:
		return 1
	case session.OperationStatusCompleted:
		return 2.5
	case session.OperationStatusFailed:
		return 3
	default:
		return 1.5
	}
}

func reentryMissionCandidateLabel(mission session.MissionState) string {
	subject := reentryMissionSubject(mission)
	switch session.NormalizeMissionStatus(mission.Status) {
	case session.MissionStatusBlocked:
		return reentryConcreteLabel("Unblock mission", subject)
	case session.MissionStatusActive:
		return reentryConcreteLabel("Resume mission", subject)
	default:
		return reentryConcreteLabel("Review mission", subject)
	}
}

func reentryMissionCandidateBodyText(mission session.MissionState) string {
	subject := reentryMissionBodySubject(mission)
	switch session.NormalizeMissionStatus(mission.Status) {
	case session.MissionStatusBlocked:
		return reentryConcreteBodyText("Unblock mission", subject)
	case session.MissionStatusActive:
		return reentryConcreteBodyText("Resume mission", subject)
	default:
		return reentryConcreteBodyText("Review mission", subject)
	}
}

func reentryMissionSubject(mission session.MissionState) string {
	return reentryVisibleSubject(reentryMissionSubjectRaw(mission), "remembered mission")
}

func reentryMissionBodySubject(mission session.MissionState) string {
	return reentryBodySubject(reentryMissionSubjectRaw(mission), "remembered mission")
}

func reentryMissionSubjectRaw(mission session.MissionState) string {
	return firstNonEmpty(mission.Title, mission.NextAllowedAction, mission.Objective, mission.WaitingFor, mission.BlockedReason, mission.ID)
}

func reentryMissionTemporalFit(mission session.MissionState) string {
	switch session.NormalizeMissionStatus(mission.Status) {
	case session.MissionStatusBlocked:
		return "now"
	case session.MissionStatusActive:
		return "soon"
	default:
		return "later"
	}
}

func reentryMissionRelevanceScore(mission session.MissionState) float64 {
	switch session.NormalizeMissionStatus(mission.Status) {
	case session.MissionStatusBlocked:
		return 3.5
	case session.MissionStatusActive:
		return 3
	case session.MissionStatusCandidate:
		return 2
	default:
		return 1
	}
}

func reentryMissionIntentFitScore(mission session.MissionState, source string) float64 {
	text := strings.ToLower(strings.Join([]string{mission.Title, mission.Objective, mission.NextAllowedAction, mission.BlockedReason, mission.WaitingFor}, " "))
	if text == "" || source == "" {
		return 2
	}
	overlap := reentryTermOverlapScore(source, text, 1.5, 4.5)
	if overlap > 0 {
		return overlap
	}
	return 2
}

func reentryMissionResurfacingScore(mission session.MissionState, now time.Time) float64 {
	if mission.Pinned {
		return 3
	}
	if mission.LastSummonedAt.IsZero() && mission.LastTouchedAt.IsZero() {
		return 1.5
	}
	last := mission.LastTouchedAt
	if mission.LastSummonedAt.After(last) {
		last = mission.LastSummonedAt
	}
	return reentryAgeResurfacingScore(now.Sub(last))
}

func reentryMissionStalenessRisk(mission session.MissionState, now time.Time) float64 {
	last := mission.UpdatedAt
	if mission.LastTouchedAt.After(last) {
		last = mission.LastTouchedAt
	}
	return reentryAgeStalenessRisk(now.Sub(last))
}

func reentryThreadRelevanceScore(thread session.TelegramThread, source string) float64 {
	overlap := reentryTermOverlapScore(source, strings.ToLower(thread.CreatedText+" "+thread.AbsorbSummary), 1, 4)
	if overlap > 0 {
		return overlap
	}
	return 1.5
}

func reentryThreadIntentFitScore(thread session.TelegramThread, source string) float64 {
	overlap := reentryTermOverlapScore(source, strings.ToLower(thread.CreatedText), 1, 4)
	if overlap > 0 {
		return overlap
	}
	return 1.5
}

func reentryThreadResurfacingScore(thread session.TelegramThread, now time.Time) float64 {
	return reentryAgeResurfacingScore(now.Sub(thread.LastActivityAt))
}

func reentryThreadStalenessRisk(thread session.TelegramThread, now time.Time) float64 {
	return reentryAgeStalenessRisk(now.Sub(thread.LastActivityAt))
}

func reentryThreadDisplaySlot(thread session.TelegramThread) int64 {
	if thread.DisplaySlot > 0 {
		return thread.DisplaySlot
	}
	return thread.ThreadID
}

func reentryThreadSubject(thread session.TelegramThread) string {
	return reentryVisibleSubject(reentryThreadSubjectRaw(thread), "open side thread")
}

func reentryThreadBodySubject(thread session.TelegramThread) string {
	return reentryBodySubject(reentryThreadSubjectRaw(thread), "open side thread")
}

func reentryThreadSubjectRaw(thread session.TelegramThread) string {
	return firstNonEmpty(thread.AbsorbSummary, thread.CreatedText, thread.ArchivedDisplayName)
}

func reentryThreadCandidateLabel(displaySlot int64, subject string) string {
	subject = reentryVisibleSubject(subject, "open side thread")
	return truncatePreview(fmt.Sprintf("Thread %d: %s", displaySlot, subject), 80)
}

func reentryThreadCandidateBodyText(displaySlot int64, subject string) string {
	subject = reentryBodySubject(subject, "open side thread")
	return reentryBoundRecommendationBodyText(fmt.Sprintf("Thread %d: %s", displaySlot, subject))
}

func reentryThreadTemporalFit(thread session.TelegramThread, now time.Time) string {
	age := now.Sub(thread.LastActivityAt)
	if age <= 24*time.Hour {
		return "soon"
	}
	return "later"
}

func reentrySignalRelevanceScore(signal session.InteriorSignalState) float64 {
	return 1 + 4*clampReentryScore(signal.Intensity)
}

func reentrySignalSubject(signal session.InteriorSignalState) string {
	return reentryVisibleSubject(reentrySignalSubjectRaw(signal), "recurring pressure")
}

func reentrySignalBodySubject(signal session.InteriorSignalState) string {
	return reentryBodySubject(reentrySignalSubjectRaw(signal), "recurring pressure")
}

func reentrySignalSubjectRaw(signal session.InteriorSignalState) string {
	return firstNonEmpty(signal.Summary, signal.SubjectKey, signal.Category)
}

func reentrySignalResurfacingScore(signal session.InteriorSignalState) float64 {
	return 1 + 3*clampReentryScore(signal.Intensity)
}

func reentryAgeResurfacingScore(age time.Duration) float64 {
	switch {
	case age < 0:
		return 1
	case age <= time.Hour:
		return 0.5
	case age <= 24*time.Hour:
		return 1.5
	case age <= 7*24*time.Hour:
		return 2.5
	default:
		return 1.5
	}
}

func reentryAgeStalenessRisk(age time.Duration) float64 {
	switch {
	case age < 0:
		return 0.5
	case age <= 24*time.Hour:
		return 0.5
	case age <= 7*24*time.Hour:
		return 1.5
	case age <= 30*24*time.Hour:
		return 3
	default:
		return 4
	}
}

func reentryTermOverlapScore(source string, target string, base float64, maxScore float64) float64 {
	sourceTerms := reentrySignificantTerms(source)
	if len(sourceTerms) == 0 || strings.TrimSpace(target) == "" {
		return 0
	}
	score := base
	for term := range sourceTerms {
		if strings.Contains(target, term) {
			score += 0.75
		}
		if score >= maxScore {
			return maxScore
		}
	}
	if score == base {
		return 0
	}
	return score
}

func reentrySignificantTerms(text string) map[string]struct{} {
	terms := map[string]struct{}{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(word) < 4 || reentryStopTerm(word) {
			continue
		}
		terms[word] = struct{}{}
	}
	return terms
}

func reentryStopTerm(term string) bool {
	switch term {
	case "this", "that", "with", "from", "have", "what", "when", "where", "work", "next", "step", "state", "review", "would", "should", "could", "after", "before", "current":
		return true
	default:
		return false
	}
}

func clampReentryScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func normalizeReentryCandidates(candidates []session.ReentryRecommendationCandidate) []session.ReentryRecommendationCandidate {
	out := make([]session.ReentryRecommendationCandidate, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = session.NormalizeReentryRecommendationCandidate(candidate)
		if candidate.ID == "" || candidate.Kind == "" || candidate.PromptText == "" {
			continue
		}
		candidate.Label = normalizeReentryCandidateLabel(candidate.Label)
		if candidate.Label == "" {
			continue
		}
		if candidate.BodyText != "" {
			candidate.BodyText = reentryBoundRecommendationBodyText(candidate.BodyText)
		}
		if candidate.DampeningKey == "" {
			candidate.DampeningKey = reentryCandidateDampeningKey(candidate)
		}
		if _, ok := seen[candidate.ID]; ok {
			continue
		}
		seen[candidate.ID] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func reentryVisibleSubject(subject string, fallback string) string {
	subject = strings.Join(strings.Fields(strings.TrimSpace(subject)), " ")
	subject = strings.Trim(subject, " .\t\n\r")
	if subject == "" {
		subject = fallback
	}
	return truncatePreview(subject, 72)
}

func reentryBodySubject(subject string, fallback string) string {
	subject = strings.Join(strings.Fields(strings.TrimSpace(subject)), " ")
	subject = strings.Trim(subject, " .\t\n\r")
	if subject == "" {
		subject = fallback
	}
	return reentryBoundRecommendationBodyText(subject)
}

func reentryConcreteLabel(prefix string, subject string) string {
	prefix = strings.TrimSpace(prefix)
	subject = reentryVisibleSubject(subject, "saved state")
	if prefix == "" {
		return subject
	}
	return truncatePreview(prefix+": "+subject, 80)
}

func reentryConcreteBodyText(prefix string, subject string) string {
	prefix = strings.TrimSpace(prefix)
	subject = reentryBodySubject(subject, "saved state")
	if prefix == "" {
		return subject
	}
	return reentryBoundRecommendationBodyText(prefix + ": " + subject)
}

func reentryBoundRecommendationBodyText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	text = strings.Trim(text, " .\t\n\r")
	text = redactRuntimeText(text, 0)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= reentryBodyTextRuneLimit {
		return text
	}
	cut := reentryBodyTextRuneLimit - len(" [truncated]")
	if cut < 1 {
		cut = reentryBodyTextRuneLimit
	}
	for cut > 1 && runes[cut-1] != ' ' {
		cut--
	}
	if cut <= 1 {
		cut = reentryBodyTextRuneLimit - len(" [truncated]")
	}
	return strings.TrimSpace(string(runes[:cut])) + " [truncated]"
}

func reentryConcreteSummary(base string, subject string) string {
	base = strings.TrimSpace(base)
	subject = reentryVisibleSubject(subject, "saved state")
	if base == "" {
		return subject
	}
	return truncatePreview(base+" Subject: "+subject, 220)
}

func normalizeReentryCandidateLabel(label string) string {
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	label = strings.Trim(label, " .\t\n\r")
	return label
}

func normalizeReentryButtonLabel(label string) string {
	label = redactRuntimeText(label, 64)
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	if label == "" {
		return ""
	}
	words := strings.Fields(label)
	if len(words) > 3 {
		words = words[:3]
		label = strings.Join(words, " ")
	}
	const maxRunes = 28
	if len([]rune(label)) > maxRunes {
		runes := []rune(label)
		label = strings.TrimSpace(string(runes[:maxRunes-3])) + "..."
	}
	return label
}

func reentryPromptForCandidate(instruction string) string {
	return strings.TrimSpace(instruction)
}

func reentryLooksReleaseRelated(source string) bool {
	return len(reentryRecommendationSignalFlags(source)) > 0
}

func reentryRecommendationSourceText(state reentryRecommendationState) string {
	parts := []string{
		state.Run.RequestText,
		state.Run.ErrorText,
		state.Operation.Objective,
		string(state.Operation.Status),
		state.Operation.Stage,
		state.Operation.Summary,
		state.Operation.Proposal.Summary,
		state.Operation.Proposal.WhyNow,
		state.Operation.Proposal.BoundedEffect,
		state.Plan.Explanation,
		state.Continuation.Objective,
		state.Continuation.StageSummary,
		state.Continuation.ActionProposal.Summary,
		state.Continuation.ActionProposal.WhyNow,
		state.Continuation.ActionProposal.BoundedEffect,
	}
	for _, phase := range state.Operation.PhasePlan.Phases {
		parts = append(parts, phase.Summary, phase.WhyNow, phase.BoundedEffect, string(phase.Status), phase.AuthorityClass)
	}
	for _, mission := range state.Missions {
		parts = append(parts, mission.Title, mission.Objective, mission.BlockedReason, mission.WaitingFor, mission.NextAllowedAction)
	}
	for _, thread := range state.Threads {
		parts = append(parts, thread.CreatedText, thread.AbsorbSummary, string(thread.Status))
	}
	for _, signal := range state.Signals {
		parts = append(parts, signal.Category, signal.SubjectKey, signal.Summary)
	}
	parts = append(parts, state.MemoryNotes...)
	return strings.Join(parts, "\n")
}

type reentryJudgeResponse struct {
	Candidates []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Rank  int    `json:"rank"`
	} `json:"candidates"`
}

type reentryRecommendationRankContext struct {
	Turn             reentryRecommendationRankTurn         `json:"turn"`
	Operation        reentryRecommendationRankOperation    `json:"operation,omitempty"`
	Plan             reentryRecommendationRankPlan         `json:"plan,omitempty"`
	Continuation     reentryRecommendationRankContinuation `json:"continuation,omitempty"`
	MissionCounts    map[string]int                        `json:"mission_counts,omitempty"`
	MemoryNoteCount  int                                   `json:"memory_note_count,omitempty"`
	ThreadCount      int                                   `json:"thread_count,omitempty"`
	SignalCount      int                                   `json:"signal_count,omitempty"`
	EvidenceCount    int                                   `json:"evidence_count,omitempty"`
	EvidenceFallback bool                                  `json:"evidence_fallback,omitempty"`
	EvidenceGaps     int                                   `json:"evidence_gaps,omitempty"`
	Signals          []string                              `json:"signals,omitempty"`
}

type reentryRecommendationRankTurn struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
}

type reentryRecommendationRankOperation struct {
	Present                bool           `json:"present,omitempty"`
	Status                 string         `json:"status,omitempty"`
	HasObjective           bool           `json:"has_objective,omitempty"`
	ProposalStatus         string         `json:"proposal_status,omitempty"`
	PhaseCount             int            `json:"phase_count,omitempty"`
	PhaseStatusCounts      map[string]int `json:"phase_status_counts,omitempty"`
	CurrentAuthorityClass  string         `json:"current_authority_class,omitempty"`
	PlanLeaseStatus        string         `json:"plan_lease_status,omitempty"`
	PlanLeaseLaneCount     int            `json:"plan_lease_lane_count,omitempty"`
	PlanLeaseTurnsSpent    int            `json:"plan_lease_turns_spent,omitempty"`
	PlanLeaseCompleted     int            `json:"plan_lease_completed,omitempty"`
	PlanLeaseBlocked       int            `json:"plan_lease_blocked,omitempty"`
	PlanLeaseSuggestedNext bool           `json:"plan_lease_suggested_next,omitempty"`
}

type reentryRecommendationRankPlan struct {
	StepCount    int            `json:"step_count,omitempty"`
	StatusCounts map[string]int `json:"status_counts,omitempty"`
}

type reentryRecommendationRankContinuation struct {
	Status         string `json:"status,omitempty"`
	LeaseStatus    string `json:"lease_status,omitempty"`
	RemainingTurns int    `json:"remaining_turns,omitempty"`
	HasProposal    bool   `json:"has_proposal,omitempty"`
}

type reentryRecommendationRankOption struct {
	ID               string                       `json:"id"`
	Kind             session.ReentryCandidateKind `json:"kind"`
	Label            string                       `json:"label"`
	Summary          string                       `json:"summary,omitempty"`
	IntentClass      string                       `json:"intent_class,omitempty"`
	TemporalFit      string                       `json:"temporal_fit,omitempty"`
	WhyNowCategory   string                       `json:"why_now_category,omitempty"`
	AuthorityClass   string                       `json:"authority_class,omitempty"`
	RequiresApproval bool                         `json:"requires_approval,omitempty"`
	SourceKind       string                       `json:"source_kind,omitempty"`
	Scores           map[string]float64           `json:"scores,omitempty"`
}

func (r *Runtime) rankReentryRecommendationCandidates(ctx context.Context, state reentryRecommendationState, candidates []session.ReentryRecommendationCandidate) []session.ReentryRecommendationCandidate {
	candidates = normalizeReentryCandidates(candidates)
	if len(candidates) <= 1 || r == nil || r.provider == nil {
		return candidates
	}
	payload, _ := json.Marshal(struct {
		Terminal string                            `json:"terminal"`
		Context  reentryRecommendationRankContext  `json:"context"`
		Options  []reentryRecommendationRankOption `json:"options"`
	}{
		Terminal: fmt.Sprintf("turn_run=%d status=%s", state.Run.ID, state.Run.Status),
		Context:  reentryRecommendationRankPayloadContext(state),
		Options:  reentryRecommendationRankOptions(candidates),
	})
	system := "Rank existing Aphelion idle re-entry candidates by ID only. Do not invent candidates, labels, or authority. Return compact JSON only: {\"candidates\":[{\"id\":\"c1\",\"rank\":1}]}."
	resp, err := completeProvider(ctx, r.provider, []agent.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: string(payload)},
	}, nil, nil)
	if err != nil {
		r.recordReentryRecommendationJudgment(state.Key, "provider_failed", map[string]any{
			"turn_run_id":     state.Run.ID,
			"candidate_order": reentryRecommendationCandidateIDs(candidates),
			"error":           trimError(err.Error()),
		}, time.Now().UTC())
		return candidates
	}
	var parsed reentryJudgeResponse
	if err := json.Unmarshal([]byte(extractReentryJSON(resp.Content)), &parsed); err != nil {
		r.recordReentryRecommendationJudgment(state.Key, "provider_malformed", map[string]any{
			"turn_run_id":     state.Run.ID,
			"candidate_order": reentryRecommendationCandidateIDs(candidates),
			"response":        truncatePreview(resp.Content, 220),
		}, time.Now().UTC())
		return candidates
	}
	byID := make(map[string]session.ReentryRecommendationCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.ID] = candidate
	}
	type rankedCandidate struct {
		candidate session.ReentryRecommendationCandidate
		rank      int
		index     int
	}
	ranked := make([]rankedCandidate, 0, len(candidates))
	used := map[string]struct{}{}
	ignoredIDs := make([]string, 0)
	for i, item := range parsed.Candidates {
		id := strings.TrimSpace(item.ID)
		candidate, ok := byID[id]
		if !ok {
			if id != "" {
				ignoredIDs = append(ignoredIDs, id)
			}
			continue
		}
		// The judge ranks candidates, but labels come from the local content contract.
		// This keeps the user-visible suggestions value-articulated instead of
		// allowing the ranking response to compress them back into button-sized text.
		rank := item.Rank
		if rank <= 0 {
			rank = i + 1
		}
		ranked = append(ranked, rankedCandidate{candidate: candidate, rank: rank, index: i})
		used[candidate.ID] = struct{}{}
	}
	for i, candidate := range candidates {
		if _, ok := used[candidate.ID]; ok {
			continue
		}
		ranked = append(ranked, rankedCandidate{candidate: candidate, rank: 100 + i, index: i})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].rank == ranked[j].rank {
			return ranked[i].index < ranked[j].index
		}
		return ranked[i].rank < ranked[j].rank
	})
	out := make([]session.ReentryRecommendationCandidate, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.candidate)
	}
	out = normalizeReentryCandidates(out)
	r.recordReentryRecommendationJudgment(state.Key, "provider_ranked", map[string]any{
		"turn_run_id":            state.Run.ID,
		"provider_candidate_ids": reentryRecommendationJudgeCandidateIDs(parsed),
		"ignored_candidate_ids":  ignoredIDs,
		"ranked_order":           reentryRecommendationCandidateIDs(out),
	}, time.Now().UTC())
	return out
}

func reentryRecommendationRankPayloadContext(state reentryRecommendationState) reentryRecommendationRankContext {
	op := session.NormalizeOperationState(state.Operation)
	plan := session.NormalizePlanState(state.Plan)
	cont := session.NormalizeContinuationState(state.Continuation)
	source := strings.ToLower(reentryRecommendationSourceText(state))

	payload := reentryRecommendationRankContext{
		Turn: reentryRecommendationRankTurn{
			ID:     state.Run.ID,
			Kind:   string(state.Run.Kind),
			Status: string(state.Run.Status),
		},
		MemoryNoteCount:  len(state.MemoryNotes),
		ThreadCount:      len(state.Threads),
		SignalCount:      len(state.Signals),
		EvidenceCount:    len(state.Evidence.Selected),
		EvidenceFallback: state.Evidence.FallbackUsed,
		EvidenceGaps:     len(state.Evidence.MissingEvidenceIDs),
		Signals:          reentryRecommendationSignalFlags(source),
	}
	if op.ID != "" || op.Status != "" || op.Proposal.Status != "" || len(op.PhasePlan.Phases) > 0 || op.PlanLease.Status != "" {
		payload.Operation = reentryRecommendationRankOperation{
			Present:                true,
			Status:                 string(op.Status),
			HasObjective:           strings.TrimSpace(op.Objective) != "",
			ProposalStatus:         string(op.Proposal.Status),
			PhaseCount:             len(op.PhasePlan.Phases),
			PhaseStatusCounts:      reentryPhaseStatusCounts(op.PhasePlan.Phases),
			CurrentAuthorityClass:  reentryCurrentPhaseAuthorityClass(op.PhasePlan),
			PlanLeaseStatus:        string(op.PlanLease.Status),
			PlanLeaseLaneCount:     len(op.PlanLease.Lanes),
			PlanLeaseTurnsSpent:    op.PlanLease.EvidenceDigest.TurnsSpent,
			PlanLeaseCompleted:     len(op.PlanLease.EvidenceDigest.Completed),
			PlanLeaseBlocked:       len(op.PlanLease.EvidenceDigest.Blocked),
			PlanLeaseSuggestedNext: strings.TrimSpace(op.PlanLease.EvidenceDigest.SuggestedNextLease) != "",
		}
	}
	if len(plan.Steps) > 0 {
		payload.Plan = reentryRecommendationRankPlan{
			StepCount:    len(plan.Steps),
			StatusCounts: reentryPlanStatusCounts(plan.Steps),
		}
	}
	if cont.Status != "" || cont.ContinuationLease.Status != "" || cont.RemainingTurns > 0 || cont.ActionProposal.ID != "" {
		payload.Continuation = reentryRecommendationRankContinuation{
			Status:         string(cont.Status),
			LeaseStatus:    string(cont.ContinuationLease.Status),
			RemainingTurns: cont.RemainingTurns,
			HasProposal:    cont.ActionProposal.ID != "",
		}
	}
	if len(state.Missions) > 0 {
		payload.MissionCounts = reentryMissionStatusCounts(state.Missions)
	}
	return payload
}

func reentryRecommendationRankOptions(candidates []session.ReentryRecommendationCandidate) []reentryRecommendationRankOption {
	out := make([]reentryRecommendationRankOption, 0, len(candidates))
	for _, candidate := range normalizeReentryCandidates(candidates) {
		out = append(out, reentryRecommendationRankOption{
			ID:               candidate.ID,
			Kind:             candidate.Kind,
			Label:            reentryCandidateRankSummary(candidate.Kind),
			Summary:          reentryCandidateRankSummary(candidate.Kind),
			IntentClass:      strings.TrimSpace(candidate.IntentClass),
			TemporalFit:      strings.TrimSpace(candidate.TemporalFit),
			WhyNowCategory:   reentryCandidateWhyNowCategory(candidate),
			AuthorityClass:   candidate.AuthorityClass,
			RequiresApproval: candidate.RequiresApproval,
			SourceKind:       candidate.SourceKind,
			Scores:           candidate.Scores,
		})
	}
	return out
}

func reentryCandidateWhyNowCategory(candidate session.ReentryRecommendationCandidate) string {
	candidate = session.NormalizeReentryRecommendationCandidate(candidate)
	switch {
	case candidate.IntentClass != "":
		return strings.TrimSpace(candidate.IntentClass)
	case candidate.SourceKind != "":
		return strings.TrimSpace(candidate.SourceKind)
	default:
		return string(candidate.Kind)
	}
}

func (r *Runtime) recordReentryRecommendationJudgment(key session.SessionKey, status string, payload map[string]any, at time.Time) {
	if r == nil || r.store == nil {
		return
	}
	r.recordExecutionEvent(key, core.ExecutionEventReentryRecommendationJudged, "reentry_recommendation", status, payload, at)
}

func reentryRecommendationAuditCandidates(candidates []session.ReentryRecommendationCandidate) []map[string]any {
	candidates = normalizeReentryCandidates(candidates)
	out := make([]map[string]any, 0, len(candidates))
	for index, candidate := range candidates {
		item := map[string]any{
			"id":                candidate.ID,
			"rank":              index + 1,
			"kind":              string(candidate.Kind),
			"label":             normalizeReentryCandidateLabel(candidate.Label),
			"display_text":      reentryRecommendationCandidateBodyLine(candidate),
			"intent_class":      strings.TrimSpace(candidate.IntentClass),
			"temporal_fit":      strings.TrimSpace(candidate.TemporalFit),
			"why_now":           strings.TrimSpace(candidate.WhyNow),
			"source_kind":       strings.TrimSpace(candidate.SourceKind),
			"source_ref":        strings.TrimSpace(candidate.SourceRef),
			"authority_class":   strings.TrimSpace(candidate.AuthorityClass),
			"requires_approval": candidate.RequiresApproval,
			"dampening_key":     strings.TrimSpace(reentryCandidateDampeningKey(candidate)),
			"weighted_score":    reentryCandidateWeightedScore(candidate),
		}
		if len(candidate.EvidenceRefs) > 0 {
			item["evidence_refs"] = append([]string(nil), candidate.EvidenceRefs...)
		}
		if len(candidate.Scores) > 0 {
			item["scores"] = candidate.Scores
		}
		if reason := strings.TrimSpace(candidate.JudgmentReason); reason != "" {
			item["judgment_reason"] = truncatePreview(reason, 220)
		}
		out = append(out, item)
	}
	return out
}

func reentryRecommendationCandidateIDs(candidates []session.ReentryRecommendationCandidate) []string {
	candidates = normalizeReentryCandidates(candidates)
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if id := strings.TrimSpace(candidate.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func reentryRecommendationJudgeCandidateIDs(parsed reentryJudgeResponse) []string {
	out := make([]string, 0, len(parsed.Candidates))
	for _, candidate := range parsed.Candidates {
		if id := strings.TrimSpace(candidate.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func reentryCandidateRankSummary(kind session.ReentryCandidateKind) string {
	switch session.NormalizeReentryCandidateKind(kind) {
	case session.ReentryCandidateContinueOperation:
		return "continue an operation only if saved state supports a clear follow-up"
	case session.ReentryCandidateRequestNextLease:
		return "ask approval for the next operation step"
	case session.ReentryCandidateResumeMission:
		return "review mission state without assuming authority"
	case session.ReentryCandidateReviewReleaseReadiness:
		return "review release readiness and identify a clear next step"
	case session.ReentryCandidateReviewMemoryHealth:
		return "inspect memory and continuity health before more work"
	case session.ReentryCandidateReflectWithOperator:
		return "reorient with the operator around work, repair, conversation, or rest"
	case session.ReentryCandidateClarifyGoal:
		return "ask the operator for missing goal context"
	default:
		return ""
	}
}

func reentryRecommendationSignalFlags(source string) []string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		return nil
	}
	checks := []struct {
		needle string
		flag   string
	}{
		{"release", "release"},
		{"deploy", "deploy"},
		{"release workflow", "release_workflow"},
		{"release pr", "release_pr"},
		{"github release", "github_release"},
		{"changelog", "changelog"},
		{"tag v", "version_tag"},
	}
	out := make([]string, 0, len(checks))
	for _, check := range checks {
		if strings.Contains(source, check.needle) {
			out = append(out, check.flag)
		}
	}
	return out
}

func reentryPhaseStatusCounts(phases []session.OperationPhase) map[string]int {
	counts := make(map[string]int)
	for _, phase := range phases {
		status := string(session.NormalizePlanStatus(phase.Status))
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	return emptyMapAsNil(counts)
}

func reentryPlanStatusCounts(steps []session.PlanStep) map[string]int {
	counts := make(map[string]int)
	for _, step := range steps {
		status := string(session.NormalizePlanStatus(step.Status))
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	return emptyMapAsNil(counts)
}

func reentryMissionStatusCounts(missions []session.MissionState) map[string]int {
	counts := make(map[string]int)
	for _, mission := range missions {
		status := string(session.NormalizeMissionStatus(mission.Status))
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	return emptyMapAsNil(counts)
}

func reentryCurrentPhaseAuthorityClass(plan session.OperationPhasePlan) string {
	if len(plan.Phases) == 0 {
		return ""
	}
	currentPhaseID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentPhaseID != "" {
		for _, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentPhaseID {
				return strings.TrimSpace(phase.AuthorityClass)
			}
		}
	}
	for _, phase := range plan.Phases {
		if phase.Status == session.PlanStatusInProgress || phase.Status == session.PlanStatusPending {
			return strings.TrimSpace(phase.AuthorityClass)
		}
	}
	return ""
}

func emptyMapAsNil(counts map[string]int) map[string]int {
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func extractReentryJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		return text
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		return text[start : end+1]
	}
	return text
}

func (r *Runtime) deliverReentryRecommendation(ctx context.Context, key session.SessionKey, record session.ReentryRecommendation, now time.Time) error {
	rows := reentryRecommendationButtonRows(record)
	if len(rows) == 0 {
		return nil
	}
	msg := core.OutboundMessage{
		ChatID:     record.ChatID,
		Text:       r.prefixTelegramPresentedText(r.telegramPresentationForMessage(reentryInboundForRecord(record)), reentryRecommendationMessageText(record)),
		ButtonRows: rows,
	}
	messageID, err := r.outbound.SendMessage(ctx, msg)
	if err != nil {
		_, _, _ = r.store.MarkReentryRecommendationStale(record.ID, "recommendation delivery failed", now)
		return nil
	}
	if messageID > 0 {
		updated, _, err := r.store.MarkReentryRecommendationShown(record.ID, messageID, now)
		if err != nil {
			return err
		}
		record = updated
		if sess, loadErr := r.store.Load(key); loadErr == nil && sess != nil {
			if recordErr := r.store.RecordOutbound(key, sess.TurnCount, messageID, "reentry_recommendation"); recordErr != nil {
				return fmt.Errorf("record reentry recommendation outbound: %w", recordErr)
			}
		}
		if threadID := telegramThreadIDFromScope(record.ChatID, record.Scope); threadID > 0 {
			if err := r.store.RecordTelegramCallbackMessageThread(record.ChatID, messageID, threadID, "reentry_recommendation", now); err != nil {
				return fmt.Errorf("record reentry recommendation callback thread: %w", err)
			}
		}
		r.recordExecutionEvent(key, core.ExecutionEventReentryRecommendationShown, "reentry_recommendation", "shown", map[string]any{
			"recommendation_id": record.ID,
			"turn_run_id":       record.SourceTurnRunID,
			"candidate_count":   len(record.Candidates),
			"candidate_order":   reentryRecommendationCandidateIDs(record.Candidates),
			"candidates":        reentryRecommendationAuditCandidates(record.Candidates),
			"message_id":        messageID,
		}, now)
		r.markReentryInteriorSignalsSurfaced(key, record, now)
	}
	return nil
}

func (r *Runtime) markReentryInteriorSignalsSurfaced(key session.SessionKey, record session.ReentryRecommendation, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	refs := make([]session.InteriorSignalRef, 0, len(record.Candidates))
	for _, candidate := range session.NormalizeReentryRecommendation(record).Candidates {
		if candidate.SourceKind != "interior_signal" {
			continue
		}
		category, subject, ok := strings.Cut(strings.TrimSpace(candidate.SourceRef), ":")
		if !ok || strings.TrimSpace(category) == "" || strings.TrimSpace(subject) == "" {
			continue
		}
		refs = append(refs, session.InteriorSignalRef{Category: category, SubjectKey: subject})
	}
	if len(refs) == 0 {
		return
	}
	_ = r.store.MarkInteriorSignalsSurfaced(key, refs, now)
	_ = r.store.MarkInteriorSignalsSurfaced(heartbeatSignalKey(), refs, now)
}

func reentryRecommendationMessageText(record session.ReentryRecommendation) string {
	record = session.NormalizeReentryRecommendation(record)
	lines := []string{"Possible next steps:"}
	index := 1
	for _, candidate := range record.Candidates {
		label := reentryRecommendationCandidateBodyLine(candidate)
		if label == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index, label))
		index++
	}
	return strings.Join(lines, "\n")
}

func reentryRecommendationCandidateBodyLine(candidate session.ReentryRecommendationCandidate) string {
	candidate = session.NormalizeReentryRecommendationCandidate(candidate)
	body := strings.TrimSpace(candidate.BodyText)
	if body == "" {
		body = candidate.Label
	}
	return normalizeReentryRecommendationBodyLabel(body)
}

func normalizeReentryRecommendationBodyLabel(label string) string {
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	label = strings.Trim(label, " .\t\n\r")
	if label == "" {
		return ""
	}
	return neutralizeReentryTelegramMarkdown(reentryBoundRecommendationBodyText(label))
}

func neutralizeReentryTelegramMarkdown(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"`", "'",
		"**", "''",
		"*", "'",
		"[", "",
		"]", "",
		"(", " - ",
		")", "",
	)
	return replacer.Replace(text)
}

func reentryRecommendationButtonRows(record session.ReentryRecommendation) [][]core.OutboundButton {
	record = session.NormalizeReentryRecommendation(record)
	row := make([]core.OutboundButton, 0, len(record.Candidates)+1)
	index := 1
	for _, candidate := range record.Candidates {
		data := core.EncodeReentryRecommendationCallbackData(record.ID, candidate.ID, core.ReentryRecommendationCallbackSelect)
		if data == "" || normalizeReentryRecommendationBodyLabel(candidate.Label) == "" {
			continue
		}
		row = append(row, core.OutboundButton{Text: strconv.Itoa(index), CallbackData: data})
		index++
	}
	ignore := core.EncodeReentryRecommendationCallbackData(record.ID, "", core.ReentryRecommendationCallbackIgnore)
	if ignore != "" {
		row = append(row, core.OutboundButton{Text: "Ignore", CallbackData: ignore})
	}
	if len(row) == 0 {
		return nil
	}
	return [][]core.OutboundButton{row}
}

func reentryRecommendationID(fingerprint string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(fingerprint)))
	return "reentry-" + hex.EncodeToString(sum[:6])
}

func reentryRecommendationOwner(run session.TurnRun) string {
	if run.ChatID == 0 {
		return ""
	}
	return fmt.Sprintf("telegram:%d", run.ChatID)
}

func reentryInboundForRecord(record session.ReentryRecommendation) core.InboundMessage {
	return core.InboundMessage{
		ChatID:           record.ChatID,
		SenderID:         record.SenderID,
		TelegramThreadID: telegramThreadIDFromScope(record.ChatID, record.Scope),
	}
}

func (r *Runtime) CurrentReentryRecommendationFingerprint(key session.SessionKey) (string, bool, error) {
	if r == nil || r.store == nil {
		return "", false, nil
	}
	run, err := r.store.LatestTurnRun(key)
	if err != nil {
		return "", false, err
	}
	if run == nil || run.Status == session.TurnRunStatusRunning {
		return "", false, nil
	}
	state, ok, _, err := r.reentryRecommendationState(context.Background(), key, *run, time.Now().UTC().Add(reentryRecommendationDelay))
	if err != nil || !ok {
		return "", ok, err
	}
	return state.Fingerprint, true, nil
}
