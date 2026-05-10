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

func TestRenderStartupRecoveryRequestIncludesMissionEvidenceBeforeRunFacts(t *testing.T) {
	t.Parallel()

	text := renderStartupRecoveryRequest([]session.TurnRun{{
		ID:          43,
		Kind:        session.TurnRunKindInteractive,
		ChatID:      1001,
		StartedAt:   time.Date(2026, time.April, 14, 2, 0, 0, 0, time.UTC),
		RequestText: "restart the service",
	}}, startupRecoveryMissionEvidence{
		PendingHandoffs: []session.MissionHandoff{{
			ID:                   "handoff-restart",
			MissionID:            "mission-release",
			OperationID:          "op-release",
			PlannedAction:        "restart aphelion.service",
			ExpectedEvidenceJSON: `["systemctl status","doctor"]`,
			RecoveryQuestion:     "Did restart verification pass?",
			Status:               "pending",
		}},
		RecentResults: []session.MissionResult{{
			ID:               "result-build",
			HandoffID:        "handoff-build",
			MissionID:        "mission-release",
			OperationID:      "op-release",
			Status:           "completed",
			EvidenceRefsJSON: `["tes:build"]`,
			Summary:          "build completed",
		}},
	})

	for _, needle := range []string{
		"Mission handoff/result evidence:",
		"pending_handoffs:",
		"handoff_id=handoff-restart",
		`planned_action="restart aphelion.service"`,
		"recent_results:",
		"result_id=result-build",
		`summary="build completed"`,
		"- run_id=43",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("renderStartupRecoveryRequest() = %q, want substring %q", text, needle)
		}
	}
	if strings.Index(text, "Mission handoff/result evidence:") > strings.Index(text, "- run_id=43") {
		t.Fatalf("renderStartupRecoveryRequest() = %q, want mission evidence before run facts", text)
	}
}
