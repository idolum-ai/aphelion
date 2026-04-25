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

func TestStatusDiagnosticsPrefersTurnProjectionFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9333, UserID: 0, Scope: telegramDMScopeRef(9333)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "legacy failed row")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusFailed, "legacy failure"); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"turn_kind":"interactive","request_text":"event timeline"}`,
		CreatedAt:   now.Add(time.Second),
	}, {
		EventType:   core.ExecutionEventTurnCompleted,
		Stage:       "turn",
		Status:      "completed",
		PayloadJSON: `{"turn_kind":"interactive","request_text":"event timeline"}`,
		CreatedAt:   now.Add(2 * time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(9333)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.ToLower(strings.Join(lines, "\n"))
	if !strings.Contains(text, "completed") {
		t.Fatalf("StatusDiagnostics() = %q, want completed state from TES projection", text)
	}
	if strings.Contains(text, "failed") {
		t.Fatalf("StatusDiagnostics() = %q, do not want stale failed state from legacy row", text)
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
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(session) err = %v", err)
	}
	sess.OperationState = session.OperationState{
		Status:  session.OperationStatusBlocked,
		Stage:   "approval_wait",
		Summary: "Waiting for admin review",
	}
	sess.PlanState = session.PlanState{
		Steps: []session.PlanStep{{
			Step:   "Await admin approval",
			Status: session.PlanStatusInProgress,
		}},
	}
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(session state) err = %v", err)
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
	if snapshot.OperationStatus != "blocked" {
		t.Fatalf("OperationStatus = %q, want blocked", snapshot.OperationStatus)
	}
	if snapshot.OperationStage != "approval_wait" {
		t.Fatalf("OperationStage = %q, want approval_wait", snapshot.OperationStage)
	}
	if snapshot.OperationSummary != "Waiting for admin review" {
		t.Fatalf("OperationSummary = %q, want waiting summary", snapshot.OperationSummary)
	}
	if snapshot.PlanStepStatus != "in_progress" {
		t.Fatalf("PlanStepStatus = %q, want in_progress", snapshot.PlanStepStatus)
	}
	if snapshot.PlanStep != "Await admin approval" {
		t.Fatalf("PlanStep = %q, want Await admin approval", snapshot.PlanStep)
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

func TestSystemStatusSnapshotPrefersOperationalPendingDecisionsOverTES(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-from-events",
		Sequence:      1,
		OwnerKey:      "chat:9001:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        9001,
		SenderID:      1002,
		MessageID:     111,
		Prompt:        "Approve from events?",
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(decision-from-events) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-legacy-only",
		Sequence:      2,
		OwnerKey:      "chat:9002:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        9002,
		SenderID:      1002,
		MessageID:     222,
		Prompt:        "Legacy only decision?",
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(decision-legacy-only) err = %v", err)
	}

	key := session.SessionKey{ChatID: 9001, UserID: 0, Scope: telegramDMScopeRef(9001)}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType: core.ExecutionEventDecisionOpened,
			Stage:     "decision",
			Status:    "pending",
			PayloadJSON: `{
				"decision_id":"decision-from-events",
				"decision_kind":"proposal_approval",
				"owner_key":"chat:9001:sender:1002",
				"prompt":"Approve from events?"
			}`,
			CreatedAt: now.Add(-80 * time.Second),
		},
		{
			EventType: core.ExecutionEventDecisionResolved,
			Stage:     "decision",
			Status:    "resolved",
			PayloadJSON: `{
				"decision_id":"decision-from-events",
				"decision_kind":"proposal_approval",
				"owner_key":"chat:9001:sender:1002",
				"choice":"deny",
				"reason":"callback"
			}`,
			CreatedAt: now.Add(-70 * time.Second),
		},
		{
			EventType: core.ExecutionEventDecisionOpened,
			Stage:     "decision",
			Status:    "pending",
			PayloadJSON: `{
					"decision_id":"decision-events-only",
					"decision_kind":"proposal_approval",
					"owner_key":"chat:9003:sender:1002",
					"prompt":"Events only decision?"
				}`,
			CreatedAt: now.Add(-50 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(decision events) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}

	if !pendingDecisionByID(snapshot.PendingItems, "decision-from-events") {
		t.Fatalf("PendingItems missing operational decision decision-from-events: %#v", snapshot.PendingItems)
	}
	if !pendingDecisionByID(snapshot.PendingItems, "decision-legacy-only") {
		t.Fatalf("PendingItems missing legacy fallback decision decision-legacy-only: %#v", snapshot.PendingItems)
	}
	if !pendingDecisionByID(snapshot.PendingItems, "decision-events-only") {
		t.Fatalf("PendingItems missing TES fallback decision decision-events-only: %#v", snapshot.PendingItems)
	}
}

func TestChatStatusSnapshotPrefersOperationalContinuationStateOverTES(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9011, UserID: 0, Scope: telegramDMScopeRef(9011)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 2,
		DecisionID:     "continuation-legacy",
		UpdatedAt:      time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpdateContinuationState(legacy pending) err = %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType: core.ExecutionEventContinuationOffered,
			Stage:     "continuation",
			Status:    "pending",
			PayloadJSON: `{
				"decision_id":"continuation-legacy",
				"remaining_turns":2
			}`,
			CreatedAt: now.Add(-90 * time.Second),
		},
		{
			EventType: core.ExecutionEventContinuationRevoked,
			Stage:     "continuation",
			Status:    "revoked",
			PayloadJSON: `{
				"decision_id":"continuation-legacy",
				"remaining_turns":0
			}`,
			CreatedAt: now.Add(-60 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(continuation events) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(9011, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.Continuation == nil {
		t.Fatalf("Continuation = nil, want operational continuation snapshot")
	}
	if snapshot.Continuation.Status != string(session.ContinuationStatusPending) {
		t.Fatalf("Continuation.Status = %q, want pending from operational state", snapshot.Continuation.Status)
	}
	if snapshot.Continuation.RemainingTurns != 2 {
		t.Fatalf("Continuation.RemainingTurns = %d, want 2 from operational state", snapshot.Continuation.RemainingTurns)
	}
	if pendingKindCount(snapshot.PendingItems, core.PendingItemKindContinuation) != 1 {
		t.Fatalf("Pending continuation item should stay visible from operational state: %#v", snapshot.PendingItems)
	}
}

func TestSystemStatusSnapshotIncludesPendingReviewQueueItems(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	for _, event := range []session.ReviewEvent{
		{
			SourceChatID:      101,
			SourceRole:        "approved_user",
			TargetAdminChatID: 9001,
			Summary:           "pending-review",
		},
		{
			SourceChatID:      102,
			SourceRole:        "approved_user",
			TargetAdminChatID: 9001,
			Summary:           "delivered-review",
			Status:            "delivered",
		},
	} {
		if err := store.EnqueueReviewEvent(event); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	found := false
	for _, item := range snapshot.PendingItems {
		if item.Kind != core.PendingItemKindReview {
			continue
		}
		found = true
		if strings.TrimSpace(item.SourceSurface) != "review_events.pending" {
			t.Fatalf("review pending SourceSurface = %q, want review_events.pending", item.SourceSurface)
		}
	}
	if !found {
		t.Fatalf("PendingItems missing pending review item: %#v", snapshot.PendingItems)
	}
}

func TestChatStatusSnapshotLatestTurnSourceMarkers(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9311, UserID: 0, Scope: telegramDMScopeRef(9311)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "fallback run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	fallbackSnapshot, err := rt.ChatStatusSnapshot(9311, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot(fallback) err = %v", err)
	}
	if fallbackSnapshot.LatestTurnRun == nil {
		t.Fatalf("LatestTurnRun = nil, want fallback turn run snapshot")
	}
	if got := strings.TrimSpace(fallbackSnapshot.LatestTurnRun.Source); got != "compatibility_fallback:turn_runs" {
		t.Fatalf("LatestTurnRun.Source = %q, want compatibility_fallback:turn_runs", got)
	}

	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":42,"run_kind":"interactive","request_text":"tes run"}`,
			CreatedAt:   time.Now().UTC().Add(-2 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"run_id":42}`,
			CreatedAt:   time.Now().UTC().Add(-time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	tesSnapshot, err := rt.ChatStatusSnapshot(9311, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot(tes) err = %v", err)
	}
	if tesSnapshot.LatestTurnRun == nil {
		t.Fatalf("LatestTurnRun = nil, want TES turn snapshot")
	}
	if got := strings.TrimSpace(tesSnapshot.LatestTurnRun.Source); got != "canonical:execution_events.turn" {
		t.Fatalf("LatestTurnRun.Source = %q, want canonical:execution_events.turn", got)
	}
}

func TestSystemStatusSnapshotDerivesRecoveryPendingFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType: core.ExecutionEventRecoveryDetected,
			Stage:     "recovery",
			Status:    "detected",
			PayloadJSON: `{
				"pending_count": 2
			}`,
			CreatedAt: now.Add(-2 * time.Minute),
		},
		{
			EventType: core.ExecutionEventRecoveryIssued,
			Stage:     "recovery",
			Status:    "issued",
			PayloadJSON: `{
				"pending_count": 2
			}`,
			CreatedAt: now.Add(-90 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(recovery pending) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if !pendingRecoveryByID(snapshot.PendingItems, "recovery:startup") {
		t.Fatalf("PendingItems missing TES-derived startup recovery item: %#v", snapshot.PendingItems)
	}

	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventRecoveryCompleted,
		Stage:     "recovery",
		Status:    "completed",
		PayloadJSON: `{
			"pending_count": 2,
			"recovered_count": 2
		}`,
		CreatedAt: now.Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(recovery completed) err = %v", err)
	}

	snapshot, err = rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot(after completion) err = %v", err)
	}
	if pendingRecoveryByID(snapshot.PendingItems, "recovery:startup") {
		t.Fatalf("PendingItems still contains startup recovery item after completion: %#v", snapshot.PendingItems)
	}
}

func TestChatStatusSnapshotIncludesRecentExecutionTimeline(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9021, UserID: 0, Scope: telegramDMScopeRef(9021)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventIngressAccepted,
			Stage:       "ingress",
			Status:      "accepted",
			PayloadJSON: `{"message_id":11}`,
			CreatedAt:   now.Add(-30 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"turn_kind":"interactive"}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"summary":"completed delivery"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(chat timeline) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(9021, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentExecution) != 3 {
		t.Fatalf("RecentExecution len = %d, want 3", len(snapshot.RecentExecution))
	}
	if snapshot.RecentExecution[0].EventType != core.ExecutionEventTurnCompleted {
		t.Fatalf("RecentExecution[0].EventType = %q, want %q", snapshot.RecentExecution[0].EventType, core.ExecutionEventTurnCompleted)
	}
	if snapshot.RecentExecution[1].EventType != core.ExecutionEventTurnStarted {
		t.Fatalf("RecentExecution[1].EventType = %q, want %q", snapshot.RecentExecution[1].EventType, core.ExecutionEventTurnStarted)
	}
}

func TestChatStatusSnapshotSummarizesToolInstallEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 90214, UserID: 0, Scope: telegramDMScopeRef(90214)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventToolInstallUpdated,
		Stage:       "tool_authority",
		Status:      "verified",
		PayloadJSON: `{"tool_name":"browse_page","status":"verified","probe_status":"passed","install_ref":"workspace:tooling-v1"}`,
		CreatedAt:   now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(tool install) err = %v", err)
	}
	snapshot, err := rt.ChatStatusSnapshot(90214, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentExecution) == 0 {
		t.Fatal("RecentExecution empty, want tool install event")
	}
	if snapshot.RecentExecution[0].Summary != "tool_name=browse_page status=verified probe_status=passed install_ref=workspace:tooling-v1" {
		t.Fatalf("RecentExecution[0].Summary = %q, want tool install summary", snapshot.RecentExecution[0].Summary)
	}
}

func TestChatStatusSnapshotIncludesCanonicalToolLifecycleState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	installedAt := time.Now().UTC().Add(-2 * time.Minute)
	lastProbedAt := time.Now().UTC().Add(-1 * time.Minute)
	auditedAt := time.Now().UTC()
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{
		ToolName:            "browse_page",
		Installer:           "aphelion",
		InstallRef:          "workspace:tooling-v3",
		Status:              session.ToolInstallStatusVerified,
		BaselineFingerprint: "sha256:baseline",
		CurrentFingerprint:  "sha256:current",
		InstalledAt:         installedAt,
		AttestedAt:          auditedAt,
	}); err != nil {
		t.Fatalf("UpsertToolInstallRecord() err = %v", err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{
		ToolName:    "browse_page",
		Status:      session.ToolProbeStatusPassed,
		ProbeOutput: "stdout: probe ok",
		ProbedAt:    lastProbedAt,
	}); err != nil {
		t.Fatalf("UpsertToolProbeRecord() err = %v", err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{
		ToolName:    "browse_page",
		Status:      session.ToolAuditStatusPassed,
		AuditOutput: "entry_path: /workspace/run.sh",
		AuditedAt:   auditedAt,
	}); err != nil {
		t.Fatalf("UpsertToolAuditRecord() err = %v", err)
	}
	snapshot, err := rt.ChatStatusSnapshot(90216, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.ToolLifecycle) != 1 {
		t.Fatalf("ToolLifecycle len = %d, want 1", len(snapshot.ToolLifecycle))
	}
	row := snapshot.ToolLifecycle[0]
	if row.ToolName != "browse_page" || row.InstallStatus != "verified" || row.ProbeStatus != "passed" || row.AuditStatus != "passed" {
		t.Fatalf("ToolLifecycle[0] = %#v, want browse_page verified/passed/passed", row)
	}
	if row.InstallRef != "workspace:tooling-v3" {
		t.Fatalf("ToolLifecycle[0].InstallRef = %q, want workspace:tooling-v3", row.InstallRef)
	}
	if row.BaselineFingerprint != "sha256:baseline" || row.CurrentFingerprint != "sha256:current" {
		t.Fatalf("ToolLifecycle[0] fingerprints = %q/%q, want persisted fingerprints", row.BaselineFingerprint, row.CurrentFingerprint)
	}
}

func TestChatStatusSnapshotIncludesCanonicalToolLifecycleTraceability(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	base := time.Now().UTC()
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{
		ToolName:     "browse_page",
		Installer:    "aphelion",
		InstallRef:   "workspace:tooling-v3",
		Status:       session.ToolInstallStatusInstalled,
		Rationale:    "install_execute ran the manifest install command",
		ArtifactRefs: []session.RecordReference{{Kind: "file_path", Ref: "/workspace/install.sh"}},
		UpdatedAt:    base.Add(-2 * time.Minute),
		InstalledAt:  base.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertToolInstallRecord() err = %v", err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{
		ToolName:     "browse_page",
		Status:       session.ToolAuditStatusPassed,
		AuditOutput:  "entry_path: /workspace/run.sh",
		Rationale:    "audit_run resolved the declared execution entry",
		ArtifactRefs: []session.RecordReference{{Kind: "file_path", Ref: "/workspace/run.sh"}},
		UpdatedAt:    base.Add(-1 * time.Minute),
		AuditedAt:    base.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertToolAuditRecord() err = %v", err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{
		ToolName:     "browse_page",
		Status:       session.ToolProbeStatusPassed,
		ProbeOutput:  "stdout: probe ok",
		Rationale:    "probe_run passed against the declared probe command",
		ArtifactRefs: []session.RecordReference{{Kind: "file_path", Ref: "/workspace/probe.sh"}},
		UpdatedAt:    base,
		ProbedAt:     base,
	}); err != nil {
		t.Fatalf("UpsertToolProbeRecord() err = %v", err)
	}
	snapshot, err := rt.ChatStatusSnapshot(90217, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.ToolLifecycle) != 1 {
		t.Fatalf("ToolLifecycle len = %d, want 1", len(snapshot.ToolLifecycle))
	}
	row := snapshot.ToolLifecycle[0]
	if row.TraceStage != "probe" || row.TraceSummary != "probe_run passed against the declared probe command" || row.TraceArtifactCount != 1 {
		t.Fatalf("ToolLifecycle[0] trace = %#v, want latest probe trace with one ref", row)
	}
}

func TestChatStatusSnapshotSummarizesToolAuthorityLifecycleEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 90215, UserID: 0, Scope: telegramDMScopeRef(90215)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType: core.ExecutionEventToolProposalReviewed,
			Stage:     "tool_authority",
			Status:    "approved",
			PayloadJSON: `{
				"proposal_id":"tp_123",
				"tool_name":"search_web",
				"review_status":"approved",
				"ratified_via":"decision_broker",
				"transition_reason":"approved"
			}`,
			CreatedAt: now.Add(-10 * time.Second),
		},
		{
			EventType: core.ExecutionEventToolRegistered,
			Stage:     "tool_authority",
			Status:    "enabled",
			PayloadJSON: `{
				"tool_name":"search_web",
				"registered":true,
				"implementation_ref":"tool/search_web.go"
			}`,
			CreatedAt: now.Add(-5 * time.Second),
		},
		{
			EventType: core.ExecutionEventToolExposureChanged,
			Stage:     "tool_authority",
			Status:    "enabled",
			PayloadJSON: `{
				"tool_name":"search_web",
				"principal":"idolum-email",
				"active":true
			}`,
			CreatedAt: now.Add(-2 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(tool authority) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(90215, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentExecution) < 3 {
		t.Fatalf("RecentExecution len = %d, want at least 3", len(snapshot.RecentExecution))
	}
	if snapshot.RecentExecution[0].EventType != core.ExecutionEventToolExposureChanged {
		t.Fatalf("RecentExecution[0].EventType = %q, want %q", snapshot.RecentExecution[0].EventType, core.ExecutionEventToolExposureChanged)
	}
	if !strings.Contains(snapshot.RecentExecution[0].Summary, "active=true") {
		t.Fatalf("RecentExecution[0].Summary = %q, want active=true", snapshot.RecentExecution[0].Summary)
	}
	if snapshot.RecentExecution[1].EventType != core.ExecutionEventToolRegistered {
		t.Fatalf("RecentExecution[1].EventType = %q, want %q", snapshot.RecentExecution[1].EventType, core.ExecutionEventToolRegistered)
	}
	if !strings.Contains(snapshot.RecentExecution[1].Summary, "registered=true") {
		t.Fatalf("RecentExecution[1].Summary = %q, want registered=true", snapshot.RecentExecution[1].Summary)
	}
	if snapshot.RecentExecution[2].EventType != core.ExecutionEventToolProposalReviewed {
		t.Fatalf("RecentExecution[2].EventType = %q, want %q", snapshot.RecentExecution[2].EventType, core.ExecutionEventToolProposalReviewed)
	}
	if !strings.Contains(snapshot.RecentExecution[2].Summary, "review_status=approved") {
		t.Fatalf("RecentExecution[2].Summary = %q, want review_status=approved", snapshot.RecentExecution[2].Summary)
	}
}

func TestSystemStatusSnapshotIncludesRecentExecutionTimeline(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	keyA := session.SessionKey{ChatID: 9022, UserID: 0, Scope: telegramDMScopeRef(9022)}
	keyB := session.SessionKey{ChatID: 9023, UserID: 0, Scope: telegramDMScopeRef(9023)}
	if _, err := store.AppendExecutionEvents(keyA, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventDecisionOpened,
			Stage:       "decision",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"d-1"}`,
			CreatedAt:   now.Add(-25 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyA) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(keyB, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventRecoveryIssued,
			Stage:       "recovery",
			Status:      "issued",
			PayloadJSON: `{"pending_count":1}`,
			CreatedAt:   now.Add(-5 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyB) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentExecution) < 2 {
		t.Fatalf("RecentExecution len = %d, want at least 2", len(snapshot.RecentExecution))
	}
	if snapshot.RecentExecution[0].EventType != core.ExecutionEventRecoveryIssued {
		t.Fatalf("RecentExecution[0].EventType = %q, want %q", snapshot.RecentExecution[0].EventType, core.ExecutionEventRecoveryIssued)
	}
}

