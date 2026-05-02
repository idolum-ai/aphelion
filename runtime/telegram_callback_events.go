//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	if r == nil || err == nil {
		return
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: decisionScopeRef(chatID)}
	payload := map[string]any{
		"callback_kind": strings.TrimSpace(callbackKind),
		"error":         trimError(err.Error()),
	}
	r.recordExecutionEvent(key, core.ExecutionEventTelegramCallbackFailed, "telegram_callback", "failed", payload, time.Now().UTC())
}
