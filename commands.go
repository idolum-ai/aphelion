//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type commandSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type commandCallbackSender interface {
	commandSender
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	Detach(chatID int64, senderID int64) (core.DetachResult, error)
	Restart(chatID int64) error
	CanRestart(senderID int64) bool
	Status(chatID int64) core.SessionStatus
	StatusChat(chatID int64) (core.ChatStatusSnapshot, error)
	StatusSystem(senderID int64) (core.SystemStatusSnapshot, error)
	ContinuationState(chatID int64) (session.ContinuationState, error)
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	StopContinuation(chatID int64) (core.StopResult, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	QueueReinstall(ctx context.Context, msg core.InboundMessage) error
	CurrentEfforts() (persona string, governor string)
	CurrentPersonaModel() string
	PersonaModelOptions() []string
	SetPersonaModel(model string) (string, error)
	GovernorEffortOptions() []string
	SetGovernorEffort(effort string) (string, error)
}

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show live status and controls"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "detach", Description: "Detach from pending work in this chat"},
	{Command: "restart", Description: "Force an immediate gateway restart"},
	{Command: "reinstall", Description: "Queue a rebuild/reinstall/restart request"},
	{Command: "set_persona_model", Description: "Choose Idolum persona model"},
	{Command: "set_governor_effort", Description: "Choose governor reasoning effort"},
}

const recipeCallbackPrefix = "recipe:"
const continuationCallbackPrefix = "continuation:"
const statusCallbackPrefix = "status:"
const staleContinuationCallbackText = "This continuation prompt is no longer active. Use the newest prompt."
const staleStatusCallbackText = "This status action is no longer available. Run /status again."
const adminStatusOnlyText = "This status view is available to Telegram admins only."
const statusMessageChunkLimit = 3800

type statusView string

const (
	statusViewChat       statusView = "chat"
	statusViewPending    statusView = "pending"
	statusViewSystem     statusView = "system"
	statusViewHotChats   statusView = "hot"
	statusViewFindChat   statusView = "find"
	statusViewChatTarget statusView = "chat_target"
)

func registerTelegramCommands(ctx context.Context, client *telegram.Client) error {
	if client == nil {
		return fmt.Errorf("telegram client is required")
	}
	return client.SetMyCommands(ctx, defaultTelegramCommands)
}

func handleTelegramCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false, nil
	}
	command, ok := parseTelegramCommand(msg.Text)
	if !ok {
		return false, nil
	}

	personaEffort, governorEffort := router.CurrentEfforts()
	var text string
	restartRequested := false
	switch command {
	case "start":
		text = face.RenderTelegramStart(personaEffort, governorEffort)
	case "help":
		text = face.RenderTelegramHelp(personaEffort, governorEffort)
	case "status":
		rendered, rows, renderErr := renderStatusView(router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
		if renderErr != nil {
			return true, renderErr
		}
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "stop":
		text = face.RenderTelegramStop(router.Stop(msg.ChatID))
	case "detach":
		detached, detachErr := router.Detach(msg.ChatID, msg.SenderID)
		if detachErr != nil {
			return true, detachErr
		}
		text = face.RenderTelegramDetach(detached)
	case "restart":
		if router.CanRestart(msg.SenderID) {
			text = face.RenderTelegramRestart()
			restartRequested = true
		} else {
			text = face.RenderTelegramRestartDenied()
		}
	case "reinstall":
		if err := router.QueueReinstall(ctx, msg); err != nil {
			return true, err
		}
		text = face.RenderTelegramQueuedReinstall()
	case "set_persona_model":
		return sendPersonaModelSelector(ctx, sender, router, msg)
	case "set_governor_effort":
		return sendGovernorEffortSelector(ctx, sender, router, msg)
	default:
		return false, nil
	}

	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    text,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if restartRequested {
		if restartErr := router.Restart(msg.ChatID); restartErr != nil {
			return true, restartErr
		}
	}
	if err != nil {
		return true, err
	}
	return true, nil
}

func sendPersonaModelSelector(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if sender == nil || router == nil {
		return true, fmt.Errorf("command handler unavailable")
	}
	current := strings.TrimSpace(router.CurrentPersonaModel())
	options := router.PersonaModelOptions()
	rows := recipeSelectorRows("persona_model", current, options, personaModelButtonLabel)
	text := strings.TrimSpace(face.RenderTelegramPersonaModelSelector(current, options))
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	return true, nil
}

