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
	candidates := r.reentryRecommendationCandidates(ctx, state)
	if len(candidates) == 0 {
		return nil
	}
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
	Fingerprint  string
}

func (r *Runtime) reentryRecommendationState(ctx context.Context, key session.SessionKey, run session.TurnRun, now time.Time) (reentryRecommendationState, bool, string, error) {
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
	}
	state.Fingerprint = r.reentryRecommendationFingerprint(state)
	return state, true, "", nil
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
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r *Runtime) reentryRecommendationCandidates(ctx context.Context, state reentryRecommendationState) []session.ReentryRecommendationCandidate {
	_ = ctx
	source := strings.ToLower(reentryRecommendationSourceText(state))
	candidates := make([]session.ReentryRecommendationCandidate, 0, 4)
	if reentryLooksReleaseRelated(source) {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               "c1",
			Kind:             session.ReentryCandidateReviewReleaseReadiness,
			Label:            "Review whether the latest release work is safe to deploy",
			Summary:          "Review the completed release-oriented state and identify whether a deploy/release proposal is actually warranted.",
			PromptText:       reentryPrompt("Review release readiness from saved state. Explain whether the latest release work is safe to deploy; if action is needed, ask for approval before taking it."),
			AuthorityClass:   "read_only",
			RequiresApproval: true,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID), "operation_state"},
		})
	}
	op := session.NormalizeOperationState(state.Operation)
	if op.Active() && (op.Status == session.OperationStatusCompleted || op.Status == session.OperationStatusBlocked || op.Status == session.OperationStatusActive) {
		kind := session.ReentryCandidateRequestNextLease
		label := "Identify the smallest useful next step for the active operation"
		if op.Status == session.OperationStatusBlocked {
			label = "Find the repair path for the blocked operation"
		}
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             kind,
			Label:            label,
			Summary:          "Reconstruct the operation state, decide whether work remains, and ask only for the smallest useful next approval.",
			PromptText:       reentryPrompt("Re-enter the current operation from saved state. Identify the smallest useful next step, explain why it matters, and ask for approval before acting."),
			AuthorityClass:   firstNonEmpty(op.PhasePlan.CurrentPhaseID, "operation"),
			RequiresApproval: true,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID), "operation_state"},
		})
	}
	if len(state.Missions) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateResumeMission,
			Label:            "Review which remembered mission is worth attention now",
			Summary:          "Review the highest-signal mission currently recorded in the mission ledger without turning memory into hidden authority.",
			PromptText:       reentryPrompt("Review the selected mission and current saved state. Explain whether it deserves attention now; ask for approval if action is needed, otherwise say why it should remain parked."),
			AuthorityClass:   "mission_review",
			RequiresApproval: true,
			BasisRefs:        []string{"mission"},
		})
	}
	if len(state.MemoryNotes) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReviewMemoryHealth,
			Label:            "Inspect whether memory state is clean after the recent work",
			Summary:          "Check memory and continuity notes for stale, noisy, or unresolved state before starting more work.",
			PromptText:       reentryPrompt("Inspect memory and continuity health from saved state. Summarize whether anything looks stale, noisy, or unresolved; ask approval before making changes."),
			AuthorityClass:   "read_only",
			RequiresApproval: true,
			BasisRefs:        []string{"memory_state"},
		})
	}
	if strings.TrimSpace(state.Run.RequestText) != "" || op.Active() || len(state.Missions) > 0 || len(state.MemoryNotes) > 0 {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               nextReentryCandidateID(candidates),
			Kind:             session.ReentryCandidateReflectWithOperator,
			Label:            "Pause and choose whether work, repair, or conversation would help most",
			Summary:          "Offer a reflective reorientation option so productivity and system wellbeing stay connected.",
			PromptText:       reentryPrompt("Pause and reorient with the operator. Ask whether the next move should be work, repair, conversation, or rest; do not start external actions without approval."),
			AuthorityClass:   "conversation",
			RequiresApproval: false,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID)},
		})
	}
	if len(candidates) == 0 && (op.Active() || len(state.MemoryNotes) > 0 || strings.TrimSpace(state.Run.RequestText) != "") {
		candidates = append(candidates, session.ReentryRecommendationCandidate{
			ID:               "c1",
			Kind:             session.ReentryCandidateClarifyGoal,
			Label:            "Ask what would be genuinely useful next",
			Summary:          "Ask the operator for the missing next objective instead of inventing authority.",
			PromptText:       reentryPrompt("Saved state does not show a concrete next step. Ask one concise clarification question about the next objective."),
			AuthorityClass:   "clarification",
			RequiresApproval: false,
			BasisRefs:        []string{fmt.Sprintf("turn_run:%d", state.Run.ID)},
		})
	}
	return normalizeReentryCandidates(candidates)
}

