//go:build linux

package tool

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

type OperatorRewriteOperation struct {
	ID               string
	RejectedShape    string
	Summary          string
	RequiredAction   string
	RequiredResource string
}

var operatorRewriteOperations = []OperatorRewriteOperation{
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
		RequiredAction:   session.NextActionOperationKindOperatorRewrite,
		RequiredResource: "effect_plan",
	},
}

func OperatorRewriteOperationForRejectedShape(shape string) (OperatorRewriteOperation, bool) {
	shape = strings.ToLower(strings.TrimSpace(shape))
	if shape == "" {
		return OperatorRewriteOperation{}, false
	}
	for _, op := range operatorRewriteOperations {
		if strings.ToLower(strings.TrimSpace(op.RejectedShape)) == shape {
			return op, true
		}
	}
	return OperatorRewriteOperation{}, false
}

func OperatorRewriteOperations() []OperatorRewriteOperation {
	out := make([]OperatorRewriteOperation, len(operatorRewriteOperations))
	copy(out, operatorRewriteOperations)
	return out
}
