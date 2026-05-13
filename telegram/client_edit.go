//go:build linux

package telegram

import (
	"context"
	"errors"
	"fmt"
)

func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}
	text = truncateTelegramText(text, telegramTextChunkLimit)
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       formatted.Text,
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok {
		if isTelegramMessageNotModified(resp.Description) {
			return nil
		}
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.editMessageTextFallback(ctx, chatID, messageID, formatted.PlainText, nil)
		}
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}
	text = truncateTelegramText(text, telegramTextChunkLimit)
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       formatted.Text,
		"reply_markup": inlineKeyboardMarkup{
			InlineKeyboard: [][]InlineButton{},
		},
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok {
		if isTelegramMessageNotModified(resp.Description) {
			return nil
		}
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.editMessageTextFallbackWithReplyMarkup(ctx, chatID, messageID, formatted.PlainText, [][]InlineButton{})
		}
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]InlineButton) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}
	if len(rows) == 0 {
		return errors.New("inline keyboard rows are required")
	}
	if err := validateInlineKeyboardRows(rows); err != nil {
		return err
	}
	text = truncateTelegramText(text, telegramTextChunkLimit)
	formatted := prepareFormattedText(text, parseMode)
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       formatted.Text,
		"reply_markup": inlineKeyboardMarkup{
			InlineKeyboard: rows,
		},
	}
	if formatted.ParseMode != "" {
		body["parse_mode"] = formatted.ParseMode
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok {
		if isTelegramMessageNotModified(resp.Description) {
			return nil
		}
		if formatted.ParseMode != "" && isTelegramParseError(resp.Description) {
			return c.editMessageTextFallback(ctx, chatID, messageID, formatted.PlainText, rows)
		}
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) editMessageTextRequest(ctx context.Context, body map[string]interface{}) (*editMessageResponse, error) {
	var resp editMessageResponse
	if err := c.post(ctx, "editMessageText", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) editMessageTextFallback(ctx context.Context, chatID int64, messageID int64, text string, rows [][]InlineButton) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if len(rows) > 0 {
		body["reply_markup"] = inlineKeyboardMarkup{
			InlineKeyboard: rows,
		}
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok && !isTelegramMessageNotModified(resp.Description) {
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}

func (c *Client) editMessageTextFallbackWithReplyMarkup(ctx context.Context, chatID int64, messageID int64, text string, rows [][]InlineButton) error {
	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
		"reply_markup": inlineKeyboardMarkup{
			InlineKeyboard: rows,
		},
	}
	resp, err := c.editMessageTextRequest(ctx, body)
	if err != nil {
		return err
	}
	if !resp.Ok && !isTelegramMessageNotModified(resp.Description) {
		return fmt.Errorf("telegram editMessageText failed: %s", resp.Description)
	}
	return nil
}
