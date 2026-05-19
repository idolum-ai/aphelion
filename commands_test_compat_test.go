//go:build linux

package main

import (
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/telegram"
)

const telegramThreadSummaryCallbackData = "thread_summary"
const telegramPageViewList = "list"
const telegramPageViewNonOpen = "nonopen"

func commandRowsContain(rows [][]telegram.InlineButton, label string, callback string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label && button.CallbackData == callback {
				return true
			}
		}
	}
	return false
}

var _ = telegramcommands.TelegramThreadSummaryIngressSurfaceName
