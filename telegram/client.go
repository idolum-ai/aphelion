//go:build linux

package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const defaultPollTimeoutSeconds = 30
const telegramTextChunkLimit = 3800
const telegramCaptionLimit = 1024
const telegramCallbackAnswerLimit = 200

type Client struct {
	token       string
	baseURL     string
	httpClient  *http.Client
	pollTimeout int
}

type FileInfo struct {
	Path string
	Size int64
}

type ClientOption func(*Client)

func NewClient(token string, opts ...ClientOption) *Client {
	base := fmt.Sprintf("https://api.telegram.org/bot%s/", token)
	c := &Client{
		token:       token,
		baseURL:     base,
		httpClient:  http.DefaultClient,
		pollTimeout: defaultPollTimeoutSeconds,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithBaseURL(base string) ClientOption {
	return func(c *Client) {
		if base != "" {
			c.baseURL = base
		}
	}
}

func WithPollTimeout(seconds int) ClientOption {
	return func(c *Client) {
		if seconds > 0 {
			c.pollTimeout = seconds
		}
	}
}

func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s%s", c.baseURL, method)
}

type redactedTelegramError struct {
	err  error
	text string
}

func (e redactedTelegramError) Error() string {
	return e.text
}

func (e redactedTelegramError) Unwrap() error {
	return e.err
}

func (c *Client) redactError(err error) error {
	if err == nil {
		return nil
	}
	text := redactTelegramToken(err.Error(), c.token)
	if text == err.Error() {
		return err
	}
	return redactedTelegramError{err: err, text: text}
}

func redactTelegramToken(text string, token string) string {
	token = strings.TrimSpace(token)
	if token == "" || text == "" {
		return text
	}
	redacted := strings.ReplaceAll(text, "/file/bot"+token+"/", "/file/bot[REDACTED]/")
	redacted = strings.ReplaceAll(redacted, "/bot"+token+"/", "/bot[REDACTED]/")
	redacted = strings.ReplaceAll(redacted, "bot"+token, "bot[REDACTED]")
	return redacted
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int64) error {
	if chatID == 0 {
		return errors.New("chat_id is required")
	}
	if messageID == 0 {
		return errors.New("message_id is required")
	}

	body := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
	}
	var resp telegramOKResponse
	if err := c.post(ctx, "deleteMessage", body, &resp); err != nil {
		return err
	}
	if !resp.Ok {
		return fmt.Errorf("telegram deleteMessage failed: %s", resp.Description)
	}
	return nil
}
