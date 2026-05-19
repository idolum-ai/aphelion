//go:build linux

package telegramruntime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
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

func telegramSessionTargetForScope(chatID int64, scope session.ScopeRef) telegramSessionTarget {
	scope = session.NormalizeScopeRef(scope)
	threadID := telegramThreadIDFromScope(chatID, scope)
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: scope}
	return telegramSessionTarget{
		ChatID:    chatID,
		ThreadID:  threadID,
		Scope:     scope,
		SessionID: session.SessionIDForKey(key),
	}
}

func telegramThreadIDFromScope(chatID int64, scope session.ScopeRef) int64 {
	scope = session.NormalizeScopeRef(scope)
	if scope.Kind != session.ScopeKindTelegramThread {
		return 0
	}
	prefix := strconv.FormatInt(chatID, 10) + ":"
	raw := strings.TrimSpace(scope.ID)
	if !strings.HasPrefix(raw, prefix) {
		return 0
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(raw, prefix)), 10, 64)
	if err != nil || threadID <= 0 {
		return 0
	}
	return threadID
}

func telegramInboundForSessionTarget(chatID int64, senderID int64, target telegramSessionTarget) core.InboundMessage {
	msg := core.InboundMessage{ChatID: chatID, SenderID: senderID}
	if target.ThreadID > 0 {
		msg.TelegramThreadID = target.ThreadID
	}
	return msg
}

func telegramSessionOwnerKey(msg core.InboundMessage) string {
	target := telegramSessionTargetForMessage(msg)
	senderID := msg.SenderID
	if strings.TrimSpace(target.SessionID) == "" {
		if msg.ChatID != 0 && senderID != 0 {
			return fmt.Sprintf("chat:%d:sender:%d", msg.ChatID, senderID)
		}
		return ""
	}
	if senderID != 0 {
		return fmt.Sprintf("session:%s:sender:%d", target.SessionID, senderID)
	}
	return "session:" + target.SessionID
}

func telegramThreadDisplayPrefixForMessage(msg core.InboundMessage) string {
	if msg.TelegramThreadID <= 0 {
		return ""
	}
	return fmt.Sprintf("(thread %d)\n\n", msg.TelegramThreadID)
}

func telegramInboundForTurnRun(run session.TurnRun, senderID int64) core.InboundMessage {
	target := telegramSessionTargetForScope(run.ChatID, run.Scope)
	msg := core.InboundMessage{ChatID: run.ChatID, SenderID: senderID}
	if target.ThreadID > 0 {
		msg.TelegramThreadID = target.ThreadID
	}
	return msg
}
