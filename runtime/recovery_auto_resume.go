//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const startupRecoveryResumePrefix = "[restart recovery resume proposal]"

type startupRecoveryResumeProposalResult struct {
	Proposed int
	Skipped  int
}

func (r startupRecoveryResumeProposalResult) total() int {
	return r.Proposed + r.Skipped
}

func recordStartupRecoveryResumeProposalPayload(result startupRecoveryResumeProposalResult) map[string]any {
	return map[string]any{
		"proposed": result.Proposed,
		"skipped":  result.Skipped,
	}
}

func (r *Runtime) startStartupRecoveryResumeProposals(ctx context.Context, runs []session.TurnRun, now time.Time) startupRecoveryResumeProposalResult {
	result := startupRecoveryResumeProposalResult{}
	if r == nil || r.store == nil || len(runs) == 0 {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	for _, run := range latestRecoveryResumableRunsBySession(runs) {
		key := startupRecoveryRunSessionKey(run)
		payload := startupRecoveryResumePayload(run)
		if prior, exists, err := r.store.ContinuationStateIfExists(key); err == nil && exists {
			prior = session.NormalizeContinuationState(prior)
			if prior.Active() || continuationStateRestartParked(prior) {
				payload["reason"] = "continuation_state_already_requires_confirmation"
				r.recordExecutionEvent(key, core.ExecutionEventRecoveryResume, "recovery", "skipped", payload, now)
				result.Skipped++
				continue
			}
		}
		actor, ok := r.startupRecoveryResumeActor(run)
		if !ok {
			payload["reason"] = "actor_not_admitted"
			r.recordExecutionEvent(key, core.ExecutionEventRecoveryResume, "recovery", "skipped", payload, now)
			result.Skipped++
			continue
		}
		if err := r.proposeStartupRecoveryResume(ctx, key, run, actor, now); err != nil {
			payload["error"] = trimError(err.Error())
			r.recordExecutionEvent(key, core.ExecutionEventRecoveryResume, "recovery", "failed", payload, now)
			result.Skipped++
			continue
		}
		r.recordExecutionEvent(key, core.ExecutionEventRecoveryResume, "recovery", "proposed", payload, now)
		result.Proposed++
	}
	return result
}

func (r *Runtime) proposeStartupRecoveryResume(ctx context.Context, key session.SessionKey, run session.TurnRun, actor principal.Principal, now time.Time) error {
	if r == nil || r.store == nil || r.outbound == nil {
		return fmt.Errorf("startup recovery resume proposal dependencies are unavailable")
	}
	if key.ChatID == 0 {
		return fmt.Errorf("startup recovery resume proposal chat id is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := startupRecoveryResumeContinuationState(run, actor, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist startup recovery resume continuation: %w", err)
	}
	msg := continuationPromptInboundForKey(key, startupRecoveryResumePrefix, core.InboundOriginStartupRecovery, "resume_proposal")
	text := r.renderContinuationPrompt(ctx, key, msg, state)
	if strings.TrimSpace(text) == "" {
		text = renderContinuationPromptFallback(state)
	}
	if err := r.sendContinuationApprovalPrompt(ctx, key, msg, state, text); err != nil {
		return fmt.Errorf("send startup recovery resume proposal: %w", err)
	}
	return nil
}

func startupRecoveryResumeContinuationState(run session.TurnRun, actor principal.Principal, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	decisionID := fmt.Sprintf("recovery-resume-%d", run.ID)
	requestPreview := truncatePreview(strings.TrimSpace(run.RequestText), 500)
	lastTool := strings.TrimSpace(run.LastToolName)
	lastResult := truncatePreview(strings.TrimSpace(run.LastToolResultPreview), 260)
	lastErr := truncatePreview(strings.TrimSpace(run.LastToolError), 260)

	summary := "Resume interrupted work"
	if requestPreview != "" {
		summary = "Resume interrupted work: " + truncatePreview(requestPreview, 120)
	}
	whyParts := []string{"The service restarted while this was running, so I need one confirmation before continuing."}
	if lastTool != "" {
		whyParts = append(whyParts, "Last tool: "+lastTool+".")
	}
	if lastResult != "" {
		whyParts = append(whyParts, "Last result: "+lastResult)
	}
	if lastErr != "" {
		whyParts = append(whyParts, "Last error: "+lastErr)
	}
	boundedEffect := "Verify persisted state, continue only still-needed work from the interrupted request, do not repeat destructive or external actions without current evidence, report evidence, and stop."

	action := session.ActionProposal{
		ID:               "aprop-" + decisionID,
		Summary:          summary,
		WhyNow:           strings.Join(whyParts, " "),
		BoundedEffect:    boundedEffect,
		RiskClass:        "restart_recovery",
		AllowedActions:   []string{"verify_persisted_state", "continue_still_needed_bounded_work", "report_evidence"},
		ForbiddenActions: []string{"auto_resume_without_user_confirmation", "repeat_destructive_or_external_action_without_current_evidence", "expand_authority_without_new_approval"},
		ValidationPlan:   []string{"verify the interrupted work is still needed", "consume at most one approved recovery turn", "report evidence and residual risk"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      firstNonEmptyContinuation(requestPreview, "Recover interrupted turn after restart"),
		StageSummary:   summary,
		RemainingTurns: 1,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "The interrupted work can resume after a short safety check.",
			NextStep:   "Verify persisted state, then resume only still-needed bounded work.",
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   "Restart happened after the original request; confirm once before continuing the preserved work.",
			NextStep:    "Verify persisted state, then continue only still-needed bounded work.",
			Constraints: boundedEffect,
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		ActionProposal: action,
		UpdatedAt:      now,
	}
	state.ContinuationLease = buildContinuationLease(action, 1, now)
	return session.NormalizeContinuationState(state)
}

func latestRecoveryResumableRunsBySession(runs []session.TurnRun) []session.TurnRun {
	bySession := make(map[string]session.TurnRun)
	for _, run := range runs {
		if !startupRecoveryRunResumable(run) {
			continue
		}
		key := startupRecoveryRunSessionKey(run)
		sessionID := session.SessionIDForKey(key)
		prior, exists := bySession[sessionID]
		if !exists || run.StartedAt.After(prior.StartedAt) || (run.StartedAt.Equal(prior.StartedAt) && run.ID > prior.ID) {
			bySession[sessionID] = run
		}
	}
	out := make([]session.TurnRun, 0, len(bySession))
	for _, run := range bySession {
		out = append(out, run)
	}
	return out
}

func startupRecoveryRunResumable(run session.TurnRun) bool {
	if run.Kind != session.TurnRunKindInteractive || run.ChatID <= 0 {
		return false
	}
	scope := session.NormalizeScopeRef(run.Scope)
	if strings.TrimSpace(scope.DurableAgentID) != "" {
		return false
	}
	if scope.Kind != "" && scope.Kind != session.ScopeKindTelegramDM && scope.Kind != session.ScopeKindTelegramThread {
		return false
	}
	request := strings.TrimSpace(run.RequestText)
	if request == "" || strings.HasPrefix(request, startupRecoveryResumePrefix) {
		return false
	}
	return true
}

func (r *Runtime) startupRecoveryResumeActor(run session.TurnRun) (principal.Principal, bool) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, false
	}
	if actor, ok := r.resolver.ResolveTelegramUser(run.ChatID); ok {
		return actor, true
	}
	scope := session.NormalizeScopeRef(run.Scope)
	if run.UserID > 0 && (run.ChatID == run.UserID || (scope.Kind == session.ScopeKindTelegramDM && scope.ID == strconv.FormatInt(run.UserID, 10))) {
		return r.resolver.ResolveTelegramUser(run.UserID)
	}
	return principal.Principal{}, false
}

func startupRecoveryResumePayload(run session.TurnRun) map[string]any {
	return map[string]any{
		"run_id":           run.ID,
		"chat_id":          run.ChatID,
		"session_id":       strings.TrimSpace(run.SessionID),
		"request_preview":  truncatePreview(run.RequestText, 220),
		"auto_resume_mode": "requires_user_confirmation",
	}
}

func startupRecoveryRunSessionKey(run session.TurnRun) session.SessionKey {
	scope := session.NormalizeScopeRef(run.Scope)
	if scope.IsZero() && run.ChatID != 0 {
		scope = telegramDMScopeRef(run.ChatID)
	}
	return session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: scope}
}