func nextReentryCandidateID(existing []session.ReentryRecommendationCandidate) string {
	return fmt.Sprintf("c%d", len(existing)+1)
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
		if _, ok := seen[candidate.ID]; ok {
			continue
		}
		seen[candidate.ID] = struct{}{}
		out = append(out, candidate)
	}
	return out
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

func reentryPrompt(instruction string) string {
	return strings.TrimSpace(instruction) + "\n\nThis suggestion only chose a path. If action is needed, ask before doing it."
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
	Turn            reentryRecommendationRankTurn         `json:"turn"`
	Operation       reentryRecommendationRankOperation    `json:"operation,omitempty"`
	Plan            reentryRecommendationRankPlan         `json:"plan,omitempty"`
	Continuation    reentryRecommendationRankContinuation `json:"continuation,omitempty"`
	MissionCounts   map[string]int                        `json:"mission_counts,omitempty"`
	MemoryNoteCount int                                   `json:"memory_note_count,omitempty"`
	Signals         []string                              `json:"signals,omitempty"`
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
	AuthorityClass   string                       `json:"authority_class,omitempty"`
	RequiresApproval bool                         `json:"requires_approval,omitempty"`
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
	system := "Rank existing Aphelion idle re-entry candidates. You may shorten labels, but do not invent new candidates or authority. Return compact JSON only: {\"candidates\":[{\"id\":\"c1\",\"label\":\"Two Words\",\"rank\":1}]}."
	resp, err := completeProvider(ctx, r.provider, []agent.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: string(payload)},
	}, nil, nil)
	if err != nil {
		r.recordExecutionEvent(state.Key, core.ExecutionEventReentryRecommendationJudged, "reentry_recommendation", "failed", map[string]any{
			"turn_run_id": state.Run.ID,
			"error":       trimError(err.Error()),
		}, time.Now().UTC())
		return candidates
	}
	var parsed reentryJudgeResponse
	if err := json.Unmarshal([]byte(extractReentryJSON(resp.Content)), &parsed); err != nil {
		r.recordExecutionEvent(state.Key, core.ExecutionEventReentryRecommendationJudged, "reentry_recommendation", "malformed", map[string]any{
			"turn_run_id": state.Run.ID,
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
	for i, item := range parsed.Candidates {
		candidate, ok := byID[strings.TrimSpace(item.ID)]
		if !ok {
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
	return normalizeReentryCandidates(out)
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
		MemoryNoteCount: len(state.MemoryNotes),
		Signals:         reentryRecommendationSignalFlags(source),
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
			Label:            normalizeReentryCandidateLabel(candidate.Label),
			Summary:          reentryCandidateRankSummary(candidate.Kind),
			AuthorityClass:   candidate.AuthorityClass,
			RequiresApproval: candidate.RequiresApproval,
		})
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
		{"reinstall", "reinstall"},
		{"restart", "restart"},
		{"latest main", "latest_main"},
		{"service", "service"},
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
			"message_id":        messageID,
		}, now)
	}
	return nil
}

func reentryRecommendationMessageText(record session.ReentryRecommendation) string {
	record = session.NormalizeReentryRecommendation(record)
	lines := []string{"Possible next steps:"}
	index := 1
	for _, candidate := range record.Candidates {
		label := normalizeReentryRecommendationBodyLabel(candidate.Label)
		if label == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", index, label))
		index++
	}
	return strings.Join(lines, "\n")
}

func normalizeReentryRecommendationBodyLabel(label string) string {
	label = strings.Join(strings.Fields(strings.TrimSpace(label)), " ")
	label = strings.Trim(label, " .\t\n\r")
	if label == "" {
		return ""
	}
	return label
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
