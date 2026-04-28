//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundOffersContinuationApprovalUI(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"Continue now because the scoped plan is actively in progress.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"The operation remains active and ratified for one bounded follow-up.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8101, UserID: 0, Scope: telegramDMScopeRef(8101)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Fix the continuation UI before merge.",
		Steps: []session.PlanStep{
			{Step: "Swap continuation button order so stop is left and continue is right", Status: session.PlanStatusCompleted},
			{Step: "Summarize the actual next-step plan in the continuation prompt", Status: session.PlanStatusInProgress},
		},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		Objective: "Land the continuation UI polish cleanly.",
		Summary:   "Use plan/proposal content instead of the request preamble.",
		Proposal: session.OperationProposal{
			Summary:       "Patch continuation UI button order and summary text.",
			BoundedEffect: "Local code/test changes limited to continuation UI generation and directly affected tests.",
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 8101, SenderID: 1001, SenderName: "admin", Text: "keep going on the implementation", MessageID: 1})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Should I continue for 1 more turn") {
		t.Fatalf("inline text = %q, want continuation approval prompt", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "keep going on the implementation") {
		t.Fatalf("inline text = %q, want plan/proposal summary instead of user preamble", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Land the continuation UI polish cleanly.") {
		t.Fatalf("inline text = %q, want operation objective in summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Summarize the actual next-step plan in the continuation prompt") {
		t.Fatalf("inline text = %q, want in-progress plan step as next action", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Continue now because the scoped plan is actively in progress.") {
		t.Fatalf("inline text = %q, want explicit persona rationale summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "The operation remains active and ratified for one bounded follow-up.") {
		t.Fatalf("inline text = %q, want explicit governor rationale summary", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "persona intent:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "governor intent:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "persona rationale:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if strings.Contains(strings.ToLower(sender.inline[0].text), "governor rationale:") {
		t.Fatalf("inline text = %q, want single-system framing without persona/governor blocks", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 2 {
		t.Fatalf("rows = %#v, want Stop/Continue row", sender.inline[0].rows)
	}
	if sender.inline[0].rows[0][0].Text != "Stop" || sender.inline[0].rows[0][1].Text != "Continue" {
		t.Fatalf("button order = %#v, want left=Stop right=Continue", sender.inline[0].rows[0])
	}
	state, err := store.ContinuationState(session.SessionKey{ChatID: 8101, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending {
		t.Fatalf("status = %q, want pending", state.Status)
	}
	if state.Objective != "Land the continuation UI polish cleanly." {
		t.Fatalf("objective = %q, want operation objective", state.Objective)
	}
	if state.StageSummary != "Summarize the actual next-step plan in the continuation prompt" {
		t.Fatalf("stage summary = %q, want in-progress plan step", state.StageSummary)
	}
	if strings.TrimSpace(state.DecisionID) == "" {
		t.Fatal("DecisionID empty, want persisted continuation decision id")
	}
	if state.PersonaIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("persona decision = %q, want continue", state.PersonaIntent.Decision)
	}
	if strings.TrimSpace(state.PersonaIntent.Rationale) == "" {
		t.Fatal("persona rationale empty, want persisted rationale")
	}
	if state.GovernorIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("governor decision = %q, want continue", state.GovernorIntent.Decision)
	}
	if !state.GovernorIntent.Ratified {
		t.Fatal("governor ratified = false, want true")
	}
	if state.HandshakeBlockedReason != "" {
		t.Fatalf("handshake blocked reason = %q, want empty", state.HandshakeBlockedReason)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; !strings.Contains(got, state.DecisionID) {
		t.Fatalf("stop callback = %q, want decision id %q", got, state.DecisionID)
	}
	if got := sender.inline[0].rows[0][1].CallbackData; !strings.Contains(got, state.DecisionID) {
		t.Fatalf("continue callback = %q, want decision id %q", got, state.DecisionID)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var offered session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered {
			offered = event
		}
	}
	if offered.ID == 0 {
		t.Fatalf("events = %#v, want continuation.offered event", events)
	}
	payload := executionEventPayload(offered.PayloadJSON)
	if payloadString(payload, "decision_id") != state.DecisionID {
		t.Fatalf("offered payload decision_id = %q, want %q", payloadString(payload, "decision_id"), state.DecisionID)
	}
	if payloadString(payload, "objective") != state.Objective {
		t.Fatalf("offered payload objective = %q, want %q", payloadString(payload, "objective"), state.Objective)
	}
	if payloadString(payload, "stage_summary") != state.StageSummary {
		t.Fatalf("offered payload stage_summary = %q, want %q", payloadString(payload, "stage_summary"), state.StageSummary)
	}
	remainingTurns, ok := payloadInt64(payload, "remaining_turns")
	if !ok || remainingTurns != 1 {
		t.Fatalf("offered payload remaining_turns = %d (ok=%v), want 1", remainingTurns, ok)
	}
	if payloadString(payload, "state_source") != "continuation_state" {
		t.Fatalf("offered payload state_source = %q, want continuation_state", payloadString(payload, "state_source"))
	}
}

func TestHandleInboundContinuationApprovalPromptFallsBackWhenRenderedTextUsesSplitRoleLabels(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = strings.Join([]string{
		"I can continue from here.",
		"",
		"Persona intent:",
		"continue",
		"",
		"Governor intent:",
		"continue",
		"",
		"Approve 1 more turn(s)?",
	}, "\n")
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I should continue because this turn has a clear next step.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"The bounded next step remains ratified.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8116, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1", len(sender.inline))
	}
	inline := strings.ToLower(sender.inline[0].text)
	if strings.Contains(inline, "persona intent:") || strings.Contains(inline, "governor intent:") {
		t.Fatalf("inline text = %q, want fallback without split-role labels", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Should I continue for 1 more turn") {
		t.Fatalf("inline text = %q, want single-system fallback approval question", sender.inline[0].text)
	}
}

func TestGroundContinuationPromptWithExecutionEvidenceFallsBackWithoutMatchingEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8191, UserID: 0, Scope: telegramDMScopeRef(8191)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-missing",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The thread still has one bounded action left.",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The bounded step is ratified.",
			Ratified:  true,
		},
	}

	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationPromptFallback(state) {
		t.Fatalf("grounded prompt = %q, want fallback when TES continuation evidence is missing", grounded)
	}
	if !strings.Contains(note, "continuation evidence is unavailable") {
		t.Fatalf("grounding note = %q, want missing-evidence explanation", note)
	}
}

func TestGroundContinuationPromptWithExecutionEvidenceFallsBackAfterRevocation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8192, UserID: 0, Scope: telegramDMScopeRef(8192)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-revoked",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The thread still has one bounded action left.",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "The bounded step is ratified.",
			Ratified:  true,
		},
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventContinuationOffered,
			Stage:       "continuation",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"continuation-revoked","remaining_turns":1}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventContinuationRevoked,
			Stage:       "continuation",
			Status:      "revoked",
			PayloadJSON: `{"decision_id":"continuation-revoked"}`,
			CreatedAt:   now.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationPromptFallback(state) {
		t.Fatalf("grounded prompt = %q, want fallback when latest continuation event is revoked", grounded)
	}
	if !strings.Contains(note, "latest=continuation.revoked") {
		t.Fatalf("grounding note = %q, want revoked latest-event explanation", note)
	}
}

