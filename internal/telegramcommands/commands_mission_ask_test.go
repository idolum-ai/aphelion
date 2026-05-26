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

func TestMissionAskAskCallbackQueuesClarificationWork(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionAskPrompt: session.MissionAskPrompt{
			ID:           "mission-ask-1",
			Status:       session.MissionAskStatusPending,
			QuestionText: "Should this belong with the README mission?",
		},
	}
	cb := telegram.CallbackQuery{
		ID:       "cb-mission-ask",
		UpdateID: 9901,
		Data:     core.EncodeMissionAskCallbackData("mission-ask-1", core.MissionAskCallbackAsk),
		From:     &telegram.User{ID: 1001},
		Message:  &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.missionAskPromptSenderID != 1001 || router.missionAskPromptID != "mission-ask-1" {
		t.Fatalf("mission ask lookup sender=%d id=%q, want sender 1001 prompt mission-ask-1", router.missionAskPromptSenderID, router.missionAskPromptID)
	}
	if router.queueMissionClarificationMsg == nil || router.queueMissionClarificationID != "mission-ask-1" {
		t.Fatalf("queued clarification msg=%#v id=%q, want prompt work queued", router.queueMissionClarificationMsg, router.queueMissionClarificationID)
	}
	queued := router.queueMissionClarificationMsg
	if queued.ChatID != 7 || queued.MessageID != 77 || queued.SenderID != 1001 || queued.IngressUpdateID != 9901 {
		t.Fatalf("queued clarification = %#v, want callback target identity", queued)
	}
	if queued.IngressSurface != telegramMissionClarificationIngressSurface {
		t.Fatalf("queued surface = %q, want %q", queued.IngressSurface, telegramMissionClarificationIngressSurface)
	}
	if !strings.Contains(queued.Text, "Should this belong with the README mission?") || !strings.Contains(queued.Text, "mission-ask-1") {
		t.Fatalf("queued text = %q, want question and prompt id", queued.Text)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Clarification queued." {
		t.Fatalf("answers = %#v, want clarification acknowledgement", sender.answers)
	}
}

func TestMissionAskIgnoreCallbackResolvesPrompt(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionAskPrompt: session.MissionAskPrompt{
			ID:           "mission-ask-ignore",
			Status:       session.MissionAskStatusPending,
			QuestionText: "Should this belong with a mission?",
		},
	}
	cb := telegram.CallbackQuery{
		ID:      "cb-mission-ignore",
		Data:    core.EncodeMissionAskCallbackData("mission-ask-ignore", core.MissionAskCallbackIgnore),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.resolveMissionAskStatus != session.MissionAskStatusIgnored {
		t.Fatalf("resolved status = %q, want ignored", router.resolveMissionAskStatus)
	}
	if router.queueMissionClarificationMsg != nil {
		t.Fatalf("queued clarification = %#v, want none for ignore", router.queueMissionClarificationMsg)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Ignored." {
		t.Fatalf("answers = %#v, want ignored acknowledgement", sender.answers)
	}
}

func TestMissionAskCallbackStalePromptDoesNotQueueWork(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionAskPrompt: session.MissionAskPrompt{
			ID:     "mission-ask-stale",
			Status: session.MissionAskStatusResolved,
		},
	}
	cb := telegram.CallbackQuery{
		ID:      "cb-mission-stale",
		Data:    core.EncodeMissionAskCallbackData("mission-ask-stale", core.MissionAskCallbackAsk),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.queueMissionClarificationMsg != nil || router.resolveMissionAskStatus != "" {
		t.Fatalf("queue=%#v resolved=%q, want no work for stale prompt", router.queueMissionClarificationMsg, router.resolveMissionAskStatus)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != staleMissionAskCallback {
		t.Fatalf("answers = %#v, want stale acknowledgement", sender.answers)
	}
}
