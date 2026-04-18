//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
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

func TestChatStatusSnapshotAggregatesRouterStoreAndPendingSignals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7001, UserID: 0, Scope: telegramDMScopeRef(7001)}
	running, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "run diagnostics")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(running.ID, "exec", `{"command":"curl https://api.github.com/zen"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 2,
		DecisionID:     "continuation-1",
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-1",
		Sequence:      1,
		OwnerKey:      "chat:7001:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        7001,
		SenderID:      1002,
		MessageID:     500,
		Prompt:        "Approve this proposal?",
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		TimeoutNanos:  int64(10 * time.Second),
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
		UpdatedAt:     time.Now().UTC().Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}
	recovery, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "recover me")
	if err != nil {
		t.Fatalf("BeginTurnRun(recovery) err = %v", err)
	}
	if err := store.CompleteTurnRun(recovery.ID, session.TurnRunStatusInterrupted, "process restart"); err != nil {
		t.Fatalf("CompleteTurnRun(interrupted) err = %v", err)
	}

	rt.staleTurnThreshold = time.Second
	rt.staleTurnSweep = func(cutoff time.Time, limit int) ([]session.TurnRun, error) {
		_ = cutoff
		_ = limit
		return []session.TurnRun{{
			ID:             99,
			ChatID:         7001,
			Kind:           session.TurnRunKindInteractive,
			Status:         session.TurnRunStatusRunning,
			LastActivityAt: time.Now().UTC().Add(-5 * time.Minute),
			LastToolName:   "exec",
		}}, nil
	}
	rt.staleWatchdogTriggered.Store(true)

	snapshot, err := rt.ChatStatusSnapshot(7001, core.RouterStatusSnapshot{
		ActiveTurnsByChat: map[int64][]uint64{7001: {11}},
		QueueDepthByChat:  map[int64]int{7001: 3},
	})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.ChatID != 7001 {
		t.Fatalf("ChatID = %d, want 7001", snapshot.ChatID)
	}
	if got := snapshot.QueueDepth; got != 3 {
		t.Fatalf("QueueDepth = %d, want 3", got)
	}
	if got := len(snapshot.ActiveTurnIDs); got != 1 || snapshot.ActiveTurnIDs[0] != 11 {
		t.Fatalf("ActiveTurnIDs = %#v, want [11]", snapshot.ActiveTurnIDs)
	}
	if snapshot.Continuation == nil || snapshot.Continuation.Status != string(session.ContinuationStatusPending) {
		t.Fatalf("Continuation = %#v, want pending continuation", snapshot.Continuation)
	}
	if snapshot.LatestTurnRun == nil {
		t.Fatal("LatestTurnRun = nil, want latest run data")
	}
	if !snapshot.RestartHealth.WatchdogTriggered {
		t.Fatalf("RestartHealth = %#v, want watchdog triggered", snapshot.RestartHealth)
	}
	if got := len(snapshot.StaleRunningTurns); got != 1 {
		t.Fatalf("StaleRunningTurns len = %d, want 1", got)
	}
	kinds := make([]core.PendingItemKind, 0, len(snapshot.PendingItems))
	staleDecisionSeen := false
	for _, item := range snapshot.PendingItems {
		kinds = append(kinds, item.Kind)
		if item.Kind == core.PendingItemKindDecision && item.Stale {
			staleDecisionSeen = true
		}
	}
	for _, want := range []core.PendingItemKind{
		core.PendingItemKindQueue,
		core.PendingItemKindDecision,
		core.PendingItemKindContinuation,
		core.PendingItemKindRecovery,
		core.PendingItemKindStaleTurn,
	} {
		if !containsPendingKind(kinds, want) {
			t.Fatalf("PendingItems kinds = %#v, want %q present", kinds, want)
		}
	}
	if !staleDecisionSeen {
		t.Fatalf("PendingItems = %#v, want stale decision visibility", snapshot.PendingItems)
	}
}

func TestSystemStatusSnapshotBuildsAdminViewAndHotChats(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	keyA := session.SessionKey{ChatID: 8101, UserID: 0, Scope: telegramDMScopeRef(8101)}
	keyB := session.SessionKey{ChatID: 8102, UserID: 0, Scope: telegramDMScopeRef(8102)}
	runA, err := store.BeginTurnRun(keyA, session.TurnRunKindInteractive, "chat A")
	if err != nil {
		t.Fatalf("BeginTurnRun(chat A) err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(runA.ID, "exec", `{"command":"echo a"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart(chat A) err = %v", err)
	}
	runB, err := store.BeginTurnRun(keyB, session.TurnRunKindInteractive, "chat B")
	if err != nil {
		t.Fatalf("BeginTurnRun(chat B) err = %v", err)
	}
	if err := store.CompleteTurnRun(runB.ID, session.TurnRunStatusFailed, "tool error"); err != nil {
		t.Fatalf("CompleteTurnRun(chat B) err = %v", err)
	}
	if err := store.UpdateContinuationState(keyB, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		DecisionID:     "cont-b",
		ApprovedBy:     1001,
	}); err != nil {
		t.Fatalf("UpdateContinuationState(chat B) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-b",
		Sequence:      2,
		OwnerKey:      "chat:8102:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        8102,
		SenderID:      1002,
		MessageID:     910,
		Prompt:        "Approve?",
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		CreatedAt:     time.Now().UTC().Add(-20 * time.Second),
		UpdatedAt:     time.Now().UTC().Add(-20 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(chat B) err = %v", err)
	}
	rt.staleTurnThreshold = time.Second
	rt.staleTurnSweep = func(cutoff time.Time, limit int) ([]session.TurnRun, error) {
		_ = cutoff
		_ = limit
		return []session.TurnRun{{
			ID:             301,
			ChatID:         8101,
			Kind:           session.TurnRunKindInteractive,
			Status:         session.TurnRunStatusRunning,
			LastActivityAt: time.Now().UTC().Add(-4 * time.Minute),
		}}, nil
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{
		ActiveTurnsByChat: map[int64][]uint64{
			8101: {41},
		},
		QueueDepthByChat: map[int64]int{
			8102: 2,
		},
	})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}

	if got := snapshot.ActiveTurnCount; got != 1 {
		t.Fatalf("ActiveTurnCount = %d, want 1", got)
	}
	if got := snapshot.QueueDepthByChat[8102]; got != 2 {
		t.Fatalf("QueueDepthByChat[8102] = %d, want 2", got)
	}
	if _, ok := snapshot.LatestTurnRunsByChat[8101]; !ok {
		t.Fatalf("LatestTurnRunsByChat missing chat 8101: %#v", snapshot.LatestTurnRunsByChat)
	}
	if _, ok := snapshot.LatestTurnRunsByChat[8102]; !ok {
		t.Fatalf("LatestTurnRunsByChat missing chat 8102: %#v", snapshot.LatestTurnRunsByChat)
	}
	if got := len(snapshot.HotChats); got == 0 {
		t.Fatal("HotChats is empty, want ranked chat summaries")
	}
	if got := len(snapshot.StaleRunningTurns); got != 1 {
		t.Fatalf("StaleRunningTurns len = %d, want 1", got)
	}
	if len(snapshot.Continuations) == 0 {
		t.Fatalf("Continuations = %#v, want approved continuation", snapshot.Continuations)
	}
}

func containsPendingKind(items []core.PendingItemKind, target core.PendingItemKind) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