func TestChatStatusSnapshotPrefersLatestTurnFromExecutionEventsOverLegacyTurnRun(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9051, UserID: 0, Scope: telegramDMScopeRef(9051)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "legacy status row")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusFailed, "legacy failed row"); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_kind":"interactive","request_text":"event-projected run"}`,
			CreatedAt:   now.Add(10 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"summary":"event-projected completion"}`,
			CreatedAt:   now.Add(20 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(9051, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.LatestTurnRun == nil {
		t.Fatal("LatestTurnRun = nil, want TES-projected latest turn")
	}
	if snapshot.LatestTurnRun.Status != string(session.TurnRunStatusCompleted) {
		t.Fatalf("LatestTurnRun.Status = %q, want completed from TES", snapshot.LatestTurnRun.Status)
	}
	if snapshot.LatestTurnRun.RequestText != "event-projected run" {
		t.Fatalf("LatestTurnRun.RequestText = %q, want event-projected request text", snapshot.LatestTurnRun.RequestText)
	}
}

func TestSystemStatusSnapshotIncludesLatestTurnProjectionFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9052, UserID: 0, Scope: telegramDMScopeRef(9052)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_kind":"interactive","request_text":"system projection run"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"tool":"exec","preview":"{\"command\":\"echo hi\"}"}`,
			CreatedAt:   now.Add(-5 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	latest, ok := snapshot.LatestTurnRunsByChat[9052]
	if !ok {
		t.Fatalf("LatestTurnRunsByChat missing chat 9052: %#v", snapshot.LatestTurnRunsByChat)
	}
	if latest.Status != string(session.TurnRunStatusRunning) {
		t.Fatalf("latest.Status = %q, want running", latest.Status)
	}
	if latest.LastToolName != "exec" {
		t.Fatalf("latest.LastToolName = %q, want exec", latest.LastToolName)
	}
}

