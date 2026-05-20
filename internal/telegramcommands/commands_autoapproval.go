//go:build linux

package telegramcommands

import (
	"context"
	"log"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/internal/telegrampresentation"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	autoCallbackPrefix = "auto:"

	autoSurfaceHome      = "home"
	autoSurfaceMode      = "mode"
	autoSurfaceApprovals = "approvals"
	autoSurfaceLimits    = "limits"

	autoActionShow    = "show"
	autoActionRefresh = "refresh"
	autoActionDouble  = "double"
)

const staleAutoCallbackText = "This auto action is no longer available. Run /auto again."

type operatorAutoPreset struct {
	Action      string
	Label       string
	AutoApprove string
	Mode        string
}

var operatorAutoPresets = []operatorAutoPreset{
	{Action: "off", Label: "Off", AutoApprove: "off", Mode: "off"},
	{Action: "work15", Label: "15m Work", AutoApprove: "15m workspace uses=2", Mode: "leased 15m workspace"},
	{Action: "deploy15", Label: "15m Deploy", AutoApprove: "15m deploy uses=1", Mode: "leased 15m deploy"},
	{Action: "all15", Label: "15m All", AutoApprove: "15m all uses=1", Mode: "leased 15m all"},
}

func handleTelegramAutoCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	target, rest := nextCommandToken(telegramCommandArgs(msg.Text))
	if target == "thread" {
		threadID, remaining, ok := parseAutoThreadTarget(rest)
		if !ok {
			return sendAutoCommandText(ctx, sender, msg, renderAutoCommandUsage("thread"))
		}
		threadRouter, ok := router.(commandThreadRouter)
		if !ok {
			return sendAutoCommandText(ctx, sender, msg, "Thread controls are unavailable.")
		}
		visibleThreadID := threadID
		resolvedThreadID, resolvedThread, err := resolveTelegramThreadVisibleTarget(threadRouter, msg.ChatID, visibleThreadID)
		if err != nil {
			return sendAutoCommandText(ctx, sender, msg, err.Error())
		}
		threadID = resolvedThreadID
		thread, ok, err := threadRouter.TelegramThread(msg.ChatID, threadID)
		if err != nil {
			return true, err
		}
		if !ok {
			return sendAutoCommandText(ctx, sender, msg, "Thread "+strconv.FormatInt(threadID, 10)+" does not exist. Start a new side thread with `/thread <message>`.")
		}
		if !thread.Open() {
			return sendAutoCommandText(ctx, sender, msg, "Thread "+strconv.FormatInt(threadID, 10)+" is closed. Start a new side thread with `/thread <message>`.")
		}
		_ = resolvedThread
		msg.TelegramThreadID = threadID
		msg.OriginDetail = telegrampresentation.OriginDetailForDisplaySlot(visibleThreadID)
		if strings.TrimSpace(remaining) == "" {
			return sendAutoHomePanel(ctx, sender, router, msg)
		}
		target, rest = nextCommandToken(remaining)
	}
	switch target {
	case "", "home", autoActionRefresh:
		return sendAutoHomePanel(ctx, sender, router, msg)
	case autoSurfaceMode:
		if strings.TrimSpace(rest) == "" || strings.EqualFold(strings.TrimSpace(rest), "status") {
			return sendAutoModePanel(ctx, sender, router, msg)
		}
		configured, err := configureAutonomyForAutoCommand(ctx, router, msg, rest)
		if err != nil {
			log.Printf("WARN auto mode command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			return sendAutoCommandText(ctx, sender, msg, renderAutonomyCommandError(err))
		}
		return sendAutoCommandText(ctx, sender, msg, configured)
	case "approval", autoSurfaceApprovals:
		if strings.TrimSpace(rest) == "" || strings.EqualFold(strings.TrimSpace(rest), "status") {
			return sendAutoApprovalsPanel(ctx, sender, router, msg)
		}
		configured, err := configureAutoApprovalForAutoCommand(ctx, router, msg, rest)
		if err != nil {
			log.Printf("WARN auto approvals command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			return sendAutoCommandText(ctx, sender, msg, renderAutoApprovalCommandError(err))
		}
		return sendAutoCommandText(ctx, sender, msg, configured)
	case "limit", autoSurfaceLimits:
		return sendAutoLimitsPanel(ctx, sender, router, msg)
	default:
		return sendAutoCommandText(ctx, sender, msg, renderAutoCommandUsage(target))
	}
}

func parseAutoThreadTarget(raw string) (int64, string, bool) {
	threadRaw, rest := nextCommandToken(raw)
	threadID, err := strconv.ParseInt(strings.TrimSpace(threadRaw), 10, 64)
	if err != nil || threadID <= 0 {
		return 0, "", false
	}
	return threadID, strings.TrimSpace(rest), true
}

func autonomyStatusForAutoCommand(router commandRouter, msg core.InboundMessage) (core.AutonomyStatusSnapshot, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedAutoRouter); ok {
			return scoped.AutonomyStatusForMessage(msg)
		}
	}
	return router.AutonomyStatus(msg.ChatID, msg.SenderID)
}

