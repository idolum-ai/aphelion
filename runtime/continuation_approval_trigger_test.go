//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestApproveContinuationActivatesContinuationLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8107, UserID: 0, Scope: telegramDMScopeRef(8107)}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusPending,
		DecisionID:        "decision-lease-approve",
		RemainingTurns:    1,
		ActionProposal:    session.ActionProposal{ID: "aprop-lease-approve", Summary: "Approve lease", ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
		ContinuationLease: session.ContinuationLease{ID: "lease-approve", ProposalID: "aprop-lease-approve", Status: session.ContinuationLeaseStatusPending, MaxTurns: 1, RemainingTurns: 1, ExpiresAt: expiresAt, PlanHash: "sha256:lease"},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8107, 1002)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if state.ActionProposal.Status != session.ProposalStatusApproved {
		t.Fatalf("proposal status = %q, want approved", state.ActionProposal.Status)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("lease status = %q, want active", state.ContinuationLease.Status)
	}
	if state.ContinuationLease.ApprovedBy != 1002 || state.ContinuationLease.ApprovedAt.IsZero() {
		t.Fatalf("lease approval = by %d at %v, want recorded approver", state.ContinuationLease.ApprovedBy, state.ContinuationLease.ApprovedAt)
	}
}

func TestHandleInboundTypedApprovalConsumesPendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8116, UserID: 0, Scope: telegramDMScopeRef(8116)}
	action := session.ActionProposal{
		ID:            "aprop-typed-approval",
		Summary:       "Run the approved typed continuation.",
		BoundedEffect: "Run one bounded follow-up and report evidence.",
		Status:        session.ProposalStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "typed-approval",
		Objective:         "Continue a pending plan.",
		StageSummary:      "Run the approved typed continuation.",
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: buildContinuationLease(action, 1, now),
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8116, SenderID: 1001, SenderName: "admin", Text: "approved", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for approved continuation")
	}
	if recorder.input.Msg.Origin != core.InboundOriginTurnAuthorization {
		t.Fatalf("origin = %q, want turn authorization", recorder.input.Msg.Origin)
	}
	if recorder.input.Msg.Text == "approved" || !strings.Contains(recorder.input.Msg.Text, "Next:\nRun the approved typed continuation") {
		t.Fatalf("continuation text = %q, want machine-authored approved step", recorder.input.Msg.Text)
	}
	for _, notWant := range []string{"approved_step:", "proposal_id:", "lease_id:", "risk_class:"} {
		if strings.Contains(recorder.input.Msg.Text, notWant) {
			t.Fatalf("continuation text = %q, did not want internal fragment %q", recorder.input.Msg.Text, notWant)
		}
	}
	if recorder.input.Actor.Role != principal.RoleAdmin {
		t.Fatalf("actor role = %q, want admin", recorder.input.Actor.Role)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ApprovedBy != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want idle state with consumed lease after typed approval", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationApproved) || !hasExecutionEvent(events, core.ExecutionEventContinuationConsumed) {
		t.Fatalf("events = %#v, want approved and consumed events", events)
	}
}

func TestHandleInboundRunTextRoutesThroughNormalTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8120, UserID: 0, Scope: telegramDMScopeRef(8120)}
	action := session.ActionProposal{
		ID:               "aprop-run-text-approved",
		Summary:          "Run one already approved continuation.",
		BoundedEffect:    "Use the stored approved lease and report evidence.",
		RiskClass:        "continuation",
		AllowedActions:   []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "run-text-approved",
		Objective:      "Run the existing approved continuation.",
		StageSummary:   "Run one already approved continuation.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-run-text-approved",
			ProposalID:       action.ID,
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			ApprovedBy:       1001,
			AllowedActions:   action.AllowedActions,
			ForbiddenActions: action.ForbiddenActions,
			ExpiresAt:        now.Add(time.Hour),
			PlanHash:         action.PlanHash,
			ApprovedAt:       now,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8120, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || result.Text != "continued" {
		t.Fatalf("HandleInbound() result = %#v, want normal turn result", result)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for plain text continue")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusApproved || got.RemainingTurns != 1 || got.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("continuation = %#v, want approved continuation preserved for button-backed execution", got)
	}
}

