//go:build linux

package runtime

import (
	"strings"
	"time"
)

type statusTurnPhase struct {
	Phase     string
	Summary   string
	UpdatedAt time.Time
}

func (r *Runtime) markChatTurnPhase(chatID int64, phase string, summary string) {
	if r == nil || chatID == 0 {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return
	}
	r.statusStageMu.Lock()
	defer r.statusStageMu.Unlock()
	if r.statusStageByChat == nil {
		r.statusStageByChat = make(map[int64]statusTurnPhase)
	}
	r.statusStageByChat[chatID] = statusTurnPhase{
		Phase:     phase,
		Summary:   strings.TrimSpace(summary),
		UpdatedAt: time.Now().UTC(),
	}
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
