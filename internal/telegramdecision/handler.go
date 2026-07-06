//go:build linux

package telegramdecision

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	DefaultInterruptTimeout         = 30 * time.Second
	DefaultStopWordTimeout          = 15 * time.Second
	DefaultArtifactRetentionTimeout = DefaultUserApprovalTimeout
)

type Router interface {
	Status(chatID int64) core.SessionStatus
	Stop(chatID int64) core.StopResult
	Route(ctx context.Context, msg core.InboundMessage)
}

type MessageStatusRouter interface {
	StatusForMessage(msg core.InboundMessage) core.SessionStatus
}

type MessageStopRouter interface {
	StopForMessage(msg core.InboundMessage) core.StopResult
}

type PermanentArtifactKeeper interface {
	KeepTelegramArtifactsPermanently(ctx context.Context, msg core.InboundMessage) error
}

type Handler struct {
	sender                   DecisionSender
	router                   Router
	broker                   *decision.Broker
	store                    *session.SQLiteStore
	artifactRetentionKeeper  PermanentArtifactKeeper
	interruptTimeout         time.Duration
	stopWordTimeout          time.Duration
	artifactRetentionTimeout time.Duration
}

func NewHandler(sender DecisionSender, router Router, broker *decision.Broker, store *session.SQLiteStore, keepers ...PermanentArtifactKeeper) *Handler {
	var keeper PermanentArtifactKeeper
	if len(keepers) > 0 {
		keeper = keepers[0]
	}
	return &Handler{
		sender:                   sender,
		router:                   router,
		broker:                   broker,
		store:                    store,
		artifactRetentionKeeper:  keeper,
		interruptTimeout:         DefaultInterruptTimeout,
		stopWordTimeout:          DefaultStopWordTimeout,
		artifactRetentionTimeout: DefaultArtifactRetentionTimeout,
	}
}

func (h *Handler) SetRouter(router Router) {
	if h != nil {
		h.router = router
	}
}
func (h *Handler) SetInterruptTimeout(timeout time.Duration) {
	if h != nil {
		h.interruptTimeout = timeout
	}
}
func (h *Handler) InterruptTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.interruptTimeout
}
func (h *Handler) SetStopWordTimeout(timeout time.Duration) {
	if h != nil {
		h.stopWordTimeout = timeout
	}
}
func (h *Handler) StopWordTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.stopWordTimeout
}
func (h *Handler) SetArtifactRetentionTimeout(timeout time.Duration) {
	if h != nil {
		h.artifactRetentionTimeout = timeout
	}
}
func (h *Handler) ArtifactRetentionTimeout() time.Duration {
	if h == nil {
		return 0
	}
	return h.artifactRetentionTimeout
}

type SummaryFunc func(context.Context, decision.PendingDecision) string

func NewBroker(sender DecisionSender, opts ...decision.BrokerOption) *decision.Broker {
	return NewBrokerWithSummary(sender, nil, opts...)
}

func NewBrokerWithSummary(sender DecisionSender, summarize SummaryFunc, opts ...decision.BrokerOption) *decision.Broker {
	return NewBrokerWithSummaryAndUI(sender, summarize, BrokerUIOptions{}, opts...)
}

func NewBrokerWithSummaryAndUI(sender DecisionSender, summarize SummaryFunc, ui BrokerUIOptions, opts ...decision.BrokerOption) *decision.Broker {
	return decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := RenderPendingDecisionSummary(pending)
		if summarize != nil {
			if summary := strings.TrimSpace(summarize(ctx, pending)); summary != "" {
				text = summary
			}
		}
		text = prefixDecisionText(pending, ui.ThreadResolver, text)
		rows, offerID, err := initialDecisionRows(ctx, pending, InlineButtonRows(pending), ui.ApprovalWindows)
		if err != nil {
			return decision.Delivery{}, err
		}
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, rows, telegramcommands.ReplyToMessageID(pending.MessageID))
		if err != nil {
			if offerID != "" {
				_ = closeApprovalWindowOffer(ctx, ui.ApprovalWindows, offerID)
			}
			return decision.Delivery{}, err
		}
		if err := recordDecisionCallbackThread(ctx, ui.ThreadRecorder, pending, msgID); err != nil {
			return decision.Delivery{}, err
		}
		return decision.Delivery{MessageID: msgID}, nil
	}, opts...)
}

