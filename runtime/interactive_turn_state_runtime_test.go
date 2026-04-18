//go:build linux

package runtime

import (
	"reflect"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestInteractiveReviewEventPayloadUsesVisibleReplyAndToolLog(t *testing.T) {
	t.Parallel()

	result := &turn.Result{
		VisibleReply: "  rendered idolum scene  ",
		Turn: &core.TurnResult{
			ToolLog: []string{"tool:fetch", "tool:write"},
		},
	}

	sceneText, toolLog := interactiveReviewEventPayload(result)
	if sceneText != "rendered idolum scene" {
		t.Fatalf("sceneText = %q, want rendered idolum scene", sceneText)
	}
	if !reflect.DeepEqual(toolLog, []string{"tool:fetch", "tool:write"}) {
		t.Fatalf("toolLog = %#v, want tool log copy", toolLog)
	}

	toolLog[0] = "mutated"
	if result.Turn.ToolLog[0] != "tool:fetch" {
		t.Fatalf("result turn tool log mutated via payload copy: %#v", result.Turn.ToolLog)
	}
}

func TestInteractiveReviewEventPayloadHandlesNilResult(t *testing.T) {
	t.Parallel()

	sceneText, toolLog := interactiveReviewEventPayload(nil)
	if sceneText != "" {
		t.Fatalf("sceneText = %q, want empty", sceneText)
	}
	if len(toolLog) != 0 {
		t.Fatalf("toolLog len = %d, want 0", len(toolLog))
	}
}

func TestInteractiveTurnStateApplyExecutionTracksCrossStageInputs(t *testing.T) {
	t.Parallel()

	initialSession := &session.Session{TurnCount: 1}
	state := newInteractiveTurnState(initialSession)
	updatedSession := &session.Session{TurnCount: 7}
	governor := &turn.GovernorResult{
		Turn: &core.TurnResult{},
	}
	awareness := prompt.RuntimeAwareness{DeliveryMode: "idolum_render"}

	state.applyExecution(turnCoordinatorExecuteOutput{
		Sess:              updatedSession,
		GovernorResult:    governor,
		LastFaceAwareness: awareness,
	}, true)

	if state.session() != updatedSession {
		t.Fatalf("session pointer not updated")
	}
	if state.governor() != governor {
		t.Fatalf("governor pointer not updated")
	}
	if state.faceAwareness().DeliveryMode != "idolum_render" {
		t.Fatalf("face awareness delivery mode = %q, want idolum_render", state.faceAwareness().DeliveryMode)
	}
	if !state.replyWithVoice() {
		t.Fatal("replyWithVoice = false, want true when voice preference active and turn has no media")
	}
}

func TestInteractiveTurnStateApplyExecutionDisablesVoiceWhenMediaPresent(t *testing.T) {
	t.Parallel()

	state := newInteractiveTurnState(&session.Session{})
	state.applyExecution(turnCoordinatorExecuteOutput{
		Sess: &session.Session{},
		GovernorResult: &turn.GovernorResult{
			Turn: &core.TurnResult{
				Media: []core.Media{{Type: "photo"}},
			},
		},
	}, true)

	if state.replyWithVoice() {
		t.Fatal("replyWithVoice = true, want false when turn output contains media")
	}
}

func TestInteractivePreparedLedgerTextPrefersResultPreparedText(t *testing.T) {
	t.Parallel()

	got := interactivePreparedLedgerText("fallback text", &turn.Result{
		Prepared: pipeline.TurnPrepareContract{
			LedgerText: "result prepared text",
		},
	})
	if got != "result prepared text" {
		t.Fatalf("interactivePreparedLedgerText() = %q, want result prepared text", got)
	}
}

func TestInteractivePreparedLedgerTextFallsBackToCapturedPreparedText(t *testing.T) {
	t.Parallel()

	got := interactivePreparedLedgerText("fallback text", &turn.Result{})
	if got != "fallback text" {
		t.Fatalf("interactivePreparedLedgerText() = %q, want fallback text", got)
	}

	got = interactivePreparedLedgerText("fallback text", nil)
	if got != "fallback text" {
		t.Fatalf("interactivePreparedLedgerText(nil) = %q, want fallback text", got)
	}
}
