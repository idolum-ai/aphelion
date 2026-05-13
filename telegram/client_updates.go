//go:build linux

package telegram

import (
	"context"
	"fmt"
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
		return nil, fmt.Errorf("telegram getUpdates failed: %s", resp.Description)
	}
	return resp.Result, nil
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
