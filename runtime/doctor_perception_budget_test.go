//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestDoctorPerceptionBudgetIncludesLatestProjection(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7703, UserID: 0, Scope: telegramDMScopeRef(7703)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventProviderAttemptStarted,
		Stage:       "provider",
		Status:      "started",
		PayloadJSON: `{"perception_posture":"implementation","perception_total_budget_tokens":96000,"perception_total_estimated_tokens":1200,"perception_remaining_headroom_tokens":94800,"perception_current_input_tokens":100,"perception_tool_evidence_tokens":55,"perception_admitted_layers":["authority","current_input","tool_evidence"],"perception_suppressed_layers":["rhizome:posture_precision_suppresses_motifs"],"perception_observed_evidence_sources":["media.document_text_extraction"]}`,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(perception) err = %v", err)
	}

	var b strings.Builder
	rt.writeDoctorPerceptionBudget(&b, key, now)
	report := b.String()
	for _, want := range []string{
		`perception_budget_source="execution_events.provider.attempt.started"`,
		`perception_budget_posture="implementation"`,
		`perception_budget_remaining_headroom_tokens="94800"`,
		`perception_budget_admitted_layers="authority,current_input,tool_evidence"`,
		`perception_budget_suppressed_layers="rhizome:posture_precision_suppresses_motifs"`,
		`perception_budget_observed_evidence_sources="media.document_text_extraction"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor perception budget report = %s, want %s", report, want)
		}
	}
}
