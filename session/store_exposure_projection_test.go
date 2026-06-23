//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRecordExposureProjectionStoresStructuredPolicyAndProtectedEvidence(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99201, UserID: 1001}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "inspect tool output")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	raw := "stdout:\ngithub_pat_1234567890abcdef\npath: /workspace/credential-slot\n"
	record, err := store.RecordExposureProjection(ExposureProjectionInput{
		Key:          key,
		TurnRunID:    run.ID,
		InvocationID: "turn:projection-test:tool:1",
		ToolName:     "exec",
		Audience:     ExposureAudienceModelPreview,
		Purpose:      ExposurePurposeToolResultModelContext,
		RawText:      raw,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordExposureProjection() err = %v", err)
	}
	if record.Audience != ExposureAudienceModelPreview || record.Purpose != ExposurePurposeToolResultModelContext {
		t.Fatalf("audience/purpose = %s/%s, want model_preview/tool_result_model_context", record.Audience, record.Purpose)
	}
	if record.PolicyRef != ExposureProjectionPolicyToolOutputV1 || record.ProjectionKind != ExposureProjectionProtectedRef {
		t.Fatalf("policy/kind = %s/%s, want tool-output policy protected_ref", record.PolicyRef, record.ProjectionKind)
	}
	if record.Sensitivity != EvidenceRedactionSecret {
		t.Fatalf("sensitivity = %q, want credential-bearing", record.Sensitivity)
	}
	for _, want := range []string{"pattern:github_token", "pattern:credential_metadata"} {
		if !stringListContains(record.SensitivityProvenance, want) {
			t.Fatalf("provenance = %#v, want %s", record.SensitivityProvenance, want)
		}
	}
	if strings.TrimSpace(record.ProtectedEvidenceRef) == "" {
		t.Fatalf("protected evidence ref is empty in %#v", record)
	}
	for _, leaked := range []string{"github_pat_1234567890abcdef", "/workspace/credential-slot", "[EXPOSURE_PROJECTION]", "policy_ref:", "sensitivity:"} {
		if strings.Contains(record.ProjectedText, leaked) {
			t.Fatalf("projected text leaked %q: %s", leaked, record.ProjectedText)
		}
	}

	protected, ok, err := store.EvidenceObject(record.ProtectedEvidenceRef)
	if err != nil || !ok {
		t.Fatalf("EvidenceObject(%s) ok=%t err=%v", record.ProtectedEvidenceRef, ok, err)
	}
	if protected.SourceKind != EvidenceSourceToolOutput || protected.RedactionClass != EvidenceRedactionSecret {
		t.Fatalf("protected evidence = %#v, want credential-bearing tool output", protected)
	}
	if !strings.Contains(protected.PayloadJSON, "github_pat_1234567890abcdef") {
		t.Fatalf("protected evidence payload does not retain raw source fact: %s", protected.PayloadJSON)
	}
	if EvidencePayloadHydrationAllowed(protected.RedactionClass) {
		t.Fatalf("protected evidence redaction class %q should not hydrate to ordinary payload", protected.RedactionClass)
	}

	events, err := store.ExecutionEventsByTurnRun(key, run.ID, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsByTurnRun() err = %v", err)
	}
	payload := ""
	for _, event := range events {
		if event.EventType == core.ExecutionEventExposureProjected {
			payload = event.PayloadJSON
			break
		}
	}
	if payload == "" {
		t.Fatalf("events = %#v, want exposure.projected event", events)
	}
	for _, want := range []string{`"audience":"model_preview"`, `"purpose":"tool_result_model_context"`, `"policy_ref":"` + ExposureProjectionPolicyToolOutputV1 + `"`, `"protected_evidence_ref":"` + record.ProtectedEvidenceRef + `"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("event payload = %s, want %s", payload, want)
		}
	}
	for _, leaked := range []string{"github_pat_1234567890abcdef", "/workspace/credential-slot", "[EXPOSURE_PROJECTION]"} {
		if strings.Contains(payload, leaked) {
			t.Fatalf("event payload leaked %q: %s", leaked, payload)
		}
	}
}

func TestMigratesSchemaV75ToV76ExposureProjectionLedger(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v75.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v75 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (75)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v75 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v75 db: %v", err)
	}
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v75) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteColumn(t, store.db, "exposure_projection_events", "audience")
	assertSQLiteColumn(t, store.db, "exposure_projection_events", "sensitivity_provenance_json")
	assertSQLiteColumn(t, store.db, "exposure_projection_events", "protected_evidence_ref")
}
