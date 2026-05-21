//go:build linux

package telegramdecision

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegrampresentation"
	"github.com/idolum-ai/aphelion/session"
)

type DecisionThreadResolver interface {
	TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error)
}

type DecisionCallbackThreadRecorder interface {
	RecordTelegramCallbackMessageThread(chatID int64, messageID int64, threadID int64, surface string, at time.Time) error
}

type BrokerUIOptions struct {
	ApprovalWindows ApprovalWindowOfferer
	ThreadResolver  DecisionThreadResolver
	ThreadRecorder  DecisionCallbackThreadRecorder
}

func pendingDecisionSessionKey(pending decision.PendingDecision) session.SessionKey {
	scope := session.NormalizeScopeRef(session.ScopeRef{
		Kind:           session.ScopeKind(pending.ScopeKind),
		ID:             pending.ScopeID,
		DurableAgentID: pending.DurableAgentID,
	})
	if scope.IsZero() && pending.ChatID != 0 {
		scope = session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(pending.ChatID, 10),
		}
	}
	return session.SessionKey{
		ChatID: pending.ChatID,
		Scope:  scope,
	}
}

func prefixDecisionTextForKey(key session.SessionKey, resolver DecisionThreadResolver, text string) string {
	scope := session.NormalizeScopeRef(key.Scope)
	if scope.Kind != session.ScopeKindTelegramThread || key.ChatID == 0 {
		return strings.TrimSpace(text)
	}
	threadID, ok := telegramThreadIDFromScopeID(key.ChatID, scope.ID)
	if !ok {
		return strings.TrimSpace(text)
	}
	return prefixDecisionTextForThread(key.ChatID, threadID, resolver, text)
}

func prefixDecisionText(pending decision.PendingDecision, resolver DecisionThreadResolver, text string) string {
	threadID, ok := pendingDecisionThreadID(pending)
	if !ok {
		return strings.TrimSpace(text)
	}
	return prefixDecisionTextForThread(pending.ChatID, threadID, resolver, text)
}

func prefixDecisionTextForThread(chatID int64, threadID int64, resolver DecisionThreadResolver, text string) string {
	text = strings.TrimSpace(text)
	if chatID == 0 || threadID <= 0 {
		return text
	}
	prefix := telegrampresentation.ThreadPrefix(strconv.FormatInt(threadID, 10))
	if resolver != nil {
		if thread, ok, err := resolver.TelegramThread(chatID, threadID); err == nil && ok {
			prefix = telegrampresentation.PresentationForThread(chatID, thread, threadID).Prefix
		}
	}
	return telegrampresentation.PrefixText(prefix, text)
}

func pendingDecisionThreadID(pending decision.PendingDecision) (int64, bool) {
	if pending.ChatID == 0 || session.ScopeKind(strings.TrimSpace(pending.ScopeKind)) != session.ScopeKindTelegramThread {
		return 0, false
	}
	return telegramThreadIDFromScopeID(pending.ChatID, pending.ScopeID)
}

func telegramThreadIDFromScopeID(chatID int64, scopeID string) (int64, bool) {
	scopeID = strings.TrimSpace(scopeID)
	if chatID == 0 || scopeID == "" {
		return 0, false
	}
	prefix := fmt.Sprintf("%d:", chatID)
	if !strings.HasPrefix(scopeID, prefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(scopeID, prefix)), 10, 64)
	if err != nil || threadID <= 0 {
		return 0, false
	}
	return threadID, true
}
