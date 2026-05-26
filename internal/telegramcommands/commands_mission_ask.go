//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const staleMissionAskCallback = "This mission prompt is no longer active."

func handleMissionAskCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, promptID string, action string) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	senderID := int64(0)
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if targetMsg.ChatID == 0 || targetMsg.MessageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMissionAskCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	prompt, ok, err := router.MissionAskPrompt(ctx, senderID, promptID)
	if err != nil {
		return true, err
	}
	if !ok || session.MissionAskStatusTerminal(prompt.Status) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMissionAskCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	switch action {
	case core.MissionAskCallbackIgnore:
		if _, err := router.ResolveMissionAskPrompt(ctx, senderID, prompt.ID, session.MissionAskStatusIgnored, "operator ignored mission association"); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Ignored."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	case core.MissionAskCallbackAsk:
		queued := targetMsg
		queued.SenderID = senderID
		queued.Text = missionAskClarificationPrompt(prompt)
		queued.IngressSurface = telegramMissionClarificationIngressSurface
		queued.IngressUpdateID = cb.UpdateID
		if err := router.QueueMissionClarification(ctx, queued, prompt.ID); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Clarification queued."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func missionAskClarificationPrompt(prompt session.MissionAskPrompt) string {
	return strings.TrimSpace(prompt.QuestionText + "\n\nAsk me one concise natural question to clarify this mission association. Do not write Mission Ledger state yet. If I answer later, use the Mission Question prompt id " + prompt.ID + " when resolving it.")
}
