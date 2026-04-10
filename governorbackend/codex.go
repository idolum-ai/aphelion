//go:build linux

package governorbackend

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"github.com/idolum-ai/aphelion/internal"
)

const (
	maxCodexResponseBytes = 1 << 20 // 1 MiB
	codexRefreshClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexModel     = "gpt-5.4"
	defaultCodexPrompt    = "You are Codex, a coding agent. Help the user directly and use tools when needed."
)

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
	AccountID    string
	RefreshURL   string
	HTTPClient   *http.Client
	UserAgent    string
	LoadTokens   func() (governorauth.CodexTokens, error)
	SaveTokens   func(governorauth.CodexTokens, time.Time) error
	Now          func() time.Time
}

type Codex struct {
	endpoint   string
	refreshURL string
	client     *http.Client
	userAgent  string
	loadTokens func() (governorauth.CodexTokens, error)
	saveTokens func(governorauth.CodexTokens, time.Time) error
	now        func() time.Time

	mu           sync.Mutex
	accessToken  string
	refreshToken string
	accountID    string
}

var _ agent.Provider = (*Codex)(nil)
var _ agent.ProviderWithOptions = (*Codex)(nil)
var _ agent.StreamingProvider = (*Codex)(nil)

func NewCodex(opts CodexOptions) (*Codex, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, fmt.Errorf("codex: base url is required")
	}
	if strings.TrimSpace(opts.AccessToken) == "" {
		return nil, fmt.Errorf("codex: access token is required")
	}
	if strings.TrimSpace(opts.AccountID) == "" {
		return nil, fmt.Errorf("codex: account id is required")
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
		endpoint:     codexResponsesEndpoint(opts.BaseURL),
		refreshURL:   refreshURL,
		accessToken:  strings.TrimSpace(opts.AccessToken),
		refreshToken: strings.TrimSpace(opts.RefreshToken),
		accountID:    strings.TrimSpace(opts.AccountID),
		client:       client,
		userAgent:    opts.UserAgent,
		loadTokens:   opts.LoadTokens,
		saveTokens:   opts.SaveTokens,
		now:          now,
	}, nil
}

func (c *Codex) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return c.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (c *Codex) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	return c.complete(ctx, messages, tools, opts, nil, true)
}

func (c *Codex) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return c.complete(ctx, messages, tools, agent.CompleteOptions{}, cb, true)
}

func (c *Codex) complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback, allowRetry bool) (*agent.Response, error) {
	reqBody := buildCodexRequest(messages, tools, opts, true)

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("codex: encode request: %w", err)
	}

	accessToken, accountID := c.currentCredentials()
	resp, err := c.doRequest(ctx, &body, accessToken, accountID)
	if err != nil {
		var apiErr codexAPIError
		if allowRetry && errors.As(err, &apiErr) && apiErr.statusCode == http.StatusUnauthorized {
			reauthorized, reauthErr := c.reauthorize(ctx, accessToken)
			if reauthorized {
				return c.complete(ctx, messages, tools, opts, cb, false)
			}
			if reauthErr != nil {
				return nil, fmt.Errorf("%w: reauthorization failed: %v", err, reauthErr)
			}
		}
		return nil, err
	}
	defer resp.Body.Close()
	return consumeCodexStream(resp.Body, cb)
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
		return nil, codexAPIError{
			statusCode: resp.StatusCode,
			message:    codexStatusMessage(resp.StatusCode, bodyMessage),
			cause:      codexStatusCause(resp.StatusCode),
		}
	}
	return resp, nil
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

func buildCodexRequest(messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, stream bool) map[string]any {
	instructions := collectCodexInstructions(messages)
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" || role == "system" {
			continue
		}

		switch role {
		case "user", "assistant":
			if item, ok := codexMessageInputItem(role, msg); ok {
				input = append(input, item)
			}
			if role == "assistant" {
				for _, call := range msg.ToolCalls {
					input = append(input, map[string]any{
						"type":      "function_call",
						"name":      call.Name,
						"arguments": normalizeArguments(call.Input),
						"call_id":   firstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(msg.ToolCallID)),
					})
				}
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": strings.TrimSpace(msg.ToolCallID),
				"output":  strings.TrimSpace(msg.Content),
			})
		}
	}

	reqBody := map[string]any{
		"model":        defaultCodexModel,
		"instructions": instructions,
		"input":        input,
		"store":        false,
		"stream":       stream,
	}
	if defs := toCodexTools(tools); len(defs) > 0 {
		reqBody["tools"] = defs
		reqBody["tool_choice"] = "auto"
	}
	if reasoning := mapCodexReasoning(opts.Reasoning); len(reasoning) > 0 {
		reqBody["reasoning"] = reasoning
	}
	return reqBody
}

func consumeCodexStream(body io.Reader, cb agent.StreamCallback) (*agent.Response, error) {
	parser := &codexStreamParser{}
	for event := range internal.ParseSSE(body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event, cb); err != nil {
			return nil, err
		}
	}
	return parser.response()
}