func TestSystemStatusSnapshotPrefersExecutionEventLiveSignalsOverRouterSnapshot(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9053, UserID: 0, Scope: telegramDMScopeRef(9053)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventIngressQueued,
			Stage:       "ingress",
			PayloadJSON: `{"queue_depth":2}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":77,"run_kind":"interactive"}`,
			CreatedAt:   now.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{
		ActiveTurnsByChat: map[int64][]uint64{
			9053: {999},
			9054: {1000},
		},
		QueueDepthByChat: map[int64]int{
			9053: 5,
			9054: 3,
		},
	})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if got := snapshot.QueueDepthByChat[9053]; got != 2 {
		t.Fatalf("QueueDepthByChat[9053] = %d, want 2 from TES", got)
	}
	if got := snapshot.QueueDepthByChat[9054]; got != 3 {
		t.Fatalf("QueueDepthByChat[9054] = %d, want router fallback 3", got)
	}
	if got := snapshot.ActiveTurnsByChat[9053]; len(got) != 1 || got[0] != 77 {
		t.Fatalf("ActiveTurnsByChat[9053] = %#v, want TES run id 77", got)
	}
	if got := snapshot.ActiveTurnsByChat[9054]; len(got) != 1 || got[0] != 1000 {
		t.Fatalf("ActiveTurnsByChat[9054] = %#v, want router fallback [1000]", got)
	}
}

