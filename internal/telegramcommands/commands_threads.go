//go:build linux

package telegramcommands

import (
	"context"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	telegramThreadCallbackPrefix         = "thread_absorb:"
	telegramThreadPromoteCallbackPrefix  = "thread_promote:"
	telegramThreadDetailCallbackPrefix   = "thread_detail:"
	telegramThreadBackCallbackData       = "thread_back"
	telegramThreadPromotionReadyPrefix   = "thread_promo_ready:"
	telegramThreadPromotionCancelPrefix  = "thread_promo_cancel:"
	telegramThreadPromotionRefreshPrefix = "thread_promo_refresh:"
	telegramThreadSummaryCallbackData    = "thread_summary"
	telegramThreadsPageSize              = 6
)

var telegramThreadPrefixPattern = regexp.MustCompile(`(?is)^\(\s*thread\s+([0-9]+)\s*\)\s*`)

type commandThreadRouter interface {
	CreateTelegramThread(ctx context.Context, msg core.InboundMessage) (session.TelegramThread, error)
	StartTelegramThreadTarget(ctx context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error)
	RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error
	TargetTelegramThreadMessage(ctx context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error)
	TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error)
	TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error)
	TelegramThreads(chatID int64) ([]session.TelegramThread, error)
	QueueTelegramThreadSummary(ctx context.Context, msg core.InboundMessage) (string, error)
	PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error)
	PrepareTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error)
	CancelTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error)
	SupersedeTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error)
	AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error)
}

type commandThreadCallbackRecorder interface {
	RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error
	ClearTelegramThreadCallbackMessage(chatID int64, messageID int64, surface string) error
}

type telegramThreadUserError string

func (e telegramThreadUserError) Error() string {
	return string(e)
}
func handleTelegramThreadCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, command string) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return sendTelegramThreadText(ctx, sender, msg, "Thread controls are unavailable.")
	}
	switch command {
	case "thread":
		text := strings.TrimSpace(telegramCommandArgs(msg.Text))
		if text == "" {
			thread, err := threadRouter.CreateTelegramThread(ctx, msg)
			if err != nil {
				if isTelegramThreadUserError(err) {
					return sendTelegramThreadText(ctx, sender, msg, err.Error())
				}
				return true, err
			}
			return sendTelegramThreadGuide(ctx, sender, threadRouter, msg, thread)
		}
		return false, nil
	case "threads":
		view := telegramThreadsViewFromArgs(telegramCommandArgs(msg.Text))
		threads, err := threadRouter.TelegramThreads(msg.ChatID)
		if err != nil {
			return true, err
		}
		return sendTelegramThreadsPanel(ctx, sender, msg, threads, view)
	case "absorb":
		threadID, ok := parseTelegramThreadIDArg(telegramCommandArgs(msg.Text))
		if !ok {
			threads, err := threadRouter.TelegramThreads(msg.ChatID)
			if err != nil {
				return true, err
			}
			return sendTelegramThreadsPanel(ctx, sender, msg, threads, telegramPageViewList)
		}
		threadID, err := resolveTelegramThreadTargetID(threadRouter, msg.ChatID, threadID)
		if err != nil {
			return true, err
		}
		text, err := threadRouter.AbsorbTelegramThread(ctx, msg.ChatID, msg.SenderID, threadID)
		if err != nil {
			if isTelegramThreadUserError(err) {
				return sendTelegramThreadText(ctx, sender, msg, err.Error())
			}
			return true, err
		}
		return sendTelegramThreadText(ctx, sender, msg, text)
	default:
		return false, nil
	}
}
