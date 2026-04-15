//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

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
