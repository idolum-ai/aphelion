//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

func TestStartupRecoverySendsAwakeSignalWhenNoInterruptedRuns(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := store.UpsertMission(session.MissionState{
		ID:        "mission-awake-candidate",
		Title:     "Awake candidate",
		Objective: "Keep restart awake signals honest.",
		Scope:     "principal",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusCandidate,
	}, "test", "create"); err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	startedAt := time.Date(2026, time.May, 1, 14, 29, 56, 0, time.UTC)
	if err := rt.runStartupRecoveryOnce(context.Background(), startedAt); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1 awake signal", len(sender.sent))
	}
	got := sender.sent[0]
	if got.ChatID != 1001 {
		t.Fatalf("awake chat id = %d, want admin 1001", got.ChatID)
	}
	for _, needle := range []string{
		"Restart awake signal.",
		"started_at_utc: 2026-05-01T14:29:56Z",
		"startup_recovery: no interrupted turns pending",
		"mission_control: candidates=1 active=0 pending_handoffs=0",
		"next: no auto-resume",
	} {
		if !strings.Contains(got.Text, needle) {
			t.Fatalf("awake text = %q, want substring %q", got.Text, needle)
		}
	}
}

func TestStartupRecoverySendsAdminCatchupMessage(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Cannot write the maintenance ledger from this session. Append:\n\n```text\n[2026-04-10] run_id=90 recovery\n  Recovered: inspect the interrupted turn before resuming.\n```"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1500, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "resume semantic substrate implementation")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"go test ./provider"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) == 0 {
		t.Fatal("no startup recovery catch-up message was sent")
	}
	got := sender.sent[len(sender.sent)-1]
	if got.ChatID != 1001 {
		t.Fatalf("catch-up chat id = %d, want 1001", got.ChatID)
	}
	if !strings.Contains(got.Text, "Restart catch-up.") {
		t.Fatalf("catch-up text = %q, want restart heading", got.Text)
	}
	if !strings.Contains(got.Text, "resume semantic substrate implementation") {
		t.Fatalf("catch-up text = %q, want interrupted request", got.Text)
	}
	if strings.Contains(got.Text, "Cannot write the maintenance ledger") || strings.Contains(got.Text, "```") || strings.Contains(got.Text, "run_id=90") {
		t.Fatalf("catch-up text = %q, want sanitized operator-facing summary", got.Text)
	}
	if !strings.Contains(got.Text, "Recovered: inspect the interrupted turn before resuming.") {
		t.Fatalf("catch-up text = %q, want sanitized recovery summary", got.Text)
	}
}

func TestStartupRecoverySuppressesNoToolsLedgerDisclaimer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.Join([]string{
		"Cannot write the maintenance ledger from this recovery context because no tools are available.",
		"",
		"Concise note to record: run 91 completed all recorded tool calls and scheduled a finalizer.",
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1502, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "reinstall the service")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"systemctl --user restart aphelion"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) == 0 {
		t.Fatal("no startup recovery catch-up message was sent")
	}
	got := sender.sent[len(sender.sent)-1].Text
	if strings.Contains(got, "Cannot write the maintenance ledger") {
		t.Fatalf("catch-up text = %q, want ledger disclaimer suppressed", got)
	}
	if !strings.Contains(got, "Concise note to record") {
		t.Fatalf("catch-up text = %q, want retained recovery note", got)
	}
	storedRun, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if strings.Contains(storedRun.RecoverySummary, "Cannot write the maintenance ledger") {
		t.Fatalf("stored recovery summary = %q, want ledger disclaimer suppressed", storedRun.RecoverySummary)
	}
}

func TestStartupRecoveryLogsMaintenanceAnalysis(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Recovered: rerun the interrupted inspection if still needed."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1500, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "study the codebase")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg aphelion"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 55); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 9, 20, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()})
	if err != nil {
		t.Fatalf("Load(maintenance) err = %v", err)
	}
	if maintenance.LastFloorText != provider.replyText {
		t.Fatalf("maintenance floor = %q, want %q", maintenance.LastFloorText, provider.replyText)
	}
	if len(maintenance.Messages) != 2 || maintenance.Messages[0].Role != "user" || maintenance.Messages[1].Role != "assistant" {
		t.Fatalf("maintenance messages = %#v, want synthetic user + assistant", maintenance.Messages)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending recovery runs = %d, want 0", len(pending))
	}

	storedRun, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if storedRun.RecoverySummary != provider.replyText {
		t.Fatalf("recovery summary = %q, want %q", storedRun.RecoverySummary, provider.replyText)
	}
}

func TestStartupRecoveryFlushesInterruptedChatMemory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Aggressive.Enabled = true
	cfg.Memory.Aggressive.FlushOnSessionBoundary = true
	provider.replyText = "Recovered: restart recovery complete."
	provider.memoryFlushReplyText = "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n- Interrupted restart work should be preserved in durable memory.\n[/KNOWLEDGE]\n[DECISIONS]\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	chatID := int64(1501)
	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     chatID,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "remember that interrupted restarts must preserve memory",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	if _, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "resume interrupted restart work"); err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	proposals, err := memstore.ListProposals(memstore.ProposalListOptions{Root: cfg.Agent.PromptRoot})
	if err != nil {
		t.Fatalf("ListProposals() err = %v", err)
	}
	found := false
	for _, proposal := range proposals {
		if proposal.Store == memstore.StoreKnowledge && strings.Contains(proposal.Content, "Interrupted restart work") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("proposals = %#v, want startup recovery memory proposal", proposals)
	}
}
