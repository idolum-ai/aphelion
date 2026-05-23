//go:build linux

package telegramcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
)

type ThreadController struct {
	Store              *session.SQLiteStore
	Rebind             func(core.InboundMessage) error
	RouteAccepted      func(context.Context, core.InboundMessage) error
	StopForMessage     func(core.InboundMessage) core.StopResult
	Promote            func(context.Context, int64, int64, int64) (session.TelegramThreadPromotionResult, error)
	PreparePromotion   func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	CancelPromotion    func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	SupersedePromotion func(context.Context, int64, int64, string) (session.TelegramThreadPromotionResult, error)
	Absorb             func(context.Context, int64, int64, int64) (string, error)
	IsAbsorbUserError  func(error) bool
}

func (c ThreadController) rebindTelegramIngressForMessage(msg core.InboundMessage) error {
	if c.Rebind == nil {
		return nil
	}
	return c.Rebind(msg)
}

func (c ThreadController) routeAccepted(ctx context.Context, msg core.InboundMessage) error {
	if c.RouteAccepted == nil {
		return nil
	}
	return c.RouteAccepted(ctx, msg)
}

func (c ThreadController) stopForMessage(msg core.InboundMessage) core.StopResult {
	if c.StopForMessage == nil {
		return core.StopResult{}
	}
	return c.StopForMessage(msg)
}

func (c ThreadController) CreateTelegramThread(_ context.Context, msg core.InboundMessage) (session.TelegramThread, error) {
	if c.Store == nil {
		return session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, _, err := c.Store.CreateTelegramThreadForUpdate(msg.ChatID, msg.SenderID, msg.IngressUpdateID, msg.MessageID, "", time.Now().UTC())
	if err != nil {
		return session.TelegramThread{}, err
	}
	return thread, nil
}

func (c ThreadController) RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error {
	if c.Store == nil || chatID == 0 || threadID <= 0 || messageID <= 0 {
		return nil
	}
	return c.Store.RecordTelegramThreadMessage(chatID, threadID, messageID, "thread_guide", "thread_guide", time.Now().UTC())
}

func (c ThreadController) RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error {
	if c.Store == nil {
		return nil
	}
	return c.Store.RecordTelegramCallbackMessageThread(chatID, messageID, threadID, surface, time.Now().UTC())
}

func (c ThreadController) StartTelegramThreadTarget(_ context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError("Usage: /thread <message>")
	}
	if c.Store == nil {
		return core.InboundMessage{}, session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, _, err := c.Store.CreateTelegramThreadForUpdate(msg.ChatID, msg.SenderID, msg.IngressUpdateID, msg.MessageID, text, time.Now().UTC())
	if err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	routed.Text = text
	if err := c.rebindTelegramIngressForMessage(routed); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	return routed, thread, nil
}

func (c ThreadController) TargetTelegramThreadMessage(_ context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Add a message after `(thread %d)`.", threadID))
	}
	if c.Store == nil {
		return core.InboundMessage{}, session.TelegramThread{}, fmt.Errorf("thread store is unavailable")
	}
	thread, ok, err := c.Store.TelegramThread(msg.ChatID, threadID)
	if err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	if !ok {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	if !thread.Open() {
		return core.InboundMessage{}, session.TelegramThread{}, telegramcommands.ThreadUserError(fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", threadID))
	}
	routed := msg
	routed.TelegramThreadID = threadID
	routed.Text = text
	if err := c.Store.TouchTelegramThread(msg.ChatID, threadID, time.Now().UTC()); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	if err := c.rebindTelegramIngressForMessage(routed); err != nil {
		return core.InboundMessage{}, session.TelegramThread{}, err
	}
	return routed, thread, nil
}

func (c ThreadController) TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error) {
	if c.Store == nil {
		return session.TelegramThread{}, false, fmt.Errorf("thread store is unavailable")
	}
	threadID, ok, err := c.Store.TelegramThreadIDForReplyMessage(chatID, replyMessageID)
	if err != nil || !ok {
		return session.TelegramThread{}, false, err
	}
	return c.Store.TelegramThread(chatID, threadID)
}

func (c ThreadController) TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error) {
	if c.Store == nil {
		return session.TelegramThread{}, false, fmt.Errorf("thread store is unavailable")
	}
	return c.Store.TelegramThread(chatID, threadID)
}

func (c ThreadController) TelegramThreads(chatID int64) ([]session.TelegramThread, error) {
	if c.Store == nil {
		return nil, fmt.Errorf("thread store is unavailable")
	}
	return c.Store.ListTelegramThreads(chatID, 20)
}