func TestHandleInboundRunTextDoesNotShortCircuitExpiredContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	expiredAt := now.Add(-time.Minute)
	key := session.SessionKey{ChatID: 8125, UserID: 0, Scope: telegramDMScopeRef(8125)}
	action := session.ActionProposal{
		ID:               "aprop-run-text-expired",
		Summary:          "Run an expired approved continuation.",
		BoundedEffect:    "Should not run after expiry.",
		RiskClass:        "continuation",
		AllowedActions:   []string{"continue_one_turn"},
		ForbiddenActions: []string{"expand_authority_without_new_approval"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        expiredAt,
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now.Add(-time.Hour),
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "run-text-expired",
		Objective:      "Do not run expired continuation.",
		StageSummary:   "Expired approved continuation.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-run-text-expired",
			ProposalID:       action.ID,
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			ApprovedBy:       1001,
			AllowedActions:   action.AllowedActions,
			ForbiddenActions: action.ForbiddenActions,
			ExpiresAt:        expiredAt,
			PlanHash:         action.PlanHash,
			ApprovedAt:       now.Add(-time.Hour),
			CreatedAt:        now.Add(-time.Hour),
			UpdatedAt:        now.Add(-time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8125, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || result.Text != "continued" {
		t.Fatalf("HandleInbound() result = %#v, want normal turn result", result)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for plain text continue")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusApproved || got.RemainingTurns != 1 || got.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("continuation = %#v, want expired approved continuation left for explicit control path", got)
	}
}

func TestApprovedContinuationRunIntentDoesNotApprovePendingOrNegatedState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8124, UserID: 0, Scope: telegramDMScopeRef(8124)}
	action := session.ActionProposal{
		ID:            "aprop-run-text-pending",
		Summary:       "Pending continuation.",
		BoundedEffect: "Wait for explicit approval.",
		Status:        session.ProposalStatusPending,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	pending := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "run-text-pending",
		Objective:      "Wait for approval.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-run-text-pending",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := store.UpdateContinuationState(key, pending); err != nil {
		t.Fatalf("UpdateContinuationState(pending) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	handled, _, err := rt.maybeHandleApprovedContinuationRunIntent(context.Background(), core.InboundMessage{
		ChatID: 8124, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 1,
	}, actor)
	if err != nil {
		t.Fatalf("maybeHandleApprovedContinuationRunIntent(pending) err = %v", err)
	}
	if handled {
		t.Fatal("pending continuation handled as approved run intent")
	}

	approved := pending
	approved.Status = session.ContinuationStatusApproved
	approved.ApprovedBy = 1001
	approved.ActionProposal.Status = session.ProposalStatusApproved
	approved.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	approved.ContinuationLease.ApprovedBy = 1001
	approved.ContinuationLease.ApprovedAt = now
	if err := store.UpdateContinuationState(key, approved); err != nil {
		t.Fatalf("UpdateContinuationState(approved) err = %v", err)
	}
	handled, _, err = rt.maybeHandleApprovedContinuationRunIntent(context.Background(), core.InboundMessage{
		ChatID: 8124, SenderID: 1001, SenderName: "admin", Text: "don't continue", MessageID: 2,
	}, actor)
	if err != nil {
		t.Fatalf("maybeHandleApprovedContinuationRunIntent(negated) err = %v", err)
	}
	if handled {
		t.Fatal("negated continuation run text was handled")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusApproved || got.RemainingTurns != 1 || got.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("continuation = %#v, want approved state preserved after negated text", got)
	}
}

func TestPositiveReactionSurfacesPendingContinuationApprovalCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8125, UserID: 0, Scope: telegramDMScopeRef(8125)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-reaction-surfaces-approval",
		Objective: "Surface the pending approval card.",
		Status:    session.OperationStatusBlocked,
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		CausalRefs:         []string{"test:positive-reaction"},
		NextAction:         "show the approval card for one bounded child wake",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "idolum-email", "reaction-surfaces-child-wake"),
		OperatorProjection: "Approve one bounded child wake.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}

	handled, result, err := rt.maybeHandleApprovedContinuationRunIntent(context.Background(), core.InboundMessage{
		ChatID:     8125,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "reaction_update message_id=24769 new=👍",
		MessageID:  3,
		Reaction:   &core.InboundReaction{MessageID: 24769, New: []string{"👍"}},
	}, principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if err != nil {
		t.Fatalf("maybeHandleApprovedContinuationRunIntent(reaction) err = %v", err)
	}
	if !handled {
		t.Fatal("reaction handled = false, want pending approval card surfaced")
	}
	if result == nil || !strings.Contains(result.Text, "surfaced the pending continuation approval") {
		t.Fatalf("result = %#v, want surfaced approval acknowledgement", result)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	if !strings.Contains(inlineText, "idolum-email") {
		t.Fatalf("inline text = %q, want idolum-email approval card", inlineText)
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if pending.Status != session.ContinuationStatusPending || pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("pending continuation = %#v, want pending child_wake continuation", pending)
	}
}

func TestDeliveryTypedApprovalRequestSurfacesCardAndSuppressesProse(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.interpretationReplyText = fakeApprovalRequestInterpretationReply()
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8126, UserID: 0, Scope: telegramDMScopeRef(8126)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-delivery-surfaces-approval",
		Objective: "Surface the pending approval card from delivery.",
		Status:    session.OperationStatusBlocked,
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", "grant-idolum-email-wake", "durable_agent", "wake_once", ""),
		CausalRefs:         []string{"test:delivery-approval-request"},
		NextAction:         "show the approval card for one bounded child wake",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "ask_for_grant",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "idolum-email", "delivery-surfaces-child-wake"),
		OperatorProjection: "Approve one bounded child wake.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	prose := "I’m ready.\n\nSend this exact approval:\n\nI approve one child-local wake for idolum-email."
	port := &turnDeliveryPort{
		runtime:        rt,
		key:            key,
		sess:           sess,
		msg:            core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, SenderName: "admin", Text: "try again", MessageID: 1},
		deliver:        true,
		recordOutbound: true,
		audit:          newTurnAuditRecorder(key, "telegram", string(principal.RoleAdmin), "try again"),
	}

	result := &turn.Result{VisibleReply: prose, Turn: &core.TurnResult{Text: prose}}
	if _, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: key.ChatID, Text: prose},
		Result:  result,
	}); err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	sentCount := len(sender.sent)
	sentText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	if sentCount > 0 {
		sentText = sender.sent[sentCount-1].Text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "idolum-email") {
		t.Fatalf("inline count/text = %d/%q, want one idolum-email approval card", inlineCount, inlineText)
	}
	if sentText != "I surfaced the approval card." {
		t.Fatalf("sent text = %q, want prose suppressed after card materialization", sentText)
	}
	if strings.Contains(result.VisibleReply, "Send this exact approval") || strings.Contains(result.Turn.Text, "Send this exact approval") {
		t.Fatalf("result = %#v, want exact approval prose suppressed", result)
	}
}

func TestRetryTextMaterializesFreshChildWakeApprovalFromRepairBlocker(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8128, UserID: 0, Scope: telegramDMScopeRef(8128)}
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", grant.GrantID, "durable_agent", "wake_once", "")
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-live-child-wake-runtime-repair",
		Key:                key,
		Owner:              "approved_retry",
		State:              session.NextActionBlockedNeedsResourceRepair,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         subjectRef,
		CausalRefs:         []string{"continuation:lease-consumed-child-wake"},
		NextAction:         "inspect active or terminal child task ownership for the claimed batch, then retry only after the lease state is repaired",
		RequiredAuthority:  session.NextActionOperationKindDurableChildRecovery,
		ResourceBlocker:    "child_task_attempt_claim_failed",
		RetryPolicy:        "retry_after_child_task_lease_repair",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: `{"action":"repair_child_wake_failure","agent_id":"idolum-email","failure_class":"child_task_attempt_claim_failed","request_instance_id":"lease-request-instance-live","recovery_contract":"aphelion.recovery_handoff.v1","recovery_operation_kind":"durable_child_recovery","recovery_family":"durable_child_recovery","recovery_action":"repair_child_wake_failure"}`,
		OperatorProjection: "The approved child wake retry ran with authority but did not produce a child completion.",
		CreatedAt:          time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("RecordNextAction(durable_child_recovery) err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8128,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Retry the idolum-email wake once through the approved continuation path. Stop after the first result or typed blocker.",
		MessageID:  55,
	})
	if err != nil {
		t.Fatalf("HandleInbound(retry child_wake repair) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh approval materialization acknowledgement", result)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval for idolum-email", state)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	for _, want := range []string{"idolum-email", "wake only idolum-email once", "up to 1 turn"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == "next-live-child-wake-runtime-repair" {
			t.Fatalf("open actions = %#v, want resource repair blocker resolved after fresh approval prompt", open)
		}
		if action.OperationTool == "request_approval" && action.OperationKind == "continuation_lease_request" {
			t.Fatalf("open actions = %#v, want generated authority handoff resolved after materialization", open)
		}
	}
}