func TestGroundContinuationBlockedNoticeWithExecutionEvidenceFallsBackWithoutBlockedEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8193, UserID: 0, Scope: telegramDMScopeRef(8193)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	candidate := "I can't continue right now."
	grounded, note := rt.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationBlockedFallback(state) {
		t.Fatalf("grounded blocked notice = %q, want deterministic fallback without TES evidence", grounded)
	}
	if !strings.Contains(note, "continuation evidence is unavailable") {
		t.Fatalf("grounding note = %q, want missing-evidence explanation", note)
	}
}

func TestGroundContinuationBlockedNoticeWithExecutionEvidenceFallsBackWhenLatestIsNotBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8194, UserID: 0, Scope: telegramDMScopeRef(8194)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventContinuationOffered,
		Stage:       "continuation",
		Status:      "pending",
		PayloadJSON: `{"decision_id":"continuation-foo"}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	candidate := "I can't continue right now."
	grounded, note := rt.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, candidate)
	if grounded != renderContinuationBlockedFallback(state) {
		t.Fatalf("grounded blocked notice = %q, want deterministic fallback when latest event is not blocked", grounded)
	}
	if !strings.Contains(note, "latest=continuation.offered") {
		t.Fatalf("grounding note = %q, want latest continuation event explanation", note)
	}
}

func TestHandleInboundSkipsContinuationWhenPersonaRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor still ratifies continuation for the next bounded step.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8111, UserID: 0, Scope: telegramDMScopeRef(8111)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Keep moving.",
		Steps: []session.PlanStep{
			{Step: "Ship the remaining tests", Status: session.PlanStatusInProgress},
		},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		Objective: "Finalize continuation behavior.",
		Proposal: session.OperationProposal{
			Summary: "Only ask for continuation when rationale is clear.",
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-persona",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8111, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without persona rationale", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8111, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when clearing stale pending continuation", state.Status)
	}
	if state.DecisionID != "" {
		t.Fatalf("decision id = %q, want cleared", state.DecisionID)
	}
	if state.PersonaIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("persona decision = %q, want continue when explicit intent is present", state.PersonaIntent.Decision)
	}
	if state.HandshakeBlockedReason != "persona_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want persona_rationale_missing", state.HandshakeBlockedReason)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var blocked session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			blocked = event
		}
	}
	if blocked.ID == 0 {
		t.Fatalf("events = %#v, want continuation.blocked event", events)
	}
	payload := executionEventPayload(blocked.PayloadJSON)
	if payloadString(payload, "reason") != state.HandshakeBlockedReason {
		t.Fatalf("blocked payload reason = %q, want %q", payloadString(payload, "reason"), state.HandshakeBlockedReason)
	}
	remainingTurns, ok := payloadInt64(payload, "remaining_turns")
	if !ok || remainingTurns != 0 {
		t.Fatalf("blocked payload remaining_turns = %d (ok=%v), want 0", remainingTurns, ok)
	}
	if payloadString(payload, "state_source") != "continuation_state" {
		t.Fatalf("blocked payload state_source = %q, want continuation_state", payloadString(payload, "state_source"))
	}
}

func TestHandleInboundSkipsContinuationWhenGovernorRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I should continue because there is a concrete next step.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8112, UserID: 0, Scope: telegramDMScopeRef(8112)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-governor",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8112, SenderID: 1001, SenderName: "admin", Text: "hello", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without governor rationale", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8112, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when clearing stale pending continuation", state.Status)
	}
	if state.DecisionID != "" {
		t.Fatalf("decision id = %q, want cleared", state.DecisionID)
	}
	if state.GovernorIntent.Decision != session.ContinuationIntentDecisionContinue {
		t.Fatalf("governor decision = %q, want continue when explicit intent is present", state.GovernorIntent.Decision)
	}
	if state.HandshakeBlockedReason != "governor_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want governor_rationale_missing", state.HandshakeBlockedReason)
	}
}

func TestHandleInboundSkipsContinuationWithoutExplicitPersonaIntent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = strings.Join([]string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"I can keep moving.",
	}, "\n")
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor ratifies another bounded turn.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8113, UserID: 0, Scope: telegramDMScopeRef(8113)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-persona-intent",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8113, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want 0 without explicit persona intent contract", len(sender.inline))
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8113, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.HandshakeBlockedReason != "persona_intent_missing" {
		t.Fatalf("handshake blocked reason = %q, want persona_intent_missing", state.HandshakeBlockedReason)
	}
}

func TestHandleInboundSendsPersonaVoicedContinuationBlockedNotice(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor rationale exists but ratification is withheld.",
		false,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8114, UserID: 0, Scope: telegramDMScopeRef(8114)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-pending",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8114, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) < 2 {
		t.Fatalf("sent count = %d, want main reply plus continuation blocked notice", len(sender.sent))
	}
	notice := sender.sent[len(sender.sent)-1].Text
	if notice != provider.repairReplyText {
		t.Fatalf("blocked notice = %q, want persona-rendered repair text", notice)
	}
	if !strings.HasPrefix(strings.TrimSpace(notice), "I ") {
		t.Fatalf("blocked notice = %q, want first-person phrasing", notice)
	}
}

func TestHandleInboundDoesNotSendContinuationBlockedNoticeWithoutPriorActiveContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor rationale exists but ratification is withheld.",
		false,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8115, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent count = %d, want only main reply when no prior continuation was active", len(sender.sent))
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: 8115, UserID: 0, Scope: telegramDMScopeRef(8115)}, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var blocked session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			blocked = event
		}
	}
	if blocked.ID == 0 {
		t.Fatalf("events = %#v, want blocked event for internal telemetry", events)
	}
	payload := executionEventPayload(blocked.PayloadJSON)
	got, ok := payload["user_visible"].(bool)
	if !ok || got {
		t.Fatalf("blocked payload user_visible = %v (ok=%v), want false", got, ok)
	}
}

func TestApproveContinuationPersistsApproverIdentity(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8102, UserID: 0, Scope: telegramDMScopeRef(8102)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusPending, RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8102, 1002)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if state.ApprovedBy != 1002 {
		t.Fatalf("ApprovedBy = %d, want 1002", state.ApprovedBy)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ApprovedBy != 1002 {
		t.Fatalf("persisted ApprovedBy = %d, want 1002", got.ApprovedBy)
	}
}

func TestApproveContinuationRejectsNonPendingState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8106, UserID: 0, Scope: telegramDMScopeRef(8106)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision",
		RemainingTurns: 1,
		ApprovedBy:     1002,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	_, err = rt.ApproveContinuation(8106, 1001)
	if err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("ApproveContinuation() err = %v, want not pending error", err)
	}
}

func TestTriggerContinuationRunsAsApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{defs: []agent.ToolDef{testExecToolDef()}, supportsPrincipal: true}
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	key := session.SessionKey{ChatID: 8103, UserID: 0, Scope: telegramDMScopeRef(8103)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, ApprovedBy: 1002}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8103); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if tools.lastPrincipal.Role != principal.RoleApprovedUser {
		t.Fatalf("last principal role = %q, want approved_user", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.TelegramUserID != 1002 {
		t.Fatalf("last principal user id = %d, want 1002", tools.lastPrincipal.TelegramUserID)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle {
		t.Fatalf("status = %q, want idle when continuation consensus is missing", got.Status)
	}
	if got.ApprovedBy != 0 {
		t.Fatalf("ApprovedBy = %d, want cleared after approved continuation turn", got.ApprovedBy)
	}
	if got.HandshakeBlockedReason == "" {
		t.Fatal("HandshakeBlockedReason empty, want explicit reason when continuation is not offered again")
	}
}

func TestTriggerContinuationFailsClosedWithoutRecordedApprover(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	err = rt.TriggerContinuation(context.Background(), 8104)
	if err == nil || !strings.Contains(err.Error(), "approver is not recorded") {
		t.Fatalf("TriggerContinuation() err = %v, want missing approver error", err)
	}
}

func TestTriggerContinuationUsesMachineAuthoredContinuationEventText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8105, UserID: 0, Scope: telegramDMScopeRef(8105)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusApproved, RemainingTurns: 1, ApprovedBy: 1002}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8105); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("lastGovernorMsgs empty, want continuation turn input")
	}
	last := provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1]
	if last.Role != "user" {
		t.Fatalf("last role = %q, want user-compatible provider input", last.Role)
	}
	if last.Content != approvedContinuationEventText {
		t.Fatalf("last content = %q, want machine-authored continuation event text", last.Content)
	}
}

func testPersonaContinuationProposal(decision session.ContinuationIntentDecision, rationale string) string {
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_NEXT_STEP: Resume the next bounded step.",
		"CONTINUATION_CONFIDENCE: medium",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}

func testGovernorContinuationRatification(decision session.ContinuationIntentDecision, rationale string, ratified bool) string {
	ratifiedToken := "no"
	if ratified {
		ratifiedToken = "yes"
	}
	lines := []string{
		"INSPECT: no",
		"QUESTION: no",
		"ANSWER: yes",
		"RATIFICATION: accept",
		"PLAN:",
		"- Continue with the next bounded step.",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: " + string(decision),
		"CONTINUATION_RATIFIED: " + ratifiedToken,
		"CONTINUATION_NEXT_STEP: Continue with the next bounded step.",
		"CONTINUATION_CONSTRAINTS: Stay within the current objective and local repo scope.",
		"CONTINUATION_CONFIDENCE: high",
	}
	if strings.TrimSpace(rationale) != "" {
		lines = append(lines, "CONTINUATION_RATIONALE: "+strings.TrimSpace(rationale))
	}
	return strings.Join(lines, "\n")
}
