//go:build linux

package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestWriteDoctorApprovalBundleWidthSummarizesNarrowEvents(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	events := []session.ExecutionEvent{
		{
			ID:          1,
			ChatID:      9044,
			Seq:         1,
			EventType:   core.ExecutionEventContinuationOffered,
			PayloadJSON: `{"materialized_from":"operation_plan_lease"}`,
			CreatedAt:   now.Add(-time.Minute),
		},
		{
			ID:        2,
			ChatID:    9044,
			Seq:       2,
			EventType: core.ExecutionEventContinuationBundleNarrowed,
			PayloadJSON: `{
				"phase_id":"phase-implement-local",
				"phase_family":"local_workspace",
				"phase_category":"mechanical",
				"materialized_from":"operation_plan_lease",
				"narrow_streak":2,
				"prior_phase_id":"phase-prior-commit"
			}`,
			CreatedAt: now,
		},
	}
	var b strings.Builder
	writeDoctorApprovalBundleWidth(&b, events, 8)
	text := b.String()
	for _, want := range []string{
		"phase_id=phase-implement-local",
		"phase_family=local_workspace",
		"phase_category=mechanical",
		"materialized_from=operation_plan_lease",
		"narrow_streak=2",
		"prior_phase_id=phase-prior-commit",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor bundle width text = %q, want %q", text, want)
		}
	}
}

func TestWriteDoctorContinuationCompileRepairSummarizesEvents(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	events := []session.ExecutionEvent{
		{
			ID:        1,
			ChatID:    9048,
			Seq:       1,
			EventType: core.ExecutionEventContinuationCompileRepaired,
			PayloadJSON: `{
				"repair_kind":"clarify_authority_contract",
				"normalized_reason":"invalid_authority_no_safe_repair",
				"phase_id":"phase-deploy",
				"repair_phase_id":"phase-clarify-authority-contract-for-phase-deploy",
				"blocked_phase_id":"phase-deploy",
				"operation_id":"op-deploy",
				"phase_plan_id":"plan-deploy",
				"materialization_source":"operation_phase_plan",
				"authority_contract_summary":"invalid authority contract",
				"authority_contract_contradiction_count":2
			}`,
			CreatedAt: now,
		},
	}
	var b strings.Builder
	writeDoctorContinuationCompileRepair(&b, events, 8)
	text := b.String()
	for _, want := range []string{
		"type=continuation.compile_repaired",
		"repair_kind=\"clarify_authority_contract\"",
		"normalized_reason=\"invalid_authority_no_safe_repair\"",
		"repair_phase_id=\"phase-clarify-authority-contract-for-phase-deploy\"",
		"blocked_phase_id=\"phase-deploy\"",
		"operation_id=\"op-deploy\"",
		"authority_contract_contradiction_count=2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor compile repair text = %q, want %q", text, want)
		}
	}
}