func TestRetryTextMaterializesFreshChildWakeApprovalFromChildSessionTransientBlocker(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	parentKey := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	childKey := rt.durableAgentExecutionKey("idolum-email")
	now := time.Now().UTC().Add(-time.Minute)
	resultInput := session.ChildTaskResultInput{
		PacketID:     "child_task:transient-timeout",
		ResultID:     "child_result:transient-timeout",
		Status:       session.ChildTaskResultBlocked,
		BlockerKind:  "external_transient",
		ResultKind:   "blocker",
		NextState:    session.NextActionScheduledRetry,
		Summary:      "The read-only unread job/opportunity search is timing out before it returns mailbox results.",
		CreatedAt:    now,
		FencingToken: "fence-transient-timeout",
	}
	classification := durableWakeChildBlockerClassification{
		Kind:               "external_transient",
		State:              session.NextActionScheduledRetry,
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		DiagnosticOnly:     true,
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-child-session-external-transient",
		Key:                childKey,
		Owner:              "durable_wake",
		State:              session.NextActionScheduledRetry,
		SubjectKind:        "task_packet",
		SubjectRef:         resultInput.PacketID,
		CausalRefs:         []string{"task_packet:" + resultInput.PacketID, "child_task_result:" + resultInput.ResultID},
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: durableWakeChildBlockerOperationInputJSON("idolum-email", "mail_cli", "mail_cli", classification, resultInput),
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(child external_transient) err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "retry idolum-email wake now after the transient timeout",
		MessageID:  71,
	})
	if err != nil {
		t.Fatalf("HandleInbound(retry child-session external transient) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh child_wake approval prompt acknowledgement", result)
	}
	state, err := store.ContinuationState(parentKey)
	if err != nil {
		t.Fatalf("ContinuationState(parent) err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" ||
		len(state.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		state.ContinuationLease.RequiredCapabilityGrants[0].GrantID == "" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval bound to active wake grant", state)
	}
	retry := session.NormalizeContinuationRetryOperation(state.ContinuationLease.RetryOperation)
	if !retry.Active() || retry.Tool != "durable_agent" || retry.OperationKind != "durable_agent_wake_once" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) {
		t.Fatalf("retry operation = %#v, want durable_agent wake_once retry for idolum-email", retry)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one approval card", len(sender.inline))
	}
	childOpen, err := store.OpenNextActionsBySession(childKey, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(child) err = %v", err)
	}
	for _, action := range childOpen {
		if action.RecordID == "next-child-session-external-transient" {
			t.Fatalf("child open actions = %#v, want source child-session transient action resolved after retry approval materialization", childOpen)
		}
	}
	if _, err := rt.ApproveContinuationForKey(parentKey, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(parent retry) err = %v", err)
	}
	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       1001,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    72,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(approved transient retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(approved transient retry) result = %#v, want approved continuation acknowledgement", result)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake after approving transient retry", runner.calls)
	}

	resultInput.ResultID = "child_result:transient-timeout-reaction"
	resultInput.PacketID = "child_task:transient-timeout-reaction"
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-child-session-external-transient-reaction",
		Key:                childKey,
		Owner:              "durable_wake",
		State:              session.NextActionScheduledRetry,
		SubjectKind:        "task_packet",
		SubjectRef:         resultInput.PacketID,
		CausalRefs:         []string{"task_packet:" + resultInput.PacketID, "child_task_result:" + resultInput.ResultID},
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: durableWakeChildBlockerOperationInputJSON("idolum-email", "mail_cli", "mail_cli", classification, resultInput),
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		CreatedAt:          time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordNextAction(child external_transient reaction) err = %v", err)
	}
	handled, reactionResult, err := rt.maybeHandleApprovedContinuationRunIntent(context.Background(), core.InboundMessage{
		ChatID:     1001,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "reaction_update message_id=25061 new=👍",
		MessageID:  73,
		Reaction:   &core.InboundReaction{MessageID: 25061, New: []string{"👍"}},
	}, principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	if err != nil {
		t.Fatalf("maybeHandleApprovedContinuationRunIntent(reaction transient retry) err = %v", err)
	}
	if !handled {
		t.Fatal("reaction handled = false, want transient retry approval card surfaced")
	}
	if reactionResult == nil || !strings.Contains(reactionResult.Text, "fresh bounded child_wake approval") {
		t.Fatalf("reaction result = %#v, want fresh child_wake approval prompt acknowledgement", reactionResult)
	}
	if len(sender.inline) != 2 {
		t.Fatalf("inline count after reaction = %d, want second approval card", len(sender.inline))
	}
}

func TestReviewEventRetryButtonMaterializesFreshChildWakeApproval(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	parentKey := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	childKey := rt.durableAgentExecutionKey("idolum-email")
	now := time.Now().UTC().Add(-time.Minute)
	resultInput := session.ChildTaskResultInput{
		PacketID:     "child_task:review-button-transient",
		ResultID:     "child_result:review-button-transient",
		Status:       session.ChildTaskResultBlocked,
		BlockerKind:  "external_transient",
		ResultKind:   "blocker",
		NextState:    session.NextActionScheduledRetry,
		Summary:      "The read-only child task timed out before returning results.",
		CreatedAt:    now,
		FencingToken: "fence-review-button-transient",
	}
	classification := durableWakeChildBlockerClassification{
		Kind:               "external_transient",
		State:              session.NextActionScheduledRetry,
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		DiagnosticOnly:     true,
	}
	nextAction, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-child-session-external-transient-review-button",
		Key:                childKey,
		Owner:              "durable_wake",
		State:              session.NextActionScheduledRetry,
		SubjectKind:        "task_packet",
		SubjectRef:         resultInput.PacketID,
		CausalRefs:         []string{"task_packet:" + resultInput.PacketID, "child_task_result:" + resultInput.ResultID},
		NextAction:         "wait for the bounded retry window before retrying the child task",
		ResourceBlocker:    "external_transient",
		RetryPolicy:        "bounded_backoff",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: durableWakeChildBlockerOperationInputJSON("idolum-email", "mail_cli", "mail_cli", classification, resultInput),
		OperatorProjection: "Child task hit a transient external blocker; retry only after bounded backoff.",
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordNextAction(child external_transient) err = %v", err)
	}
	event := session.ReviewEvent{
		ID:                693,
		SourceSessionID:   "durable_agent:idolum-email",
		SourceRole:        "durable_agent",
		TargetSessionID:   "telegram_dm:1001",
		TargetAdminChatID: 1001,
		Status:            "delivered",
		CreatedAt:         now,
		DeliveredAt:       now,
		MetadataJSON: `{
			"agent_id":"idolum-email",
			"summary":"Idolum Email stopped on external_transient.",
			"risk_flags":["durable_child","external_transient"],
			"metadata":{
				"child_blocker_kind":"external_transient",
				"child_next_state":"scheduled_retry",
				"child_task_packet_id":"child_task:review-button-transient",
				"next_action_record_id":"next-child-session-external-transient-review-button",
				"retry_policy":"bounded_backoff"
			}
		}`,
	}
	text, err := rt.HandleReviewEventAction(context.Background(), telegram.CallbackQuery{
		ID:      "cb-review-child-retry",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 25068, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionChildWakeRetry),
	}, event, core.ReviewEventActionChildWakeRetry)
	if err != nil {
		t.Fatalf("HandleReviewEventAction(child retry) err = %v", err)
	}
	if !strings.Contains(text, "approval surfaced") {
		t.Fatalf("callback text = %q, want approval surfaced acknowledgement", text)
	}
	state, err := store.ContinuationState(parentKey)
	if err != nil {
		t.Fatalf("ContinuationState(parent) err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("continuation state = %#v, want pending idolum-email child_wake approval", state)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one approval card", len(sender.inline))
	}
	open, err := store.OpenNextActionsBySession(childKey, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(child) err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == nextAction.RecordID {
			t.Fatalf("child open actions = %#v, want source retry action resolved after callback materializes approval", open)
		}
	}
}

