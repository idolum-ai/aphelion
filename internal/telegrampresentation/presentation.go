//go:build linux

package telegrampresentation

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const ThreadDisplayOriginPrefix = "thread_display:"

type ThreadPresentation struct {
	ChatID   int64
	ThreadID int64
	Label    string
	Prefix   string
}

func OriginDetailForDisplaySlot(slot int64) string {
	if slot <= 0 {
		return ""
	}
	return ThreadDisplayOriginPrefix + strconv.FormatInt(slot, 10)
}

func DisplaySlotFromOriginDetail(detail string) (int64, bool) {
	detail = strings.TrimSpace(detail)
	if !strings.HasPrefix(detail, ThreadDisplayOriginPrefix) {
		return 0, false
	}
	slot, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(detail, ThreadDisplayOriginPrefix)), 10, 64)
	if err != nil || slot <= 0 {
		return 0, false
	}
	return slot, true
}

func LabelForThread(thread session.TelegramThread, fallbackID int64) string {
	if thread.Open() && thread.DisplaySlot > 0 {
		return strconv.FormatInt(thread.DisplaySlot, 10)
	}
	if !thread.Open() {
		if name := strings.TrimSpace(thread.ArchivedDisplayName); name != "" {
			return name
		}
	}
	if thread.ThreadID > 0 {
		return strconv.FormatInt(thread.ThreadID, 10)
	}
	if fallbackID > 0 {
		return strconv.FormatInt(fallbackID, 10)
	}
	return "unknown"
}

func ThreadPrefix(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return "(thread " + label + ")"
}

func PresentationForThread(chatID int64, thread session.TelegramThread, fallbackID int64) ThreadPresentation {
	threadID := thread.ThreadID
	if threadID <= 0 {
		threadID = fallbackID
	}
	p := ThreadPresentation{ChatID: chatID, ThreadID: threadID}
	if chatID == 0 || threadID <= 0 {
		return p
	}
	p.Label = LabelForThread(thread, fallbackID)
	p.Prefix = ThreadPrefix(p.Label)
	return p
}

func FallbackPresentation(chatID int64, threadID int64) ThreadPresentation {
	p := ThreadPresentation{ChatID: chatID, ThreadID: threadID}
	if chatID == 0 || threadID <= 0 {
		return p
	}
	p.Label = "unresolved"
	p.Prefix = ThreadPrefix(p.Label)
	return p
}

func PrefixText(prefix string, text string) string {
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

func PrefixForMessage(msg core.InboundMessage) string {
	visibleThreadID := msg.TelegramThreadID
	if slot, ok := DisplaySlotFromOriginDetail(msg.OriginDetail); ok {
		visibleThreadID = slot
	}
	if visibleThreadID <= 0 {
		return ""
	}
	return fmt.Sprintf("(thread %d)\n\n", visibleThreadID)
}
