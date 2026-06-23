//go:build linux

package tool

import "strings"

type TypedRepairOperation struct {
	ID               string
	RejectedShape    string
	Summary          string
	RequiredAction   string
	RequiredResource string
}

var typedRepairOperations = []TypedRepairOperation{
	{
		ID:               "materialize_child_slot",
		RejectedShape:    "path-qualified executable",
		Summary:          "Materialize or adjust a child-local configuration slot through a typed file operation.",
		RequiredAction:   "write_child_slot",
		RequiredResource: "child_local_config",
	},
	{
		ID:               "apply_child_config_patch",
		RejectedShape:    "interpreter repair",
		Summary:          "Apply a bounded child-local configuration patch without granting interpreter-shaped shell authority.",
		RequiredAction:   "apply_config_patch",
		RequiredResource: "child_local_config",
	},
	{
		ID:               "split_multi_effect_repair",
		RejectedShape:    "multi-effect repair",
		Summary:          "Split a compound repair into separate authorized effect steps.",
		RequiredAction:   "split_effect_plan",
		RequiredResource: "effect_plan",
	},
}

func TypedRepairOperationForRejectedShape(shape string) (TypedRepairOperation, bool) {
	shape = strings.ToLower(strings.TrimSpace(shape))
	if shape == "" {
		return TypedRepairOperation{}, false
	}
	for _, op := range typedRepairOperations {
		if strings.ToLower(strings.TrimSpace(op.RejectedShape)) == shape {
			return op, true
		}
	}
	return TypedRepairOperation{}, false
}

func TypedRepairOperations() []TypedRepairOperation {
	out := make([]TypedRepairOperation, len(typedRepairOperations))
	copy(out, typedRepairOperations)
	return out
}
