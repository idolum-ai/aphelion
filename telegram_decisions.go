//go:build linux

package main

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramdecision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	defaultInterruptTimeout         = telegramdecision.DefaultInterruptTimeout
	defaultStopWordTimeout          = telegramdecision.DefaultStopWordTimeout
	defaultUserApprovalTimeout      = telegramdecision.DefaultUserApprovalTimeout
	defaultExecApprovalTimeout      = telegramdecision.DefaultExecApprovalTimeout
	defaultArtifactRetentionTimeout = telegramdecision.DefaultArtifactRetentionTimeout
	defaultMemoryDelegationTimeout  = telegramdecision.DefaultMemoryDelegationTimeout
	defaultSnapshotRestoreTimeout   = telegramdecision.DefaultSnapshotRestoreTimeout
)

type telegramDecisionSender = telegramdecision.DecisionSender
type telegramDecisionKeyboardEditor = telegramdecision.DecisionKeyboardEditor
type telegramDecisionKeyboardClearer = telegramdecision.DecisionKeyboardClearer
type telegramDecisionRouter = telegramdecision.Router
type telegramDecisionMessageStatusRouter = telegramdecision.MessageStatusRouter
type telegramDecisionMessageStopRouter = telegramdecision.MessageStopRouter
type telegramPermanentArtifactKeeper = telegramdecision.PermanentArtifactKeeper
type telegramDecisionSummaryFunc = telegramdecision.SummaryFunc
type telegramDecisionBrokerUIOptions = telegramdecision.BrokerUIOptions

type telegramDecisionHandler struct {
	*telegramdecision.Handler
	sender                   telegramDecisionSender
	broker                   *decision.Broker
	store                    *session.SQLiteStore
	router                   telegramDecisionRouter
	interruptTimeout         time.Duration
	stopWordTimeout          time.Duration
	artifactRetentionTimeout time.Duration
}

func editDecisionMessageClearingInlineKeyboard(ctx context.Context, sender telegramDecisionSender, chatID int64, messageID int64, text string) error {
	return telegramdecision.EditDecisionMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text)
}

func newTelegramDecisionHandler(sender telegramDecisionSender, router telegramDecisionRouter, broker *decision.Broker, store *session.SQLiteStore, keepers ...telegramPermanentArtifactKeeper) *telegramDecisionHandler {
	inner := telegramdecision.NewHandler(sender, router, broker, store, keepers...)
	return &telegramDecisionHandler{
		Handler:                  inner,
		sender:                   sender,
		broker:                   broker,
		store:                    store,
		router:                   router,
		interruptTimeout:         defaultInterruptTimeout,
		stopWordTimeout:          defaultStopWordTimeout,
		artifactRetentionTimeout: defaultArtifactRetentionTimeout,
	}
}

func newTelegramDecisionBroker(sender telegramDecisionSender, opts ...decision.BrokerOption) *decision.Broker {
	return telegramdecision.NewBroker(sender, opts...)
}

func newTelegramDecisionBrokerWithSummary(sender telegramDecisionSender, summarize telegramDecisionSummaryFunc, ui telegramDecisionBrokerUIOptions, opts ...decision.BrokerOption) *decision.Broker {
	return telegramdecision.NewBrokerWithSummaryAndUI(sender, summarize, ui, opts...)
}

func (h *telegramDecisionHandler) syncDecisionHandler() *telegramdecision.Handler {
	if h == nil {
		return nil
	}
	if h.Handler == nil {
		h.Handler = telegramdecision.NewHandler(h.sender, h.router, h.broker, h.store)
	}
	if h.Handler != nil {
		h.Handler.SetRouter(h.router)
		h.Handler.SetInterruptTimeout(h.interruptTimeout)
		h.Handler.SetStopWordTimeout(h.stopWordTimeout)
		h.Handler.SetArtifactRetentionTimeout(h.artifactRetentionTimeout)
	}
	return h.Handler
}

