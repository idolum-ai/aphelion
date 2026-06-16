//go:build linux

package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = c.pollTimeout
	}
	payload := map[string]interface{}{
		"offset":          offset,
		"timeout":         timeoutSeconds,
		"allowed_updates": []string{"message", "callback_query", "message_reaction"},
	}
	var resp getUpdatesResponse
	if err := c.post(ctx, "getUpdates", payload, &resp); err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, telegramGetUpdatesError{
			description: strings.TrimSpace(resp.Description),
			retryAfter:  time.Duration(resp.Parameters.RetryAfter) * time.Second,
		}
	}
	return resp.Result, nil
}

type telegramGetUpdatesError struct {
	description string
	retryAfter  time.Duration
}

func (e telegramGetUpdatesError) Error() string {
	description := strings.TrimSpace(e.description)
	if description == "" {
		description = "unknown telegram error"
	}
	if e.retryAfter > 0 {
		return fmt.Sprintf("telegram getUpdates failed: %s (retry_after=%s)", description, e.retryAfter)
	}
	return "telegram getUpdates failed: " + description
}

func (e telegramGetUpdatesError) RetryAfterDelay() time.Duration {
	if e.retryAfter <= 0 {
		return 0
	}
	return e.retryAfter
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var resp getMeResponse
	if err := c.post(ctx, "getMe", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	if !resp.Ok {
		return nil, fmt.Errorf("telegram getMe failed: %s", resp.Description)
	}
	user := resp.Result
	return &user, nil
}
