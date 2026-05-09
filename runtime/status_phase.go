//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type statusTurnPhase struct {
	Phase     string
	Summary   string
	UpdatedAt time.Time
}

func (r *Runtime) markSessionTurnPhase(key session.SessionKey, phase string, summary string) {
	if r == nil || key.ChatID == 0 {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return
	}
	summary = strings.TrimSpace(summary)
	now := time.Now().UTC()
	r.recordExecutionEvent(key, core.ExecutionEventTurnStageChanged, phase, "active", map[string]any{
		"phase":   phase,
		"summary": summary,
	}, now)
}

func (r *Runtime) clearChatTurnPhase(chatID int64) {
	_ = r
	_ = chatID
}