func configureAutonomyForAutoCommand(ctx context.Context, router commandRouter, msg core.InboundMessage, args string) (string, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedAutoRouter); ok {
			return scoped.ConfigureAutonomyForMessage(ctx, msg, args)
		}
	}
	return router.ConfigureAutonomy(ctx, msg.ChatID, msg.SenderID, args)
}

func autoApprovalStatusForAutoCommand(ctx context.Context, router commandRouter, msg core.InboundMessage) (string, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedAutoRouter); ok {
			return scoped.AutoApprovalStatusForMessage(ctx, msg)
		}
	}
	return router.AutoApprovalStatus(ctx, msg.ChatID, msg.SenderID)
}

func configureAutoApprovalForAutoCommand(ctx context.Context, router commandRouter, msg core.InboundMessage, args string) (string, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedAutoRouter); ok {
			return scoped.ConfigureAutoApprovalForMessage(ctx, msg, args)
		}
	}
	return router.ConfigureAutoApproval(ctx, msg.ChatID, msg.SenderID, args)
}

func sendAutoCommandText(ctx context.Context, sender commandSender, msg core.InboundMessage, text string) (bool, error) {
	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    prefixAutoThreadPanel(msg, strings.TrimSpace(text)),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	return true, err
}

func sendAutoHomePanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	err := sendAutoInlineKeyboard(ctx, sender, router, msg, prefixAutoThreadPanel(msg, renderAutoHomePanel()), autoHomeRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func sendAutoInlineKeyboard(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, text string, rows [][]telegram.InlineButton, replyTo *int64) error {
	messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, strings.TrimSpace(text), rows, replyTo)
	if err != nil {
		return err
	}
	if msg.TelegramThreadID > 0 && messageID > 0 {
		if recorder, ok := router.(commandThreadCallbackRecorder); ok {
			if err := recorder.RecordTelegramThreadCallbackMessage(msg.ChatID, msg.TelegramThreadID, messageID, "auto"); err != nil {
				return err
			}
		}
	}
	return nil
}

func prefixAutoThreadPanel(msg core.InboundMessage, text string) string {
	return telegrampresentation.PrefixText(strings.TrimSpace(telegrampresentation.PrefixForMessage(msg)), text)
}

func sendAutoModePanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	snapshot, err := autonomyStatusForAutoCommand(router, msg)
	if err != nil {
		return true, err
	}
	text := prefixAutoThreadPanel(msg, face.RenderTelegramAutonomyStatus(snapshot))
	err = sendAutoInlineKeyboard(ctx, sender, router, msg, text, autoModeRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func sendAutoApprovalsPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	text, err := autoApprovalStatusForAutoCommand(ctx, router, msg)
	if err != nil {
		log.Printf("WARN auto approvals status rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
		text = renderAutoApprovalCommandError(err)
	}
	sendErr := sendAutoInlineKeyboard(ctx, sender, router, msg, prefixAutoThreadPanel(msg, text), autoApprovalsRows(), replyToMessageID(msg.MessageID))
	return true, sendErr
}

func sendAutoLimitsPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	snapshot, err := autonomyStatusForAutoCommand(router, msg)
	if err != nil {
		return true, err
	}
	text := prefixAutoThreadPanel(msg, face.RenderTelegramAutoLimits(snapshot))
	err = sendAutoInlineKeyboard(ctx, sender, router, msg, text, autoLimitsRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func renderAutoHomePanel() string {
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Auto",
		State: "ready",
		Why:   "Authority controls are split by mode gates, approval grants, and configured limits.",
		Next:  "Open mode for the gate, approvals for spendable prompts, or limits for read-only config.",
		Details: []string{
			"Mode opens or closes the temporary automation gate.",
			"Approvals grant bounded automatic approval for eligible admin prompts.",
			"Limits show the configured default, ceiling, and maximum live mode duration.",
		},
	})
}

func renderAutoCommandUsage(target string) string {
	target = strings.TrimSpace(target)
	why := "Unknown auto target."
	if target != "" {
		why = "Unknown auto target: " + target + "."
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Auto",
		State: "not applied",
		Why:   why,
		Next:  "Use /auto mode, /auto approvals, /auto limits, /auto thread <id> mode leased <duration> <scope>, or /auto thread <id> approvals <duration> <scope>.",
	})
}

func autoHomeRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			autoButton(autoSurfaceMode, autoActionShow, "Mode"),
			autoButton(autoSurfaceApprovals, autoActionShow, "Approvals"),
			autoButton(autoSurfaceLimits, autoActionShow, "Limits"),
		},
		{
			autoButton(autoSurfaceHome, autoActionRefresh, "Refresh"),
		},
	}
}

func autoModeRows() [][]telegram.InlineButton {
	return operatorAutoPresetRows(autoSurfaceMode)
}

func autoApprovalsRows() [][]telegram.InlineButton {
	return operatorAutoPresetRows(autoSurfaceApprovals)
}

func autoLimitsRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			autoButton(autoSurfaceHome, autoActionShow, "Back"),
			autoButton(autoSurfaceLimits, autoActionRefresh, "Refresh"),
		},
	}
}

func operatorAutoPresetRows(surface string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			autoButton(autoSurfaceHome, autoActionShow, "Back"),
			autoButton(surface, autoActionRefresh, "Refresh"),
		},
		{
			operatorAutoPresetButton(surface, "off"),
			autoButton(surface, autoActionDouble, "2× Time"),
		},
		{
			operatorAutoPresetButton(surface, "work15"),
			operatorAutoPresetButton(surface, "deploy15"),
			operatorAutoPresetButton(surface, "all15"),
		},
	}
}

func operatorAutoPresetButton(surface string, action string) telegram.InlineButton {
	preset, ok := operatorAutoPresetForAction(action)
	if !ok {
		return autoButton(surface, action, strings.TrimSpace(action))
	}
	return autoButton(surface, preset.Action, preset.Label)
}

func autoButton(surface string, action string, label string) telegram.InlineButton {
	return telegram.InlineButton{
		Text:         strings.TrimSpace(label),
		CallbackData: encodeAutoCallbackData(surface, action),
	}
}

func encodeAutoCallbackData(surface string, action string) string {
	surface = strings.TrimSpace(surface)
	action = strings.TrimSpace(action)
	if surface == "" {
		surface = autoSurfaceHome
	}
	if action == "" {
		action = autoActionShow
	}
	return autoCallbackPrefix + surface + ":" + action
}

func decodeAutoCallbackData(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, autoCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, autoCallbackPrefix))
	surface, action := nextCommandToken(strings.ReplaceAll(payload, ":", " "))
	if action == "" {
		action = autoActionShow
	}
	if !validAutoSurface(surface) || !validAutoAction(action) {
		return "", "", false
	}
	return surface, action, true
}

func validAutoSurface(surface string) bool {
	switch strings.TrimSpace(surface) {
	case autoSurfaceHome, autoSurfaceMode, autoSurfaceApprovals, autoSurfaceLimits:
		return true
	default:
		return false
	}
}

