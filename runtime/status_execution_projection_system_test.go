//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

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
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":1,"run_kind":"interactive","request_text":"status telemetry probe"}`,
			CreatedAt:   now.Add(-2 * time.Second),
		},
		{
			EventType:   core.ExecutionEventTurnFailed,
			Stage:       "turn",
			Status:      "failed",
			PayloadJSON: `{"error":"send outbound reply: telegram timeout"}`,
			CreatedAt:   now.Add(-time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(delivery fallback) err = %v", err)
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
		Stage:   "old-stage",
		Summary: "old operation status",
	}
	sess.PlanState = session.PlanState{
		Steps: []session.PlanStep{
			{Step: "Old plan step", Status: session.PlanStatusInProgress},
		},
	}
	sess.LastFloorMetadata = encodeFloorMetadata(core.FloorMetadata{
		HiddenInputs:      []core.HiddenInput{{Category: "old", Summary: "old summary"}},
		ProvenanceSummary: "old hidden summary",
	})
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(session old sidecars) err = %v", err)
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
		AdminPrincipal:  "telegram:1001",
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
	if got := snapshot.CapabilityRequests[0]; got.ParentPrincipal != "telegram:200" || got.AdminPrincipal != "telegram:1001" {
		t.Fatalf("CapabilityRequests[0] principals = parent %q admin %q, want telegram:200 and telegram:1001", got.ParentPrincipal, got.AdminPrincipal)
	}
	if len(snapshot.CapabilityGrants) != 1 {
		t.Fatalf("CapabilityGrants len = %d, want 1", len(snapshot.CapabilityGrants))
	}
	if got := snapshot.CapabilityGrants[0]; got.GrantID != "capg-status" || got.Status != "active" || got.AllowedActions[0] != "order" {
		t.Fatalf("CapabilityGrants[0] = %#v, want active order grant", got)
	}
}
