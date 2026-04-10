//go:build linux

package governorbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/governorauth"
)

const maxCodexResponseBytes = 1 << 20 // 1 MiB

const codexRefreshClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

var (
	ErrCodexUnauthorized = errors.New("codex unauthorized")
	ErrCodexForbidden    = errors.New("codex forbidden")
	ErrCodexRateLimited  = errors.New("codex rate limited")
	ErrCodexServer       = errors.New("codex upstream failure")
)

type CodexOptions struct {
	BaseURL      string
	AccessToken  string
	RefreshToken string
	RefreshURL   string
	HTTPClient   *http.Client
	UserAgent    string
	LoadTokens   func() (governorauth.CodexTokens, error)
	SaveTokens   func(governorauth.CodexTokens, time.Time) error
	Now          func() time.Time
}

type Codex struct {
	baseURL    string
	refreshURL string
	client     *http.Client
	userAgent  string
	loadTokens func() (governorauth.CodexTokens, error)
	saveTokens func(governorauth.CodexTokens, time.Time) error
	now        func() time.Time

	mu           sync.Mutex
	accessToken  string
	refreshToken string
}

var _ agent.Provider = (*Codex)(nil)

func NewCodex(opts CodexOptions) (*Codex, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("codex: base url is required")
	}
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fmt.Errorf("codex: access token is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	refreshURL := strings.TrimSpace(opts.RefreshURL)
	if refreshURL == "" {
		refreshURL = governorauth.DefaultCodexRefreshURL
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Codex{
		baseURL:      opts.BaseURL,
		refreshURL:   refreshURL,
		accessToken:  opts.AccessToken,
		refreshToken: strings.TrimSpace(opts.RefreshToken),
		client:       client,
		userAgent:    opts.UserAgent,
		loadTokens:   opts.LoadTokens,
		saveTokens:   opts.SaveTokens,
		now:          now,
	}, nil
}

func (c *Codex) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return c.complete(ctx, messages, tools, true)
}

func (c *Codex) complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, allowRetry bool) (*agent.Response, error) {
	reqBody := codexRequest{
		Messages: toCodexMessages(messages),
		Tools:    toCodexTools(tools),
		Stream:   false,
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("codex: encode request: %w", err)
	}

	accessToken := c.currentAccessToken()
	resp, err := c.doRequest(ctx, &body, accessToken)
	if err != nil {
		var apiErr codexAPIError
		if allowRetry && errors.As(err, &apiErr) && apiErr.statusCode == http.StatusUnauthorized {
			reauthorized, reauthErr := c.reauthorize(ctx, accessToken)
			if reauthorized {
				return c.complete(ctx, messages, tools, false)
			}
			if reauthErr != nil {
				return nil, fmt.Errorf("%w: reauthorization failed: %v", err, reauthErr)
			}
		}
		return nil, err
	}
	return resp, nil
}

func (c *Codex) doRequest(ctx context.Context, body *bytes.Buffer, accessToken string) (*agent.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, body)
	if err != nil {
		return nil, fmt.Errorf("codex: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex: request: %w", redactError(err, accessToken))
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCodexResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("codex: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyMessage := redactBodyExcerpt(raw, accessToken, refreshTokenForRedaction(c))
		return nil, codexAPIError{
			statusCode: resp.StatusCode,
			message:    codexStatusMessage(resp.StatusCode, bodyMessage),
			cause:      codexStatusCause(resp.StatusCode),
		}
	}

	res, err := parseCodexResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("codex: decode response: %w", err)
	}
	return res, nil
}

func (c *Codex) currentAccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessToken
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
				return true, nil
			}
			if strings.TrimSpace(tokens.RefreshToken) != "" {
				c.refreshToken = strings.TrimSpace(tokens.RefreshToken)
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
	}, nil
}

type codexRequest struct {
	Messages []codexMessage `json:"messages"`
	Tools    []codexTool    `json:"tools,omitempty"`
	Stream   bool           `json:"stream"`
}

type codexMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []agent.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type codexTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func toCodexMessages(messages []agent.Message) []codexMessage {
	out := make([]codexMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			continue
		}
		out = append(out, codexMessage{
			Role:       role,
			Content:    msg.Content,
			ToolCalls:  append([]agent.ToolCall(nil), msg.ToolCalls...),
			ToolCallID: msg.ToolCallID,
		})
	}
	return out
}

func toCodexTools(tools []agent.ToolDef) []codexTool {
	out := make([]codexTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out = append(out, codexTool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}
	return out
}

type codexResponse struct {
	Content    string           `json:"content"`
	OutputText string           `json:"output_text"`
	ToolCalls  []agent.ToolCall `json:"tool_calls"`
	Usage      struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func parseCodexResponse(raw []byte) (*agent.Response, error) {
	var parsed codexResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	content := strings.TrimSpace(parsed.OutputText)
	if content == "" {
		content = strings.TrimSpace(parsed.Content)
	}
	if content == "" {
		for _, item := range parsed.Output {
			for _, block := range item.Content {
				if strings.EqualFold(block.Type, "output_text") || strings.EqualFold(block.Type, "text") {
					if strings.TrimSpace(block.Text) != "" {
						content = block.Text
						break
					}
				}
			}
			if content != "" {
				break
			}
		}
	}

	usage := core.TokenUsage{
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
		TotalTokens:  parsed.Usage.TotalTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return &agent.Response{
		Content:   content,
		ToolCalls: parsed.ToolCalls,
		Usage:     usage,
	}, nil
}

func codexStatusMessage(statusCode int, body string) string {
	suffix := ""
	if strings.TrimSpace(body) != "" {
		suffix = ": " + body
	}
	switch statusCode {
	case http.StatusUnauthorized:
		return "codex: status 401 unauthorized" + suffix
	case http.StatusForbidden:
		return "codex: status 403 forbidden" + suffix
	case http.StatusTooManyRequests:
		return "codex: status 429 rate_limited" + suffix
	default:
		if statusCode >= 500 {
			return fmt.Sprintf("codex: status %d server_error%s", statusCode, suffix)
		}
		return fmt.Sprintf("codex: status %d request_failed%s", statusCode, suffix)
	}
}

func codexStatusCause(statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrCodexUnauthorized
	case http.StatusForbidden:
		return ErrCodexForbidden
	case http.StatusTooManyRequests:
		return ErrCodexRateLimited
	default:
		if statusCode >= 500 {
			return ErrCodexServer
		}
		return nil
	}
}

func redactError(err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	return errors.New(msg)
}

func refreshTokenForRedaction(c *Codex) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshToken
}

func redactBodyExcerpt(raw []byte, secrets ...string) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	const maxLen = 300
	if len(text) > maxLen {
		text = strings.TrimSpace(text[:maxLen]) + "…"
	}
	return text
}

type codexAPIError struct {
	statusCode int
	message    string
	cause      error
}

func (e codexAPIError) Error() string {
	return e.message
}

func (e codexAPIError) StatusCode() int {
	return e.statusCode
}

func (e codexAPIError) Unwrap() error {
	return e.cause
}