func TestChatStatusSnapshotIncludesTurnPhaseFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7344, UserID: 0, Scope: telegramDMScopeRef(7344)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"turn_kind":"interactive"}`,
		CreatedAt:   now,
	}, {
		EventType:   core.ExecutionEventTurnStageChanged,
		Stage:       "render",
		Status:      "active",
		PayloadJSON: `{"phase":"render","summary":"authoring scene reply"}`,
		CreatedAt:   now.Add(2 * time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(turn lifecycle) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(7344, core.RouterStatusSnapshot{
		ActiveTurnsByChat: map[int64][]uint64{7344: {61}},
	})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.TurnPhase != "render" {
		t.Fatalf("TurnPhase = %q, want render", snapshot.TurnPhase)
	}
	if snapshot.TurnPhaseSummary != "authoring scene reply" {
		t.Fatalf("TurnPhaseSummary = %q, want phase summary", snapshot.TurnPhaseSummary)
	}
	if snapshot.TurnPhaseUpdatedAt.IsZero() {
		t.Fatalf("TurnPhaseUpdatedAt = %s, want non-zero timestamp", snapshot.TurnPhaseUpdatedAt.Format(time.RFC3339Nano))
	}
}

func TestChatStatusSnapshotClearsTurnPhaseAfterTerminalEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7346, UserID: 0, Scope: telegramDMScopeRef(7346)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"turn_kind":"interactive"}`,
		CreatedAt:   now,
	}, {
		EventType:   core.ExecutionEventTurnStageChanged,
		Stage:       "governor",
		Status:      "active",
		PayloadJSON: `{"summary":"running governor loop"}`,
		CreatedAt:   now.Add(time.Second),
	}, {
		EventType:   core.ExecutionEventTurnCompleted,
		Stage:       "turn",
		Status:      "completed",
		PayloadJSON: `{"turn_kind":"interactive"}`,
		CreatedAt:   now.Add(2 * time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(turn lifecycle) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(7346, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.TurnPhase != "" {
		t.Fatalf("TurnPhase = %q, want empty after completed terminal event", snapshot.TurnPhase)
	}
}

func TestChatStatusSnapshotIncludesHiddenInputDeliveryAndPlanProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7345, UserID: 0, Scope: telegramDMScopeRef(7345)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "status telemetry probe")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusFailed, "send outbound reply: telegram timeout"); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.LastFloorMetadata = encodeFloorMetadata(core.FloorMetadata{
		HiddenInputs: []core.HiddenInput{
			{Category: "unresolved_memory_state", Summary: "follow-up question still open"},
			{Category: "semantic_recurrence", Summary: "same decision loops across recent turns"},
		},
		ProvenanceSummary: "pending review events keep converging around approvals",
	})
	sess.PlanState = session.PlanState{
		Steps: []session.PlanStep{
			{Step: "Audit pending approvals", Status: session.PlanStatusCompleted},
			{Step: "Publish operator summary", Status: session.PlanStatusCompleted},
		},
	}
	sess.TurnCount = 4
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(7345, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.HiddenInputSummary == "" {
		t.Fatalf("HiddenInputSummary = %q, want hidden-input provenance summary", snapshot.HiddenInputSummary)
	}
	if len(snapshot.HiddenInputCategories) != 2 {
		t.Fatalf("HiddenInputCategories = %#v, want two categories", snapshot.HiddenInputCategories)
	}
	if snapshot.PlanCompletedSteps != 2 || snapshot.PlanTotalSteps != 2 || !snapshot.PlanFullyExecuted {
		t.Fatalf("plan progress = (%d/%d fully=%t), want 2/2 fully executed", snapshot.PlanCompletedSteps, snapshot.PlanTotalSteps, snapshot.PlanFullyExecuted)
	}
	if snapshot.DeliveryStatus != "delivery_failed" {
		t.Fatalf("DeliveryStatus = %q, want delivery_failed", snapshot.DeliveryStatus)
	}
	if !strings.Contains(snapshot.DeliverySummary, "no retry queue") {
		t.Fatalf("DeliverySummary = %q, want no-retry guidance", snapshot.DeliverySummary)
	}
}

