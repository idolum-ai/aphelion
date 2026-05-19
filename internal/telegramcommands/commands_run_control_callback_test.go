//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleTelegramCommandCallbackDeliberationStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop: core.StopResult{ActiveCanceled: true, QueuedDropped: true},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     501,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(501, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 240,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want (1,7)", router.stopCalls, router.stopInput)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "Stopped the current turn and cleared queued work for this chat.") {
		t.Fatalf("edited text = %q, want stop summary", got)
	}
}

func TestHandleTelegramCommandCallbackStreamStop(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop:           core.StopResult{ActiveCanceled: true},
		streamControls: map[string]int64{"stream-abc": 7},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeStreamControlCallbackData("stream-abc", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 241,
			Text:      "partial streamed reply...",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.streamStopCalls != 1 || router.streamStopID != "stream-abc" || router.streamStopChatID != 7 {
		t.Fatalf("stream stop = calls:%d id:%q chat:%d, want stream-abc/7", router.streamStopCalls, router.streamStopID, router.streamStopChatID)
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want (1,7)", router.stopCalls, router.stopInput)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Stopping stream." {
		t.Fatalf("answers = %#v, want stopping answer", sender.answers)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "partial streamed reply") || !strings.Contains(got, "Stopping.") {
		t.Fatalf("edited text = %q, want partial reply with stopping marker", got)
	}
}

func TestHandleTelegramCommandCallbackStreamStopTargetsThreadLane(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stopMessageResult: core.StopResult{ActiveCanceled: true},
		streamControls:    map[string]int64{"stream-thread": 7},
		threadReplyOK:     true,
		threadReplyReturn: session.TelegramThread{ChatID: 7, ThreadID: 4, Status: session.TelegramThreadStatusOpen},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-thread-stop",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeStreamControlCallbackData("stream-thread", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 241,
			Text:      "partial streamed reply...",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadReplyChatID != 7 || router.threadReplyMessageID != 241 {
		t.Fatalf("thread reply lookup = chat:%d message:%d, want 7/241", router.threadReplyChatID, router.threadReplyMessageID)
	}
	if router.stopMessage == nil || router.stopMessage.TelegramThreadID != 4 {
		t.Fatalf("stopMessage = %#v, want thread 4 stream stop", router.stopMessage)
	}
	if router.stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want chat-wide stop path unused", router.stopCalls)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "partial streamed reply") || !strings.Contains(got, "Stopping.") {
		t.Fatalf("edited text = %q, want thread stream stop marker", got)
	}
}

func TestHandleTelegramCommandCallbackStreamStopDoesNotTrustThreadPrefixWithoutLedger(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		stop:           core.StopResult{ActiveCanceled: true},
		streamControls: map[string]int64{"stream-prefix-only": 7},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-prefix-only",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeStreamControlCallbackData("stream-prefix-only", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 243,
			Text:      "(thread 4)\n\npartial streamed reply...",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopMessage != nil {
		t.Fatalf("stopMessage = %#v, want no thread-scoped stop from presentation text", router.stopMessage)
	}
	if router.stopCalls != 1 || router.stopInput != 7 {
		t.Fatalf("stop calls/input = (%d,%d), want main chat stop", router.stopCalls, router.stopInput)
	}
}

func TestHandleTelegramCommandCallbackStreamStopRejectsStale(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-stream-stale",
		Data: core.EncodeStreamControlCallbackData("stream-missing", core.StreamControlActionStop),
		Message: &telegram.Message{
			MessageID: 242,
			Text:      "already done",
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 0 {
		t.Fatalf("stop calls = %d, want 0", router.stopCalls)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleStreamCallbackText {
		t.Fatalf("answers = %#v, want stale stream answer", sender.answers)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].text != "already done" {
		t.Fatalf("editClear = %#v, want keyboard clear with original text", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetach(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart: true,
		detach: core.DetachResult{
			ActiveCanceled:           true,
			QueuedDropped:            true,
			ContinuationRevoked:      true,
			PendingDecisionsDetached: 1,
		},
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     777,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-detach",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(777, core.DeliberationControlActionDetach),
		Message: &telegram.Message{
			MessageID: 241,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 7 || router.detachSenderID != 1002 {
		t.Fatalf("detach inputs = (%d,%d), want (7,1002)", router.detachChatID, router.detachSenderID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 plain edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].text; !strings.Contains(got, "Detached this chat from pending work") {
		t.Fatalf("edited text = %q, want detach summary", got)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetachRequiresAdmin(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     778,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-detach-denied",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(778, core.DeliberationControlActionDetach),
		Message: &telegram.Message{
			MessageID: 243,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.detachChatID != 0 {
		t.Fatalf("detachChatID = %d, want 0", router.detachChatID)
	}
	if router.stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want 0", router.stopCalls)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want admin denial", len(sender.answers))
	}
	if sender.answers[0].text != adminDeliberationDetachCallbackText {
		t.Fatalf("answer text = %q, want admin denial", sender.answers[0].text)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want no message edit", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetailsTogglesProgressView(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:            true,
		toggleProgressUpdated: true,
		toggleProgressText:    "Working...\n- exec {\"command\":\"rg progress\"}",
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     901,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-details",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: core.EncodeDeliberationControlCallbackData(901, core.DeliberationControlActionDetails),
		Message: &telegram.Message{
			MessageID: 240,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.toggleProgressChatID != 7 || router.toggleProgressSenderID != 1001 || router.toggleProgressRunID != 901 || !router.toggleProgressDetails {
		t.Fatalf("toggle inputs chat=%d sender=%d run=%d details=%t, want 7/1001/901/details", router.toggleProgressChatID, router.toggleProgressSenderID, router.toggleProgressRunID, router.toggleProgressDetails)
	}
	if router.stopCalls != 0 || router.detachChatID != 0 {
		t.Fatalf("toggle should not stop/detach: stop=%d detach=%d", router.stopCalls, router.detachChatID)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want progress toggle to preserve inline keyboard via runtime", sender.editClear)
	}
}

func TestHandleTelegramCommandCallbackDeliberationDetailsUsesProgressToggleEvenWhenStatusProjectionIsStale(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		canRestart:            true,
		toggleProgressUpdated: true,
		toggleProgressText:    "Working...\n- exec {\"command\":\"rg progress\"}",
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     901,
				ChatID: 7,
				Status: string(session.TurnRunStatusCompleted),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-details-stale-projection",
		From: &telegram.User{ID: 1001, Username: "admin"},
		Data: core.EncodeDeliberationControlCallbackData(901, core.DeliberationControlActionDetails),
		Message: &telegram.Message{
			MessageID: 240,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "Working...\n- Searching files",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.toggleProgressChatID != 7 || router.toggleProgressSenderID != 1001 || router.toggleProgressRunID != 901 || !router.toggleProgressDetails {
		t.Fatalf("toggle inputs chat=%d sender=%d run=%d details=%t, want 7/1001/901/details", router.toggleProgressChatID, router.toggleProgressSenderID, router.toggleProgressRunID, router.toggleProgressDetails)
	}
	if len(sender.editClear) != 0 {
		t.Fatalf("editClear = %#v, want no keyboard-clearing fallback for details toggle", sender.editClear)
	}
	if len(sender.answers) != 1 || strings.TrimSpace(sender.answers[0].text) != "" {
		t.Fatalf("answers = %#v, want one empty callback answer", sender.answers)
	}
}

func TestHandleTelegramCommandCallbackDeliberationSummaryTogglesProgressView(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		toggleProgressUpdated: true,
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     902,
				ChatID: 7,
				Status: string(session.TurnRunStatusRunning),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-summary",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(902, core.DeliberationControlActionSummary),
		Message: &telegram.Message{
			MessageID: 241,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.toggleProgressChatID != 7 || router.toggleProgressRunID != 902 || router.toggleProgressDetails {
		t.Fatalf("toggle inputs chat=%d run=%d details=%t, want 7/902/summary", router.toggleProgressChatID, router.toggleProgressRunID, router.toggleProgressDetails)
	}
}

func TestHandleTelegramCommandCallbackDeliberationRejectsStaleRun(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := stubCommandRouter{
		statusChat: core.ChatStatusSnapshot{
			ChatID: 7,
			LatestTurnRun: &core.TurnRunStatusSnapshot{
				ID:     700,
				ChatID: 7,
				Status: string(session.TurnRunStatusCompleted),
			},
		},
	}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, &router, telegram.CallbackQuery{
		ID:   "cb-delib-stale",
		From: &telegram.User{ID: 1002, Username: "approved"},
		Data: core.EncodeDeliberationControlCallbackData(701, core.DeliberationControlActionStop),
		Message: &telegram.Message{
			MessageID: 242,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
			Text:      "Done.\n- Finished earlier.",
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.stopCalls != 0 {
		t.Fatalf("stopCalls = %d, want 0 for stale callback", router.stopCalls)
	}
	if router.detachChatID != 0 {
		t.Fatalf("detachChatID = %d, want 0 for stale callback", router.detachChatID)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers count = %d, want 1", len(sender.answers))
	}
	if sender.answers[0].text != staleDeliberationCallbackText {
		t.Fatalf("answer text = %q, want stale callback warning", sender.answers[0].text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits count = %d, want 0 for stale callback", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear count = %d, want 1 stale cleanup edit", len(sender.editClear))
	}
	if sender.editClear[0].text != "Done.\n- Finished earlier." {
		t.Fatalf("stale cleanup text = %q, want existing message text", sender.editClear[0].text)
	}
}
