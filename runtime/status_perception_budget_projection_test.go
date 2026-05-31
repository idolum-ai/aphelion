//go:build linux

package runtime

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestPerceptionBudgetStatusProjectionFromProviderAttempt(t *testing.T) {
	event := session.ExecutionEvent{
		SessionID:   "telegram:42",
		ChatID:      42,
		Scope:       telegramDMScopeRef(42),
		Seq:         7,
		EventType:   core.ExecutionEventProviderAttemptStarted,
		PayloadJSON: `{"perception_posture":"implementation","perception_total_budget_tokens":96000,"perception_remaining_headroom_tokens":90000,"perception_current_input_tokens":12,"perception_tool_evidence_tokens":34,"perception_admitted_layers":["authority","current_input","tool_evidence"],"perception_suppressed_layers":["rhizome:posture_precision_suppresses_motifs"],"perception_observed_evidence_sources":["media.document_text_extraction","floor_metadata.retained_artifact_context"]}`,
		CreatedAt:   time.Unix(100, 0),
	}
	projection, ok := perceptionBudgetStatusFromProviderAttempt(event)
	if !ok {
		t.Fatal("expected projection")
	}
	if projection.Posture != "implementation" || projection.RemainingHeadroomTokens != 90000 || projection.ToolEvidenceTokens != 34 {
		t.Fatalf("unexpected projection: %#v", projection)
	}
	if got := strings.Join(projection.ObservedEvidenceSources, ","); got != "media.document_text_extraction,floor_metadata.retained_artifact_context" {
		t.Fatalf("observed evidence sources = %q", got)
	}
}

func TestPerceptionBudgetStatusDiagnosticsLine(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := session.SessionKey{ChatID: 9042, UserID: 0, Scope: telegramDMScopeRef(9042)}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventProviderAttemptStarted,
		Stage:       "provider",
		Status:      "started",
		PayloadJSON: `{"perception_posture":"implementation","perception_total_budget_tokens":96000,"perception_remaining_headroom_tokens":91000,"perception_admitted_layers":["authority","current_input","tool_evidence"],"perception_observed_evidence_sources":["media.document_text_extraction"]}`,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}
	rt := &Runtime{store: store}
	lines, err := rt.StatusDiagnostics(9042)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Perception budget:") || !strings.Contains(joined, "media.document_text_extraction") {
		t.Fatalf("diagnostics = %q, want perception budget media evidence", joined)
	}
}
