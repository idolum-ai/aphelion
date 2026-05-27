//go:build linux

package telegramcommands

import (
	"context"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	healthCallbackPrefix = "health:"

	healthActionHome      = "home"
	healthActionRefresh   = "refresh"
	healthActionStatus    = "status"
	healthActionTrace     = "trace"
	healthActionTraceMore = "trace_more"
	healthActionDiagnose  = "diagnose"
)

const staleHealthCallbackText = "This health action is no longer available. Run /health again."

func handleTelegramHealthCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, personaEffort string, governorEffort string, isAdmin bool) (bool, error) {
	action, _ := nextCommandToken(telegramCommandArgs(msg.Text))
	switch action {
	case "", healthActionHome, healthActionRefresh:
		return sendHealthHomePanel(ctx, sender, msg, isAdmin)
	case healthActionStatus:
		rendered, rows, err := renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		_, err = sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID))
		return true, err
	case healthActionTrace:
		return sendHealthTracePanel(ctx, sender, router, msg, personaEffort, governorEffort)
	case healthActionDiagnose:
		text, err := queueHealthDiagnosis(ctx, router, msg, isAdmin)
		if err != nil {
			return true, err
		}
		_, err = sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  msg.ChatID,
			Text:    text,
			ReplyTo: replyToMessageID(msg.MessageID),
		})
		return true, err
	default:
		_, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  msg.ChatID,
			Text:    renderHealthCommandUsage(action),
			ReplyTo: replyToMessageID(msg.MessageID),
		})
		return true, err
	}
}

func sendHealthHomePanel(ctx context.Context, sender commandSender, msg core.InboundMessage, isAdmin bool) (bool, error) {
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, renderHealthHomePanel(isAdmin), healthHomeRows(isAdmin), replyToMessageID(msg.MessageID))
	return true, err
}

func sendHealthTracePanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, personaEffort string, governorEffort string) (bool, error) {
	quickText, _, err := renderDebugSnapshot(ctx, router, msg.ChatID, msg.SenderID, personaEffort, governorEffort)
	if err != nil {
		return true, err
	}
	if strings.TrimSpace(quickText) == "" {
		quickText = "Quick Read: unavailable. Tap Read More for the full trace."
	}
	_, err = sender.SendInlineKeyboard(ctx, msg.ChatID, quickText, healthTraceRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func renderHealthHomePanel(isAdmin bool) string {
	details := []string{
		"Status shows live chat state and available control surfaces.",
		"Trace expands the status ledger into recent execution evidence.",
	}
	if isAdmin {
		details = append(details, "Diagnose queues a read-only runtime analysis from a private admin chat.")
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title:   "Health",
		State:   "ready",
		Why:     "Health is the operator surface for status, trace evidence, and read-only diagnosis.",
		Next:    "Open the view that matches the question you need answered.",
		Details: details,
	})
}

func renderHealthCommandUsage(action string) string {
	action = strings.TrimSpace(action)
	why := "Unknown health action."
	if action != "" {
		why = "Unknown health action: " + action + "."
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Health",
		State: "not applied",
		Why:   why,
		Next:  "Use /health, /health status, /health trace, or /health diagnose.",
	})
}

func healthHomeRows(isAdmin bool) [][]telegram.InlineButton {
	rows := [][]telegram.InlineButton{
		{
			healthButton(healthActionStatus, "Status"),
			healthButton(healthActionTrace, "Trace"),
		},
	}
	refreshRow := []telegram.InlineButton{healthButton(healthActionRefresh, "Refresh")}
	if isAdmin {
		refreshRow = append(refreshRow, healthButton(healthActionDiagnose, "Diagnose"))
	}
	return append(rows, refreshRow)
}

func healthTraceRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			healthButton(healthActionTraceMore, "Read More"),
		},
		{
			healthButton(healthActionHome, "Back"),
			healthButton(healthActionTrace, "Refresh"),
		},
	}
}

func healthTracePageRows(info telegramPageInfo) [][]telegram.InlineButton {
	rows := telegramPageNavigationRows(info, telegramPageSurfaceHealth, telegramPageViewTrace)
	rows = append(rows, []telegram.InlineButton{
		healthButton(healthActionTrace, "Summary"),
		healthButton(healthActionHome, "Back"),
	})
	return rows
}

func healthButton(action string, label string) telegram.InlineButton {
	return telegram.InlineButton{
		Text:         strings.TrimSpace(label),
		CallbackData: encodeHealthCallbackData(action),
	}
}

func encodeHealthCallbackData(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		action = healthActionHome
	}
	return healthCallbackPrefix + action
}

func decodeHealthCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, healthCallbackPrefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, healthCallbackPrefix))
	switch action {
	case healthActionHome, healthActionRefresh, healthActionStatus, healthActionTrace, healthActionTraceMore, healthActionDiagnose:
		return action, true
	default:
		return "", false
	}
}

func handleHealthCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	chatType := ""
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			chatType = cb.Message.Chat.Type
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleHealthCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	isAdmin := router.CanRestart(senderID)
	switch action {
	case healthActionHome, healthActionRefresh:
		return editHealthCallbackPanel(ctx, sender, cb, chatID, messageID, renderHealthHomePanel(isAdmin), healthHomeRows(isAdmin))
	case healthActionStatus:
		if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
			return true, err
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		rendered, rows, err := renderStatusView(ctx, router, chatID, senderID, statusViewChat, chatID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
			return true, err
		}
		return true, nil
	case healthActionTrace:
		if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
			return true, err
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		quickText, _, err := renderDebugSnapshot(ctx, router, chatID, senderID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if strings.TrimSpace(quickText) == "" {
			quickText = "Quick Read: unavailable. Tap Read More for the full trace."
		}
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, quickText, "", healthTraceRows()); err != nil {
			return true, err
		}
		return true, nil
	case healthActionTraceMore:
		if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
			return true, err
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		projection, err := renderDebugSnapshotProjection(ctx, router, chatID, senderID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		rendered, rows := renderHealthTracePage(projection, 1)
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
			return true, err
		}
		return true, nil
	case healthActionDiagnose:
		if !isAdmin {
			if err := answerHealthCallback(ctx, sender, cb, "Health diagnosis is admin only."); err != nil {
				return true, err
			}
			return true, nil
		}
		text, err := queueHealthDiagnosis(ctx, router, core.InboundMessage{
			ChatID:          chatID,
			SenderID:        senderID,
			MessageID:       messageID,
			ChatType:        chatType,
			Text:            "/health diagnose",
			IngressSurface:  telegramDoctorIngressSurface,
			IngressUpdateID: cb.UpdateID,
		}, isAdmin)
		if err != nil {
			return true, err
		}
		if strings.Contains(strings.ToLower(text), "private chat") {
			if err := answerHealthCallback(ctx, sender, cb, text); err != nil {
				return true, err
			}
			return true, nil
		}
		if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
			return true, err
		}
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", healthHomeRows(isAdmin)); err != nil {
			return true, err
		}
		return true, nil
	default:
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleHealthCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
}

func renderHealthTracePage(projection debugSnapshotProjection, page int) (string, [][]telegram.InlineButton) {
	sections, info := telegramPageItems(projection.Sections, page, 1)
	if len(sections) == 0 {
		panel := face.OperatorPanel{
			Title: "Health Trace",
			State: "unavailable",
			Why:   "No trace sections were available for this snapshot.",
			Next:  "Return to the summary and refresh.",
		}
		return renderTelegramCompactPanel(panel, false), healthTracePageRows(info)
	}
	section := sections[0]
	var b strings.Builder
	// Single-line breadcrumb collapses Title + page + section name. The
	// previous header repeated a constant 'Why: This page is a projection
	// of the current status ledger.' on every page; it carried no
	// per-section information and ate scarce mobile viewport.
	b.WriteString("Health Trace · section ")
	b.WriteString(strconv.Itoa(info.Page))
	b.WriteString(" of ")
	b.WriteString(strconv.Itoa(info.PageCount))
	b.WriteString(" · ")
	b.WriteString(strings.TrimSpace(section.Title))
	b.WriteString("\n")
	b.WriteString("Next or Prev for nearby evidence; Summary for the compact view.\n\n")
	b.WriteString(strings.TrimSpace(section.Text))
	return strings.TrimSpace(b.String()), healthTracePageRows(info)
}

func editHealthCallbackPanel(ctx context.Context, sender commandCallbackSender, cb telegram.CallbackQuery, chatID int64, messageID int64, text string, rows [][]telegram.InlineButton) (bool, error) {
	if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
		return true, err
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func answerHealthCallback(ctx context.Context, sender commandCallbackSender, cb telegram.CallbackQuery, text string) error {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func queueHealthDiagnosis(ctx context.Context, router commandRouter, msg core.InboundMessage, isAdmin bool) (string, error) {
	if !isAdmin {
		return "Health diagnosis is admin only.", nil
	}
	if chatType := strings.TrimSpace(msg.ChatType); chatType != "" && chatType != "private" && chatType != "dm" {
		return "Health diagnosis must be run from an admin private chat.", nil
	}
	queued := msg
	queued.Text = "/health diagnose"
	if err := router.QueueDoctor(ctx, queued); err != nil {
		return "", err
	}
	return "Health diagnosis started. I will post the report here when the read-only model analysis finishes.", nil
}