func TestContinueTextMaterializesChildWakeApprovalFromChildToolRuntimeRepairBlocker(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8129, UserID: 0, Scope: telegramDMScopeRef(8129)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", grant.GrantID, "durable_agent", "wake_once", "")
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-live-child-tool-runtime-repair",
		Key:                key,
		Owner:              "approved_retry",
		State:              session.NextActionBlockedNeedsResourceRepair,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         subjectRef,
		CausalRefs:         []string{"child_task_result:child-result-runtime-missing"},
		NextAction:         "repair the child-local tool runtime, then run one no-content readiness probe",
		ResourceBlocker:    "tool_runtime_not_executable",
		RetryPolicy:        "retry_after_tool_runtime_repair",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: `{"merge":true,"status":"blocked","stage":"durable_child_blocker","summary":"Child-local tool runtime is missing or not executable; repair materialization, then run one no-content readiness probe.","recovery_contract":"aphelion.recovery_handoff.v1","recovery_operation_kind":"durable_child_recovery","recovery_family":"durable_child_recovery","recovery_action":"tool_runtime_not_executable","durable_agent_id":"idolum-email","child_blocker_kind":"tool_runtime_not_executable","diagnostic_only":true,"no_content_probe":true,"tool":"gog_cli"}`,
		OperatorProjection: "Child-local tool runtime is missing or not executable; repair the wrapper/materialization, then run one no-content readiness probe.",
		CreatedAt:          time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("RecordNextAction(durable_child_recovery) err = %v", err)
	}

	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "diagnostic routed"}}
	rt.interactiveDMAssembler = recorder
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8129,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Inspect the idolum-email child-local runtime materialization blocker. Focus only on why runtime-bin/gog and runtime-bin/gog_cli are present parent-side but not executable/visible inside the child wake sandbox.",
		MessageID:  55,
	})
	if err != nil {
		t.Fatalf("HandleInbound(diagnostic durable_child_recovery) err = %v", err)
	}
	if result == nil || result.Text != "diagnostic routed" {
		t.Fatalf("HandleInbound(diagnostic durable_child_recovery) result = %#v, want normal turn result", result)
	}
	if !recorder.called {
		t.Fatal("diagnostic durable_child_recovery prompt did not reach the normal turn assembler")
	}
	if len(sender.inline) != 0 {
		t.Fatalf("inline count after diagnostic prompt = %d, want no approval card", len(sender.inline))
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after diagnostic prompt) err = %v", err)
	}
	foundRepairAction := false
	for _, action := range open {
		if action.RecordID == "next-live-child-tool-runtime-repair" {
			foundRepairAction = true
			break
		}
	}
	if !foundRepairAction {
		t.Fatalf("open actions after diagnostic prompt = %#v, want durable_child_recovery blocker still open", open)
	}

	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8129,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Continue the idolum-email repair from the current durable child recovery blocker. Stop after the next typed blocker.",
		MessageID:  56,
	})
	if err != nil {
		t.Fatalf("HandleInbound(continue durable_child_recovery) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh child_wake approval prompt acknowledgement", result)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval for idolum-email", state)
	}
	retry := session.NormalizeContinuationRetryOperation(state.ContinuationLease.RetryOperation)
	if !retry.Active() || retry.Tool != "durable_agent" || retry.OperationKind != "durable_agent_wake_once" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) {
		t.Fatalf("retry operation = %#v, want durable_agent wake_once retry for idolum-email", retry)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one approval card", len(sender.inline))
	}
	open, err = store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == "next-live-child-tool-runtime-repair" {
			t.Fatalf("open actions = %#v, want durable_child_recovery blocker resolved after approval materialization", open)
		}
		if action.OperationTool == "request_approval" && action.OperationKind == "continuation_lease_request" {
			t.Fatalf("open actions = %#v, want generated child_wake handoff resolved after materialization", open)
		}
	}

	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(durable_child_recovery retry) err = %v", err)
	}
	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       8129,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    57,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(approved durable_child_recovery retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(approved durable_child_recovery retry) result = %#v, want approved continuation acknowledgement", result)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake after approving repair retry", runner.calls)
	}
	consumed, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(consumed retry) err = %v", err)
	}
	if consumed.Status != session.ContinuationStatusIdle ||
		consumed.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed ||
		consumed.ContinuationLease.RetryOperation.Tool != "durable_agent" {
		t.Fatalf("consumed continuation = %#v, want consumed child_wake retry lease after wake execution", consumed)
	}
}

func buildCredentialProbeRetryFixture(t *testing.T, chatID int64, recordID string) (*Runtime, *session.SQLiteStore, *runtimeWakeRunner, *fakeSender, session.SessionKey) {
	t.Helper()
	cfg, store, _, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "idolum-email", grant.GrantID, "durable_agent", "wake_once", "")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "idolum-email-gog-smoke-test",
		Objective: "Materialize a fresh bounded child_wake approval card for idolum-email; do not wake yet.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		Summary:   "Button-backed approval requested: Fresh bounded child_wake approval for idolum-email.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "idolum-email-finish-child-setup-less-ceremony",
			Goal:           "Recover idolum-email readiness.",
			CurrentPhaseID: "idolum-email-fresh-child-wake-approval-after-runtime-repair",
			Phases: []session.OperationPhase{{
				ID:               "idolum-email-fresh-child-wake-approval-after-runtime-repair",
				Summary:          "Fresh bounded child_wake approval for idolum-email after runtime-bin materialization checks.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "child_wake",
				GateLevel:        "escalated_operator_approval",
				GateReasonCode:   "capability_grant",
				WhyNow:           "The operator requested a fresh approval card only.",
				BoundedEffect:    "This phase only sends a fresh approval card and then stops. If approved later, it permits exactly one approved-continuation child_wake for idolum-email.",
				AllowedActions:   []string{"materialize fresh child_wake approval card for idolum-email", "prepare_capability_request", "capability_access_check"},
				ForbiddenActions: []string{"direct durable_agent.wake_once", "retry wake_once"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(live-shaped phase) err = %v", err)
	}
	resultInput := session.ChildTaskResultInput{
		PacketID:     "child_task:credential-probe-packet",
		ResultID:     "child_result:credential-probe-result",
		Status:       session.ChildTaskResultBlocked,
		BlockerKind:  "credential_unverified",
		ResultKind:   "credential_status_probe",
		NextState:    session.NextActionWaitingForOperator,
		Summary:      "Credential state is not proven; run a no-content status probe before any mailbox action.",
		CreatedAt:    time.Now().UTC().Add(-time.Minute),
		FencingToken: "fence-credential-probe",
	}
	classification := durableWakeChildBlockerClassification{
		Kind:               "credential_unverified",
		State:              session.NextActionWaitingForOperator,
		NextAction:         "run or review a no-content credential/account-status probe before mailbox work",
		RequiredAuthority:  "credential_status_probe",
		ResourceBlocker:    "credential_unverified",
		RetryPolicy:        "retry_after_credential_verification",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperatorProjection: "Credential state is not proven; run a no-content status probe before any mailbox action.",
		NoContentProbe:     true,
		DiagnosticOnly:     true,
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "approved_retry",
		State:              session.NextActionWaitingForOperator,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         subjectRef,
		CausalRefs:         []string{"child_task_result:" + resultInput.ResultID},
		NextAction:         "run or review a no-content credential/account-status probe before mailbox work",
		RequiredAuthority:  "credential_status_probe",
		ResourceBlocker:    "credential_unverified",
		RetryPolicy:        "retry_after_credential_verification",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: durableWakeChildBlockerOperationInputJSON("idolum-email", "gog_cli", "gog_cli", classification, resultInput),
		OperatorProjection: "Credential state is not proven; run a no-content status probe before any mailbox action.",
		CreatedAt:          time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("RecordNextAction(durable_child_recovery credential probe) err = %v", err)
	}
	return rt, store, runner, sender, key
}

func TestExplicitAdminTextMaterializesPendingChildWakeApprovalWithoutContinueKeyword(t *testing.T) {
	t.Parallel()

	rt, store, runner, _, key := buildCredentialProbeRetryFixture(t, 8131, "next-live-child-credential-probe-no-keyword")

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8131,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "retry wake approval for idolum-email after the no-content runtime repair",
		MessageID:  63,
	})
	if err != nil {
		t.Fatalf("HandleInbound(explicit credential probe retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh child_wake approval prompt acknowledgement", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want pending admin text to materialize approval without executing wake", runner.calls)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval for idolum-email", state)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationOffered, session.NextActionOperationKindDurableChildRecovery+"_retry") {
		t.Fatalf("events = %#v, want durable_child_recovery retry continuation offer", events)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventContinuationOffered, "operation_phase_plan") {
		t.Fatalf("events = %#v, did not want operation_phase_plan offer for typed child_wake repair", events)
	}
}

