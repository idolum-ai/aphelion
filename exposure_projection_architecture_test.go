//go:build linux

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestArchitectureExposureProjectionLedgerIsStructuredNotInBandHeader(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := session.SessionKey{ChatID: 99301, UserID: 1001}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "project exposure")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	record, err := store.RecordExposureProjection(session.ExposureProjectionInput{
		Key:          key,
		TurnRunID:    run.ID,
		InvocationID: "turn:architecture:tool:1",
		ToolName:     "exec",
		Audience:     session.ExposureAudienceModelPreview,
		Purpose:      session.ExposurePurposeToolResultModelContext,
		RawText:      "stdout:\ngithub_pat_architecture123456\n",
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RecordExposureProjection() err = %v", err)
	}
	if record.Audience != session.ExposureAudienceModelPreview ||
		record.Purpose != session.ExposurePurposeToolResultModelContext ||
		record.PolicyRef == "" ||
		record.Sensitivity != session.EvidenceRedactionSecret ||
		record.ProjectionKind != session.ExposureProjectionProtectedRef ||
		record.ProtectedEvidenceRef == "" ||
		!containsStringForArchitecture(record.SensitivityProvenance, "pattern:github_token") {
		t.Fatalf("record = %#v, want structured audience/purpose/policy/sensitivity/protected ref", record)
	}
	for _, forbidden := range []string{"github_pat_architecture123456", "[EXPOSURE_PROJECTION]", "audience:", "policy_ref:", "sensitivity:"} {
		if strings.Contains(record.ProjectedText, forbidden) {
			t.Fatalf("projected text contains %q: %s", forbidden, record.ProjectedText)
		}
	}

	events, err := store.ExecutionEventsByTurnRun(key, run.ID, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsByTurnRun() err = %v", err)
	}
	var payload string
	for _, event := range events {
		if event.EventType == core.ExecutionEventExposureProjected {
			payload = event.PayloadJSON
			break
		}
	}
	if payload == "" {
		t.Fatalf("events = %#v, want exposure.projected event", events)
	}
	for _, want := range []string{`"audience":"model_preview"`, `"sensitivity":"credential_bearing"`, `"protected_evidence_ref":"` + record.ProtectedEvidenceRef + `"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload = %s, want structured field %s", payload, want)
		}
	}
	if strings.Contains(payload, "github_pat_architecture123456") || strings.Contains(payload, "[EXPOSURE_PROJECTION]") {
		t.Fatalf("projection event leaked raw output or header convention: %s", payload)
	}
}

func containsStringForArchitecture(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
