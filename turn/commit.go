//go:build linux

package turn

// CommitMode names the intended ordering between durable write and outward
// delivery for one turn.
type CommitMode string

const (
	// CommitModePersistThenDeliver mirrors the current production shape: persist
	// the visible scene and floor sidecar first, then attempt outbound delivery.
	CommitModePersistThenDeliver CommitMode = "persist_then_deliver"
)

// CommitPlan is the explicit commit ordering the engine intends to apply.
type CommitPlan struct {
	Mode   CommitMode
	Reason string
}

// CommitRequest is the durable handoff from the turn engine to persistence.
type CommitRequest struct {
	Request Request
	Result  *Result
	Plan    CommitPlan
}

// CommitResult records the machine-visible outcome of persistence.
type CommitResult struct {
	Persisted bool
}

// DefaultCommitPlan returns the default commit ordering policy.
func DefaultCommitPlan(policy Policy) CommitPlan {
	_ = policy
	return CommitPlan{
		Mode:   CommitModePersistThenDeliver,
		Reason: "visible_scene_and_floor_should_be_durable_before_delivery",
	}
}
