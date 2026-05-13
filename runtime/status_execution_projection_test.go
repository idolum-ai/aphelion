//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestChatStatusSnapshotLatestTurnSourceMarkersFromTES(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9311, UserID: 0, Scope: telegramDMScopeRef(9311)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "operational run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	preTES, err := rt.ChatStatusSnapshot(9311, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot(preTES) err = %v", err)
	}
	if preTES.LatestTurnRun != nil {
		t.Fatalf("LatestTurnRun = %#v, want no turn_runs fallback before TES events", preTES.LatestTurnRun)
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

func TestSystemStatusSnapshotTreatsStaleTESRunningTurnAsRecovery(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.staleTurnThreshold = time.Minute

	key := session.SessionKey{ChatID: 9122, UserID: 0, Scope: telegramDMScopeRef(9122)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":77,"run_kind":"interactive","request_text":"inspect old commit"}`,
			CreatedAt:   now.Add(-2 * time.Hour),
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "running",
			PayloadJSON: `{"tool":"exec"}`,
			CreatedAt:   now.Add(-119 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(stale turn) err = %v", err)
	}

	system, err := rt.SystemStatusSnapshot(core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("SystemStatusSnapshot() err = %v", err)
	}
	if got := system.ActiveTurnCount; got != 0 {
		t.Fatalf("ActiveTurnCount = %d, want stale TES turn excluded from active count", got)
	}
	if got := len(system.StaleRunningTurns); got != 1 {
		t.Fatalf("StaleRunningTurns len = %d, want TES stale turn", got)
	}
	if !pendingItemByID(system.PendingItems, "stale:tes:77") {
		t.Fatalf("PendingItems = %#v, want TES stale turn pending item", system.PendingItems)
	}

	chat, err := rt.ChatStatusSnapshot(9122, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(chat.ActiveTurnIDs) != 0 {
		t.Fatalf("ActiveTurnIDs = %#v, want stale TES turn excluded", chat.ActiveTurnIDs)
	}
	if len(chat.StaleRunningTurns) != 1 {
		t.Fatalf("chat stale turns = %#v, want TES stale turn", chat.StaleRunningTurns)
	}
	if chat.LatestTurnRun == nil || chat.LatestTurnRun.Status != string(session.TurnRunStatusRunning) {
		t.Fatalf("LatestTurnRun = %#v, want running TES latest for debug evidence", chat.LatestTurnRun)
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

func TestStatusSurfacesRuntimeAdjudications(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 90211, UserID: 0, Scope: telegramDMScopeRef(90211)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType: core.ExecutionEventReplyClaimAdjudicated,
		Stage:     "reply",
		Status:    "adjudicated",
		PayloadJSON: `{
			"adjudication_kind":"execution_claim",
			"surface":"final_reply",
			"subject_id":"latest_turn",
			"operator_label":"Reply claim repaired",
			"visible_action":"persona_repaired",
			"findings":[{"kind":"test_execution","claim_type":"test_execution","evidence_status":"not_observed_in_current_turn","detail":"test-execution claim has no test-related tool evidence"}],
			"evidence_refs":["tes:turn_seq:12"]
		}`,
		CreatedAt: now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(adjudication) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(90211, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentAdjudications) != 1 {
		t.Fatalf("RecentAdjudications len = %d, want 1", len(snapshot.RecentAdjudications))
	}
	if got := snapshot.RecentAdjudications[0].Findings[0].Kind; got != "test_execution" {
		t.Fatalf("adjudication finding kind = %q, want test_execution", got)
	}
	if len(snapshot.RecentExecution) == 0 || !strings.Contains(snapshot.RecentExecution[0].Summary, "action=persona_repaired") {
		t.Fatalf("RecentExecution = %#v, want adjudication summary", snapshot.RecentExecution)
	}

	lines, err := rt.StatusDiagnostics(90211)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, want := range []string{"Runtime adjudication", "Reply claim repaired", "persona_repaired", "test-execution claim"} {
		if !strings.Contains(text, want) {
			t.Fatalf("StatusDiagnostics() = %q, want %q", text, want)
		}
	}
}

func TestStatusSurfacesContinuationApprovalAdjudications(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 90212, UserID: 0, Scope: telegramDMScopeRef(90212)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType: core.ExecutionEventContinuationAdjudicated,
		Stage:     "continuation",
		Status:    "adjudicated",
		PayloadJSON: `{
			"adjudication_kind":"continuation_approval",
			"surface":"materialization_repair",
			"subject_id":"bundle-mada",
			"operator_label":"Invalid continuation approval repaired",
			"visible_action":"repair_invalid_pending_approval",
			"findings":[{"kind":"invalid_pending_approval","claim_type":"invalid_pending_approval","evidence_status":"detected_from_phase_contract","detail":"mixed authority classes require separate approvals"}]
		}`,
		CreatedAt: now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(adjudication) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(90212, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentAdjudications) != 1 {
		t.Fatalf("RecentAdjudications len = %d, want 1", len(snapshot.RecentAdjudications))
	}
	if got := snapshot.RecentAdjudications[0].Kind; got != "continuation_approval" {
		t.Fatalf("adjudication kind = %q, want continuation_approval", got)
	}
	if got := snapshot.RecentAdjudications[0].Findings[0].Kind; got != "invalid_pending_approval" {
		t.Fatalf("adjudication finding kind = %q, want invalid_pending_approval", got)
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
			EventType: core.ExecutionEventToolRegistered,
			Stage:     "tool_authority",
			Status:    "enabled",
			PayloadJSON: `{
				"tool_name":"browse_page",
				"registered":true,
				"implementation_ref":"external:browse_page"
			}`,
			CreatedAt: now.Add(-5 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(tool authority) err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(90215, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if len(snapshot.RecentExecution) < 1 {
		t.Fatalf("RecentExecution len = %d, want at least 1", len(snapshot.RecentExecution))
	}
	if snapshot.RecentExecution[0].EventType != core.ExecutionEventToolRegistered {
		t.Fatalf("RecentExecution[0].EventType = %q, want %q", snapshot.RecentExecution[0].EventType, core.ExecutionEventToolRegistered)
	}
	if !strings.Contains(snapshot.RecentExecution[0].Summary, "registered=true") {
		t.Fatalf("RecentExecution[0].Summary = %q, want registered=true", snapshot.RecentExecution[0].Summary)
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

func TestChatStatusSnapshotPrefersLatestTurnFromExecutionEventsOverOldTurnRun(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9051, UserID: 0, Scope: telegramDMScopeRef(9051)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "old status row")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusFailed, "old failed row"); err != nil {
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
