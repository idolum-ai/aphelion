//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

// interactiveTurnState keeps mutable execution/render state for one interactive
// turn so ports can consume explicit stage outputs without reaching into
// coordinator-owned caches.
type interactiveTurnState struct {
	sess              *session.Session
	lastRunID         int64
	lastGovernor      *turn.GovernorResult
	lastFaceAwareness prompt.RuntimeAwareness
	replyWithVoiceOn  bool
}

func newInteractiveTurnState(sess *session.Session) *interactiveTurnState {
	return &interactiveTurnState{sess: sess}
}

func (s *interactiveTurnState) applyExecution(output turnCoordinatorExecuteOutput, preferVoice bool) {
	if s == nil {
		return
	}
	if output.Sess != nil {
		s.sess = output.Sess
	}
	s.lastRunID = output.RunID
	s.lastGovernor = output.GovernorResult
	s.lastFaceAwareness = output.LastFaceAwareness
	s.replyWithVoiceOn = preferVoice && !governorHasMedia(output.GovernorResult)
}

func (s *interactiveTurnState) session() *session.Session {
	if s == nil {
		return nil
	}
	return s.sess
}

func (s *interactiveTurnState) turnRunID() int64 {
	if s == nil {
		return 0
	}
	return s.lastRunID
}

func (s *interactiveTurnState) governor() *turn.GovernorResult {
	if s == nil {
		return nil
	}
	return s.lastGovernor
}

func (s *interactiveTurnState) faceAwareness() prompt.RuntimeAwareness {
	if s == nil {
		return prompt.RuntimeAwareness{}
	}
	return s.lastFaceAwareness
}

func (s *interactiveTurnState) replyWithVoice() bool {
	return s != nil && s.replyWithVoiceOn
}

func governorHasMedia(result *turn.GovernorResult) bool {
	if result == nil || result.Turn == nil {
		return false
	}
	return len(result.Turn.Media) > 0
}

func interactiveReviewEventPayload(result *turn.Result) (sceneText string, toolLog []string) {
	if result == nil {
		return "", nil
	}
	sceneText = strings.TrimSpace(result.VisibleReply)
	if result.Turn == nil || len(result.Turn.ToolLog) == 0 {
		return sceneText, nil
	}
	toolLog = append(toolLog, result.Turn.ToolLog...)
	return sceneText, toolLog
}

func interactivePreparedLedgerText(capturedPreparedText string, result *turn.Result) string {
	if result != nil {
		if preparedText := strings.TrimSpace(result.Prepared.LedgerText); preparedText != "" {
			return preparedText
		}
	}
	return strings.TrimSpace(capturedPreparedText)
}