func TestExplicitAdminTextMaterializesPendingOperationPhaseApprovalWithoutContinueKeyword(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8132, UserID: 0, Scope: telegramDMScopeRef(8132)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "pending-phase-no-keyword",
		Objective: "Surface pending approval without requiring a keyword.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		Summary:   "A bounded phase needs operator approval.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "phase-plan-no-keyword",
			Goal:           "Run one bounded phase.",
			CurrentPhaseID: "phase-no-keyword",
			Phases: []session.OperationPhase{{
				ID:               "phase-no-keyword",
				Summary:          "Run one bounded diagnostic phase.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "child_wake",
				GateLevel:        "escalated_operator_approval",
				GateReasonCode:   "bounded_phase",
				RequiresApproval: true,
				WhyNow:           "The durable operation has a pending next phase.",
				BoundedEffect:    "Ask for approval before running one diagnostic phase.",
				AllowedActions:   []string{"wake_named_child_once", "report_evidence"},
				ForbiddenActions: []string{"external_mutation", "unbounded_retry"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(pending phase) err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8132,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "surface the pending approval card",
		MessageID:  64,
	})
	if err != nil {
		t.Fatalf("HandleInbound(explicit pending phase approval surface) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "pending continuation approval") {
		t.Fatalf("HandleInbound result = %#v, want pending approval acknowledgement", result)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending || state.DecisionID == "" {
		t.Fatalf("continuation state = %#v, want pending operation phase approval", state)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationOffered, "operation_plan_lease") {
		t.Fatalf("events = %#v, want operation_plan_lease continuation offer", events)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one approval card", len(sender.inline))
	}
}

func TestAdminTextDoesNotExecuteApprovedRetryWithoutAuthorizationEvent(t *testing.T) {
	t.Parallel()

	rt, store, runner, _, key := buildCredentialProbeRetryFixture(t, 8133, "next-live-child-credential-probe-approved-no-keyword")
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "normal diagnostic response"}}
	rt.interactiveDMAssembler = recorder

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8133,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "retry wake approval for idolum-email after the no-content runtime repair",
		MessageID:  65,
	})
	if err != nil {
		t.Fatalf("HandleInbound(materialize explicit credential probe retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh child_wake approval prompt acknowledgement", result)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(credential probe retry) err = %v", err)
	}
	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     8133,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "what is pending now?",
		MessageID:  66,
	})
	if err != nil {
		t.Fatalf("HandleInbound(unrelated text after approval) err = %v", err)
	}
	if result == nil || result.Text != "normal diagnostic response" {
		t.Fatalf("HandleInbound(unrelated text after approval) result = %#v, want normal turn response", result)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for unrelated text after approval")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want unrelated admin text not to execute approved retry", runner.calls)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns != 1 {
		t.Fatalf("continuation state = %#v, want approved retry preserved for button-backed execution", state)
	}
}

func TestContinueTextMaterializesChildWakeApprovalFromCredentialProbeBlocker(t *testing.T) {
	t.Parallel()

	rt, store, runner, _, key := buildCredentialProbeRetryFixture(t, 8130, "next-live-child-credential-probe")

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       8130,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]\n\nApproved work: Plan: Fresh bounded child_wake approval for idolum-email after runtime-bin materialization checks.\nBudget: up to 1 turn\nNext: Fresh bounded child_wake approval for idolum-email after runtime-bin materialization checks.",
		MessageID:    61,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(turn_authorization credential probe) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "fresh bounded child_wake approval") {
		t.Fatalf("HandleInbound result = %#v, want fresh child_wake approval prompt acknowledgement", result)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval for idolum-email", state)
	}
	retry := session.NormalizeContinuationRetryOperation(state.ContinuationLease.RetryOperation)
	if !retry.Active() || retry.Tool != "durable_agent" || retry.OperationKind != "durable_agent_wake_once" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) {
		t.Fatalf("retry operation = %#v, want durable_agent wake_once retry for idolum-email", retry)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationOffered, session.NextActionOperationKindDurableChildRecovery+"_retry") {
		t.Fatalf("events = %#v, want durable_child_recovery retry continuation offer", events)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventContinuationOffered, "operation_phase_plan") {
		t.Fatalf("events = %#v, did not want operation_phase_plan offer for typed child_wake repair", events)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(credential probe retry) err = %v", err)
	}
	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       8130,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    62,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(approved turn_authorization credential probe retry) err = %v", err)
	}
	if result == nil {
		t.Fatalf("HandleInbound(approved turn_authorization credential probe retry) result = nil, want continuation result")
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake from credential probe retry", runner.calls)
	}
}

