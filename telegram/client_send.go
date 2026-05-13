//go:build linux

package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/idolum-ai/aphelion/core"
)

func (c *Client) SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error) {
	if msg.ChatID == 0 {
		return 0, errors.New("chat_id is required")
	}
	if len(msg.Reactions) > 0 {
		if msg.ReplyTo == nil || *msg.ReplyTo == 0 {
			return 0, errors.New("reply_to message id is required for reactions")
		}
		if err := c.SetMessageReactions(ctx, msg.ChatID, *msg.ReplyTo, msg.Reactions); err != nil {
			return 0, err
		}
		if strings.TrimSpace(msg.Text) == "" && len(msg.Media) == 0 {
			return *msg.ReplyTo, nil
		}
	}
	if len(msg.Media) > 0 {
		return c.sendMediaMessage(ctx, msg)
	}
	chunks := splitTelegramTextChunks(msg.Text, telegramTextChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{""}
	}

	firstMessageID := int64(0)
	for i, chunk := range chunks {
		replyTo := (*int64)(nil)
		if i == 0 {
			replyTo = msg.ReplyTo
		}
		messageID, err := c.sendMessageChunk(ctx, msg.ChatID, chunk, msg.ParseMode, replyTo)
		if err != nil {
			return 0, err
		}
		if firstMessageID == 0 {
			firstMessageID = messageID
		}
	}
	return firstMessageID, nil
}

func (c *Client) sendMediaMessage(ctx context.Context, msg core.OutboundMessage) (int64, error) {
	caption, overflow := splitTelegramCaption(msg.Text)
	firstMessageID := int64(0)
	replyTo := msg.ReplyTo
	for idx, media := range msg.Media {
		itemCaption := ""
		if idx == 0 {
			itemCaption = caption
		}
		messageID, err := c.sendMediaItem(ctx, msg.ChatID, media, itemCaption, replyTo)
		if err != nil {
			return 0, err
		}
		if firstMessageID == 0 {
			firstMessageID = messageID
		}
		replyTo = nil
	}
	for _, chunk := range splitTelegramTextChunks(overflow, telegramTextChunkLimit) {
		messageID, err := c.sendMessageChunk(ctx, msg.ChatID, chunk, msg.ParseMode, nil)
		if err != nil {
			return 0, err
		}
		if firstMessageID == 0 {
			firstMessageID = messageID
		}
	}
	return firstMessageID, nil
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	if len(commands) == 0 {
		return errors.New("commands are required")
	}

	body := map[string]interface{}{
		"commands": commands,
	}
	var resp setMyCommandsResponse
	if err := c.post(ctx, "setMyCommands", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram setMyCommands failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return errors.New("action is required")
	}

	body := map[string]interface{}{
		"chat_id": chatID,
		"action":  action,
	}
	var resp telegramOKResponse
	if err := c.post(ctx, "sendChatAction", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram sendChatAction failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) SetMessageReaction(ctx context.Context, chatID int64, messageID int64, emoji string) error {
	return c.SetMessageReactions(ctx, chatID, messageID, []string{emoji})
}

func (c *Client) SetMessageReactions(ctx context.Context, chatID int64, messageID int64, emojis []string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}
	reactions := make([]map[string]string, 0, len(emojis))
	for _, emoji := range emojis {
		emoji = strings.TrimSpace(emoji)
		if emoji == "" {
			continue
		}
		reactions = append(reactions, map[string]string{"type": "emoji", "emoji": emoji})
	}
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction":   reactions,
	}
	var resp telegramOKResponse
	if err := c.post(ctx, "setMessageReaction", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram setMessageReaction failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]InlineButton, replyTo *int64) (int64, error) {
	if chatID == 0 {
		return 0, errors.New("chat_id is required")
	}
	if len(rows) == 0 {
		return 0, errors.New("inline keyboard rows are required")
	}
	if err := validateInlineKeyboardRows(rows); err != nil {
		return 0, err
	}

	chunks := splitTelegramTextChunks(text, telegramTextChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{"Decision required."}
	}

	firstMessageID := int64(0)
	for i, chunk := range chunks {
		if i == 0 {
			messageID, err := c.sendInlineKeyboardChunk(ctx, chatID, chunk, rows, replyTo)
			if err != nil {
				return 0, err
			}
			firstMessageID = messageID
			continue
		}

		messageID, err := c.sendMessageChunk(ctx, chatID, chunk, "", nil)
		if err != nil {
			return 0, err
		}
		if firstMessageID == 0 {
			firstMessageID = messageID
		}
	}
	return firstMessageID, nil
}

