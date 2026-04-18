//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

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
	provider.proposalReplyText = "Continue now because the scoped plan is actively in progress."

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
	if !strings.Contains(sender.inline[0].text, "Approve 1 more turn") {
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
	if !strings.Contains(sender.inline[0].text, "Persona rationale:") {
		t.Fatalf("inline text = %q, want persona rationale block", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Persona intent:") {
		t.Fatalf("inline text = %q, want persona intent block", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Continue now because the scoped plan is actively in progress.") {
		t.Fatalf("inline text = %q, want proposal rationale summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Governor rationale:") {
		t.Fatalf("inline text = %q, want governor rationale block", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Governor intent:") {
		t.Fatalf("inline text = %q, want governor intent block", sender.inline[0].text)
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
}

func TestHandleInboundSkipsContinuationWhenPersonaRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"

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
	if state.PersonaIntent.Decision != session.ContinuationIntentDecisionHold {
		t.Fatalf("persona decision = %q, want hold", state.PersonaIntent.Decision)
	}
	if state.HandshakeBlockedReason != "persona_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want persona_rationale_missing", state.HandshakeBlockedReason)
	}
}

func TestHandleInboundSkipsContinuationWhenGovernorRationaleMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "grounded reply"
	provider.faceReplyText = "visible scene"
	provider.proposalReplyText = "I should continue because there is a concrete next step."

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
	if state.GovernorIntent.Decision != session.ContinuationIntentDecisionHold {
		t.Fatalf("governor decision = %q, want hold", state.GovernorIntent.Decision)
	}
	if state.HandshakeBlockedReason != "governor_rationale_missing" {
		t.Fatalf("handshake blocked reason = %q, want governor_rationale_missing", state.HandshakeBlockedReason)
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
	if last.Content != "[approved continuation event]" {
		t.Fatalf("last content = %q, want machine-authored continuation event text", last.Content)
	}
}