func sendGovernorEffortSelector(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if sender == nil || router == nil {
		return true, fmt.Errorf("command handler unavailable")
	}
	_, current := router.CurrentEfforts()
	options := router.GovernorEffortOptions()
	rows := recipeSelectorRows("governor_effort", current, options, governorEffortButtonLabel)
	text := strings.TrimSpace(face.RenderTelegramGovernorEffortSelector(current, options))
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	return true, nil
}

func handleTelegramCommandCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	if sender == nil || router == nil {
		return false, nil
	}
	if view, targetChatID, ok := decodeStatusCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		senderID := int64(0)
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if statusViewRequiresAdmin(view, chatID, targetChatID) && !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminStatusOnlyText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		rendered, rows, err := renderStatusView(router, chatID, senderID, view, targetChatID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if chatID == 0 {
			chatID = targetChatID
		}
		if chatID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := deliverStatusCallbackView(ctx, sender, chatID, messageID, rendered, rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if decisionID, action, ok := decodeContinuationCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil && cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			messageID = cb.Message.MessageID
		}
		state, err := router.ContinuationState(chatID)
		if err != nil {
			return true, err
		}
		if !continuationCallbackMatchesState(state, decisionID, action) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleContinuationCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		var text string
		switch action {
		case "approve":
			approverID := int64(0)
			if cb.From != nil {
				approverID = cb.From.ID
			}
			state, err := router.ApproveContinuation(chatID, approverID)
			if err != nil {
				return true, err
			}
			if err := router.TriggerContinuation(ctx, chatID); err != nil {
				return true, err
			}
			text = renderContinuationDecision(state, true)
			if messageID != 0 {
				if err := sender.EditMessageText(ctx, chatID, messageID, text, ""); err != nil {
					log.Printf("WARN continuation approve message update failed chat_id=%d message_id=%d err=%v", chatID, messageID, err)
				}
			}
			return true, nil
		case "stop":
			stopped, err := router.StopContinuation(chatID)
			if err != nil {
				return true, err
			}
			text = face.RenderTelegramStop(stopped)
			if messageID != 0 {
				if err := sender.EditMessageText(ctx, chatID, messageID, text, ""); err != nil {
					return true, err
				}
			}
			return true, nil
		default:
			return true, nil
		}
	}
	kind, value, ok := decodeRecipeCallbackData(cb.Data)
	if !ok {
		return false, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}

	var (
		text string
		err  error
	)
	switch kind {
	case "persona_model":
		var selected string
		selected, err = router.SetPersonaModel(value)
		if err == nil {
			text = face.RenderTelegramSetPersonaModel(selected)
		}
	case "governor_effort":
		var selected string
		selected, err = router.SetGovernorEffort(value)
		if err == nil {
			text = face.RenderTelegramSetGovernorEffort(selected)
		}
	default:
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if cb.Message != nil && cb.Message.Chat != nil && cb.Message.MessageID != 0 {
		if editErr := sender.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, ""); editErr != nil {
			return true, editErr
		}
	}
	return true, nil
}

func recipeSelectorRows(kind string, current string, options []string, labelFor func(string) string) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, 2)
	row := make([]telegram.InlineButton, 0, 2)
	current = strings.ToLower(strings.TrimSpace(current))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		label := labelFor(option)
		if strings.EqualFold(option, current) {
			label = "• " + label
		}
		row = append(row, telegram.InlineButton{
			Text:         label,
			CallbackData: encodeRecipeCallbackData(kind, option),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]telegram.InlineButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func encodeRecipeCallbackData(kind string, value string) string {
	return recipeCallbackPrefix + strings.TrimSpace(kind) + ":" + strings.TrimSpace(value)
}

func decodeRecipeCallbackData(data string) (kind string, value string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, recipeCallbackPrefix) {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	kind = strings.TrimSpace(parts[1])
	value = strings.TrimSpace(parts[2])
	if kind == "" || value == "" {
		return "", "", false
	}
	return kind, value, true
}

func continuationApprovalRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: encodeContinuationCallbackData("decision", "stop")},
		{Text: "Continue", CallbackData: encodeContinuationCallbackData("decision", "approve")},
	}}
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if decisionID == "" {
		return continuationCallbackPrefix + action
	}
	return continuationCallbackPrefix + decisionID + ":" + action
}