func TestChatStatusSnapshotPrefersSidecarProjectionEventsOverStatusState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7347, UserID: 0, Scope: telegramDMScopeRef(7347)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = session.OperationState{
		Status:  "active",
		Stage:   "legacy-stage",
		Summary: "legacy operation status",
	}
	sess.PlanState = session.PlanState{
		Steps: []session.PlanStep{
			{Step: "Legacy plan step", Status: session.PlanStatusInProgress},
		},
	}
	sess.LastFloorMetadata = encodeFloorMetadata(core.FloorMetadata{
		HiddenInputs:      []core.HiddenInput{{Category: "legacy", Summary: "legacy summary"}},
		ProvenanceSummary: "legacy hidden summary",
	})
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(session legacy sidecars) err = %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventTurnSidecarsCaptured,
		Stage:     "persist",
		Status:    "captured",
		PayloadJSON: `{
			"operation_status":"blocked",
			"operation_stage":"event-stage",
			"operation_summary":"event operation status",
			"plan_step_status":"in_progress",
			"plan_step":"Event plan step",
			"plan_completed_steps":1,
			"plan_total_steps":3,
			"plan_fully_executed":false,
			"hidden_input_categories":["event_a","event_b"],
			"hidden_input_summary":"event hidden summary"
		}`,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(turn sidecars captured) err = %v", err)
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDeliveryFinalFailed,
		Stage:       "delivery",
		Status:      "failed",
		PayloadJSON: `{"error":"telegram timeout"}`,
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(delivery final failed) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(7347, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.OperationStatus != "blocked" || snapshot.OperationStage != "event-stage" {
		t.Fatalf("operation fields = (%q,%q), want TES sidecar projection", snapshot.OperationStatus, snapshot.OperationStage)
	}
	if snapshot.OperationSummary != "event operation status" {
		t.Fatalf("OperationSummary = %q, want TES sidecar summary", snapshot.OperationSummary)
	}
	if snapshot.PlanStep != "Event plan step" || snapshot.PlanStepStatus != "in_progress" {
		t.Fatalf("plan fields = (%q,%q), want TES sidecar plan state", snapshot.PlanStepStatus, snapshot.PlanStep)
	}
	if snapshot.PlanCompletedSteps != 1 || snapshot.PlanTotalSteps != 3 || snapshot.PlanFullyExecuted {
		t.Fatalf("plan progress = (%d/%d fully=%t), want 1/3 false", snapshot.PlanCompletedSteps, snapshot.PlanTotalSteps, snapshot.PlanFullyExecuted)
	}
	if len(snapshot.HiddenInputCategories) != 2 || snapshot.HiddenInputCategories[0] != "event_a" || snapshot.HiddenInputCategories[1] != "event_b" {
		t.Fatalf("HiddenInputCategories = %#v, want TES event categories", snapshot.HiddenInputCategories)
	}
	if snapshot.HiddenInputSummary != "event hidden summary" {
		t.Fatalf("HiddenInputSummary = %q, want TES event hidden summary", snapshot.HiddenInputSummary)
	}
	if snapshot.DeliveryStatus != "delivery_failed" {
		t.Fatalf("DeliveryStatus = %q, want delivery_failed from TES delivery event", snapshot.DeliveryStatus)
	}
	if !strings.Contains(snapshot.DeliverySummary, "telegram timeout") {
		t.Fatalf("DeliverySummary = %q, want TES delivery error text", snapshot.DeliverySummary)
	}
}

