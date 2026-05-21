//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegrampresentation"
	"github.com/idolum-ai/aphelion/session"
)

type telegramOutboundPresentation struct {
	ChatID      int64
	ThreadID    int64
	ThreadLabel string
	Prefix      string
}

func (r *Runtime) telegramPresentationForMessage(msg core.InboundMessage) telegramOutboundPresentation {
	return r.telegramPresentationForThread(msg.ChatID, msg.TelegramThreadID)
}

func (r *Runtime) telegramPresentationForKey(key session.SessionKey) telegramOutboundPresentation {
	return r.telegramPresentationForThread(key.ChatID, telegramThreadIDFromScope(key.ChatID, key.Scope))
}

func (r *Runtime) telegramPresentationForTurnRun(run session.TurnRun) telegramOutboundPresentation {
	return r.telegramPresentationForKey(session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: run.Scope})
}

func (r *Runtime) telegramPresentationForThread(chatID int64, threadID int64) telegramOutboundPresentation {
	if chatID == 0 || threadID <= 0 {
		return telegramOutboundPresentation{ChatID: chatID, ThreadID: threadID}
	}
	if r != nil && r.store != nil {
		if thread, ok, err := r.store.TelegramThread(chatID, threadID); err == nil && ok {
			return telegramOutboundPresentationFromPure(telegrampresentation.PresentationForThread(chatID, thread, threadID))
		}
	}
	return telegramOutboundPresentationFromPure(telegrampresentation.FallbackPresentation(chatID, threadID))
}

func telegramOutboundPresentationFromPure(p telegrampresentation.ThreadPresentation) telegramOutboundPresentation {
	return telegramOutboundPresentation{
		ChatID:      p.ChatID,
		ThreadID:    p.ThreadID,
		ThreadLabel: p.Label,
		Prefix:      p.Prefix,
	}
}

func (r *Runtime) prefixTelegramPresentedText(presentation telegramOutboundPresentation, text string) string {
	return telegrampresentation.PrefixText(presentation.Prefix, text)
}

func prefixTelegramPresentationText(prefix string, text string) string {
	return telegrampresentation.PrefixText(prefix, text)
}
