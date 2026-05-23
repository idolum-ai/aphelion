//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	_ = ctx
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
	if actor.Role != principal.RoleAdmin {
		return "", telegramThreadRuntimeUserError("Promote is admin only.")
	}

	threadKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramThreadScopeRef(chatID, threadID)}
	unlockThread := r.lockSession(threadKey)
	defer unlockThread()

	thread, ok, err := r.store.TelegramThread(chatID, threadID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
	}
	threadLabel := telegramThreadOperatorLabel(thread, threadID)
	if !thread.Open() {
		return "", telegramThreadRuntimeUserError(fmt.Sprintf("Thread %s is closed. Start a new side thread with `/thread <message>`.", threadLabel))
	}

	handoff, created, err := r.store.CreateTelegramThreadPromotionDraft(chatID, threadID, senderID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	return renderTelegramThreadPromotionDraft(threadLabel, handoff, created), nil
}

func renderTelegramThreadPromotionDraft(threadLabel string, handoff session.TelegramThreadPromotionHandoff, created bool) string {
	threadLabel = normalizeTelegramThreadOperatorLabel(threadLabel, strconv.FormatInt(handoff.ThreadID, 10))
	var b strings.Builder
	if created {
		fmt.Fprintf(&b, "Promotion draft created for thread %s.\n\n", threadLabel)
	} else {
		fmt.Fprintf(&b, "Promotion draft already exists for thread %s.\n\n", threadLabel)
	}
	fmt.Fprintf(&b, "Handoff: %s\n", strings.TrimSpace(handoff.HandoffID))
	fmt.Fprintf(&b, "Status: %s\n", strings.TrimSpace(string(handoff.Status)))
	if preview := strings.TrimSpace(handoff.SourcePreview); preview != "" {
		fmt.Fprintf(&b, "Source: %s\n", truncateRunes(strings.Join(strings.Fields(preview), " "), 220))
	}
	b.WriteString("\nReview before promotion can continue:\n")
	b.WriteString("- context digest: required\n")
	b.WriteString("- memory candidates: review required; no child memory written\n")
	b.WriteString("- resources/capabilities: review required; no grants created\n")
	b.WriteString("- policy/wake/first run: approval required\n")
	b.WriteString("\nThis draft does not create a durable child, transfer memory, grant resources, or run work.")
	return strings.TrimSpace(b.String())
}