func TestDurableAgentsStatusSnapshotIncludesHealthSignals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agentDormant := core.DurableAgent{
		AgentID:            "agent-dormant",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-dormant",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 3,
		PolicyHash:    "hash-dormant",
	}
	if err := store.UpsertDurableAgent(agentDormant); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-dormant) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:                  "agent-dormant",
		DormantAt:                time.Now().UTC().Add(-15 * time.Minute),
		LastWakeAt:               time.Now().UTC().Add(-30 * time.Minute),
		LastReviewAt:             time.Now().UTC().Add(-25 * time.Minute),
		LastAppliedPolicyVersion: 3,
		LastAppliedPolicyAt:      time.Now().UTC().Add(-40 * time.Minute),
		LastApplyStatus:          "ok",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(agent-dormant) err = %v", err)
	}

	agentDegraded := core.DurableAgent{
		AgentID:            "agent-degraded",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-degraded",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_parent_review",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 5,
		PolicyHash:    "hash-degraded",
	}
	if err := store.UpsertDurableAgent(agentDegraded); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-degraded) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         "agent-degraded",
		LastApplyStatus: "failed",
		LastApplyError:  "child runtime unavailable",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(agent-degraded) err = %v", err)
	}

	agentInactive := core.DurableAgent{
		AgentID:            "agent-inactive",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		Status:             "draft",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-inactive",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"read_channel"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-inactive",
	}
	if err := store.UpsertDurableAgent(agentInactive); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-inactive) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if snapshot.TotalAgents != 3 {
		t.Fatalf("TotalAgents = %d, want 3", snapshot.TotalAgents)
	}
	if snapshot.ActiveAgents != 2 {
		t.Fatalf("ActiveAgents = %d, want 2", snapshot.ActiveAgents)
	}
	if snapshot.DormantAgents != 1 {
		t.Fatalf("DormantAgents = %d, want 1", snapshot.DormantAgents)
	}
	if snapshot.DegradedAgents != 1 {
		t.Fatalf("DegradedAgents = %d, want 1", snapshot.DegradedAgents)
	}
	if snapshot.InactiveAgents != 1 {
		t.Fatalf("InactiveAgents = %d, want 1", snapshot.InactiveAgents)
	}
	if len(snapshot.Agents) != 3 {
		t.Fatalf("Agents len = %d, want 3", len(snapshot.Agents))
	}

	healthByID := map[string]string{}
	runtimeSourceByID := map[string]string{}
	identitySourceByID := map[string]string{}
	for _, agent := range snapshot.Agents {
		healthByID[agent.AgentID] = agent.Health
		runtimeSourceByID[agent.AgentID] = strings.TrimSpace(agent.RuntimePostureSource)
		identitySourceByID[agent.AgentID] = strings.TrimSpace(agent.IdentitySource)
	}
	if healthByID["agent-dormant"] != "dormant" {
		t.Fatalf("agent-dormant health = %q, want dormant", healthByID["agent-dormant"])
	}
	if healthByID["agent-degraded"] != "degraded" {
		t.Fatalf("agent-degraded health = %q, want degraded", healthByID["agent-degraded"])
	}
	if healthByID["agent-inactive"] != "inactive" {
		t.Fatalf("agent-inactive health = %q, want inactive", healthByID["agent-inactive"])
	}
	for id, source := range identitySourceByID {
		want := "canonical:session.durable_agents"
		if id == "agent-dormant" || id == "agent-degraded" {
			want = "canonical:session.durable_agents+canonical:session.durable_agent_identity_state"
		}
		if source != want {
			t.Fatalf("identity source for %s = %q, want %s", id, source, want)
		}
	}
	for id, source := range runtimeSourceByID {
		if source != "operational_current_state_store:session.durable_agent_state" {
			t.Fatalf("runtime posture source for %s = %q, want operational_current_state_store:session.durable_agent_state", id, source)
		}
	}
}

