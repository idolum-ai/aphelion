//go:build linux

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (c *Client) post(ctx context.Context, method string, body interface{}, out interface{}) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", method, c.redactError(err))
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		if resp.StatusCode != http.StatusOK {
			return telegramHTTPErrorFromBody(method, resp.StatusCode, bodyBytes)
		}
		return fmt.Errorf("decode %s response: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	return nil
}

func telegramHTTPError(method string, resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s unexpected status %d (read body: %v)", method, resp.StatusCode, err)
	}
	return telegramHTTPErrorFromBody(method, resp.StatusCode, body)
}

func telegramHTTPErrorFromBody(method string, status int, body []byte) error {
	description := telegramErrorDescription(body)
	if description == "" {
		description = truncateTelegramErrorBody(body)
	}
	if description == "" {
		return fmt.Errorf("%s unexpected status %d", method, status)
	}
	return fmt.Errorf("%s unexpected status %d: %s", method, status, description)
}

func telegramErrorDescription(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Description)
}

func truncateTelegramErrorBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 240 {
		return trimmed
	}
	return string(runes[:239]) + "…"
}
