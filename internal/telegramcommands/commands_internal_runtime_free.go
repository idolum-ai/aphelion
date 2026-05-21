//go:build linux

package telegramcommands

import (
	"context"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegrampresentation"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type telegramSessionTarget struct {
	ChatID    int64
	ThreadID  int64
	Scope     session.ScopeRef
	SessionID string
}

func telegramSessionTargetForMessage(msg core.InboundMessage) telegramSessionTarget {
	scope := telegramCommandMessageScope(msg)
	scope = session.NormalizeScopeRef(scope)
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: scope}
	return telegramSessionTarget{
		ChatID:    msg.ChatID,
		ThreadID:  msg.TelegramThreadID,
		Scope:     scope,
		SessionID: session.SessionIDForKey(key),
	}
}

func telegramCommandMessageScope(msg core.InboundMessage) session.ScopeRef {
	if msg.TelegramThreadID > 0 {
		return session.TelegramThreadScopeRef(msg.ChatID, msg.TelegramThreadID)
	}
	if msg.ChatID != 0 {
		return session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: strconv.FormatInt(msg.ChatID, 10)}
	}
	return session.ScopeRef{}
}

func newCommandTurnContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(parent)
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

func callbackMessageID(cb telegram.CallbackQuery) int64 {
	if cb.Message != nil {
		return cb.Message.MessageID
	}
	return 0
}

func continuationCallbackID(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if id := strings.TrimSpace(state.ActionProposal.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ProposalID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ID); id != "" {
		return id
	}
	return strings.TrimSpace(state.DecisionID)
}

func continuationApprovalButtonRows(state session.ContinuationState) [][]telegram.InlineButton {
	state = session.NormalizeContinuationState(state)
	decisionID := continuationCallbackID(state)
	if decisionID == "" {
		return nil
	}
	if continuationCallbackStateExpired(state) {
		return [][]telegram.InlineButton{
			{
				{Text: "Refresh", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskNextLease)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if continuationStateIsPlanBudget(state) && state.Status == session.ContinuationStatusApproved {
		return [][]telegram.InlineButton{
			{
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
		return [][]telegram.InlineButton{
			{
				{Text: "Run", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionResumeEdge)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusPending {
		return [][]telegram.InlineButton{
			{
				{Text: "Start", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveLease)},
				{Text: "Details", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Change", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskEdit)},
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	return [][]telegram.InlineButton{
		{
			{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
		},
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func telegramThreadDisplayPrefixForMessage(msg core.InboundMessage) string {
	return telegrampresentation.PrefixForMessage(msg)
}

func actionListContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func TelegramThreadSummaryIngressSurface() string { return telegramThreadSummaryIngressSurface }

const telegramThreadSummaryIngressSurface = "telegram:callback-work:thread-summary"
const telegramDoctorIngressSurface = "telegram:callback-work:doctor"

var _ = core.TelegramCallbackDataMaxBytes
