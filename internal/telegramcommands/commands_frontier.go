//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

func handleAuthorityFrontierCommand(ctx context.Context, router commandRouter, sender commandSender, msg core.InboundMessage) (bool, error) {
	if !router.CanRestart(msg.SenderID) {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: "Authority frontier controls are admin only.", ReplyTo: replyToMessageID(msg.MessageID)}); err != nil {
			return true, err
		}
		return true, nil
	}
	snapshot, err := router.AuthorityFrontierStatus(ctx, msg.SenderID)
	if err != nil {
		return true, err
	}
	rendered, rows := renderAuthorityFrontierCommand(snapshot)
	if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
		return true, err
	}
	return true, nil
}

func renderAuthorityFrontierCommand(snapshot core.AuthorityFrontierStatusSnapshot) (string, [][]telegram.InlineButton) {
	state := fmt.Sprintf("%d/%d unresolved slot(s) used", snapshot.Used, snapshot.Budget)
	if snapshot.Expired > 0 {
		state += fmt.Sprintf("; %d expired visible", snapshot.Expired)
	}
	details := make([]string, 0, len(snapshot.Slots)+2)
	for _, slot := range snapshot.Slots {
		details = append(details, renderAuthorityFrontierSlotLine(slot))
	}
	if len(details) == 0 {
		details = append(details, "No frontier slots available.")
	}
	evidence := []string{
		fmt.Sprintf("admin_chat_id=%d", snapshot.AdminChatID),
		"No authority was approved or executed by this view.",
	}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Authority Frontier",
		State:    state,
		Why:      "Lookahead allowances are the bounded speculative authority meter.",
		Next:     "Resolve, reject, or let open approval cards expire before spending more frontier slots.",
		Details:  details,
		Evidence: evidence,
	}, 8, 4), nil
}

func renderAuthorityFrontierSlotLine(slot core.AuthorityFrontierSlot) string {
	status := strings.TrimSpace(slot.Status)
	if status == "" {
		status = "unknown"
	}
	if status == "empty" {
		return fmt.Sprintf("%d. empty", slot.Index)
	}
	binding := firstTailnetNonEmpty(slot.NextActionRecordID, slot.EntryID, slot.AllowanceID, "-")
	ttl := renderAuthorityFrontierTTL(slot)
	line := fmt.Sprintf("%d. %s %s; ttl %s", slot.Index, status, binding, ttl)
	if slot.ReviewEventID > 0 {
		line += fmt.Sprintf("; review %d", slot.ReviewEventID)
	}
	if reason := strings.TrimSpace(slot.Reason); reason != "" {
		line += "; " + reason
	}
	return truncateOperatorLine(line, 220)
}

func renderAuthorityFrontierTTL(slot core.AuthorityFrontierSlot) string {
	if slot.Status == "expired" {
		return "expired"
	}
	if slot.TTLSeconds <= 0 {
		if slot.ExpiresAt.IsZero() {
			return "-"
		}
		return "0s"
	}
	return (time.Duration(slot.TTLSeconds) * time.Second).Round(time.Second).String()
}