func TestDirectContinuationApprovalBindsExecutionRunAuthority(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &authorityRecordingTurnAssembler{runtime: rt, result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8117, UserID: 0, Scope: telegramDMScopeRef(8117)}
	action := session.ActionProposal{
		ID:               "aprop-direct-child-wake-authority",
		Summary:          "Wake idolum-email exactly once.",
		BoundedEffect:    "Permit durable_agent wake_once to wake only idolum-email once.",
		RiskClass:        "child_wake",
		AllowedActions:   []string{"wake_named_child"},
		ForbiddenActions: []string{"wake_unnamed_child", "unbounded_retry_loop"},
		Status:           session.ProposalStatusPending,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.ID = "lease-direct-child-wake-authority"
	lease.LeaseClass = session.ContinuationLeaseClassChildWake
	lease.Constraints = map[string]string{
		"agent_id":              "idolum-email",
		"tool":                  "durable_agent",
		"tool_action":           "wake_once",
		"grant_id":              "grant-idolum-email-direct-no-content-wake-readiness",
		"grant_target_resource": "durable_agent:idolum-email:wake_once",
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "direct-child-wake-authority",
		Objective:         "Run one approved child wake.",
		StageSummary:      "Wake idolum-email exactly once.",
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8117, SenderID: 1001, SenderName: "admin", Text: "approved", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called for approved continuation")
	}
	ref, ok := toolpkg.AuthorityUseRefFromContext(recorder.ctx)
	if !ok {
		t.Fatal("AuthorityUseRefFromContext() ok=false, want direct continuation turn to carry durable run authority")
	}
	if admission, ok := toolpkg.ExecutionAuthorityAdmissionFromContext(recorder.ctx); ok {
		t.Fatalf("ExecutionAuthorityAdmissionFromContext() = %#v, want raw admission consumed after run binding", admission)
	}
	if ref.SessionID != session.SessionIDForKey(key) || ref.TurnRunID == 0 || ref.ContinuationLeaseID != "" {
		t.Fatalf("authority ref = %#v, want only durable run identity for session %q", ref, session.SessionIDForKey(key))
	}
	if recorder.runID == 0 || ref.TurnRunID != recorder.runID {
		t.Fatalf("recorded run ID = %d, authority ref turn run ID = %d, want same direct continuation turn", recorder.runID, ref.TurnRunID)
	}
	authority, ok, err := store.ExecutionRunAuthority(recorder.runID)
	if err != nil {
		t.Fatalf("ExecutionRunAuthority() err = %v", err)
	}
	if !ok {
		t.Fatalf("ExecutionRunAuthority(%d) ok=false, want durable authority row for direct continuation turn", recorder.runID)
	}
	if authority.ContinuationLeaseID != "lease-direct-child-wake-authority" ||
		authority.ExecutionSpecies != "direct_continuation" ||
		authority.LeaseClass != session.ContinuationLeaseClassChildWake ||
		!actionListContains(authority.LeaseAllowedActions, "wake_named_child") ||
		authority.LeaseConstraints["agent_id"] != "idolum-email" {
		t.Fatalf("execution authority = %#v, want child_wake authority bound to idolum-email", authority)
	}
}

func TestDirectContinuationApprovalExecutesDurableAgentWakeOnceWithBoundAuthority(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &authorityRecordingTurnAssembler{
		runtime:   rt,
		result:    &core.TurnResult{Text: "continued"},
		toolName:  "durable_agent",
		toolInput: json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`),
	}
	rt.interactiveDMAssembler = recorder

	key := session.SessionKey{ChatID: 8119, UserID: 0, Scope: telegramDMScopeRef(8119)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")

	now := time.Now().UTC()
	action := session.ActionProposal{
		ID:               "aprop-direct-child-wake-tool-execution",
		Summary:          "Wake idolum-email exactly once.",
		BoundedEffect:    "Permit durable_agent wake_once to wake only idolum-email once.",
		RiskClass:        "child_wake",
		AllowedActions:   []string{"wake_named_child"},
		ForbiddenActions: []string{"wake_unnamed_child", "unbounded_retry_loop"},
		Status:           session.ProposalStatusPending,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.ID = "lease-direct-child-wake-tool-execution"
	lease.LeaseClass = session.ContinuationLeaseClassChildWake
	lease.Constraints = map[string]string{
		"agent_id":              "idolum-email",
		"tool":                  "durable_agent",
		"tool_action":           "wake_once",
		"grant_id":              "grant-idolum-email-wake-once",
		"grant_target_resource": "durable_agent:idolum-email:wake_once",
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "direct-child-wake-tool-execution",
		Objective:         "Run one approved child wake.",
		StageSummary:      "Wake idolum-email exactly once.",
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID: 8119, SenderID: 1001, SenderName: "admin", Text: "approved", MessageID: 1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if recorder.toolErr != nil {
		t.Fatalf("durable_agent wake_once tool err = %v", recorder.toolErr)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one direct idolum-email wake", runner.calls)
	}
	if !strings.Contains(recorder.toolOutput, "wake_status: awaiting_child_pickup") && !strings.Contains(recorder.toolOutput, "wake_status: completed") {
		t.Fatalf("wake_once output = %q, want concrete wake status", recorder.toolOutput)
	}
	authority, ok, err := store.ExecutionRunAuthority(recorder.runID)
	if err != nil {
		t.Fatalf("ExecutionRunAuthority() err = %v", err)
	}
	if !ok || authority.ExecutionSpecies != "direct_continuation" || authority.ContinuationLeaseID != "lease-direct-child-wake-tool-execution" {
		t.Fatalf("execution authority = %#v ok=%v, want direct_continuation authority for wake lease", authority, ok)
	}
}

func TestDirectContinuationAuthorityAdmissionRequiresActiveLease(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8118, UserID: 0, Scope: telegramDMScopeRef(8118)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	proposal := session.ActionProposal{
		ID:               "aprop-direct-admission-active",
		Summary:          "Wake one child.",
		AllowedActions:   []string{"wake_named_child"},
		ForbiddenActions: []string{"wake_unnamed_child"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	proposal.PlanHash = actionProposalHash(proposal)
	lease := buildContinuationLease(proposal, 1, now)
	lease.ID = "lease-direct-admission-active"
	lease.Status = session.ContinuationLeaseStatusActive
	lease.LeaseClass = session.ContinuationLeaseClassChildWake
	lease.Constraints = map[string]string{"agent_id": "idolum-email"}
	state := session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		RemainingTurns:    1,
		ActionProposal:    proposal,
		ContinuationLease: lease,
	}

	admission, ok := directContinuationAuthorityAdmission(key, actor, state, now)
	if !ok {
		t.Fatal("directContinuationAuthorityAdmission() ok=false, want active lease admission")
	}
	if admission.ContinuationLeaseID != lease.ID || admission.ExecutionSpecies != "direct_continuation" || admission.LeaseConstraints["agent_id"] != "idolum-email" {
		t.Fatalf("admission = %#v, want direct continuation child_wake snapshot", admission)
	}

	expired := state
	expired.ContinuationLease.ExpiresAt = now.Add(-time.Minute)
	if admission, ok := directContinuationAuthorityAdmission(key, actor, expired, now); ok {
		t.Fatalf("expired admission = %#v, want no admission", admission)
	}

	consumed := state
	consumed.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	consumed.ContinuationLease.RemainingTurns = 0
	if admission, ok := directContinuationAuthorityAdmission(key, actor, consumed, now); ok {
		t.Fatalf("consumed admission = %#v, want no admission", admission)
	}
}

type authorityRecordingTurnAssembler struct {
	mu         sync.Mutex
	runtime    *Runtime
	called     bool
	ctx        context.Context
	runID      int64
	result     *core.TurnResult
	err        error
	toolName   string
	toolInput  json.RawMessage
	toolOutput string
	toolErr    error
}

func (a *authorityRecordingTurnAssembler) Run(ctx context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error) {
	result, err := a.RunTurn(ctx, input)
	if result == nil {
		return nil, err
	}
	return result.Turn, err
}

func (a *authorityRecordingTurnAssembler) RunTurn(ctx context.Context, input interactiveDMTurnAssemblyInput) (*turn.Result, error) {
	monitor, err := a.runtime.startTurnMonitor(ctx, input.Key, session.TurnRunKindInteractive, input.Msg.Text, nil, nil, input.Msg)
	if err != nil {
		return nil, err
	}
	monitorErr := error(nil)
	turnCtx := monitor.Context()
	defer monitor.Finish(turnCtx, monitorErr)

	a.mu.Lock()
	a.called = true
	a.ctx = turnCtx
	a.runID = monitor.runID
	a.mu.Unlock()

	if strings.TrimSpace(a.toolName) != "" {
		a.toolOutput, a.toolErr = input.Tools.Execute(turnCtx, a.toolName, a.toolInput)
		if a.toolErr != nil {
			monitorErr = a.toolErr
			return nil, a.toolErr
		}
	}
	if a.err != nil {
		monitorErr = a.err
		return nil, a.err
	}
	result := a.result
	if result == nil {
		result = &core.TurnResult{}
	}
	return &turn.Result{Turn: result, VisibleReply: strings.TrimSpace(result.Text)}, nil
}

func TestTriggerContinuationLoopsWhileApprovedLeaseHasTurns(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8121, UserID: 0, Scope: telegramDMScopeRef(8121)}
	action := session.ActionProposal{
		ID:               "aprop-loop-approved",
		Summary:          "Run the next approved continuation turn.",
		BoundedEffect:    "Use only the active approved lease and report evidence.",
		RiskClass:        "continuation",
		AllowedActions:   []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        now.Add(time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "loop-approved",
		Objective:      "Finish all approved continuation turns.",
		StageSummary:   "Run approved follow-up work.",
		RemainingTurns: 3,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-loop-approved",
			ProposalID:       action.ID,
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         3,
			RemainingTurns:   3,
			ApprovedBy:       1001,
			AllowedActions:   action.AllowedActions,
			ForbiddenActions: action.ForbiddenActions,
			ExpiresAt:        now.Add(time.Hour),
			PlanHash:         action.PlanHash,
			ApprovedAt:       now,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if recorder.callCount != 3 {
		t.Fatalf("assembler calls = %d, want all 3 approved turns consumed", recorder.callCount)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want consumed idle continuation", got)
	}
	sender.mu.Lock()
	progressCount := len(sender.sent)
	sender.mu.Unlock()
	if progressCount != 2 {
		t.Fatalf("progress messages = %d, want one compact progress line before each automatic follow-up turn", progressCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 3 {
		t.Fatalf("consumed events = %d, want 3", got)
	}
	if got := countEventsByType(events, core.ExecutionEventMissionProgressAssessed); got != 3 {
		t.Fatalf("mission progress assessments = %d, want 3", got)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationBoundaryReached) {
		t.Fatalf("events = %#v, want continuation boundary event after loop exhausts", events)
	}
}

func TestConcurrentContinuationReservationsConsumeSingleLeaseTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8122, UserID: 0, Scope: telegramDMScopeRef(8122)}
	action := session.ActionProposal{
		ID:            "aprop-concurrent-reservation",
		Summary:       "Run one reserved continuation turn.",
		BoundedEffect: "Consume exactly one approved turn.",
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "concurrent-reservation",
		Objective:      "Prove one-turn leases cannot be double-spent.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-concurrent-reservation",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	type result struct {
		reserved bool
		repair   bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reservation, _, repair, err := rt.reserveApprovedContinuationTurn(key)
			results <- result{reserved: reservation != nil, repair: repair != nil, err: err}
		}()
	}
	wg.Wait()
	close(results)

	reserved := 0
	for got := range results {
		if got.err != nil {
			t.Fatalf("reserveApprovedContinuationTurn() err = %v", got.err)
		}
		if got.repair {
			t.Fatal("reserveApprovedContinuationTurn() repair = true, want no lease repair")
		}
		if got.reserved {
			reserved++
		}
	}
	if reserved != 1 {
		t.Fatalf("reserved turns = %d, want exactly one reservation for one approved turn", reserved)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want single consumed idle lease", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want 1", got)
	}
}

func TestConcurrentTriggerContinuationExecutesSingleLeaseTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8123, UserID: 0, Scope: telegramDMScopeRef(8123)}
	action := session.ActionProposal{
		ID:            "aprop-concurrent-trigger",
		Summary:       "Run one public trigger continuation turn.",
		BoundedEffect: "Execute exactly one approved turn.",
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "concurrent-trigger",
		Objective:      "Prove concurrent triggers cannot execute more than the lease allows.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-concurrent-trigger",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rt.TriggerContinuationForKey(context.Background(), key)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("TriggerContinuationForKey() err = %v", err)
		}
	}
	if got := recorder.CallCount(); got != 1 {
		t.Fatalf("assembler calls = %d, want exactly one executed continuation turn", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want 1", got)
	}
}

func TestTriggerContinuationReplayAfterConsumedLeaseIsNoop(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued once"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8124, UserID: 0, Scope: telegramDMScopeRef(8124)}
	action := session.ActionProposal{
		ID:            "aprop-replay-consumed",
		Summary:       "Run one replay-safe continuation.",
		BoundedEffect: "Execute exactly one approved turn even if the card is pressed twice.",
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "replay-consumed",
		Objective:      "Prove a stale approval replay is a clean no-op.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-replay-consumed",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey(first) err = %v", err)
	}
	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey(replay) err = %v, want stale replay no-op", err)
	}
	if got := recorder.CallCount(); got != 1 {
		t.Fatalf("assembler calls = %d, want exactly one executed continuation turn", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want one consumed event after replay", got)
	}
}

func TestApproveContinuationReturnsTypedExpiredErrorAndRecordsBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8109, UserID: 0, Scope: telegramDMScopeRef(8109)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-expired-approval",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-expired-approval",
			Summary:   "Expired approval",
			Status:    session.ProposalStatusPending,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-expired-approval",
			ProposalID:     "aprop-expired-approval",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiredAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	state, err := rt.ApproveContinuation(8109, 1002)
	if !errors.Is(err, core.ErrContinuationExpired) {
		t.Fatalf("ApproveContinuation() err = %v, want ErrContinuationExpired", err)
	}
	if state.Status != session.ContinuationStatusIdle || state.RemainingTurns != 0 {
		t.Fatalf("state status/turns = %q/%d, want idle/0", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.Status != session.ProposalStatusExpired || state.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("state proposal/lease status = %q/%q, want expired/expired", state.ActionProposal.Status, state.ContinuationLease.Status)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ActionProposal.Status != session.ProposalStatusExpired || got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("persisted continuation = %#v, want expired idle state", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationBlocked) {
		t.Fatalf("events = %#v, want continuation blocked event", events)
	}
}

func TestRefreshContinuationProposalCreatesFreshLeaseForSameBoundedAction(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 8110, UserID: 0, Scope: telegramDMScopeRef(8110)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Finish the bounded local patch.",
		StageSummary:   "Patch and test the callback flow.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-old-expired",
			OperationID:      "op-refresh-v1",
			Summary:          "Refresh the expired lease",
			WhyNow:           "The previous prompt expired.",
			BoundedEffect:    "Patch only continuation callback refresh behavior.",
			RiskClass:        "system_change",
			AllowedActions:   []string{"patch_code", "run_tests"},
			ForbiddenActions: []string{"deploy", "restart"},
			ValidationPlan:   []string{"go test ./..."},
			Status:           session.ProposalStatusExpired,
			ExpiresAt:        expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-old-expired",
			ProposalID:     "aprop-old-expired",
			Status:         session.ContinuationLeaseStatusExpired,
			MaxTurns:       1,
			RemainingTurns: 0,
			ExpiresAt:      expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 8110, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh inline prompt")
	}
	if state.Status != session.ContinuationStatusPending || state.RemainingTurns != 1 {
		t.Fatalf("state status/turns = %q/%d, want pending/1", state.Status, state.RemainingTurns)
	}
	if state.ActionProposal.ID == prior.ActionProposal.ID || state.ContinuationLease.ID == prior.ContinuationLease.ID {
		t.Fatalf("fresh ids reused old proposal/lease: proposal=%q lease=%q", state.ActionProposal.ID, state.ContinuationLease.ID)
	}
	if state.ActionProposal.OperationID != prior.ActionProposal.OperationID || state.ActionProposal.BoundedEffect != prior.ActionProposal.BoundedEffect {
		t.Fatalf("fresh proposal = %#v, want same operation and bounded effect", state.ActionProposal)
	}
	if state.ActionProposal.Status != session.ProposalStatusPending || !state.ActionProposal.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("fresh proposal status/expires = %q/%v, want pending future expiry", state.ActionProposal.Status, state.ActionProposal.ExpiresAt)
	}
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusPending || state.ContinuationLease.ProposalID != state.ActionProposal.ID {
		t.Fatalf("fresh lease = %#v, want pending lease tied to fresh proposal", state.ContinuationLease)
	}

	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.ID != state.ActionProposal.ID || got.Status != session.ContinuationStatusPending {
		t.Fatalf("persisted state = %#v, want fresh pending state", got)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want one fresh prompt", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Patch only continuation callback refresh behavior") {
		t.Fatalf("inline text = %q, want refreshed bounded effect", sender.inline[0].text)
	}
	oldCallback := core.EncodeContinuationCallbackData(prior.ActionProposal.ID, "approve_lease")
	newCallback := sender.inline[0].rows[0][0].CallbackData
	if newCallback == "" || newCallback == oldCallback {
		t.Fatalf("fresh callback = %q old = %q, want distinct non-empty callback", newCallback, oldCallback)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want continuation offered event", events)
	}
}

func TestRefreshContinuationProposalSanitizesLiveNegatedDeployAuthority(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	key := session.SessionKey{ChatID: 81100, UserID: 0, Scope: telegramDMScopeRef(81100)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusIdle,
		Objective:      "Diagnose and recover the blocked mail-child credentials without mailbox access.",
		StageSummary:   "Repair child-scoped mailbox adapter credential materialization, then run only a non-mailbox auth-status smoke.",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-live-corrupt",
			Summary:   "Repair child-scoped mailbox adapter credential materialization",
			RiskClass: "credential_recovery",
			BoundedEffect: "May create or adjust child-scoped mailbox adapter credential materialization, wrapper/env, or grant contract. " +
				"No mailbox content/label/inbox/message query, no OAuth, no account mutation, no public/external contact, no email actions, no deploy/restart unless separately approved.",
			AllowedActions: []string{
				"create_child_scoped_mailbox_adapter_materialization_if_approved",
				"copy_or_bind_existing_host_mailbox_credentials_without_printing_values",
				"adjust_child_mailbox_adapter_wrapper_or_grant_contract_if_needed",
				"run_child_sandbox_external_account_auth_status_only",
				"report_repair_evidence",
				"deploy",
				"prepare_release_handoff",
			},
			ForbiddenActions: []string{
				"read_or_print_secret_values",
				"run_mailbox_adapter_query",
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
			Status:    session.ProposalStatusExpired,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-live-corrupt",
			ProposalID:       "aprop-live-corrupt",
			Status:           session.ContinuationLeaseStatusExpired,
			MaxTurns:         1,
			RemainingTurns:   0,
			LeaseClass:       session.ContinuationLeaseClassDeployRestart,
			AllowedActions:   []string{"deploy", "prepare_release_handoff"},
			ForbiddenActions: []string{"deploy", "restart"},
			ExpiresAt:        expiredAt,
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	state, sent, err := rt.RefreshContinuationProposal(context.Background(), 81100, "expired approval callback")
	if err != nil {
		t.Fatalf("RefreshContinuationProposal() err = %v", err)
	}
	if !sent {
		t.Fatal("sent = false, want fresh prompt")
	}
	if actionListContains(state.ActionProposal.AllowedActions, "deploy") ||
		actionListContains(state.ContinuationLease.AllowedActions, "deploy") ||
		state.ContinuationLease.LeaseClass == session.ContinuationLeaseClassDeployRestart {
		t.Fatalf("refreshed state = %#v, want deploy authority stripped from negated credential recovery", state)
	}
	if !actionListContains(state.ActionProposal.ForbiddenActions, "deploy") || !actionListContains(state.ActionProposal.ForbiddenActions, "restart") {
		t.Fatalf("forbidden actions = %#v, want deploy/restart preserved", state.ActionProposal.ForbiddenActions)
	}
}

func TestTriggerContinuationExpiresStaleLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8108, UserID: 0, Scope: telegramDMScopeRef(8108)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "decision-expired-lease",
		RemainingTurns:    1,
		ApprovedBy:        1002,
		ActionProposal:    session.ActionProposal{ID: "aprop-expired-lease", Summary: "Expired lease", Status: session.ProposalStatusApproved, ExpiresAt: expiredAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-expired", ProposalID: "aprop-expired-lease", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1002, ExpiresAt: expiredAt},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8108); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("lease status = %q, want expired", got.ContinuationLease.Status)
	}
	if got.ActionProposal.Status != session.ProposalStatusExpired {
		t.Fatalf("proposal status = %q, want expired", got.ActionProposal.Status)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 {
		t.Fatalf("continuation state = %q/%d, want idle/0", got.Status, got.RemainingTurns)
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

func TestTriggerSandboxedOrganicProposalContinuationDowngradesAdminToApprovedUserSandbox(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8104, UserID: 0, Scope: telegramDMScopeRef(8104)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "organic-proposal-sandbox",
		Objective:      "Run one Organic proposal system-change step.",
		StageSummary:   "Patch one local file and report evidence.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-organic-proposal-sandbox",
			Summary:        "Patch one local file",
			RiskClass:      "system_change",
			AllowedActions: []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:organic-proposal-sandbox",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-organic-proposal-sandbox",
			ProposalID:       "aprop-organic-proposal-sandbox",
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{organicProposalSandboxAction, organicProposalSandboxWriteBoundary},
			ForbiddenActions: []string{"deploy"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:organic-proposal-sandbox",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8104); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Actor.Role != principal.RoleApprovedUser {
		t.Fatalf("actor role = %q, want approved_user sandbox execution", recorder.input.Actor.Role)
	}
	if recorder.input.Actor.TelegramUserID != 1001 {
		t.Fatalf("actor user id = %d, want admin approver preserved", recorder.input.Actor.TelegramUserID)
	}
	if recorder.input.Scope.Profile.Mode != sandbox.ModeIsolated || recorder.input.Scope.Profile.Network != sandbox.NetworkDeny {
		t.Fatalf("scope profile = %#v, want isolated network-deny approved_user sandbox", recorder.input.Scope.Profile)
	}
	if !strings.Contains(recorder.input.Scope.WorkingRoot, "isolated/workspaces/1001") {
		t.Fatalf("working root = %q, want admin approver isolated user workspace", recorder.input.Scope.WorkingRoot)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var consumed session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationConsumed {
			consumed = event
		}
	}
	if consumed.ID == 0 {
		t.Fatalf("events = %#v, want continuation consumed event", events)
	}
	payload := executionEventPayload(consumed.PayloadJSON)
	if payloadString(payload, "execution_principal_role") != string(principal.RoleApprovedUser) {
		t.Fatalf("execution role payload = %q, want approved_user", payloadString(payload, "execution_principal_role"))
	}
	if payloadString(payload, "sandbox_profile") != organicProposalSandboxProfile || payloadString(payload, "sandboxed_from_role") != string(principal.RoleAdmin) {
		t.Fatalf("sandbox payload = profile %q from %q, want approved_user_isolated from admin", payloadString(payload, "sandbox_profile"), payloadString(payload, "sandboxed_from_role"))
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
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		StageSummary:   "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-phase-4b-rebundled-email-proof",
			OperationID:   "phase-4b-rebundled-email-proof",
			Summary:       "Bundled Phase 4B: one bounded mail-child read-only adapter proof",
			BoundedEffect: "Inspect current email due/backoff state, run at most one bounded read-only proof, then report.",
			RiskClass:     "status_check",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-phase-4b-rebundled-email-proof",
			ProposalID:     "aprop-phase-4b-rebundled-email-proof",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			AllowedActions: []string{string(WorkModeReadOnly)},
			MaxTurns:       1,
			RemainingTurns: 1,
		},
	}); err != nil {
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
		t.Fatalf("last role = %q, want provider user-role input", last.Role)
	}
	for _, want := range []string{
		approvedContinuationEventText,
		"Approved work:",
		"Next:\nBundled Phase 4B: one bounded mail-child read-only adapter proof",
		"Scope:\nInspect current email due/backoff state, run at most one bounded read-only proof, then report.",
	} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("last content = %q, want substring %q", last.Content, want)
		}
	}
	for _, notWant := range []string{"proposal_id:", "operation_id:", "lease_id:", "risk_class:", "aprop-", "lease-phase-4b"} {
		if strings.Contains(last.Content, notWant) {
			t.Fatalf("last content = %q, did not want internal fragment %q", last.Content, notWant)
		}
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
