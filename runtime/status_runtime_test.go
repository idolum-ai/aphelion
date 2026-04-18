//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestStatusDiagnosticsIncludesLatestTurnAndContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8111, UserID: 0, Scope: telegramDMScopeRef(8111)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "check status diagnostics")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"curl https://api.github.com/zen"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(8111)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"Latest persisted turn",
		"running",
		"interactive",
		"Last tool: exec.",
		"Continuation: pending",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("StatusDiagnostics() = %q, want substring %q", text, needle)
		}
	}
}

func TestStatusDiagnosticsReturnsEmptyWithoutSessionHistory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(9222)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("StatusDiagnostics() len = %d, want 0", len(lines))
	}

	state, exists, err := store.ContinuationStateIfExists(session.SessionKey{
		ChatID: 9222,
		UserID: 0,
		Scope:  telegramDMScopeRef(9222),
	})
	if err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("ContinuationStateIfExists() = %#v, exists=%v; want no row after status probe", state, exists)
	}
}

func TestIsTelegramAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if !rt.IsTelegramAdmin(1001) {
		t.Fatal("IsTelegramAdmin(1001) = false, want true")
	}
	if rt.IsTelegramAdmin(1002) {
		t.Fatal("IsTelegramAdmin(1002) = true, want false for non-admin approved user")
	}
	if rt.IsTelegramAdmin(0) {
		t.Fatal("IsTelegramAdmin(0) = true, want false")
	}
}
