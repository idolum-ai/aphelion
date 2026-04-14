//go:build linux

package turn

import "testing"

func TestDefaultCommitPlanUsesPersistThenDeliver(t *testing.T) {
	plan := DefaultCommitPlan(Policy{Render: true})
	if plan.Mode != CommitModePersistThenDeliver {
		t.Fatalf("plan.Mode = %q, want %q", plan.Mode, CommitModePersistThenDeliver)
	}
	if plan.Reason == "" {
		t.Fatal("plan.Reason empty, want explicit commit rationale")
	}
}
