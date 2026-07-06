//go:build linux

package agent

import (
	"errors"
	"fmt"
	"testing"
)

type projectedFailureErrorForTest struct{}

func (projectedFailureErrorForTest) Error() string {
	return "tool execution failed"
}

func (projectedFailureErrorForTest) ProjectedToolFailure() bool {
	return true
}

func TestToolResultContentRequiresProjectedFailureProvenance(t *testing.T) {
	t.Parallel()

	projected := `{"ok":false,"safe_summary":"safe","failure_class":"tool_error","retry_policy":"reformulate","policy_ref":"session.exposure_projection.tool_output/v1"}`
	content, failed := toolResultContent(projected, errors.New("ordinary failure"))
	if !failed {
		t.Fatal("failed = false, want true")
	}
	if content == projected {
		t.Fatalf("ordinary error with JSON-shaped output was trusted as projected failure: %s", content)
	}

	content, failed = toolResultContent(projected, projectedFailureErrorForTest{})
	if !failed {
		t.Fatal("failed = false for projected failure")
	}
	if content != projected {
		t.Fatalf("projected failure content = %s, want %s", content, projected)
	}
}

func TestToolResultContentTrustsWrappedProjectedFailure(t *testing.T) {
	t.Parallel()

	projected := `{"ok":false,"safe_summary":"sandbox denied","failure_class":"policy_denied","retry_policy":"do_not_retry","policy_ref":"sandbox"}`
	content, failed := toolResultContent(projected, fmt.Errorf("exec failed: %w", projectedFailureErrorForTest{}))
	if !failed {
		t.Fatal("failed = false for wrapped projected failure")
	}
	if content != projected {
		t.Fatalf("projected failure content = %s, want %s", content, projected)
	}
}