func TestDurableAgentsStatusSnapshotIncludesCapacityContractSignals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-capacity",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		Status:             "active",
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 2,
		PolicyHash:    "hash-capacity",
		WakeupMode:    "poll",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-capacity) err = %v", err)
	}

	now := time.Now().UTC().Add(-5 * time.Minute)
	continuity := core.DurableAgentContinuityState{
		CapabilityContract: &core.DurableAgentCapabilityContract{
			Status:           "verified",
			Can:              []string{"triage_inbox", "summarize_thread"},
			Cannot:           []string{"send_mail"},
			Uncertain:        []string{"ocr_heavy_pdf"},
			SuccessCriteria:  []string{"surface important threads within 5m"},
			EvidenceSignals:  []string{"review artifact includes surfaced_count"},
			LastNegotiatedAt: now.Add(-5 * time.Minute),
			LastProbedAt:     now.Add(-3 * time.Minute),
			LastAttestedAt:   now,
		},
	}
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "agent-capacity",
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(agent-capacity) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.AgentID != "agent-capacity" {
		t.Fatalf("AgentID = %q, want agent-capacity", row.AgentID)
	}
	if row.CapacityState != "verified" {
		t.Fatalf("CapacityState = %q, want verified", row.CapacityState)
	}
	if row.CapacityCanCount != 2 || row.CapacityCannotCount != 1 || row.CapacityUncertainCount != 1 {
		t.Fatalf("capacity counts = can:%d cannot:%d uncertain:%d, want 2/1/1", row.CapacityCanCount, row.CapacityCannotCount, row.CapacityUncertainCount)
	}
	if row.CapacitySuccessCriteriaCount != 1 || row.CapacityEvidenceSignalCount != 1 {
		t.Fatalf("capacity criteria counts = success:%d evidence:%d, want 1/1", row.CapacitySuccessCriteriaCount, row.CapacityEvidenceSignalCount)
	}
	if row.CapacityLastAttestedAt.IsZero() {
		t.Fatal("CapacityLastAttestedAt is zero, want attestation timestamp")
	}
}

func TestDurableAgentsStatusSnapshotOverlaysPolicyFailureFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-events-failure",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-events-failure",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-events-failure",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:             agent.AgentID,
		LastApplyStatus:     "applied",
		LastAppliedPolicyAt: time.Now().UTC().Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agent.AgentID,
			DurableAgentID: agent.AgentID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurablePolicyApplyFailed,
		Stage:       "durable",
		Status:      "failed",
		PayloadJSON: `{"agent_id":"agent-events-failure","error":"child runtime unavailable"}`,
		CreatedAt:   time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(durable policy failed) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.LastApplyStatus != "failed" {
		t.Fatalf("LastApplyStatus = %q, want failed from TES overlay", row.LastApplyStatus)
	}
	if !strings.Contains(strings.ToLower(row.LastApplyError), "child runtime unavailable") {
		t.Fatalf("LastApplyError = %q, want TES error propagation", row.LastApplyError)
	}
	if row.Health != "degraded" {
		t.Fatalf("Health = %q, want degraded after TES policy failure", row.Health)
	}
	if strings.TrimSpace(row.IdentitySource) != "canonical:session.durable_agents+canonical:session.durable_agent_identity_state" {
		t.Fatalf("IdentitySource = %q, want canonical durable agent identity+registry source", row.IdentitySource)
	}
	if strings.TrimSpace(row.RuntimePostureSource) != "operational_current_state_store:session.durable_agent_state+projection:tes_execution_events" {
		t.Fatalf("RuntimePostureSource = %q, want combined operational+projection source", row.RuntimePostureSource)
	}
}

