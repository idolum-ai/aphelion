//go:build linux

package session

import (
	"strings"
	"testing"
)

func TestSemanticPlanEventProjectionsDropsToolUpdatedAndNormalizesUnknown(t *testing.T) {
	t.Parallel()

	state := PlanState{
		Explanation: "Deliver the next slice.",
		Steps: []PlanStep{
			{Step: "Inspect", Status: PlanStatusCompleted},
			{Step: "Patch", Status: PlanStatusInProgress},
		},
	}
	events := []PlanEvent{
		{Kind: PlanEventKindToolUpdated, PlanState: state},
		{Kind: PlanEventKind("surprising_new_kind"), PlanState: state},
		{Kind: PlanEventKindPhaseEntered, PlanState: state},
	}

	got := SemanticPlanEventProjections(events, 5)
	if len(got) != 1 {
		t.Fatalf("semantic projections len = %d, want one: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "phase.entered") || !strings.Contains(got[0], "current=Patch") {
		t.Fatalf("semantic projection = %#v, want phase/current details", got)
	}
}
