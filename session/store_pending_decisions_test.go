//go:build linux

package session

import (
	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPendingDecisionRoundTripAndReload(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "pending-decisions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	record := PendingDecisionRecord{
		ID:        "decision-abc123",
		Sequence:  42,
		OwnerKey:  "chat:7:sender:99",
		Kind:      "proposal_approval",
		ChatID:    7,
		SenderID:  99,
		MessageID: 1001,
		Prompt:    "Approve this proposal?",
		Details:   "Install one dependency.",
		Rationale: "Dependency install is needed before the tool can be audited and verified.",
		ArtifactRefs: []RecordReference{
			{Kind: "file_path", Ref: "docs/architecture/external-tools-pilot.md", Label: "design doc"},
			{Kind: "telegram_message", Ref: "chat:7:message:1001", Label: "operator request"},
		},
		ChoicesJSON:       `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		DefaultChoice:     "deny",
		TimeoutNanos:      int64((30 * time.Second).Nanoseconds()),
		DeliveryMessageID: 5001,
	}
	if err := store.UpsertPendingDecision(record); err != nil {
		t.Fatalf("UpsertPendingDecision(insert) err = %v", err)
	}

	record.DeliveryMessageID = 5002
	if err := store.UpsertPendingDecision(record); err != nil {
		t.Fatalf("UpsertPendingDecision(update) err = %v", err)
	}

	pending, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].DeliveryMessageID != 5002 {
		t.Fatalf("DeliveryMessageID = %d, want 5002", pending[0].DeliveryMessageID)
	}
	if pending[0].Rationale != record.Rationale {
		t.Fatalf("Rationale = %q, want %q", pending[0].Rationale, record.Rationale)
	}
	if len(pending[0].ArtifactRefs) != 2 || pending[0].ArtifactRefs[0].Kind != "file_path" || pending[0].ArtifactRefs[1].Ref != "chat:7:message:1001" {
		t.Fatalf("ArtifactRefs = %#v, want file_path + telegram_message refs", pending[0].ArtifactRefs)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()

	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(reload) err = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "decision-abc123" {
		t.Fatalf("pending after reload = %#v, want decision-abc123", pending)
	}
	if pending[0].Rationale != record.Rationale || len(pending[0].ArtifactRefs) != 2 {
		t.Fatalf("pending after reload = %#v, want rationale + two artifact refs", pending[0])
	}

	if err := store.DeletePendingDecision("decision-abc123"); err != nil {
		t.Fatalf("DeletePendingDecision() err = %v", err)
	}
	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(after delete) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after delete = %d, want 0", len(pending))
	}
}

func TestDeletePendingDecisionsByOwnerAndAll(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	records := []PendingDecisionRecord{
		{
			ID:            "decision-a",
			Sequence:      1,
			OwnerKey:      "chat:7:sender:99",
			Kind:          "proposal_approval",
			ChatID:        7,
			SenderID:      99,
			Prompt:        "A",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
		{
			ID:            "decision-b",
			Sequence:      2,
			OwnerKey:      "chat:7:sender:99",
			Kind:          "proposal_approval",
			ChatID:        7,
			SenderID:      99,
			Prompt:        "B",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
		{
			ID:            "decision-c",
			Sequence:      3,
			OwnerKey:      "chat:8:sender:100",
			Kind:          "proposal_approval",
			ChatID:        8,
			SenderID:      100,
			Prompt:        "C",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
	}
	for _, record := range records {
		if err := store.UpsertPendingDecision(record); err != nil {
			t.Fatalf("UpsertPendingDecision(%s) err = %v", record.ID, err)
		}
	}

	removed, err := store.DeletePendingDecisionsByOwner("chat:7:sender:99")
	if err != nil {
		t.Fatalf("DeletePendingDecisionsByOwner() err = %v", err)
	}
	if removed != 2 {
		t.Fatalf("DeletePendingDecisionsByOwner() removed = %d, want 2", removed)
	}
	pending, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "decision-c" {
		t.Fatalf("pending after owner detach = %#v, want only decision-c", pending)
	}

	removed, err = store.DeleteAllPendingDecisions()
	if err != nil {
		t.Fatalf("DeleteAllPendingDecisions() err = %v", err)
	}
	if removed != 1 {
		t.Fatalf("DeleteAllPendingDecisions() removed = %d, want 1", removed)
	}
	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(after all delete) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after all delete = %d, want 0", len(pending))
	}
}

func TestOperationStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 80, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "80"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "assessment",
		Summary:   "Collecting public traces before requesting external access.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusPending,
		},
		Findings: []OperationFinding{
			{Claim: "A browser is not currently available.", Confidence: FindingConfidenceHigh, Basis: "No browser tool is exposed in the active manifest."},
		},
		Artifacts: []OperationArtifact{
			{Label: "working-note", Ref: "tmp/notes.md"},
		},
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "operating", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.OperationState.Objective != "Investigate the current internet footprint." {
		t.Fatalf("Objective = %q, want persisted objective", reloaded.OperationState.Objective)
	}
	if reloaded.OperationState.Proposal.Status != ProposalStatusPending {
		t.Fatalf("Proposal status = %q, want pending", reloaded.OperationState.Proposal.Status)
	}
	if len(reloaded.OperationState.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(reloaded.OperationState.Findings))
	}

	updated := OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "execution",
		Summary:   "Proposal approved and screenshot capture is underway.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusApproved,
		},
		Findings: []OperationFinding{
			{Claim: "Browser automation can be acquired locally.", Confidence: FindingConfidenceHigh, Basis: "Admin execution can install local dependencies."},
		},
		Artifacts: []OperationArtifact{
			{Label: "screenshot", Ref: "tmp/reddit.png"},
		},
	}
	if err := store.UpdateOperationState(key, updated); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	operationState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if operationState.Stage != "execution" {
		t.Fatalf("updated stage = %q, want execution", operationState.Stage)
	}
	if operationState.Proposal.Status != ProposalStatusApproved {
		t.Fatalf("updated proposal status = %q, want approved", operationState.Proposal.Status)
	}
	if len(operationState.Artifacts) != 1 || operationState.Artifacts[0].Ref != "tmp/reddit.png" {
		t.Fatalf("artifacts = %#v, want updated screenshot artifact", operationState.Artifacts)
	}
}

func TestRegisteredToolRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	registered, err := store.UpsertRegisteredTool(RegisteredTool{
		ToolName:          "browse_page",
		ImplementationRef: "external-tools/browse_page/manifest.json",
		Registered:        true,
	})
	if err != nil {
		t.Fatalf("UpsertRegisteredTool(insert) err = %v", err)
	}
	if !registered.Registered {
		t.Fatal("registered.Registered = false, want true")
	}

	loadedRegistered, ok, err := store.RegisteredTool("browse_page")
	if err != nil {
		t.Fatalf("RegisteredTool() err = %v", err)
	}
	if !ok {
		t.Fatal("RegisteredTool() ok = false, want true")
	}
	if loadedRegistered.ImplementationRef != "external-tools/browse_page/manifest.json" {
		t.Fatalf("loaded registered implementation_ref = %q, want external manifest ref", loadedRegistered.ImplementationRef)
	}

	registeredList, err := store.RegisteredTools(10)
	if err != nil {
		t.Fatalf("RegisteredTools() err = %v", err)
	}
	if len(registeredList) != 1 {
		t.Fatalf("RegisteredTools len = %d, want 1", len(registeredList))
	}

}

func TestToolProbeRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolProbeRecord(ToolProbeRecord{
		ToolName:                     "browse_page",
		Status:                       ToolProbeStatusPassed,
		ProbeOutput:                  "stdout: probe ok",
		Rationale:                    "Probe passed against the latest installed baseline.",
		ArtifactRefs:                 []RecordReference{{Kind: "execution_event", Ref: "tool.probe.updated:123", Label: "probe event"}},
		BaselineFingerprint:          "sha256:probe-baseline",
		CurrentFingerprint:           "sha256:probe-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v2",
		BaselineManifestHash:         "sha256:manifest-baseline",
		CurrentManifestHash:          "sha256:manifest-current",
		BaselineWorkspaceFingerprint: "sha256:workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:workspace-current",
		StaleReason:                  "workspace_drift: baseline=sha256:workspace-baseline current=sha256:workspace-current",
		DriftSource:                  ToolDriftSourceWorkspaceDrift,
		ProbedAt:                     time.Now().UTC(),
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolProbeRecord(insert) err = %v", err)
	}
	if record.Status != ToolProbeStatusPassed {
		t.Fatalf("record.Status = %q, want passed", record.Status)
	}
	loaded, ok, err := store.ToolProbeRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolProbeRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolProbeRecord() ok = false, want true")
	}
	if loaded.Status != ToolProbeStatusPassed {
		t.Fatalf("loaded.Status = %q, want passed", loaded.Status)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 1 || loaded.ArtifactRefs[0].Kind != "execution_event" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + execution_event ref", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineManifestHash != "sha256:manifest-baseline" || loaded.CurrentWorkspaceFingerprint != "sha256:workspace-current" || loaded.DriftSource != ToolDriftSourceWorkspaceDrift {
		t.Fatalf("loaded probe anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	list, err := store.ToolProbeRecords(ToolProbeStatusPassed, 10)
	if err != nil {
		t.Fatalf("ToolProbeRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolProbeRecords(passed) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 {
		t.Fatalf("ToolProbeRecords traceability = (%q, %#v), want persisted rationale + refs", list[0].Rationale, list[0].ArtifactRefs)
	}
}

func TestToolAuditRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolAuditRecord(ToolAuditRecord{
		ToolName:                     "browse_page",
		Status:                       ToolAuditStatusPassed,
		AuditOutput:                  "entry_path: /tmp/run.sh",
		Rationale:                    "Runtime resolution succeeded for the declared entrypoint.",
		ArtifactRefs:                 []RecordReference{{Kind: "file_path", Ref: "/tmp/run.sh", Label: "entry path"}},
		BaselineFingerprint:          "sha256:audit-baseline",
		CurrentFingerprint:           "sha256:audit-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v2",
		BaselineManifestHash:         "sha256:audit-manifest-baseline",
		CurrentManifestHash:          "sha256:audit-manifest-current",
		BaselineWorkspaceFingerprint: "sha256:audit-workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:audit-workspace-current",
		StaleReason:                  "manifest_drift: baseline=sha256:audit-manifest-baseline current=sha256:audit-manifest-current",
		DriftSource:                  ToolDriftSourceManifestDrift,
		AuditedAt:                    time.Now().UTC(),
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolAuditRecord(insert) err = %v", err)
	}
	if record.Status != ToolAuditStatusPassed {
		t.Fatalf("record.Status = %q, want passed", record.Status)
	}
	loaded, ok, err := store.ToolAuditRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolAuditRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolAuditRecord() ok = false, want true")
	}
	if loaded.Status != ToolAuditStatusPassed {
		t.Fatalf("loaded.Status = %q, want passed", loaded.Status)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 1 || loaded.ArtifactRefs[0].Ref != "/tmp/run.sh" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + file ref", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineFingerprint != "sha256:audit-baseline" || loaded.CurrentFingerprint != "sha256:audit-current" {
		t.Fatalf("loaded fingerprints = %q/%q, want persisted audit fingerprints", loaded.BaselineFingerprint, loaded.CurrentFingerprint)
	}
	if loaded.BaselineInstallRef != "workspace:tooling-v1" || loaded.CurrentManifestHash != "sha256:audit-manifest-current" || loaded.DriftSource != ToolDriftSourceManifestDrift {
		t.Fatalf("loaded audit anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	list, err := store.ToolAuditRecords(ToolAuditStatusPassed, 10)
	if err != nil {
		t.Fatalf("ToolAuditRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolAuditRecords(passed) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 {
		t.Fatalf("ToolAuditRecords traceability = (%q, %#v), want persisted rationale + refs", list[0].Rationale, list[0].ArtifactRefs)
	}
}

func TestToolInstallRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolInstallRecord(ToolInstallRecord{
		ToolName:    "browse_page",
		Installer:   "aphelion",
		InstallRef:  "workspace:tooling-v1",
		Status:      ToolInstallStatusVerified,
		ProbeStatus: ToolProbeStatusPassed,
		ProbeOutput: "self-check ok",
		Rationale:   "Install attested after successful bounded setup and probe.",
		ArtifactRefs: []RecordReference{
			{Kind: "git_commit", Ref: "a3336ad", Label: "traceability slice"},
			{Kind: "telegram_message", Ref: "chat:7:message:1001", Label: "approval prompt"},
		},
		InstalledAt:                  time.Now().UTC(),
		LastProbedAt:                 time.Now().UTC(),
		AttestedAt:                   time.Now().UTC(),
		BaselineFingerprint:          "sha256:install-baseline",
		CurrentFingerprint:           "sha256:install-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v1",
		BaselineManifestHash:         "sha256:install-manifest-baseline",
		CurrentManifestHash:          "sha256:install-manifest-current",
		BaselineWorkspaceFingerprint: "sha256:install-workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:install-workspace-current",
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolInstallRecord(insert) err = %v", err)
	}
	if record.Status != ToolInstallStatusVerified {
		t.Fatalf("record.Status = %q, want verified", record.Status)
	}
	loaded, ok, err := store.ToolInstallRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolInstallRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolInstallRecord() ok = false, want true")
	}
	if loaded.ProbeStatus != ToolProbeStatusPassed {
		t.Fatalf("loaded.ProbeStatus = %q, want passed", loaded.ProbeStatus)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 2 || loaded.ArtifactRefs[0].Kind != "git_commit" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + refs", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineFingerprint != "sha256:install-baseline" || loaded.CurrentFingerprint != "sha256:install-current" {
		t.Fatalf("loaded fingerprints = %q/%q, want persisted install fingerprints", loaded.BaselineFingerprint, loaded.CurrentFingerprint)
	}
	if loaded.BaselineManifestHash != "sha256:install-manifest-baseline" || loaded.CurrentWorkspaceFingerprint != "sha256:install-workspace-current" {
		t.Fatalf("loaded install anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	record.Status = ToolInstallStatusStale
	record.ProbeStatus = ToolProbeStatusFailed
	record.ProbeOutput = "missing shared libs"
	record.Rationale = "Workspace drift invalidated the previous verification."
	record.ArtifactRefs = []RecordReference{{Kind: "file_path", Ref: "tool-manifest.json", Label: "manifest"}}
	record.StaleReason = "fingerprint drift: baseline=sha256:install-baseline current=sha256:install-current-2"
	record.CurrentFingerprint = "sha256:install-current-2"
	record.DriftSource = ToolDriftSourceWorkspaceDrift
	if _, err := store.UpsertToolInstallRecord(record); err != nil {
		t.Fatalf("UpsertToolInstallRecord(update) err = %v", err)
	}
	list, err := store.ToolInstallRecords(ToolInstallStatusStale, 10)
	if err != nil {
		t.Fatalf("ToolInstallRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolInstallRecords(stale) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 || list[0].ArtifactRefs[0].Ref != "tool-manifest.json" {
		t.Fatalf("ToolInstallRecords traceability = (%q, %#v), want updated rationale + manifest ref", list[0].Rationale, list[0].ArtifactRefs)
	}
	if list[0].StaleReason != record.StaleReason || list[0].CurrentFingerprint != "sha256:install-current-2" || list[0].DriftSource != ToolDriftSourceWorkspaceDrift {
		t.Fatalf("ToolInstallRecords stale diagnostics = (%q, %q, %q), want persisted stale reason + current fingerprint + drift source", list[0].StaleReason, list[0].CurrentFingerprint, list[0].DriftSource)
	}
}

func TestPlanEventsRoundTripAndRehydrateFromLatestEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 78, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "78"}}
	state := PlanState{
		Explanation: "Track long-running work durably.",
		Steps: []PlanStep{
			{Step: "Inspect the current runtime.", Status: PlanStatusInProgress},
			{Step: "Patch the missing event log.", Status: PlanStatusPending},
		},
	}

	if err := store.UpdatePlanStateWithEvent(key, state, PlanEventKindToolUpdated); err != nil {
		t.Fatalf("UpdatePlanStateWithEvent() err = %v", err)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("plan events len = %d, want 1", len(events))
	}
	if events[0].Kind != PlanEventKindToolUpdated {
		t.Fatalf("plan event kind = %q, want %q", events[0].Kind, PlanEventKindToolUpdated)
	}

	rawKind := PlanEventKind("agent.phase.transitioned")
	if err := store.UpdatePlanStateWithEvent(key, state, rawKind); err != nil {
		t.Fatalf("UpdatePlanStateWithEvent(raw kind) err = %v", err)
	}
	events, err = store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents(raw kind) err = %v", err)
	}
	if events[0].Kind != rawKind {
		t.Fatalf("latest raw plan event kind = %q, want canonical raw %q", events[0].Kind, rawKind)
	}
	projected := SemanticPlanEventProjections(events[:1], 5)
	if len(projected) != 0 {
		t.Fatalf("unknown raw plan event projections = %#v, want normalized away as tool_updated", projected)
	}
	if events[0].PlanState.Explanation != state.Explanation {
		t.Fatalf("event explanation = %q, want %q", events[0].PlanState.Explanation, state.Explanation)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != state.Explanation {
		t.Fatalf("rehydrated explanation = %q, want %q", rehydrated.Explanation, state.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("rehydrated steps = %#v, want original state", rehydrated.Steps)
	}

	events, err = store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents(after rehydrate) err = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("plan events len after rehydrate = %d, want >= 2", len(events))
	}
	if events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("latest plan event kind = %q, want %q", events[0].Kind, PlanEventKindRehydrated)
	}
}

func TestPlanStateRehydratesFromTranscriptWhenEventLogMissing(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 79, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "79"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "work through this", TurnIndex: 1},
		{
			Role:     "tool",
			ToolName: "update_plan",
			Content: strings.Join([]string{
				"[PLAN_UPDATED]",
				"active: true",
				"explanation: Recover from transcript state.",
				"- [in_progress] Inspect the relevant files.",
				"- [pending] Patch the bug.",
			}, "\n"),
			TurnIndex: 1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != "Recover from transcript state." {
		t.Fatalf("rehydrated explanation = %q, want transcript-derived explanation", rehydrated.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[1].Status != PlanStatusPending {
		t.Fatalf("rehydrated steps = %#v, want transcript-derived plan", rehydrated.Steps)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) == 0 || events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("plan events = %#v, want transcript rehydration event", events)
	}
}