func TestDurableAgentsStatusSnapshotMarksDormantFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-events-dormant",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-events-dormant",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-events-dormant",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agent.AgentID,
			DurableAgentID: agent.AgentID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurableStateDormant,
		Stage:       "durable",
		Status:      "dormant",
		PayloadJSON: `{"agent_id":"agent-events-dormant"}`,
		CreatedAt:   time.Now().UTC().Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(durable dormant) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.Health != "dormant" {
		t.Fatalf("Health = %q, want dormant from TES event", row.Health)
	}
	if row.DormantAt.IsZero() {
		t.Fatalf("DormantAt = %s, want non-zero from TES event", row.DormantAt.Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(row.RuntimePostureSource) != "projection:tes_execution_events" {
		t.Fatalf("RuntimePostureSource = %q, want projection:tes_execution_events", row.RuntimePostureSource)
	}
}

func TestDurableAgentsStatusSnapshotKeepsCanonicalIdentityWhenOperationalStateConflicts(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-identity-boundary",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-identity-boundary",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 11,
		PolicyHash:    "hash-identity-boundary",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	// This operational state status intentionally conflicts with canonical durable identity.
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         agent.AgentID,
		Status:          "inactive",
		LastApplyStatus: "failed",
		LastApplyError:  "simulated runtime fault",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.AgentID != agent.AgentID {
		t.Fatalf("AgentID = %q, want %q", row.AgentID, agent.AgentID)
	}
	if row.Status != "active" {
		t.Fatalf("Status = %q, want canonical durable_agents status active", row.Status)
	}
	if row.ChannelKind != "telegram_group" {
		t.Fatalf("ChannelKind = %q, want canonical durable_agents channel telegram_group", row.ChannelKind)
	}
	if strings.TrimSpace(row.IdentitySource) != "canonical:session.durable_agents+canonical:session.durable_agent_identity_state" {
		t.Fatalf("IdentitySource = %q, want canonical durable agent identity+registry source", row.IdentitySource)
	}
}

func TestDurableAgentsStatusSnapshotDoesNotFabricateIdentityFromTESOnlyEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	ghostID := "ghost-agent"
	key := session.SessionKey{
		ChatID: 1001,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             ghostID,
			DurableAgentID: ghostID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurableWakeStarted,
		Stage:       "durable",
		Status:      "running",
		PayloadJSON: `{"agent_id":"ghost-agent"}`,
		CreatedAt:   time.Now().UTC().Add(-10 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(ghost durable wake) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 0 {
		t.Fatalf("Agents = %#v, want no fabricated durable identity rows from TES-only events", snapshot.Agents)
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

func pendingDecisionByID(items []core.PendingItem, id string) bool {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if item.Kind != core.PendingItemKindDecision {
			continue
		}
		if strings.TrimSpace(item.ID) == id {
			return true
		}
	}
	return false
}

func pendingKindCount(items []core.PendingItem, kind core.PendingItemKind) int {
	count := 0
	for _, item := range items {
		if item.Kind == kind {
			count++
		}
	}
	return count
}

func pendingRecoveryByID(items []core.PendingItem, id string) bool {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if item.Kind != core.PendingItemKindRecovery {
			continue
		}
		if strings.TrimSpace(item.ID) == id {
			return true
		}
	}
	return false
}

func TestChatStatusSnapshotIncludesCapabilityDelegationState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       "cap-status",
		RequestedBy:     "family-child",
		RequestedFor:    "family-child",
		ParentPrincipal: "telegram:200",
		Kind:            session.CapabilityKindPurchase,
		TargetResource:  "amazon",
		Purpose:         "order approved supplies",
		RiskClass:       "spend",
		ReviewStatus:    session.CapabilityReviewStatusApproved,
		GrantID:         "capg-status",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:           "capg-status",
		RequestID:         "cap-status",
		GrantedBy:         "telegram:1001",
		GrantedTo:         "family-child",
		Kind:              session.CapabilityKindPurchase,
		TargetResource:    "amazon",
		AllowedActions:    []string{"order"},
		Status:            session.CapabilityGrantStatusActive,
		AnchorFingerprint: "sha256:capability",
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(90218, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.CapabilityRequests) != 1 {
		t.Fatalf("CapabilityRequests len = %d, want 1", len(snapshot.CapabilityRequests))
	}
	if got := snapshot.CapabilityRequests[0]; got.RequestID != "cap-status" || got.Kind != "purchase" || got.ReviewStatus != "approved" || got.GrantID != "capg-status" {
		t.Fatalf("CapabilityRequests[0] = %#v, want approved cap-status", got)
	}
	if len(snapshot.CapabilityGrants) != 1 {
		t.Fatalf("CapabilityGrants len = %d, want 1", len(snapshot.CapabilityGrants))
	}
	if got := snapshot.CapabilityGrants[0]; got.GrantID != "capg-status" || got.Status != "active" || got.AllowedActions[0] != "order" {
		t.Fatalf("CapabilityGrants[0] = %#v, want active order grant", got)
	}
}