func (h *telegramDecisionHandler) SetRouter(router telegramDecisionRouter) {
	if h != nil {
		h.router = router
		if h.Handler != nil {
			h.Handler.SetRouter(router)
		}
	}
}

func (h *telegramDecisionHandler) SetInterruptTimeout(timeout time.Duration) {
	if h != nil {
		h.interruptTimeout = timeout
		if h.Handler != nil {
			h.Handler.SetInterruptTimeout(timeout)
		}
	}
}

func (h *telegramDecisionHandler) SetStopWordTimeout(timeout time.Duration) {
	if h != nil {
		h.stopWordTimeout = timeout
		if h.Handler != nil {
			h.Handler.SetStopWordTimeout(timeout)
		}
	}
}

func (h *telegramDecisionHandler) SetArtifactRetentionTimeout(timeout time.Duration) {
	if h != nil {
		h.artifactRetentionTimeout = timeout
		if h.Handler != nil {
			h.Handler.SetArtifactRetentionTimeout(timeout)
		}
	}
}

func (h *telegramDecisionHandler) InterruptTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.interruptTimeout
}

func (h *telegramDecisionHandler) StopWordTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.stopWordTimeout
}

func (h *telegramDecisionHandler) ArtifactRetentionTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.artifactRetentionTimeout
}

func (h *telegramDecisionHandler) HandleBusyMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return false, nil
	}
	return inner.HandleBusyMessage(ctx, msg)
}

func (h *telegramDecisionHandler) HandleArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return false, nil
	}
	return inner.HandleArtifactRetentionMessage(ctx, msg)
}

func (h *telegramDecisionHandler) ResumePendingBusyDecision(ctx context.Context, ownerKey string, result decision.Result) error {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return nil
	}
	return inner.ResumePendingBusyDecision(ctx, ownerKey, result)
}

func (h *telegramDecisionHandler) resumePendingBusyDecision(ctx context.Context, ownerKey string, result decision.Result) error {
	return h.ResumePendingBusyDecision(ctx, ownerKey, result)
}

func (h *telegramDecisionHandler) ResumePendingArtifactRetention(ctx context.Context, ownerKey string, result decision.Result) error {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return nil
	}
	return inner.ResumePendingArtifactRetention(ctx, ownerKey, result)
}

func (h *telegramDecisionHandler) resumePendingArtifactRetention(ctx context.Context, ownerKey string, result decision.Result) error {
	return h.ResumePendingArtifactRetention(ctx, ownerKey, result)
}

func (h *telegramDecisionHandler) ReconcileRestartLoadedDecisions(ctx context.Context) error {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return nil
	}
	return inner.ReconcileRestartLoadedDecisions(ctx)
}

func (h *telegramDecisionHandler) DecisionResumeStatus(msg core.InboundMessage, surface string) (telegramDecisionResumeStatus, error) {
	inner := h.syncDecisionHandler()
	if inner == nil {
		return telegramDecisionResumeMissing, nil
	}
	return inner.DecisionResumeStatus(msg, surface)
}

func (h *telegramDecisionHandler) decisionResumeStatus(msg core.InboundMessage, surface string) (telegramDecisionResumeStatus, error) {
	return h.DecisionResumeStatus(msg, surface)
}

func (h *telegramDecisionHandler) HandleCallbackQuery(ctx context.Context, cb telegram.CallbackQuery) error {
	if eventID, action, ok := core.DecodeReviewEventCallbackData(cb.Data); ok {
		return h.handleReviewEventCallback(ctx, cb, eventID, action)
	}
	if h == nil || h.Handler == nil {
		return nil
	}
	return h.Handler.HandleCallbackQuery(ctx, cb)
}

func callbackChatID(cb telegram.CallbackQuery) int64   { return telegramdecision.CallbackChatID(cb) }
func callbackSenderID(cb telegram.CallbackQuery) int64 { return telegramdecision.CallbackSenderID(cb) }
func callbackMessageID(cb telegram.CallbackQuery) int64 {
	return telegramdecision.CallbackMessageID(cb)
}
