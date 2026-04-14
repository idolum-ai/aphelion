//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestRenderStartupRecoveryRequestIncludesLastToolFinishFacts(t *testing.T) {
	t.Parallel()

	text := renderStartupRecoveryRequest([]session.TurnRun{{
		ID:                    42,
		Kind:                  session.TurnRunKindInteractive,
		ChatID:                1001,
		UserID:                0,
		StartedAt:             time.Date(2026, time.April, 14, 1, 0, 0, 0, time.UTC),
		LastActivityAt:        time.Date(2026, time.April, 14, 1, 5, 0, 0, time.UTC),
		RequestText:           "debug the failing turn",
		ToolCallsStarted:      2,
		ToolCallsFinished:     1,
		LastToolName:          "exec",
		LastToolPreview:       `{"command":"go test ./..."}`,
		LastToolResultPreview: "stdout:\npartial output",
		LastToolError:         "exit status 1",
	}})

	for _, needle := range []string{
		"tool_calls_finished=1",
		"last_tool_result_preview=stdout:\npartial output",
		`last_tool_error="exit status 1"`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("renderStartupRecoveryRequest() = %q, want substring %q", text, needle)
		}
	}
}
