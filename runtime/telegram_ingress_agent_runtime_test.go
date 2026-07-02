//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestAgentFuncCompletesQueuedIngressHandledBeforeTurnMonitor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.interactiveDMAssembler = &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "handled before monitor"}}

	now := time.Date(2026, time.June, 29, 16, 2, 42, 0, time.UTC)
	msg := core.InboundMessage{
		ChatID:          1601,
		ChatType:        "private",
		SenderID:        1001,
		SenderName:      "admin",
		Text:            "approval-style fast path",
		MessageID:       704,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 505,
		IngressQueuedAt: now,
	}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     msg.IngressSurface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  "message",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   session.SessionIDForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}),
		Status:      session.TelegramIngressUpdateQueued,
		InboundJSON: mustMarshalInboundMessageForTest(t, msg),
		PayloadJSON: `{"update_id":505}`,
		AcceptedAt:  now.Add(-time.Second),
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	r := router.NewRouter(rt.AgentFunc())
	r.SetEventHandler(rt.RouterEventHandler())
	r.Route(context.Background(), msg)

	record, ok, err := store.TelegramIngressUpdate(msg.IngressSurface, msg.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateCompleted || record.TurnRunID != 0 || record.CompletedAt.IsZero() {
		t.Fatalf("ingress record = %#v, want completed without turn run", record)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending ingress = %#v, want selected fast path terminalized", pending)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventIngressSelected) {
		t.Fatalf("events = %#v, want ingress.selected before fast-path completion", events)
	}
}

func TestAgentFuncRoutesQueuedIngressAfterExpiredAuthorityBundleCleanup(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "ordinary turn reply"
	provider.faceReplyText = "ordinary turn reply"
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
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Date(2026, time.July, 2, 14, 17, 35, 0, time.UTC)
	msg := core.InboundMessage{
		ChatID:          1602,
		ChatType:        "private",
		SenderID:        1001,
		SenderName:      "admin",
		Text:            "I approve this Aphelion PR #282 local-work step: inspect tool/capability.go, patch request_review approved to call existing grant materialization with request-derived defaults, rerun the three-test matrix, then stop.",
		MessageID:       25383,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 385541286,
		IngressQueuedAt: now,
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "copilotkit-codebase-study",
		Status:  session.OperationStatusActive,
		Stage:   "execution",
		Summary: "Continue the active local-work operation.",
		Proposal: session.OperationProposal{
			ID:      "admin-exact-exec-live",
			Kind:    "admin_unbounded_exact_exec",
			Summary: "Approve one exact admin shell command",
			Status:  session.ProposalStatusApproved,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "copilotkit-codebase-study-plan",
			Goal:           "Continue local work.",
			CurrentPhaseID: "aphelion-pr-282-local-fix-tests-v1",
			Phases: []session.OperationPhase{{
				ID:               "aphelion-pr-282-local-fix-tests-v1",
				Summary:          "Finish local fix and run targeted tests.",
				Status:           session.PlanStatusInProgress,
				AuthorityClass:   "workspace_write",
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	expiredRecordID := seedExpiredAuthorityBundleHandoff(t, store, key, "router-expired-before-ordinary-message", now.Add(-2*time.Hour))
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     msg.IngressSurface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  "message",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   session.SessionIDForKey(key),
		Status:      session.TelegramIngressUpdateQueued,
		InboundJSON: mustMarshalInboundMessageForTest(t, msg),
		PayloadJSON: `{"update_id":385541286}`,
		AcceptedAt:  now.Add(-time.Second),
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	r := router.NewRouter(rt.AgentFunc())
	r.SetEventHandler(rt.RouterEventHandler())
	r.Route(context.Background(), msg)

	record, ok, err := store.TelegramIngressUpdate(msg.IngressSurface, msg.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateCompleted || record.TurnRunID == 0 || record.CompletedAt.IsZero() {
		t.Fatalf("ingress record = %#v, want completed with real turn run after stale cleanup", record)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == expiredRecordID {
			t.Fatalf("open actions = %#v, want expired authority-bundle handoff resolved", open)
		}
	}
	sender.mu.Lock()
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if len(sent) == 0 {
		t.Fatal("no outbound messages sent; stale cleanup must not make the user message disappear")
	}
}
