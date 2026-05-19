//go:build linux

package telegramcommands

import (
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func continuationCallbackAuthorizationFailure(router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, state session.ContinuationState) string {
	senderID := callbackSenderID(cb)
	if senderID <= 0 || !router.CanRestart(senderID) {
		return "Continuation controls are available to Telegram admins only."
	}
	if chatID == 0 || messageID == 0 {
		return staleContinuationCallbackText
	}
	state = session.NormalizeContinuationState(state)
	if state.DecisionMessageID > 0 && state.DecisionMessageID != messageID {
		return staleContinuationCallbackText
	}
	return ""
}
