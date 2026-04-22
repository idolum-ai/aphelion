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

func TestSystemStatusSnapshotPrefersDecisionEventsOverLegacyPendingRows(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(decision events) err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}

	if pendingDecisionByID(snapshot.PendingItems, "decision-from-events") {
		t.Fatalf("PendingItems unexpectedly contains decision-from-events after TES resolved event: %#v", snapshot.PendingItems)
	}
	if !pendingDecisionByID(snapshot.PendingItems, "decision-legacy-only") {
		t.Fatalf("PendingItems missing legacy fallback decision decision-legacy-only: %#v", snapshot.PendingItems)
	}
}

func TestChatStatusSnapshotPrefersContinuationEventsOverLegacyContinuationState(t *testing.T) {
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
		t.Fatalf("Continuation = nil, want TES-derived continuation snapshot")
	}
	if snapshot.Continuation.Status != "revoked" {
		t.Fatalf("Continuation.Status = %q, want revoked from TES", snapshot.Continuation.Status)
	}
	if snapshot.Continuation.RemainingTurns != 0 {
		t.Fatalf("Continuation.RemainingTurns = %d, want 0 after revoke", snapshot.Continuation.RemainingTurns)
	}
	if pendingKindCount(snapshot.PendingItems, core.PendingItemKindContinuation) != 0 {
		t.Fatalf("Pending continuation item should be absent after revoke: %#v", snapshot.PendingItems)
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

func TestChatStatusSnapshotIncludesLiveTurnPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.markChatTurnPhase(7344, "render", "authoring scene reply")
	t.Cleanup(func() {
		rt.clearChatTurnPhase(7344)
	})

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
	for _, agent := range snapshot.Agents {
		healthByID[agent.AgentID] = agent.Health
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