func initialDecisionRows(ctx context.Context, pending decision.PendingDecision, rows [][]telegram.InlineButton, offerer ApprovalWindowOfferer) ([][]telegram.InlineButton, string, error) {
	if offerer == nil || pending.Kind != decision.KindProposalApproval {
		return rows, "", nil
	}
	offer, ok, err := offerer.CreateApprovalWindowOfferForKey(
		ctx,
		pendingDecisionSessionKey(pending),
		pending.SenderID,
		session.ApprovalWindowOfferSourceDecision,
		pending.ID,
		string(pending.Kind),
	)
	if err != nil || !ok {
		return rows, "", err
	}
	offerRows := telegramcommands.ApprovalWindowEmbeddedOfferRowsForDuration(offer, offerer.DefaultApprovalWindowDuration())
	if len(offerRows) == 0 {
		return rows, offer.ID, nil
	}
	return appendTelegramRows(rows, offerRows), offer.ID, nil
}

type approvalWindowOfferCloser interface {
	CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error
}

func closeApprovalWindowOffer(ctx context.Context, offerer ApprovalWindowOfferer, offerID string) error {
	closer, ok := offerer.(approvalWindowOfferCloser)
	if !ok {
		return nil
	}
	return closer.CloseApprovalWindowOffer(ctx, offerID, 0)
}

func recordDecisionCallbackThread(ctx context.Context, recorder DecisionCallbackThreadRecorder, pending decision.PendingDecision, messageID int64) error {
	_ = ctx
	threadID, ok := pendingDecisionThreadID(pending)
	if !ok || recorder == nil || messageID <= 0 {
		return nil
	}
	return recorder.RecordTelegramCallbackMessageThread(pending.ChatID, messageID, threadID, "decision", time.Now().UTC())
}

func CallbackChatID(cb telegram.CallbackQuery) int64 {
	if cb.Message != nil && cb.Message.Chat != nil {
		return cb.Message.Chat.ID
	}
	return 0
}

func CallbackSenderID(cb telegram.CallbackQuery) int64 {
	if cb.From != nil {
		return cb.From.ID
	}
	return 0
}

func CallbackMessageID(cb telegram.CallbackQuery) int64 {
	if cb.Message != nil {
		return cb.Message.MessageID
	}
	return 0
}

func CallbackDecisionActor(cb telegram.CallbackQuery) decision.CallbackActor {
	return decision.CallbackActor{
		TelegramUserID: CallbackSenderID(cb),
		ChatID:         CallbackChatID(cb),
		MessageID:      CallbackMessageID(cb),
	}
}

