//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestTelegramPageCallbackDataIsStrictAndCompact(t *testing.T) {
	t.Parallel()

	data := encodeTelegramPageCallbackData(telegramPageSurfaceThreads, telegramPageViewList, 3)
	if data == "" || len(data) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("page callback data = %q len=%d, want non-empty compact callback", data, len(data))
	}
	req, ok := decodeTelegramPageCallbackData(data)
	if !ok || req.Surface != telegramPageSurfaceThreads || req.View != telegramPageViewList || req.Page != 3 {
		t.Fatalf("decodeTelegramPageCallbackData(%q) = %#v ok=%t, want threads/list page 3", data, req, ok)
	}
	for _, invalid := range []string{
		"page:threads:trace:1",
		"page:agents:list:0",
		"page:health:list:1",
		"page:unknown:list:1",
		"threads:list:1",
	} {
		if _, ok := decodeTelegramPageCallbackData(invalid); ok {
			t.Fatalf("decodeTelegramPageCallbackData(%q) ok=true, want false", invalid)
		}
	}
}

func TestTelegramPageNavigationRowsClampAndNavigate(t *testing.T) {
	t.Parallel()

	_, info := telegramPageItems([]int{1, 2, 3, 4, 5, 6, 7}, 9, 3)
	if info.Page != 3 || info.PageCount != 3 || info.Start != 6 || info.End != 7 {
		t.Fatalf("page info = %#v, want clamped final page", info)
	}
	rows := telegramPageNavigationRows(info, telegramPageSurfaceThreads, telegramPageViewList)
	if !commandRowsContain(rows, "Prev", "page:threads:list:2") ||
		!commandRowsContain(rows, "Page 3/3", "page:threads:list:3") ||
		commandRowsContain(rows, "Next", "page:threads:list:4") {
		t.Fatalf("rows = %#v, want prev/current and no next", rows)
	}
}

func TestThreadsCommandPaginatesThreadList(t *testing.T) {
	t.Parallel()

	threads := make([]session.TelegramThread, 0, 8)
	for i := 1; i <= 8; i++ {
		threads = append(threads, session.TelegramThread{
			ChatID:      1001,
			ThreadID:    int64(i),
			Status:      session.TelegramThreadStatusOpen,
			CreatedText: fmt.Sprintf("side task %d", i),
		})
	}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: threads}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 3003,
		Text:      "/threads",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/threads) err = %v", err)
	}
	if !handled || len(sender.inline) != 1 {
		t.Fatalf("handled=%t inline=%#v, want one paged thread panel", handled, sender.inline)
	}
	text := sender.inline[0].text
	if !strings.Contains(text, "Page 1 of 2") || !strings.Contains(text, "thread 6: open") || strings.Contains(text, "thread 7: open") {
		t.Fatalf("thread panel text = %q, want first page only", text)
	}
	if !commandRowsContain(sender.inline[0].rows, "Next", "page:threads:list:2") ||
		!commandRowsContain(sender.inline[0].rows, "Absorb 6", "thread_absorb:6") ||
		commandRowsContain(sender.inline[0].rows, "Absorb 7", "thread_absorb:7") {
		t.Fatalf("thread rows = %#v, want page-local absorb buttons and next", sender.inline[0].rows)
	}
}

func TestThreadsPageCallbackRendersRequestedPage(t *testing.T) {
	t.Parallel()

	threads := make([]session.TelegramThread, 0, 8)
	for i := 1; i <= 8; i++ {
		threads = append(threads, session.TelegramThread{
			ChatID:      1001,
			ThreadID:    int64(i),
			Status:      session.TelegramThreadStatusOpen,
			CreatedText: fmt.Sprintf("side task %d", i),
		})
	}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: threads}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:   "cb-page-threads",
		From: &telegram.User{ID: 2002},
		Data: "page:threads:list:2",
		Message: &telegram.Message{
			MessageID: 3003,
			Chat:      &telegram.Chat{ID: 1001, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(page threads) err = %v", err)
	}
	if !handled || len(sender.editInline) != 1 {
		t.Fatalf("handled=%t editInline=%#v, want edited page", handled, sender.editInline)
	}
	text := sender.editInline[0].text
	if !strings.Contains(text, "Page 2 of 2") || !strings.Contains(text, "thread 7: open") || strings.Contains(text, "thread 6: open") {
		t.Fatalf("thread page text = %q, want second page only", text)
	}
	if !commandRowsContain(sender.editInline[0].rows, "Prev", "page:threads:list:1") ||
		!commandRowsContain(sender.editInline[0].rows, "Absorb 8", "thread_absorb:8") {
		t.Fatalf("thread page rows = %#v, want prev and second-page absorb", sender.editInline[0].rows)
	}
}