func validateInlineKeyboardRows(rows [][]InlineButton) error {
	for rowIndex, row := range rows {
		for buttonIndex, button := range row {
			text := strings.TrimSpace(button.Text)
			if text == "" {
				return fmt.Errorf("inline button label is required at row %d button %d", rowIndex, buttonIndex)
			}
			if words := strings.Fields(text); len(words) > 2 {
				return fmt.Errorf("inline button label %q has %d words; Telegram labels must use at most 2 words", text, len(words))
			}
		}
	}
	return nil
}

func (c *Client) sendInlineKeyboardChunk(ctx context.Context, chatID int64, text string, rows [][]InlineButton, replyTo *int64) (int64, error) {
	formatted := prepareFormattedText(text, "")
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    formatted.Text,
		"reply_markup": inlineKeyboardMarkup{
			InlineKeyboard: rows,
		},
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.sendInlineKeyboardFallback(ctx, chatID, formatted.PlainText, rows, replyTo)
		}
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func (c *Client) sendInlineKeyboardFallback(ctx context.Context, chatID int64, text string, rows [][]InlineButton, replyTo *int64) (int64, error) {
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": inlineKeyboardMarkup{
			InlineKeyboard: rows,
		},
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackQueryID string, text string) error {
	callbackQueryID = strings.TrimSpace(callbackQueryID)
	if callbackQueryID == "" {
		return errors.New("callback_query_id is required")
	}

	body := map[string]interface{}{
		"callback_query_id": callbackQueryID,
	}
	if strings.TrimSpace(text) != "" {
		body["text"] = strings.TrimSpace(text)
	}

	var resp telegramOKResponse
	if err := c.post(ctx, "answerCallbackQuery", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram answerCallbackQuery failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) sendMessageRequest(ctx context.Context, body map[string]interface{}) (*sendMessageResponse, error) {
	var resp sendMessageResponse
	if err := c.post(ctx, "sendMessage", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) sendMessageChunk(ctx context.Context, chatID int64, text string, parseMode string, replyTo *int64) (int64, error) {
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    formatted.Text,
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.sendMessageFallback(ctx, chatID, formatted.PlainText, replyTo)
		}
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func (c *Client) sendMessageFallback(ctx context.Context, chatID int64, text string, replyTo *int64) (int64, error) {
	body := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
	}
	if replyTo != nil {
		body["reply_to_message_id"] = *replyTo
	}
	resp, err := c.sendMessageRequest(ctx, body)
	if err != nil {
		return 0, err
	}
	if !resp.Ok {
		return 0, fmt.Errorf("telegram sendMessage failed: %s", resp.Description)
	}
	return resp.Result.MessageID, nil
}

func splitTelegramTextChunks(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if limit <= 0 {
		limit = telegramTextChunkLimit
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	var chunks []string
	for len(runes) > 0 {
		if len(runes) <= limit {
			chunk := strings.TrimSpace(string(runes))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		split := bestTelegramChunkBoundary(runes, limit)
		if split <= 0 || split > len(runes) {
			split = limit
		}
		chunk := strings.TrimSpace(string(runes[:split]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = trimLeadingTelegramChunkRunes(runes[split:])
	}
	return chunks
}

func bestTelegramChunkBoundary(runes []rune, limit int) int {
	if limit <= 0 || len(runes) <= limit {
		return len(runes)
	}
	for i := limit; i > 0; i-- {
		if i >= 2 && runes[i-2] == '\n' && runes[i-1] == '\n' {
			return i
		}
	}
	for i := limit; i > 0; i-- {
		if runes[i-1] == '\n' {
			return i
		}
	}
	for i := limit; i > 0; i-- {
		if runes[i-1] == ' ' {
			return i
		}
	}
	return limit
}

func trimLeadingTelegramChunkRunes(runes []rune) []rune {
	start := 0
	for start < len(runes) {
		if runes[start] == '\n' || runes[start] == ' ' || runes[start] == '\t' {
			start++
			continue
		}
		break
	}
	return runes[start:]
}

func truncateTelegramText(text string, limit int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if limit <= 0 {
		limit = telegramTextChunkLimit
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func runeCount(text string) int {
	return utf8.RuneCountInString(text)
}