func (h *Handler) HandleCallbackQuery(ctx context.Context, cb telegram.CallbackQuery) error {
	if h == nil || h.sender == nil || h.broker == nil {
		return nil
	}
	if messageID, ok := DecodePermanentArtifactKeepCallbackData(cb.Data); ok {
		return h.handlePermanentArtifactKeepCallback(ctx, cb, messageID)
	}
	id, choice, ok := decision.DecodeCallbackData(cb.Data)
	if !ok {
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	actor := CallbackDecisionActor(cb)
	if choice == "expand" || choice == "collapse" {
		pending, found := h.broker.PeekCallback(id, actor)
		resolved := false
		if !found {
			pending, found = h.broker.PeekResolvedCallback(id, actor)
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
			text := prefixDecisionText(pending, h.store, RenderPendingDecisionSummary(pending))
			rows := h.pendingDecisionRowsWithOffer(pending, expanded)
			if expanded {
				text = prefixDecisionText(pending, h.store, RenderPendingDecisionExpanded(pending))
			}
			if resolved {
				text = prefixDecisionText(pending, h.store, ApprovedConfirmationText(ApprovedConfirmationLabel(pending.Kind), pending.ID, pending.Kind, pending.Details))
				rows = h.approvedConfirmationRowsWithOffer(pending, expanded)
				if expanded {
					text = prefixDecisionText(pending, h.store, RenderPendingDecisionExpanded(pending))
				}
			}
			if editor, ok := h.sender.(DecisionKeyboardEditor); ok && len(rows) > 0 {
				if err := editor.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
					return err
				}
			} else if err := EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, chatID, messageID, text); err != nil {
				return err
			}
		}
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	answerText := ""
	pending, pendingFound := h.broker.PeekCallback(id, actor)
	if pendingFound && pending.LoadedFromDurable && !h.CanResumeRestartLoadedDecision(pending) {
		if _, _, err := h.broker.DetachDecision(ctx, id, "restart_loaded_non_resumable"); err != nil {
			return err
		}
		answerText = "This approval is no longer active. Use the newest prompt."
		h.EditStaleDecisionCallback(ctx, cb, answerText)
		if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, answerText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return err
		}
		return nil
	}
	resolution := h.broker.ResolveCallbackDetailed(id, choice, actor)
	if !resolution.Resolved {
		answerText = "This approval is no longer active. Use the newest prompt."
	}
	if err := h.sender.AnswerCallbackQuery(ctx, cb.ID, answerText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	if resolution.Resolved && resolution.LoadedFromDurable {
		if err := h.ResumeRestartLoadedDecision(ctx, resolution.Pending, resolution.Choice); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) approvedConfirmationRowsWithOffer(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	rows := ApprovedConfirmationRowsExpanded(pending.ID, pending.Details, expanded)
	return h.appendApprovalWindowRows(pending, rows)
}

func (h *Handler) pendingDecisionRowsWithOffer(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	rows := InlineButtonRowsExpanded(pending, expanded)
	return h.appendEmbeddedApprovalWindowRows(pending, rows)
}

func (h *Handler) appendApprovalWindowRows(pending decision.PendingDecision, rows [][]telegram.InlineButton) [][]telegram.InlineButton {
	if h == nil || h.store == nil || pending.Kind != decision.KindProposalApproval {
		return rows
	}
	offer, ok, err := h.store.ActiveApprovalWindowOfferForSource(pending.ChatID, session.ApprovalWindowOfferSourceDecision, pending.ID, time.Now().UTC())
	if err != nil || !ok {
		return rows
	}
	return appendTelegramRows(rows, telegramcommands.ApprovalWindowRowsForLiveOfferForDuration(offer, h.approvalWindowDuration()))
}

func (h *Handler) appendEmbeddedApprovalWindowRows(pending decision.PendingDecision, rows [][]telegram.InlineButton) [][]telegram.InlineButton {
	if h == nil || h.store == nil || pending.Kind != decision.KindProposalApproval {
		return rows
	}
	offer, ok, err := h.store.ActiveApprovalWindowOfferForSource(pending.ChatID, session.ApprovalWindowOfferSourceDecision, pending.ID, time.Now().UTC())
	if err != nil || !ok {
		return rows
	}
	return appendTelegramRows(rows, telegramcommands.ApprovalWindowEmbeddedOfferRowsForDuration(offer, h.approvalWindowDuration()))
}

func (h *Handler) approvalWindowDuration() time.Duration {
	if h == nil || h.router == nil {
		return 15 * time.Minute
	}
	if durations, ok := h.router.(interface{ DefaultApprovalWindowDuration() time.Duration }); ok {
		duration := durations.DefaultApprovalWindowDuration()
		if duration <= 0 {
			return 15 * time.Minute
		}
		return duration
	}
	return 15 * time.Minute
}

func (h *Handler) CanResumeRestartLoadedDecision(pending decision.PendingDecision) bool {
	if h == nil || h.store == nil {
		return false
	}
	ownerKey := strings.TrimSpace(pending.OwnerKey)
	if ownerKey == "" {
		return false
	}
	switch pending.Kind {
	case decision.KindInterrupt, decision.KindStopWord:
		_, err := h.store.PendingBusyDecision(ownerKey)
		return err == nil
	case decision.KindArtifactRetention:
		record, err := h.store.PendingArtifactRetention(ownerKey)
		if err != nil || record == nil {
			return false
		}
		msg, err := PendingArtifactRetentionMessage(*record)
		return err == nil && HasArtifactRetentionApprovalCandidates(msg)
	default:
		return false
	}
}

func (h *Handler) ResumeRestartLoadedDecision(ctx context.Context, pending decision.PendingDecision, choice string) error {
	if h == nil {
		return nil
	}
	result := decision.Result{
		DecisionID: pending.ID,
		Choice:     strings.TrimSpace(choice),
		Delivery:   pending.Delivery,
	}
	switch pending.Kind {
	case decision.KindInterrupt, decision.KindStopWord:
		return h.ResumePendingBusyDecision(ctx, pending.OwnerKey, result)
	case decision.KindArtifactRetention:
		return h.ResumePendingArtifactRetention(ctx, pending.OwnerKey, result)
	default:
		return nil
	}
}

func (h *Handler) EditStaleDecisionCallback(ctx context.Context, cb telegram.CallbackQuery, text string) {
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return
	}
	text = h.prefixCallbackMessageText(cb, text)
	_ = EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text)
}

func (h *Handler) prefixCallbackMessageText(cb telegram.CallbackQuery, text string) string {
	if h == nil || h.store == nil || cb.Message == nil || cb.Message.Chat == nil {
		return strings.TrimSpace(text)
	}
	threadID, ok, err := h.store.TelegramThreadIDForReplyMessage(cb.Message.Chat.ID, cb.Message.MessageID)
	if err != nil || !ok {
		return strings.TrimSpace(text)
	}
	return prefixDecisionTextForThread(cb.Message.Chat.ID, threadID, h.store, text)
}