func decodeContinuationCallbackData(data string) (decisionID string, action string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, continuationCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, continuationCallbackPrefix))
	if payload == "" {
		return "", "", false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) == 1 {
		action = strings.TrimSpace(parts[0])
		if action == "" {
			return "", "", false
		}
		return "", action, true
	}
	decisionID = strings.TrimSpace(parts[0])
	action = strings.TrimSpace(parts[1])
	if decisionID == "" || action == "" {
		return "", "", false
	}
	return decisionID, action, true
}

func continuationCallbackMatchesState(state session.ContinuationState, decisionID string, action string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if decisionID == "" || state.DecisionID == "" {
		return false
	}
	if decisionID != state.DecisionID {
		return false
	}
	switch action {
	case "approve":
		return state.Status == session.ContinuationStatusPending && state.RemainingTurns > 0
	case "stop":
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	default:
		return false
	}
}

func renderContinuationDecision(state session.ContinuationState, approved bool) string {
	if !approved {
		return ""
	}
	text := "Continuation approved."
	if state.RemainingTurns > 0 {
		text += fmt.Sprintf(" Remaining turns: %d.", state.RemainingTurns)
	}
	if state.StageSummary != "" {
		text += " Next: " + state.StageSummary
	}
	return text
}

func renderStatusView(router commandRouter, currentChatID int64, senderID int64, view statusView, targetChatID int64, personaEffort string, governorEffort string) (string, [][]telegram.InlineButton, error) {
	if router == nil {
		return "", nil, fmt.Errorf("status router is unavailable")
	}
	isAdmin := router.CanRestart(senderID)
	if view == "" {
		view = statusViewChat
	}
	if targetChatID == 0 {
		targetChatID = currentChatID
	}

	var (
		text         string
		systemStatus core.SystemStatusSnapshot
		systemLoaded bool
	)

	switch view {
	case statusViewChat:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	case statusViewPending:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, true)
	case statusViewChatTarget:
		chat, err := router.StatusChat(targetChatID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	case statusViewSystem:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		text = face.RenderTelegramStatusSystem(status, personaEffort, governorEffort)
	case statusViewHotChats:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		text = face.RenderTelegramStatusHotChats(status)
	case statusViewFindChat:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		text = face.RenderTelegramStatusFindChat(status)
	default:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		view = statusViewChat
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	}
	rows := statusKeyboardRows(view, currentChatID, targetChatID, isAdmin, systemStatus, systemLoaded)
	return text, rows, nil
}

