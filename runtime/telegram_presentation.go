//go:build linux

package runtime

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
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
	presentation := telegramOutboundPresentation{ChatID: chatID, ThreadID: threadID}
	if chatID == 0 || threadID <= 0 {
		return presentation
	}
	presentation.ThreadLabel = fmt.Sprint(threadID)
	if r != nil && r.store != nil {
		if thread, ok, err := r.store.TelegramThread(chatID, threadID); err == nil && ok {
			presentation.ThreadLabel = telegramThreadPresentationLabel(thread, threadID)
		}
	}
	presentation.Prefix = telegramThreadPresentationPrefix(presentation.ThreadLabel)
	return presentation
}

func telegramThreadPresentationLabel(thread session.TelegramThread, fallbackID int64) string {
	if thread.Open() && thread.DisplaySlot > 0 {
		return fmt.Sprint(thread.DisplaySlot)
	}
	if !thread.Open() {
		if name := strings.TrimSpace(thread.ArchivedDisplayName); name != "" {
			return name
		}
	}
	if thread.ThreadID > 0 {
		return fmt.Sprint(thread.ThreadID)
	}
	if fallbackID > 0 {
		return fmt.Sprint(fallbackID)
	}
	return "unknown"
}

func telegramThreadPresentationPrefix(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return "(thread " + label + ")"
}

func (r *Runtime) prefixTelegramPresentedText(presentation telegramOutboundPresentation, text string) string {
	return prefixTelegramPresentationText(presentation.Prefix, text)
}

func prefixTelegramPresentationText(prefix string, text string) string {
	text = strings.TrimSpace(text)
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || text == "" {
		return text
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
		return text
	}
	return prefix + "\n\n" + text
}