func (c ThreadController) QueueTelegramThreadSummary(ctx context.Context, msg core.InboundMessage) (string, error) {
	if c.Store == nil {
		return "", fmt.Errorf("thread store is unavailable")
	}
	text, err := c.renderTelegramThreadSummaryQuest(msg.ChatID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", telegramcommands.ThreadUserError("No open side threads to summarize.")
	}
	routed := msg
	routed.Text = text
	routed.TelegramThreadID = 0
	if err := c.recordTelegramThreadSummaryAccepted(routed); err != nil {
		return "", err
	}
	if err := c.routeAccepted(ctx, routed); err != nil {
		return "", err
	}
	return "Summary queued.", nil
}

func (c ThreadController) recordTelegramThreadSummaryAccepted(msg core.InboundMessage) error {
	if c.Store == nil || strings.TrimSpace(msg.IngressSurface) == "" || msg.IngressUpdateID <= 0 {
		return nil
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	now := time.Now().UTC()
	_, err := c.Store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     msg.IngressSurface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  "callback_thread_summary",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: encoded,
		AcceptedAt:  now,
		UpdatedAt:   now,
	})
	return err
}

func (c ThreadController) renderTelegramThreadSummaryQuest(chatID int64) (string, error) {
	threads, err := c.Store.ListTelegramThreads(chatID, 20)
	if err != nil {
		return "", err
	}
	var open []session.TelegramThread
	for _, thread := range threads {
		if thread.Open() {
			open = append(open, thread)
		}
	}
	if len(open) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("Summarize the open Telegram side threads below into one short main-chat status message.\n")
	b.WriteString("Keep it compact: one line per thread when possible, then name the most important next action if one is clear.\n")
	b.WriteString("Do not absorb, close, or modify any thread. Do not claim memory was written.\n\n")
	b.WriteString("Open side-thread evidence:\n")
	for _, thread := range open {
		fmt.Fprintf(&b, "\nThread %d\n", thread.ThreadID)
		if preview := telegramcommands.CompactThreadPreview(thread.CreatedText); preview != "" {
			fmt.Fprintf(&b, "created: %s\n", preview)
		}
		for _, line := range c.telegramThreadRecentTranscriptLines(chatID, thread.ThreadID, 6) {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (c ThreadController) telegramThreadRecentTranscriptLines(chatID int64, threadID int64, limit int) []string {
	if c.Store == nil || chatID == 0 || threadID <= 0 {
		return nil
	}
	if limit <= 0 {
		limit = 6
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: session.TelegramThreadScopeRef(chatID, threadID)}
	sess, err := c.Store.Load(key)
	if err != nil || sess == nil || len(sess.Messages) == 0 {
		return nil
	}
	var out []string
	for i := len(sess.Messages) - 1; i >= 0 && len(out) < limit; i-- {
		msg := sess.Messages[i]
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.Join(strings.Fields(strings.TrimSpace(msg.Content)), " ")
		if content == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", role, truncateTelegramThreadSummaryEvidence(content, 260)))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func truncateTelegramThreadSummaryEvidence(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 3 {
		return strings.TrimSpace(string(runes[:limit]))
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func (c ThreadController) PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error) {
	if c.Promote == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	c.stopForMessage(core.InboundMessage{
		ChatID:           chatID,
		SenderID:         senderID,
		TelegramThreadID: threadID,
	})
	text, err := c.Promote(ctx, chatID, senderID, threadID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) PrepareTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.PreparePromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.PreparePromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) CancelTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.CancelPromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.CancelPromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) SupersedeTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	if c.SupersedePromotion == nil {
		return session.TelegramThreadPromotionResult{}, fmt.Errorf("runtime is unavailable")
	}
	text, err := c.SupersedePromotion(ctx, chatID, senderID, handoffID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return session.TelegramThreadPromotionResult{}, telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}

func (c ThreadController) AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	if c.Absorb == nil {
		return "", fmt.Errorf("runtime is unavailable")
	}
	c.stopForMessage(core.InboundMessage{
		ChatID:           chatID,
		SenderID:         senderID,
		TelegramThreadID: threadID,
	})
	text, err := c.Absorb(ctx, chatID, senderID, threadID)
	if err != nil && c.IsAbsorbUserError != nil && c.IsAbsorbUserError(err) {
		return "", telegramcommands.ThreadUserError(err.Error())
	}
	return text, err
}
