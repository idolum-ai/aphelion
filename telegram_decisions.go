//go:build linux

package main

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	defaultInterruptTimeout = 30 * time.Second
	defaultStopWordTimeout  = 15 * time.Second

	// User approval prompts should survive normal operator latency on Telegram.
	// Keep busy/interrupt routing short, but give approval-style decisions enough
	// time to be reviewed without silently failing closed.
	defaultUserApprovalTimeout      = 30 * time.Minute
	defaultExecApprovalTimeout      = defaultUserApprovalTimeout
	defaultArtifactRetentionTimeout = defaultUserApprovalTimeout
	defaultMemoryDelegationTimeout  = defaultUserApprovalTimeout
	defaultSnapshotRestoreTimeout   = defaultUserApprovalTimeout
)

type telegramDecisionSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
}

type telegramDecisionKeyboardEditor interface {
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type telegramDecisionKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type telegramDecisionRouter interface {
	Status(chatID int64) core.SessionStatus
	Stop(chatID int64) core.StopResult
	Route(ctx context.Context, msg core.InboundMessage)
}

type telegramDecisionMessageStatusRouter interface {
	StatusForMessage(msg core.InboundMessage) core.SessionStatus
}

type telegramDecisionMessageStopRouter interface {
	StopForMessage(msg core.InboundMessage) core.StopResult
}

type telegramPermanentArtifactKeeper interface {
	KeepTelegramArtifactsPermanently(ctx context.Context, msg core.InboundMessage) error
}

func editDecisionMessageClearingInlineKeyboard(ctx context.Context, sender telegramDecisionSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(telegramDecisionKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}

type telegramDecisionHandler struct {
	sender                   telegramDecisionSender
	router                   telegramDecisionRouter
	broker                   *decision.Broker
	store                    *session.SQLiteStore
	artifactRetentionKeeper  telegramPermanentArtifactKeeper
	interruptTimeout         time.Duration
	stopWordTimeout          time.Duration
	artifactRetentionTimeout time.Duration
}

func newTelegramDecisionHandler(sender telegramDecisionSender, router telegramDecisionRouter, broker *decision.Broker, store *session.SQLiteStore, keepers ...telegramPermanentArtifactKeeper) *telegramDecisionHandler {
	var keeper telegramPermanentArtifactKeeper
	if len(keepers) > 0 {
		keeper = keepers[0]
	}
	return &telegramDecisionHandler{
		sender:                   sender,
		router:                   router,
		broker:                   broker,
		store:                    store,
		artifactRetentionKeeper:  keeper,
		interruptTimeout:         defaultInterruptTimeout,
		stopWordTimeout:          defaultStopWordTimeout,
		artifactRetentionTimeout: defaultArtifactRetentionTimeout,
	}
}

type telegramDecisionSummaryFunc func(context.Context, decision.PendingDecision) string

func newTelegramDecisionBroker(sender telegramDecisionSender, opts ...decision.BrokerOption) *decision.Broker {
	return newTelegramDecisionBrokerWithSummary(sender, nil, opts...)
}

func newTelegramDecisionBrokerWithSummary(sender telegramDecisionSender, summarize telegramDecisionSummaryFunc, opts ...decision.BrokerOption) *decision.Broker {
	return decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		if summarize != nil {
			if summary := strings.TrimSpace(summarize(ctx, pending)); summary != "" {
				text = summary
			}
		}
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		return decision.Delivery{MessageID: msgID}, nil
	}, opts...)
}

func (h *telegramDecisionHandler) HandleCallbackQuery(ctx context.Context, cb telegram.CallbackQuery) error {
	if h == nil || h.sender == nil || h.broker == nil {
		return nil
	}
	if eventID, action, ok := core.DecodeReviewEventCallbackData(cb.Data); ok {
		return h.handleReviewEventCallback(ctx, cb, eventID, action)
	}
	if messageID, ok := decodePermanentArtifactKeepCallbackData(cb.Data); ok {
		return h.handlePermanentArtifactKeepCallback(ctx, cb, messageID)
	}
	id, choice, ok := decision.DecodeCallbackData(cb.Data)
	if !ok {
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	if choice == "expand" || choice == "collapse" {
		pending, found := h.broker.Peek(id)
		resolved := false
		if !found {
			pending, found = h.broker.PeekResolved(id)
			resolved = found
		}
		if !found {
			if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, "This approval is no longer active. Use the newest prompt."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return err
			}
			return nil
		}
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if chatID == 0 {
			chatID = pending.ChatID
		}
		if messageID != 0 {
			expanded := choice == "expand"
			text := renderPendingDecisionSummary(pending)
			rows := inlineButtonRowsExpanded(pending, expanded)
			if expanded {
				text = renderPendingDecisionExpanded(pending)
			}
			if resolved {
				text = approvedDecisionConfirmationText(approvedDecisionConfirmationLabel(pending.Kind), pending.ID, pending.Kind, pending.Details)
				rows = approvedDecisionConfirmationRowsExpanded(pending.ID, pending.Details, expanded)
				if expanded {
					text = renderPendingDecisionExpanded(pending)
				}
			}
			if editor, ok := h.sender.(telegramDecisionKeyboardEditor); ok && len(rows) > 0 {
				if err := editor.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
					return err
				}
			} else if err := editDecisionMessageClearingInlineKeyboard(ctx, h.sender, chatID, messageID, text); err != nil {
				return err
			}
		}
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	answerText := ""
	if !h.broker.Resolve(id, choice) {
		answerText = "This approval is no longer active. Use the newest prompt."
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, answerText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func callbackChatID(cb telegram.CallbackQuery) int64 {
	if cb.Message != nil && cb.Message.Chat != nil {
		return cb.Message.Chat.ID
	}
	return 0
}

func callbackSenderID(cb telegram.CallbackQuery) int64 {
	if cb.From != nil {
		return cb.From.ID
	}
	return 0
}
