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

func (r *Runtime) markChatTurnPhase(chatID int64, phase string, summary string) {
	r.markSessionTurnPhase(
		session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)},
		phase,
		summary,
	)
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
	r.setChatTurnPhase(key.ChatID, statusTurnPhase{
		Phase:     phase,
		Summary:   summary,
		UpdatedAt: now,
	})
	r.recordExecutionEvent(key, core.ExecutionEventTurnStageChanged, phase, "active", map[string]any{
		"phase":   phase,
		"summary": summary,
	}, now)
}

func (r *Runtime) clearChatTurnPhase(chatID int64) {
	if r == nil || chatID == 0 {
		return
	}
	r.statusStageMu.Lock()
	defer r.statusStageMu.Unlock()
	delete(r.statusStageByChat, chatID)
}

func (r *Runtime) chatTurnPhase(chatID int64) (statusTurnPhase, bool) {
	if r == nil || chatID == 0 {
		return statusTurnPhase{}, false
	}
	r.statusStageMu.RLock()
	defer r.statusStageMu.RUnlock()
	phase, ok := r.statusStageByChat[chatID]
	return phase, ok
}

func (r *Runtime) setChatTurnPhase(chatID int64, phase statusTurnPhase) {
	if r == nil || chatID == 0 {
		return
	}
	r.statusStageMu.Lock()
	defer r.statusStageMu.Unlock()
	if r.statusStageByChat == nil {
		r.statusStageByChat = make(map[int64]statusTurnPhase)
	}
	r.statusStageByChat[chatID] = phase
}