func validAutoAction(action string) bool {
	switch strings.TrimSpace(action) {
	case autoActionShow, autoActionRefresh, autoActionDouble:
		return true
	default:
		_, ok := operatorAutoPresetForAction(action)
		return ok
	}
}

func handleAutoCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, surface string, action string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleAutoCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Auto controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}

	msg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	msg.ChatID = chatID
	msg.MessageID = messageID
	msg.SenderID = senderID
	text, rows, err := renderAutoCallbackResult(ctx, router, msg, surface, action)
	if err != nil {
		return true, err
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, strings.TrimSpace(text), "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func renderAutoCallbackResult(ctx context.Context, router commandRouter, msg core.InboundMessage, surface string, action string) (string, [][]telegram.InlineButton, error) {
	switch surface {
	case autoSurfaceHome:
		return prefixAutoThreadPanel(msg, renderAutoHomePanel()), autoHomeRows(), nil
	case autoSurfaceMode:
		text, err := renderAutoModeCallbackText(ctx, router, msg, action)
		return prefixAutoThreadPanel(msg, text), autoModeRows(), err
	case autoSurfaceApprovals:
		text, err := renderAutoApprovalsCallbackText(ctx, router, msg, action)
		return prefixAutoThreadPanel(msg, text), autoApprovalsRows(), err
	case autoSurfaceLimits:
		text, err := renderAutoLimitsCallbackText(router, msg)
		return prefixAutoThreadPanel(msg, text), autoLimitsRows(), err
	default:
		return prefixAutoThreadPanel(msg, renderAutoHomePanel()), autoHomeRows(), nil
	}
}

func renderAutoModeCallbackText(ctx context.Context, router commandRouter, msg core.InboundMessage, action string) (string, error) {
	switch action {
	case autoActionShow, autoActionRefresh:
		snapshot, err := autonomyStatusForAutoCommand(router, msg)
		if err != nil {
			return "", err
		}
		return face.RenderTelegramAutonomyStatus(snapshot), nil
	case autoActionDouble:
		text, err := configureAutonomyForAutoCommand(ctx, router, msg, autoActionDouble)
		if err != nil {
			return renderAutonomyCommandError(err), nil
		}
		return text, nil
	default:
		preset, ok := operatorAutoPresetForAction(action)
		if !ok {
			return renderAutonomyCommandError(nil), nil
		}
		text, err := configureAutonomyForAutoCommand(ctx, router, msg, preset.Mode)
		if err != nil {
			return renderAutonomyCommandError(err), nil
		}
		return text, nil
	}
}

func renderAutoLimitsCallbackText(router commandRouter, msg core.InboundMessage) (string, error) {
	snapshot, err := autonomyStatusForAutoCommand(router, msg)
	if err != nil {
		return "", err
	}
	return face.RenderTelegramAutoLimits(snapshot), nil
}

func renderAutoApprovalsCallbackText(ctx context.Context, router commandRouter, msg core.InboundMessage, action string) (string, error) {
	switch action {
	case autoActionShow, autoActionRefresh:
		text, err := autoApprovalStatusForAutoCommand(ctx, router, msg)
		if err != nil {
			return renderAutoApprovalCommandError(err), nil
		}
		return text, nil
	case autoActionDouble:
		text, err := configureAutoApprovalForAutoCommand(ctx, router, msg, autoActionDouble)
		if err != nil {
			return renderAutoApprovalCommandError(err), nil
		}
		return text, nil
	default:
		preset, ok := operatorAutoPresetForAction(action)
		if !ok {
			return renderAutoApprovalCommandError(nil), nil
		}
		text, err := configureAutoApprovalForAutoCommand(ctx, router, msg, preset.AutoApprove)
		if err != nil {
			return renderAutoApprovalCommandError(err), nil
		}
		return text, nil
	}
}

func operatorAutoPresetForAction(action string) (operatorAutoPreset, bool) {
	action = strings.TrimSpace(action)
	for _, preset := range operatorAutoPresets {
		if preset.Action == action {
			return preset, true
		}
	}
	return operatorAutoPreset{}, false
}
