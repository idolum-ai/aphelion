//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	if r == nil || r.store == nil {
		return "", fmt.Errorf("runtime unavailable")
	}
	if chatID == 0 || threadID <= 0 {
		return "", fmt.Errorf("thread id is required")
	}
	actor, ok := r.resolver.ResolveTelegramUser(senderID)
	if !ok {
		return "", ErrPrincipalDenied
	}
	thread, ok, err := r.store.TelegramThread(chatID, threadID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	threadLabel := telegramThreadOperatorLabel(thread, threadID)
	if !thread.Open() {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %s is already closed.", threadLabel))
	}

	threadKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramThreadScopeRef(chatID, threadID)}
	unlockThread := r.lockSession(threadKey)
	defer unlockThread()
	thread, ok, err = r.store.TelegramThread(chatID, threadID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	threadLabel = telegramThreadOperatorLabel(thread, threadID)
	if !thread.Open() {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %s is already closed.", threadLabel))
	}
	threadSession, err := r.store.Load(threadKey)
	if err != nil {
		return "", fmt.Errorf("load thread session: %w", err)
	}
	summary := r.summarizeTelegramThreadAbsorb(ctx, threadLabel, threadSession)
	if strings.TrimSpace(summary) == "" {
		summary = renderTelegramThreadAbsorbFallback(threadLabel, threadSession)
	}
	if strings.TrimSpace(summary) == "" {
		summary = fmt.Sprintf("Thread %s closed without durable outcome notes.", threadLabel)
	}

	note := renderTelegramThreadAbsorbNote(threadLabel, summary)
	defaultKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	unlockDefault := r.lockSession(defaultKey)
	defer unlockDefault()
	defaultSession, err := r.store.Load(defaultKey)
	if err != nil {
		return "", fmt.Errorf("load default session for thread absorb: %w", err)
	}
	newMessages := appendSyntheticTurn(defaultSession, "/absorb "+threadLabel, note, note, telegramThreadAbsorbFloorMetadata(threadID, threadLabel, actor))
	if _, closed, err := r.store.RecordTelegramThreadAbsorb(chatID, threadID, note, defaultSession, newMessages, time.Now().UTC()); err != nil {
		return "", err
	} else if !closed {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %s is already closed.", threadLabel))
	}
	return "Absorbed thread " + threadLabel + ".\n\n" + note, nil
}

type telegramThreadRuntimeUserError string

func (e telegramThreadRuntimeUserError) Error() string {
	return string(e)
}

func IsTelegramThreadUserError(err error) bool {
	var userErr telegramThreadRuntimeUserError
	return errors.As(err, &userErr)
}

func (r *Runtime) summarizeTelegramThreadAbsorb(ctx context.Context, threadLabel string, sess *session.Session) string {
	if r == nil || r.provider == nil || sess == nil || len(sess.Messages) == 0 {
		return ""
	}
	transcript := renderTelegramThreadAbsorbTranscript(threadLabel, sess)
	if strings.TrimSpace(transcript) == "" {
		return ""
	}
	messages := []agent.Message{
		{
			Role: "system",
			Content: strings.TrimSpace(`You write compact bookkeeping notes for a personal agent.
Summarize the side thread outcome for the main chat.
Keep durable decisions, useful context, and open questions.
Do not claim memory was written. Do not mention vector stores.`),
		},
		{
			Role:    "user",
			Content: transcript,
		},
	}
	result, _, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{Max: 1, Caution: 0.8, Warning: 0.9}, r.reasoningOptionsForRun(session.TurnRunKindInteractive), messages)
	if err != nil {
		return ""
	}
	return clampTelegramThreadAbsorbSummary(result.Text)
}

func renderTelegramThreadAbsorbTranscript(threadLabel string, sess *session.Session) string {
	if sess == nil {
		return ""
	}
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, "unknown")
	var b strings.Builder
	fmt.Fprintf(&b, "Thread %s transcript for compact absorb bookkeeping.\n", threadLabel)
	messages := sess.Messages
	if len(messages) > 24 {
		messages = messages[len(messages)-24:]
	}
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "\n%s: %s", role, truncateRunes(content, 1200))
	}
	return strings.TrimSpace(b.String())
}

func renderTelegramThreadAbsorbFallback(threadLabel string, sess *session.Session) string {
	if sess == nil {
		return ""
	}
	var parts []string
	for i := len(sess.Messages) - 1; i >= 0 && len(parts) < 4; i-- {
		msg := sess.Messages[i]
		role := strings.TrimSpace(msg.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", role, truncateRunes(strings.Join(strings.Fields(content), " "), 220)))
	}
	if len(parts) == 0 {
		return ""
	}
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, "unknown")
	var b strings.Builder
	fmt.Fprintf(&b, "Thread %s closed. Recent context:\n", threadLabel)
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString("- ")
		b.WriteString(parts[i])
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func renderTelegramThreadAbsorbNote(threadLabel string, summary string) string {
	summary = clampTelegramThreadAbsorbSummary(summary)
	if summary == "" {
		summary = "No durable outcome was recorded."
	}
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, "unknown")
	return fmt.Sprintf("Thread %s absorbed into the main chat.\n\n%s", threadLabel, summary)
}

func clampTelegramThreadAbsorbSummary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return truncateRunes(text, 1800)
}

func telegramThreadAbsorbFloorMetadata(threadID int64, threadLabel string, actor principal.Principal) string {
	return fmt.Sprintf(`{"source":"telegram_thread_absorb","thread_id":%d,"thread_label":%q,"actor_role":%q,"actor_user_id":%d}`,
		threadID,
		normalizeTelegramThreadOperatorLabel(threadLabel, fmt.Sprint(threadID)),
		string(actor.Role),
		actor.TelegramUserID,
	)
}

func telegramThreadOperatorLabel(thread session.TelegramThread, fallbackID int64) string {
	if thread.DisplaySlot > 0 {
		return fmt.Sprint(thread.DisplaySlot)
	}
	if label := strings.TrimSpace(thread.ArchivedDisplayName); label != "" {
		return label
	}
	if thread.ThreadID > 0 {
		return fmt.Sprint(thread.ThreadID)
	}
	if fallbackID > 0 {
		return fmt.Sprint(fallbackID)
	}
	return "unknown"
}

func normalizeTelegramThreadOperatorLabel(label string, fallback string) string {
	label = strings.TrimSpace(label)
	if label != "" {
		return label
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return "unknown"
}

func telegramThreadDisplayPrefix(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return fmt.Sprintf("(thread %d)", threadID)
}

func prefixTelegramThreadText(threadID int64, text string) string {
	text = strings.TrimSpace(text)
	prefix := telegramThreadDisplayPrefix(threadID)
	if prefix == "" || text == "" {
		return text
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
		return text
	}
	return prefix + "\n\n" + text
}
