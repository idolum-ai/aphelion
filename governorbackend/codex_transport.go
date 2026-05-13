//go:build linux

package governorbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/governorauth"
)

func codexSleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Codex) syncCredentialsFromStore() {
	if c == nil || c.loadTokens == nil {
		return
	}
	tokens, err := c.loadTokens()
	if err != nil {
		return
	}

	access := strings.TrimSpace(tokens.AccessToken)
	refresh := strings.TrimSpace(tokens.RefreshToken)
	accountID := strings.TrimSpace(tokens.AccountID)
	if access == "" && refresh == "" && accountID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if refresh != "" && refresh != c.refreshToken {
		c.refreshToken = refresh
		if access != "" {
			c.accessToken = access
		}
		if accountID != "" {
			c.accountID = accountID
		}
		return
	}
	if c.accessToken == "" && access != "" {
		c.accessToken = access
	}
	if c.accountID == "" && accountID != "" {
		c.accountID = accountID
	}
}

func (c *Codex) doRequest(ctx context.Context, body *bytes.Buffer, accessToken string, accountID string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("codex: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: request: %w", redactError(err, accessToken))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCodexResponseBytes))
		if err != nil {
			return nil, fmt.Errorf("codex: read response: %w", err)
		}
		bodyMessage := redactBodyExcerpt(raw, accessToken, refreshTokenForRedaction(c), accountID)
		retryAfter := parseCodexRetryAfterHeader(resp.Header.Get("Retry-After"), c.now())
		if retryAfter <= 0 {
			retryAfter = parseCodexRetryAfter(bodyMessage)
		}
		return nil, codexAPIError{
			statusCode: resp.StatusCode,
			message:    codexStatusMessage(resp.StatusCode, bodyMessage),
			cause:      codexStatusCause(resp.StatusCode),
			code:       inferCodexHTTPFailureCode(resp.StatusCode, bodyMessage),
			retryAfter: retryAfter,
		}
	}
	return resp, nil
}

func isRetryableCodexTransportError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr codexAPIError
	if errors.As(err, &apiErr) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, " eof") ||
		strings.HasSuffix(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "stream closed")
}

func (c *Codex) currentCredentials() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken, c.accountID
}

func (c *Codex) reauthorize(ctx context.Context, staleAccessToken string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && c.accessToken != staleAccessToken {
		return true, nil
	}

	var reloadErr error
	if c.loadTokens != nil {
		tokens, err := c.loadTokens()
		switch {
		case err == nil:
			if strings.TrimSpace(tokens.AccessToken) != "" && tokens.AccessToken != c.accessToken {
				c.accessToken = strings.TrimSpace(tokens.AccessToken)
				if strings.TrimSpace(tokens.RefreshToken) != "" {
					c.refreshToken = strings.TrimSpace(tokens.RefreshToken)
				}
				if strings.TrimSpace(tokens.AccountID) != "" {
					c.accountID = strings.TrimSpace(tokens.AccountID)
				}
				return true, nil
			}
			if strings.TrimSpace(tokens.RefreshToken) != "" {
				c.refreshToken = strings.TrimSpace(tokens.RefreshToken)
			}
			if strings.TrimSpace(tokens.AccountID) != "" {
				c.accountID = strings.TrimSpace(tokens.AccountID)
			}
		case !errors.Is(err, governorauth.ErrCodexAuthNotFound):
			reloadErr = fmt.Errorf("reload codex auth: %w", err)
		}
	}

	refreshToken := strings.TrimSpace(c.refreshToken)
	if refreshToken == "" {
		return false, reloadErr
	}

	tokens, err := c.refreshTokens(ctx, refreshToken)
	if err != nil {
		if reloadErr != nil {
			return false, fmt.Errorf("%w after %v", err, reloadErr)
		}
		return false, err
	}
	c.accessToken = tokens.AccessToken
	c.refreshToken = tokens.RefreshToken
	if strings.TrimSpace(tokens.AccountID) != "" {
		c.accountID = strings.TrimSpace(tokens.AccountID)
	}
	if c.saveTokens != nil {
		_ = c.saveTokens(tokens, c.now())
	}
	return true, nil
}

func (c *Codex) refreshTokens(ctx context.Context, refreshToken string) (governorauth.CodexTokens, error) {
	reqBody := map[string]string{
		"client_id":     codexRefreshClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.refreshURL, &body)
	if err != nil {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: request: %w", redactError(err, refreshToken))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCodexResponseBytes))
	if err != nil {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: status %d", resp.StatusCode)
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: decode response: %w", err)
	}
	access := strings.TrimSpace(parsed.AccessToken)
	refresh := strings.TrimSpace(parsed.RefreshToken)
	if access == "" {
		return governorauth.CodexTokens{}, fmt.Errorf("codex refresh: missing access token")
	}
	if refresh == "" {
		refresh = strings.TrimSpace(refreshToken)
	}
	return governorauth.CodexTokens{
		AccessToken:  access,
		RefreshToken: refresh,
		AccountID:    c.accountID,
	}, nil
}