func collectCodexInstructions(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" && len(msg.SystemBlocks) > 0 {
			text = renderSystemBlocks(msg.SystemBlocks)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return defaultCodexPrompt
	}
	return strings.Join(parts, "\n\n")
}

func renderSystemBlocks(blocks []agent.SystemBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func codexMessageInputItem(role string, msg agent.Message) (map[string]any, bool) {
	content := make([]map[string]any, 0, len(msg.Media)+1)
	for _, media := range msg.Media {
		if part, ok := mediaToCodexInputItem(media); ok {
			content = append(content, part)
		}
	}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	if text := strings.TrimSpace(msg.Content); text != "" || len(content) == 0 {
		content = append(content, map[string]any{
			"type": textType,
			"text": msg.Content,
		})
	}
	if len(content) == 0 {
		return nil, false
	}
	return map[string]any{
		"type":    "message",
		"role":    role,
		"content": content,
	}, true
}

func mediaToCodexInputItem(media core.Media) (map[string]any, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") || len(media.Data) == 0 {
		return nil, false
	}
	return map[string]any{
		"type":      "input_image",
		"image_url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(media.Data)),
	}, true
}

func normalizeArguments(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "{}"
	}
	if json.Valid(trimmed) {
		return string(trimmed)
	}
	quoted, err := json.Marshal(string(trimmed))
	if err != nil {
		return "{}"
	}
	return string(quoted)
}

func toCodexTools(tools []agent.ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		entry := map[string]any{
			"type": "function",
			"name": name,
		}
		if desc := strings.TrimSpace(tool.Description); desc != "" {
			entry["description"] = desc
		}
		if len(bytes.TrimSpace(tool.Parameters)) > 0 {
			entry["parameters"] = json.RawMessage(tool.Parameters)
		}
		out = append(out, entry)
	}
	return out
}

func mapCodexReasoning(cfg agent.ReasoningConfig) map[string]any {
	out := map[string]any{}
	switch cfg.Effort {
	case agent.ReasoningEffortLow:
		out["effort"] = "low"
	case agent.ReasoningEffortMedium:
		out["effort"] = "medium"
	case agent.ReasoningEffortHigh:
		out["effort"] = "high"
	case agent.ReasoningEffortXHigh:
		out["effort"] = "xhigh"
	}
	switch cfg.Summary {
	case agent.ReasoningSummaryAuto:
		out["summary"] = "auto"
	case agent.ReasoningSummaryCompact:
		out["summary"] = "concise"
	}
	return out
}

type codexResponse struct {
	OutputText string            `json:"output_text"`
	Output     []codexOutputItem `json:"output"`
	Usage      codexUsage        `json:"usage"`
}

type codexOutputItem struct {
	Type      string               `json:"type"`
	Role      string               `json:"role"`
	Name      string               `json:"name"`
	CallID    string               `json:"call_id"`
	Arguments string               `json:"arguments"`
	Content   []codexContentItem   `json:"content"`
	Summary   []codexReasoningText `json:"summary"`
}

type codexContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexReasoningText struct {
	Text string `json:"text"`
}

type codexUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type codexStreamEnvelope struct {
	Type         string          `json:"type"`
	Delta        string          `json:"delta"`
	Item         json.RawMessage `json:"item"`
	Response     json.RawMessage `json:"response"`
	SummaryIndex *int            `json:"summary_index"`
	ContentIndex *int            `json:"content_index"`
}

type codexCompletedResponse struct {
	ID    string      `json:"id"`
	Usage *codexUsage `json:"usage"`
}

type codexFailedResponse struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type codexStreamParser struct {
	text         strings.Builder
	thinking     strings.Builder
	thinkingMeta []agent.ThinkingBlock
	toolCalls    []agent.ToolCall
	usage        core.TokenUsage
	completed    bool
}

