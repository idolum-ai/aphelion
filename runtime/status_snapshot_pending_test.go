//go:build linux

package runtime

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
	"github.com/idolum-ai/aphelion/session"
)

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
	staleAt := time.Now().UTC().Add(-5 * time.Minute)
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":99,"run_kind":"interactive","request_text":"aggregate status"}`,
			CreatedAt:   staleAt,
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"tool":"exec"}`,
			CreatedAt:   staleAt.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(chat status) err = %v", err)
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventRecoveryIssued,
		Stage:       "recovery",
		Status:      "issued",
		PayloadJSON: `{"pending_count":1}`,
		CreatedAt:   staleAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(chat recovery) err = %v", err)
	}

	rt.staleTurnThreshold = 2 * time.Minute
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
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(session.SessionKey{ChatID: 8101, UserID: 0, Scope: telegramDMScopeRef(8101)}, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"run_id":301,"run_kind":"interactive"}`,
		CreatedAt:   now.Add(-4 * time.Minute),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(chat 8101) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(session.SessionKey{ChatID: 8102, UserID: 0, Scope: telegramDMScopeRef(8102)}, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":302,"run_kind":"interactive"}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventTurnCompleted,
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{"run_id":302}`,
			CreatedAt:   now.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(chat 8102) err = %v", err)
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
	if snapshot.Autonomy.DefaultMode != "ask_first" || snapshot.Autonomy.Ceiling != "leased" || !snapshot.Autonomy.AllowLiveOverrides {
		t.Fatalf("Autonomy = %#v, want default config-owned ask_first policy with leased override ceiling", snapshot.Autonomy)
	}
	if snapshot.Sandbox.GeneratedAt.IsZero() {
		t.Fatalf("Sandbox readiness snapshot missing generated_at: %#v", snapshot.Sandbox)
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
	decisionDetails := decisionprojection.FormatExecApprovalDetails(session.OperationProposal{
		Kind:          "workspace_escape",
		Summary:       "Run command outside the configured workspace",
		WhyNow:        "The requested command needs an explicit admin-approved working directory outside the current sandbox root.",
		BoundedEffect: "The command will run once.",
	}, "workspace escape", `rg -n "renderDecisionSummary" runtime`, "/home/sadasant_gmail_com/code/github.com/idolum-ai/aphelion")
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-from-events",
		Sequence:      1,
		OwnerKey:      "chat:9001:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        9001,
		SenderID:      1002,
		MessageID:     111,
		Prompt:        "Approve from events?",
		Details:       decisionDetails,
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(decision-from-events) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "decision-store-only",
		Sequence:      2,
		OwnerKey:      "chat:9002:sender:1002",
		Kind:          "proposal_approval",
		ChatID:        9002,
		SenderID:      1002,
		MessageID:     222,
		Prompt:        "Store only decision?",
		Details:       decisionDetails,
		DefaultChoice: "deny",
		ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-90 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(decision-store-only) err = %v", err)
	}

	key := session.SessionKey{ChatID: 9001, UserID: 0, Scope: telegramDMScopeRef(9001)}
	eventsOnlyPayload, err := json.Marshal(map[string]any{
		"decision_id":   "decision-events-only",
		"decision_kind": "proposal_approval",
		"owner_key":     "chat:9003:sender:1002",
		"prompt":        "Events only decision?",
		"details":       decisionDetails,
	})
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}
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
			EventType:   core.ExecutionEventDecisionOpened,
			Stage:       "decision",
			Status:      "pending",
			PayloadJSON: string(eventsOnlyPayload),
			CreatedAt:   now.Add(-50 * time.Second),
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
	if !pendingDecisionByID(snapshot.PendingItems, "decision-store-only") {
		t.Fatalf("PendingItems missing store fallback decision decision-store-only: %#v", snapshot.PendingItems)
	}
	if !pendingDecisionByID(snapshot.PendingItems, "decision-events-only") {
		t.Fatalf("PendingItems missing TES fallback decision decision-events-only: %#v", snapshot.PendingItems)
	}
	for _, id := range []string{"decision-store-only", "decision-events-only"} {
		item, ok := pendingDecisionItemByID(snapshot.PendingItems, id)
		if !ok {
			t.Fatalf("PendingItems missing %s: %#v", id, snapshot.PendingItems)
		}
		if !strings.Contains(item.Summary, "read repository files outside the configured workspace") ||
			!strings.Contains(item.Summary, "Command class: repo_read") {
			t.Fatalf("summary for %s = %q, want decision-oriented workspace-escape projection", id, item.Summary)
		}
		if strings.Contains(item.Summary, "kind=proposal_approval prompt=") {
			t.Fatalf("summary for %s = %q, should not use kind/prompt fallback while details are available", id, item.Summary)
		}
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
		DecisionID:     "continuation-store",
		UpdatedAt:      time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpdateContinuationState(store pending) err = %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType: core.ExecutionEventContinuationOffered,
			Stage:     "continuation",
			Status:    "pending",
			PayloadJSON: `{
				"decision_id":"continuation-store",
				"remaining_turns":2
			}`,
			CreatedAt: now.Add(-90 * time.Second),
		},
		{
			EventType: core.ExecutionEventContinuationRevoked,
			Stage:     "continuation",
			Status:    "revoked",
			PayloadJSON: `{
				"decision_id":"continuation-store",
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

func TestChatStatusSnapshotForKeyFiltersTelegramThreadPendingState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(90112)
	mainKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	threadKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramThreadScopeRef(chatID, 4)}
	if err := store.UpdateContinuationState(mainKey, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "main-continuation",
		RemainingTurns: 1,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateContinuationState(main) err = %v", err)
	}
	if err := store.UpdateContinuationState(threadKey, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "thread-continuation",
		RemainingTurns: 1,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateContinuationState(thread) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "main-decision",
		Sequence:      1,
		OwnerKey:      "session:" + session.SessionIDForKey(mainKey) + ":sender:1001",
		SessionID:     session.SessionIDForKey(mainKey),
		ScopeKind:     string(mainKey.Scope.Kind),
		ScopeID:       mainKey.Scope.ID,
		Kind:          "interrupt",
		ChatID:        chatID,
		SenderID:      1001,
		ChoicesJSON:   `[]`,
		DefaultChoice: "queue",
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(main) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:            "thread-decision",
		Sequence:      2,
		OwnerKey:      "session:" + session.SessionIDForKey(threadKey) + ":sender:1001",
		SessionID:     session.SessionIDForKey(threadKey),
		ScopeKind:     string(threadKey.Scope.Kind),
		ScopeID:       threadKey.Scope.ID,
		Kind:          "interrupt",
		ChatID:        chatID,
		SenderID:      1001,
		ChoicesJSON:   `[]`,
		DefaultChoice: "queue",
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(thread) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshotForKey(threadKey, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshotForKey(thread) err = %v", err)
	}
	if snapshot.SessionID != session.SessionIDForKey(threadKey) {
		t.Fatalf("SessionID = %q, want thread session", snapshot.SessionID)
	}
	if snapshot.Continuation == nil || snapshot.Continuation.DecisionID != "thread-continuation" {
		t.Fatalf("Continuation = %#v, want thread continuation", snapshot.Continuation)
	}
	if pendingItemByID(snapshot.PendingItems, "main-decision") || pendingItemByID(snapshot.PendingItems, "main-continuation") {
		t.Fatalf("PendingItems = %#v, want main pending state filtered out", snapshot.PendingItems)
	}
	if !pendingItemByID(snapshot.PendingItems, "thread-decision") || !pendingItemByID(snapshot.PendingItems, "thread-continuation") {
		t.Fatalf("PendingItems = %#v, want thread decision and continuation", snapshot.PendingItems)
	}
}

func TestSystemStatusSnapshotSurfacesCandidateMissionsAsPendingItems(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	mission, err := store.UpsertMission(session.MissionState{
		ID:                "mission-control-surface",
		Title:             "Mission Control surface",
		Objective:         "Surface candidate missions as reviewable pending work.",
		Scope:             "principal",
		Owner:             "telegram:1001",
		Status:            session.MissionStatusCandidate,
		NextAllowedAction: "Propose one bounded continuation lease.",
	}, "telegram:1001", "candidate")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(1001, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.MissionLedger.CandidateCount != 1 {
		t.Fatalf("MissionLedger.CandidateCount = %d, want 1", snapshot.MissionLedger.CandidateCount)
	}
	for _, item := range snapshot.PendingItems {
		if item.Kind == core.PendingItemKindMission && item.ID == mission.ID {
			if item.ChatID != 1001 {
				t.Fatalf("mission pending chat_id = %d, want 1001", item.ChatID)
			}
			if !strings.Contains(item.Summary, "Mission Control surface") || !strings.Contains(item.Summary, "requires_user_review=true") {
				t.Fatalf("mission pending summary = %q, want title and review boundary", item.Summary)
			}
			return
		}
	}
	t.Fatalf("PendingItems missing candidate mission %s: %#v", mission.ID, snapshot.PendingItems)
}

func TestSystemStatusSnapshotDoesNotRankBacklogOnlyChatsAsHot(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := store.UpsertMission(session.MissionState{
		ID:                "mission-backlog-only",
		Title:             "Backlog only mission",
		Objective:         "Stay visible as backlog without making the chat urgent.",
		Scope:             "principal",
		Owner:             "telegram:1001",
		Status:            session.MissionStatusCandidate,
		NextAllowedAction: "Wait for explicit review.",
	}, "telegram:1001", "candidate"); err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	snapshot, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if !pendingKindInItems(snapshot.PendingItems, core.PendingItemKindMission) {
		t.Fatalf("PendingItems = %#v, want mission backlog still present in raw snapshot", snapshot.PendingItems)
	}
	if got := len(snapshot.HotChats); got != 0 {
		t.Fatalf("HotChats len = %d, want backlog-only chat excluded from hot ranking: %#v", got, snapshot.HotChats)
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

func containsPendingKind(items []core.PendingItemKind, target core.PendingItemKind) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func pendingDecisionByID(items []core.PendingItem, id string) bool {
	_, ok := pendingDecisionItemByID(items, id)
	return ok
}

func pendingDecisionItemByID(items []core.PendingItem, id string) (core.PendingItem, bool) {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if item.Kind != core.PendingItemKindDecision {
			continue
		}
		if strings.TrimSpace(item.ID) == id {
			return item, true
		}
	}
	return core.PendingItem{}, false
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

func pendingKindInItems(items []core.PendingItem, kind core.PendingItemKind) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
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

func pendingItemByID(items []core.PendingItem, id string) bool {
	id = strings.TrimSpace(id)
	for _, item := range items {
		if strings.TrimSpace(item.ID) == id {
			return true
		}
	}
	return false
}
