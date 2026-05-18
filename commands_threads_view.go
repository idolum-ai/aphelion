//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func normalizeTelegramThreadsView(view string) string {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case telegramPageViewNonOpen, "closed", "non-open":
		return telegramPageViewNonOpen
	default:
		return telegramPageViewList
	}
}

func telegramThreadsViewFromArgs(args string) string {
	first, _ := nextCommandToken(args)
	return normalizeTelegramThreadsView(first)
}

func filterTelegramThreadsForView(threads []session.TelegramThread, view string) []session.TelegramThread {
	view = normalizeTelegramThreadsView(view)
	out := make([]session.TelegramThread, 0, len(threads))
	for _, thread := range threads {
		if view == telegramPageViewNonOpen {
			if !thread.Open() {
				out = append(out, thread)
			}
			continue
		}
		if thread.Open() {
			out = append(out, thread)
		}
	}
	return out
}

func telegramThreadDisplayLabel(thread session.TelegramThread) string {
	if thread.Open() {
		if thread.DisplaySlot > 0 {
			return fmt.Sprintf("thread %d", thread.DisplaySlot)
		}
		return fmt.Sprintf("thread %d", thread.ThreadID)
	}
	if name := strings.TrimSpace(thread.ArchivedDisplayName); name != "" {
		return name
	}
	return fmt.Sprintf("thread %d", thread.ThreadID)
}

func telegramThreadOperatorID(thread session.TelegramThread) int64 {
	if thread.Open() && thread.DisplaySlot > 0 {
		return thread.DisplaySlot
	}
	return thread.ThreadID
}

func resolveTelegramThreadTargetID(router commandThreadRouter, chatID int64, rawThreadID int64) (int64, error) {
	threadID, _, err := resolveTelegramThreadVisibleTarget(router, chatID, rawThreadID)
	return threadID, err
}

func resolveTelegramThreadVisibleTarget(router commandThreadRouter, chatID int64, visibleThreadID int64) (int64, session.TelegramThread, error) {
	if router == nil || chatID == 0 || visibleThreadID <= 0 {
		return 0, session.TelegramThread{}, fmt.Errorf("No open thread %d. Run /threads to see current thread numbers.", visibleThreadID)
	}
	threads, err := router.TelegramThreads(chatID)
	if err != nil {
		return 0, session.TelegramThread{}, err
	}
	for _, thread := range threads {
		if thread.Open() && thread.DisplaySlot == visibleThreadID && thread.ThreadID > 0 {
			return thread.ThreadID, thread, nil
		}
	}
	return 0, session.TelegramThread{}, fmt.Errorf("No open thread %d. Run /threads to see current thread numbers.", visibleThreadID)
}
