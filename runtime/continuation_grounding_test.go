//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestGroundContinuationPromptUsesLatestEventsInLongSession(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 81920, UserID: 0, Scope: telegramDMScopeRef(81920)}
	now := time.Now().UTC()
	for i := 0; i < 350; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":1}`,
			CreatedAt:   now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventContinuationOffered,
		Stage:       "continuation",
		Status:      "pending",
		PayloadJSON: `{"decision_id":"continuation-latest","remaining_turns":1}`,
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(continuation) err = %v", err)
	}

	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "continuation-latest",
		RemainingTurns: 1,
		Objective:      "Keep the refactor bounded.",
		StageSummary:   "Write focused tests first.",
	}
	candidate := "I can continue from here.\n\nShould I continue for 1 more turn(s)?"
	grounded, note := rt.groundContinuationPromptWithExecutionEvidence(key, state, candidate)
	if grounded != candidate {
		t.Fatalf("grounded prompt = %q note=%q, want candidate grounded by latest continuation event", grounded, note)
	}
	if note != "" {
		t.Fatalf("grounding note = %q, want empty", note)
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
	if grounded != renderContinuationBlockedFallback(state, rt.governorName()) {
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
	if grounded != renderContinuationBlockedFallback(state, rt.governorName()) {
		t.Fatalf("grounded blocked notice = %q, want deterministic fallback when latest event is not blocked", grounded)
	}
	if !strings.Contains(note, "latest=continuation.offered") {
		t.Fatalf("grounding note = %q, want latest continuation event explanation", note)
	}
}

func TestRenderContinuationBlockedNoticeUsesAnonymousGovernorName(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Identity.AnonymousProfile = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	key := session.SessionKey{ChatID: 8195, UserID: 0, Scope: telegramDMScopeRef(8195)}
	state := session.ContinuationState{
		Status:                 session.ContinuationStatusIdle,
		HandshakeBlockedReason: "governor_not_ratified",
	}
	got := rt.renderContinuationBlockedNotice(context.Background(), key, core.InboundMessage{
		ChatID: key.ChatID,
		Text:   "continue",
	}, state)
	if !strings.Contains(got, "I need approval before continuing") {
		t.Fatalf("blocked notice = %q, want humanized approval hold", got)
	}
	if strings.Contains(got, "Idolum") || strings.Contains(got, "System did not ratify") {
		t.Fatalf("blocked notice = %q, want no internal governor name in anonymous profile", got)
	}
}

func TestRenderContinuationPromptUsesAnonymousFaceNameInRepairNotes(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Identity.AnonymousProfile = true
	provider.repairReplyText = "Ready to continue."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8196, UserID: 0, Scope: telegramDMScopeRef(8196)}
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-anonymous-face",
		RemainingTurns: 1,
	}
	_ = rt.renderContinuationPrompt(context.Background(), key, core.InboundMessage{
		ChatID: key.ChatID,
		Text:   "continue",
	}, state)
	seen := strings.Join(provider.seenFaceSystem, "\n")
	if !strings.Contains(seen, "Keep this in first person as Assistant.") {
		t.Fatalf("face repair prompt = %q, want anonymous face repair note", seen)
	}
	if strings.Contains(seen, "first person as Idolum") {
		t.Fatalf("face repair prompt = %q, want no default face name in repair note", seen)
	}
}

func TestHandleInboundRepairsContinuationWhenPersonaRationaleMissing(t *testing.T) {
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
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one read-only repair approval without persona rationale", inlineCount)
	}
	if !strings.Contains(inlineText, "Rebuild continuation rationale") || strings.Contains(inlineText, "persona_rationale_missing") {
		t.Fatalf("inline text = %q, want normal repair approval without raw reason", inlineText)
	}

	state, err := store.ContinuationState(session.SessionKey{ChatID: 8111, UserID: 0})
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending {
		t.Fatalf("status = %q, want pending repair continuation", state.Status)
	}
	if state.DecisionID == "" {
		t.Fatalf("decision id = empty, want repair decision")
	}
	if state.HandshakeBlockedReason != "" {
		t.Fatalf("handshake blocked reason = %q, want cleared for repair approval", state.HandshakeBlockedReason)
	}
	if !strings.Contains(state.ActionProposal.Summary, "Rebuild continuation rationale") {
		t.Fatalf("action proposal = %#v, want rationale repair approval", state.ActionProposal)
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	opState = session.NormalizeOperationState(opState)
	if len(opState.PhasePlan.Phases) != 2 {
		t.Fatalf("phase count = %d, want repair phase plus original proposal phase", len(opState.PhasePlan.Phases))
	}
	repair := opState.PhasePlan.Phases[0]
	original := opState.PhasePlan.Phases[1]
	if !strings.HasPrefix(repair.ID, operationPersonaRationaleRepairPhasePrefix) || repair.AuthorityClass != "read_only_review" || !repair.RequiresApproval {
		t.Fatalf("repair phase = %#v, want read-only persona-rationale repair", repair)
	}
	if !original.RequiresApproval || original.Status != session.PlanStatusPending {
		t.Fatalf("original phase = %#v, want original work preserved for later approval", original)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var repaired session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationCompileRepaired {
			repaired = event
		}
	}
	if repaired.ID == 0 {
		t.Fatalf("events = %#v, want continuation.compile_repaired event", events)
	}
	payload := executionEventPayload(repaired.PayloadJSON)
	if payloadString(payload, "repair_kind") != string(continuationCompileRepairPersonaRationale) || payloadString(payload, "normalized_reason") != "persona_rationale_missing" {
		t.Fatalf("compile repair payload = %#v, want persona rationale repair", payload)
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

func TestHandleInboundClosesCompletedOperationContinuationQuietly(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "done"
	provider.faceReplyText = "The bounded work is complete."
	provider.repairReplyText = "I can't continue yet because Aphelion did not ratify this continuation request."
	provider.proposalReplyText = testPersonaContinuationProposal(
		session.ContinuationIntentDecisionContinue,
		"I can continue after one more approval.",
	)
	provider.planningReplyText = testGovernorContinuationRatification(
		session.ContinuationIntentDecisionContinue,
		"Governor ratified continuation, but the typed operation is already complete.",
		true,
	)

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8117, UserID: 0, Scope: telegramDMScopeRef(8117)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "prior-pending-completed",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-operation",
		Objective: "Finish the bounded phase.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "All approved work completed.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8117, SenderID: 1001, SenderName: "admin", Text: "nice", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	sentCount := len(sender.sent)
	lastText := ""
	if sentCount > 0 {
		lastText = sender.sent[sentCount-1].Text
	}
	sender.mu.Unlock()
	if sentCount != 1 || lastText != "The bounded work is complete." {
		t.Fatalf("sent count/text = %d/%q, want only main reply", sentCount, lastText)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.HandshakeBlockedReason != "" {
		t.Fatalf("continuation = %#v, want quiet idle close without blocked reason", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationBlocked {
			t.Fatalf("events = %#v, want no blocked event after completed operation close", events)
		}
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
	if !errors.Is(err, core.ErrContinuationNotPending) {
		t.Fatalf("ApproveContinuation() err = %v, want ErrContinuationNotPending", err)
	}
}