func (p *codexStreamParser) consume(event internal.Event, cb agent.StreamCallback) error {
	var env codexStreamEnvelope
	if err := json.Unmarshal([]byte(event.Data), &env); err != nil {
		return fmt.Errorf("codex: decode stream event: %w", err)
	}

	kind := strings.TrimSpace(env.Type)
	if kind == "" {
		kind = strings.TrimSpace(event.Type)
	}
	switch kind {
	case "response.created":
		return nil
	case "response.output_text.delta":
		p.text.WriteString(env.Delta)
		if cb != nil && strings.TrimSpace(env.Delta) != "" {
			return cb(agent.StreamChunk{Type: "text", Text: env.Delta})
		}
		return nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		p.thinking.WriteString(env.Delta)
		return nil
	case "response.output_item.done":
		if len(env.Item) == 0 {
			return nil
		}
		var item codexOutputItem
		if err := json.Unmarshal(env.Item, &item); err != nil {
			return fmt.Errorf("codex: decode stream item: %w", err)
		}
		switch item.Type {
		case "function_call":
			call := agent.ToolCall{
				ID:    strings.TrimSpace(item.CallID),
				Name:  strings.TrimSpace(item.Name),
				Input: json.RawMessage(normalizeArguments(json.RawMessage(item.Arguments))),
			}
			p.toolCalls = append(p.toolCalls, call)
			if cb != nil {
				return cb(agent.StreamChunk{Type: "tool_call", ToolCall: &call})
			}
		case "reasoning":
			for _, summary := range item.Summary {
				if text := strings.TrimSpace(summary.Text); text != "" {
					if p.thinking.Len() > 0 {
						p.thinking.WriteString("\n")
					}
					p.thinking.WriteString(text)
					p.thinkingMeta = append(p.thinkingMeta, agent.ThinkingBlock{
						Type:    "summary_text",
						Content: text,
					})
				}
			}
		case "message":
			// Text is normally streamed via output_text.delta; ignore here to avoid duplication.
		}
		return nil
	case "response.completed":
		p.completed = true
		var completed codexCompletedResponse
		if len(env.Response) > 0 && json.Unmarshal(env.Response, &completed) == nil && completed.Usage != nil {
			p.usage = core.TokenUsage{
				InputTokens:      completed.Usage.InputTokens,
				OutputTokens:     completed.Usage.OutputTokens,
				TotalTokens:      completed.Usage.TotalTokens,
				CacheReadTokens:  completed.Usage.InputTokensDetails.CachedTokens,
				CacheWriteTokens: completed.Usage.InputTokensDetails.CacheWriteTokens,
			}
			if p.usage.TotalTokens == 0 {
				p.usage.TotalTokens = p.usage.InputTokens + p.usage.OutputTokens
			}
			if cb != nil {
				usage := p.usage
				return cb(agent.StreamChunk{Type: "usage", Usage: &usage})
			}
		}
		return nil
	case "response.failed":
		var failed codexFailedResponse
		if len(env.Response) > 0 && json.Unmarshal(env.Response, &failed) == nil {
			if failed.Error != nil && strings.TrimSpace(failed.Error.Message) != "" {
				return fmt.Errorf("codex: stream failed: %s", strings.TrimSpace(failed.Error.Message))
			}
		}
		return fmt.Errorf("codex: stream failed")
	default:
		return nil
	}
}

func (p *codexStreamParser) response() (*agent.Response, error) {
	if !p.completed {
		return nil, fmt.Errorf("codex: stream closed before response.completed")
	}
	return &agent.Response{
		Content:      strings.TrimSpace(p.text.String()),
		Thinking:     strings.TrimSpace(p.thinking.String()),
		ThinkingMeta: append([]agent.ThinkingBlock(nil), p.thinkingMeta...),
		ToolCalls:    append([]agent.ToolCall(nil), p.toolCalls...),
		Usage:        p.usage,
	}, nil
}

func parseCodexResponse(raw []byte) (*agent.Response, error) {
	var parsed codexResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}

	var (
		contentParts   []string
		thinkingParts  []string
		thinkingBlocks []agent.ThinkingBlock
		toolCalls      []agent.ToolCall
	)

	haveRootOutputText := false
	if text := strings.TrimSpace(parsed.OutputText); text != "" {
		contentParts = append(contentParts, text)
		haveRootOutputText = true
	}

	for _, item := range parsed.Output {
		switch item.Type {
		case "message":
			if haveRootOutputText {
				continue
			}
			for _, block := range item.Content {
				if strings.EqualFold(block.Type, "output_text") || strings.EqualFold(block.Type, "text") {
					if text := strings.TrimSpace(block.Text); text != "" {
						contentParts = append(contentParts, text)
					}
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, agent.ToolCall{
				ID:    strings.TrimSpace(item.CallID),
				Name:  strings.TrimSpace(item.Name),
				Input: json.RawMessage(normalizeArguments(json.RawMessage(item.Arguments))),
			})
		case "reasoning":
			for _, summary := range item.Summary {
				if text := strings.TrimSpace(summary.Text); text != "" {
					thinkingParts = append(thinkingParts, text)
					thinkingBlocks = append(thinkingBlocks, agent.ThinkingBlock{
						Type:    "summary_text",
						Content: text,
					})
				}
			}
		}
	}

	content := strings.TrimSpace(strings.Join(contentParts, "\n\n"))
	usage := core.TokenUsage{
		InputTokens:      parsed.Usage.InputTokens,
		OutputTokens:     parsed.Usage.OutputTokens,
		TotalTokens:      parsed.Usage.TotalTokens,
		CacheReadTokens:  parsed.Usage.InputTokensDetails.CachedTokens,
		CacheWriteTokens: parsed.Usage.InputTokensDetails.CacheWriteTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return &agent.Response{
		Content:      content,
		Thinking:     strings.TrimSpace(strings.Join(thinkingParts, "\n\n")),
		ThinkingMeta: thinkingBlocks,
		ToolCalls:    toolCalls,
		Usage:        usage,
	}, nil
}

func codexResponsesEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case baseURL == "":
		return "/codex/responses"
	case strings.HasSuffix(baseURL, "/codex/responses"):
		return baseURL
	case strings.HasSuffix(baseURL, "/responses"):
		return baseURL
	case strings.HasSuffix(baseURL, "/codex"):
		return baseURL + "/responses"
	case strings.Contains(baseURL, "/backend-api"):
		return baseURL + "/codex/responses"
	default:
		return baseURL + "/responses"
	}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