func TestDurableAgentsCommandPaginatesAgentButtons(t *testing.T) {
	t.Parallel()

	agents := make([]core.DurableAgentStatusSnapshot, 0, 7)
	for i := 1; i <= 7; i++ {
		agents = append(agents, core.DurableAgentStatusSnapshot{
			AgentID:     fmt.Sprintf("child-%d", i),
			ChannelKind: "telegram_dm",
			Status:      "active",
			Health:      "ok",
		})
	}
	sender := &stubCommandSender{}
	router := &stubCommandRouter{canRestart: true, durableAgentsList: agents}
	handled, err := handleTelegramCommand(context.Background(), sender, router, core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/agents",
	})
	if err != nil {
		t.Fatalf("handleTelegramCommand(/agents) err = %v", err)
	}
	if !handled || len(sender.inline) != 1 {
		t.Fatalf("handled=%t inline=%#v, want one agents panel", handled, sender.inline)
	}
	if got := sender.inline[0].text; !strings.Contains(got, "page 1 of 2") || !strings.Contains(got, "5. child-5") || strings.Contains(got, "child-6") {
		t.Fatalf("agents text = %q, want first page details", got)
	}
	if !commandRowsContain(sender.inline[0].rows, "Chat 5", "agents:start:child-5") ||
		commandRowsContain(sender.inline[0].rows, "Chat 6", "agents:start:child-6") ||
		!commandRowsContain(sender.inline[0].rows, "Next", "page:agents:list:2") {
		t.Fatalf("agents rows = %#v, want first-page chat buttons and next", sender.inline[0].rows)
	}
}

func TestHealthTraceReadMoreUsesPagedSections(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:                    91,
				Status:                "running",
				Kind:                  "interactive",
				RequestText:           "debug this run",
				LastToolName:          "exec",
				LastToolPreview:       `{"command":"curl -fsS https://api.github.com/zen"}`,
				LastToolResultPreview: "stdout: Keep it logically awesome.",
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-debug-more",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: encodeHealthCallbackData(healthActionTraceMore),
		Message: &telegram.Message{
			MessageID: 201,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled || len(sender.editInline) != 1 || len(sender.msgs) != 0 {
		t.Fatalf("handled=%t editInline=%#v msgs=%#v, want one paged edit and no chunk messages", handled, sender.editInline, sender.msgs)
	}
	got := sender.editInline[0].text
	if !strings.Contains(got, "Health Trace") || !strings.Contains(got, "section 1 of 2") || !strings.Contains(got, "Status Scope: chat") || strings.Contains(got, "Trace Chat:") {
		t.Fatalf("trace page = %q, want first section only", got)
	}
	if !commandRowsContain(sender.editInline[0].rows, "Next", "page:health:trace:2") ||
		!commandRowsContain(sender.editInline[0].rows, "Summary", encodeHealthCallbackData(healthActionTrace)) {
		t.Fatalf("trace rows = %#v, want next and summary", sender.editInline[0].rows)
	}
}

func TestHealthTracePageCallbackNavigatesSections(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:              91,
				Status:          "running",
				Kind:            "interactive",
				LastToolPreview: `{"command":"curl -fsS https://api.github.com/zen"}`,
			},
		},
		statusReadableSummary: "Chat 7 is working and currently running exec.",
		personaEffort:         "sonnet",
		governorEffort:        "medium",
		canRestart:            false,
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-debug-page",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: "page:health:trace:2",
		Message: &telegram.Message{
			MessageID: 201,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback(page health) err = %v", err)
	}
	if !handled || len(sender.editInline) != 1 {
		t.Fatalf("handled=%t editInline=%#v, want one paged edit", handled, sender.editInline)
	}
	got := sender.editInline[0].text
	if !strings.Contains(got, "section 2 of 2") || !strings.Contains(got, "Trace Chat:") || !strings.Contains(got, "Last Exec Command: \"curl -fsS https://api.github.com/zen\"") {
		t.Fatalf("trace page = %q, want chat trace section", got)
	}
	if !commandRowsContain(sender.editInline[0].rows, "Prev", "page:health:trace:1") ||
		!commandRowsContain(sender.editInline[0].rows, "Summary", encodeHealthCallbackData(healthActionTrace)) {
		t.Fatalf("trace rows = %#v, want prev and summary", sender.editInline[0].rows)
	}
}