func statusKeyboardRows(view statusView, currentChatID int64, targetChatID int64, isAdmin bool, system core.SystemStatusSnapshot, systemLoaded bool) [][]telegram.InlineButton {
	if targetChatID == 0 {
		targetChatID = currentChatID
	}
	activeView := view
	if activeView == "" {
		activeView = statusViewChat
	}

	rows := [][]telegram.InlineButton{
		{
			{Text: "This Chat", CallbackData: encodeStatusCallbackData(statusViewChat, currentChatID)},
			{Text: "Pending Only", CallbackData: encodeStatusCallbackData(statusViewPending, currentChatID)},
			{Text: "Refresh", CallbackData: encodeStatusCallbackData(activeView, targetChatID)},
		},
	}
	if isAdmin {
		rows = append(rows, []telegram.InlineButton{
			{Text: "System Overview", CallbackData: encodeStatusCallbackData(statusViewSystem, 0)},
			{Text: "Hot Chats", CallbackData: encodeStatusCallbackData(statusViewHotChats, 0)},
			{Text: "Find Chat", CallbackData: encodeStatusCallbackData(statusViewFindChat, 0)},
		})
	}
	if isAdmin && systemLoaded && view == statusViewFindChat {
		maxChats := len(system.HotChats)
		if maxChats > 12 {
			maxChats = 12
		}
		for i := 0; i < maxChats; i += 2 {
			row := make([]telegram.InlineButton, 0, 2)
			chatA := system.HotChats[i]
			row = append(row, telegram.InlineButton{
				Text:         fmt.Sprintf("Chat %d", chatA.ChatID),
				CallbackData: encodeStatusCallbackData(statusViewChatTarget, chatA.ChatID),
			})
			if i+1 < maxChats {
				chatB := system.HotChats[i+1]
				row = append(row, telegram.InlineButton{
					Text:         fmt.Sprintf("Chat %d", chatB.ChatID),
					CallbackData: encodeStatusCallbackData(statusViewChatTarget, chatB.ChatID),
				})
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func statusViewRequiresAdmin(view statusView, callbackChatID int64, targetChatID int64) bool {
	switch view {
	case statusViewSystem, statusViewHotChats, statusViewFindChat:
		return true
	case statusViewChatTarget:
		return targetChatID != 0 && (callbackChatID == 0 || targetChatID != callbackChatID)
	default:
		return false
	}
}

func encodeStatusCallbackData(view statusView, chatID int64) string {
	switch view {
	case statusViewChat:
		return statusCallbackPrefix + "chat"
	case statusViewPending:
		return statusCallbackPrefix + "pending"
	case statusViewSystem:
		return statusCallbackPrefix + "system"
	case statusViewHotChats:
		return statusCallbackPrefix + "hot"
	case statusViewFindChat:
		return statusCallbackPrefix + "find"
	case statusViewChatTarget:
		return statusCallbackPrefix + "chat:" + strconv.FormatInt(chatID, 10)
	default:
		return statusCallbackPrefix + "chat"
	}
}

func decodeStatusCallbackData(data string) (statusView, int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, statusCallbackPrefix) {
		return "", 0, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, statusCallbackPrefix))
	if payload == "" {
		return "", 0, false
	}
	parts := strings.Split(payload, ":")
	if len(parts) == 1 {
		switch parts[0] {
		case "chat":
			return statusViewChat, 0, true
		case "pending":
			return statusViewPending, 0, true
		case "system":
			return statusViewSystem, 0, true
		case "hot":
			return statusViewHotChats, 0, true
		case "find":
			return statusViewFindChat, 0, true
		default:
			return "", 0, false
		}
	}
	if len(parts) == 2 && parts[0] == "chat" {
		chatID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || chatID == 0 {
			return "", 0, false
		}
		return statusViewChatTarget, chatID, true
	}
	return "", 0, false
}

func deliverStatusCallbackView(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string, rows [][]telegram.InlineButton) error {
	if sender == nil {
		return nil
	}
	chunks := splitStatusTextChunks(text, statusMessageChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{"status_scope=chat\nsummary unavailable"}
	}
	first := chunks[0]
	if messageID != 0 {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, first, "", rows); err != nil {
			return err
		}
	} else {
		if _, err := sender.SendInlineKeyboard(ctx, chatID, first, rows, nil); err != nil {
			return err
		}
	}
	for i := 1; i < len(chunks); i++ {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   chunks[i],
		}); err != nil {
			return err
		}
	}
	return nil
}

func splitStatusTextChunks(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if limit <= 0 {
		limit = statusMessageChunkLimit
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)/limit)+1)
	for len(runes) > 0 {
		if len(runes) <= limit {
			chunk := strings.TrimSpace(string(runes))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}
		cut := limit
		for i := cut; i > cut/2; i-- {
			if runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[cut:]
	}
	return chunks
}

func personaModelButtonLabel(model string) string {
	trimmed := strings.TrimSpace(model)
	switch strings.ToLower(trimmed) {
	case "claude-opus-4-7":
		return "Opus 4.7"
	case "claude-opus-4-6":
		return "Opus 4.6"
	case "claude-sonnet-4-6":
		return "Sonnet 4.6"
	default:
		return trimmed
	}
}

func governorEffortButtonLabel(effort string) string {
	return strings.ToUpper(strings.TrimSpace(effort))
}

func parseTelegramCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] != '/' {
		return "", false
	}

	token := text
	if idx := strings.IndexAny(token, " \n\t"); idx >= 0 {
		token = token[:idx]
	}
	if len(token) < 2 {
		return "", false
	}

	token = token[1:]
	if at := strings.IndexByte(token, '@'); at >= 0 {
		token = token[:at]
	}
	if token == "" {
		return "", false
	}
	for i, r := range token {
		if i == 0 {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return "", false
			}
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "", false
	}
	return strings.ToLower(token), true
}

func replyToMessageID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
