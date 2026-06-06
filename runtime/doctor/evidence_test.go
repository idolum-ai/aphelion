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
