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
	"net"
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
	maxCodexResponseBytes        = 1 << 20 // 1 MiB
	codexRefreshClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexModel            = "gpt-5.5"
	defaultCodexPrompt           = "You are Codex, a coding agent. Help the user directly and use tools when needed."
	maxCodexContinuations        = 3
	defaultCodexTransportRetries = 1
)

var (
	ErrCodexUnauthorized = errors.New("codex unauthorized")
	ErrCodexForbidden    = errors.New("codex forbidden")
	ErrCodexRateLimited  = errors.New("codex rate limited")
	ErrCodexServer       = errors.New("codex upstream failure")
)

type CodexOptions struct {
	BaseURL          string
	AccessToken      string
	RefreshToken     string
	AccountID        string
	RefreshURL       string
	Model            string
	StoreResponses   bool
	MaxContinuations int
	TransportRetries int
	HTTPClient       *http.Client
	UserAgent        string
	LoadTokens       func() (governorauth.CodexTokens, error)
	SaveTokens       func(governorauth.CodexTokens, time.Time) error
	Now              func() time.Time
}

type Codex struct {
	endpoint         string
	refreshURL       string
	client           *http.Client
	userAgent        string
	model            string
	storeResponses   bool
	maxContinuations int
	transportRetries int
	loadTokens       func() (governorauth.CodexTokens, error)
	saveTokens       func(governorauth.CodexTokens, time.Time) error
	now              func() time.Time

	mu               sync.Mutex
	accessToken      string
	refreshToken     string
	accountID        string
	storeUnsupported bool
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
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = defaultCodexModel
	}
	maxContinuations := opts.MaxContinuations
	if maxContinuations <= 0 {
		maxContinuations = maxCodexContinuations
	}
	transportRetries := opts.TransportRetries
	if transportRetries < 0 {
		transportRetries = defaultCodexTransportRetries
	}

	return &Codex{
		endpoint:         codexResponsesEndpoint(opts.BaseURL),
		refreshURL:       refreshURL,
		accessToken:      strings.TrimSpace(opts.AccessToken),
		refreshToken:     strings.TrimSpace(opts.RefreshToken),
		accountID:        strings.TrimSpace(opts.AccountID),
		client:           client,
		userAgent:        opts.UserAgent,
		model:            model,
		storeResponses:   opts.StoreResponses,
		maxContinuations: maxContinuations,
		transportRetries: transportRetries,
		loadTokens:       opts.LoadTokens,
		saveTokens:       opts.SaveTokens,
		now:              now,
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
	aggregate := newCodexResponseAccumulator()
	storeResponses := c.effectiveStoreResponses()
	plan := planFullCodexRequest(messages, storeResponses)
	if storeResponses {
		plan = planCodexRequest(messages)
	}
	continuations := 0
	usedPreviousResponseFallback := false

	for {
		result, err := c.completeRequest(ctx, plan, tools, opts, cb, allowRetry, storeResponses)
		if err != nil {
			if storeResponses && isStoreResponsesRejected(err) {
				c.disableStoreResponses()
				storeResponses = false
				aggregate = newCodexResponseAccumulator()
				plan = planFullCodexRequest(messages, false)
				continuations = 0
				usedPreviousResponseFallback = false
				continue
			}
			if storeResponses && plan.mode == codexTurnModeIncrementalToolResults && !usedPreviousResponseFallback && isPreviousResponseRejected(err) {
				plan = planFullCodexRequest(messages, storeResponses)
				usedPreviousResponseFallback = true
				continue
			}
			return nil, err
		}

		aggregate.merge(result.Response, result.ResponseID)
		if result.Complete {
			return aggregate.response(), nil
		}
		if !storeResponses {
			return nil, fmt.Errorf("codex: incomplete response without stored-response continuation")
		}
		if strings.TrimSpace(result.ResponseID) == "" {
			return nil, fmt.Errorf("codex: incomplete response missing response id")
		}
		continuations++
		if continuations > c.maxContinuations {
			return nil, fmt.Errorf("codex: response remained incomplete after %d continuation attempts", c.maxContinuations)
		}
		plan = planCodexContinuation(messages, result.ResponseID)
	}
}

func (c *Codex) completeRequest(ctx context.Context, plan codexRequestPlan, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback, allowRetry bool, storeResponses bool) (*codexCompletionResult, error) {
	for attempt := 0; attempt <= c.transportRetries; attempt++ {
		reqBody := buildCodexRequest(plan, tools, opts, true, c.model, storeResponses)

		var body bytes.Buffer
		if err := json.NewEncoder(&body).Encode(reqBody); err != nil {
			return nil, fmt.Errorf("codex: encode request: %w", err)
		}

		c.syncCredentialsFromStore()
		accessToken, accountID := c.currentCredentials()
		resp, err := c.doRequest(ctx, &body, accessToken, accountID)
		if err != nil {
			var apiErr codexAPIError
			if allowRetry && errors.As(err, &apiErr) && apiErr.statusCode == http.StatusUnauthorized {
				reauthorized, reauthErr := c.reauthorize(ctx, accessToken)
				if reauthorized {
					return c.completeRequest(ctx, plan, tools, opts, cb, false, storeResponses)
				}
				if reauthErr != nil {
					return nil, fmt.Errorf("%w: reauthorization failed: %v", err, reauthErr)
				}
			}
			if attempt < c.transportRetries && isRetryableCodexTransportError(err) {
				continue
			}
			return nil, err
		}

		result, consumeErr := func() (*codexCompletionResult, error) {
			defer resp.Body.Close()
			return consumeCodexStream(resp.Body, cb)
		}()
		if consumeErr != nil {
			if attempt < c.transportRetries && isRetryableCodexTransportError(consumeErr) {
				continue
			}
			return nil, consumeErr
		}
		return result, nil
	}
	return nil, fmt.Errorf("codex: transport retries exhausted")
}

func (c *Codex) effectiveStoreResponses() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeResponses && !c.storeUnsupported
}

func (c *Codex) disableStoreResponses() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeUnsupported = true
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
		return nil, codexAPIError{
			statusCode: resp.StatusCode,
			message:    codexStatusMessage(resp.StatusCode, bodyMessage),
			cause:      codexStatusCause(resp.StatusCode),
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

func buildCodexRequest(plan codexRequestPlan, tools []agent.ToolDef, opts agent.CompleteOptions, stream bool, model string, store bool) map[string]any {
	model = strings.TrimSpace(model)
	if model == "" {
		model = defaultCodexModel
	}
	reqBody := map[string]any{
		"model":        model,
		"instructions": plan.instructions,
		"input":        plan.input,
		"store":        store,
		"stream":       stream,
	}
	if store && plan.previousResponseID != "" {
		reqBody["previous_response_id"] = plan.previousResponseID
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

func consumeCodexStream(body io.Reader, cb agent.StreamCallback) (*codexCompletionResult, error) {
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
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err == nil {
			nested := bytes.TrimSpace([]byte(encoded))
			if len(nested) > 0 && json.Valid(nested) {
				return string(nested)
			}
		}
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
		if builtin, ok := codexBuiltInToolSpec(name, tool.Parameters); ok {
			out = append(out, builtin)
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

func codexBuiltInToolSpec(name string, params json.RawMessage) (map[string]any, bool) {
	if strings.TrimSpace(name) != "image_generation" {
		return nil, false
	}
	var cfg struct {
		Type         string `json:"type"`
		OutputFormat string `json:"output_format"`
	}
	if len(bytes.TrimSpace(params)) > 0 {
		_ = json.Unmarshal(params, &cfg)
	}
	if strings.TrimSpace(cfg.Type) != "builtin" {
		return nil, false
	}
	outputFormat := strings.TrimSpace(cfg.OutputFormat)
	if outputFormat == "" {
		outputFormat = "png"
	}
	return map[string]any{
		"type":          "image_generation",
		"output_format": outputFormat,
	}, true
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
	ID                string      `json:"id"`
	Status            string      `json:"status"`
	Usage             *codexUsage `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type codexOutputItem struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	CallID        string          `json:"call_id"`
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments"`
	Status        string          `json:"status"`
	RevisedPrompt string          `json:"revised_prompt"`
	Result        string          `json:"result"`
	Summary       []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type codexProviderState struct {
	Backend        string            `json:"backend"`
	ResponseID     string            `json:"response_id"`
	ReasoningItems []json.RawMessage `json:"reasoning_items,omitempty"`
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
	media        []core.Media
	reasoningRaw []json.RawMessage
	usage        core.TokenUsage
	responseID   string
	status       codexResponseStatus
}

type codexResponseStatus string

const (
	codexResponseStatusPending    codexResponseStatus = ""
	codexResponseStatusCompleted  codexResponseStatus = "completed"
	codexResponseStatusIncomplete codexResponseStatus = "incomplete"
)

type codexCompletionResult struct {
	Response   *agent.Response
	ResponseID string
	Complete   bool
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
		p.captureResponseEnvelope(env.Response)
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
			if raw := bytes.TrimSpace(env.Item); len(raw) > 0 {
				p.reasoningRaw = append(p.reasoningRaw, append(json.RawMessage(nil), raw...))
			}
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
		case "image_generation_call":
			if media, ok := codexImageGenerationCallMedia(item.ID, item.Result); ok {
				p.media = append(p.media, media)
			}
		case "message":
			// Text is normally streamed via output_text.delta; ignore here to avoid duplication.
		}
		return nil
	case "response.completed":
		p.status = codexResponseStatusCompleted
		if err := p.captureUsage(env.Response, cb); err != nil {
			return err
		}
		return nil
	case "response.incomplete":
		p.status = codexResponseStatusIncomplete
		if err := p.captureUsage(env.Response, cb); err != nil {
			return err
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

func codexImageGenerationCallMedia(id string, result string) (core.Media, bool) {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return core.Media{}, false
	}
	bytes, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil || len(bytes) == 0 {
		return core.Media{}, false
	}
	mimeType := http.DetectContentType(bytes)
	ext := codexImageExtensionForMimeType(mimeType)
	return core.Media{
		Type:     "image",
		Data:     bytes,
		MimeType: mimeType,
		Filename: "image-generation-call-" + sanitizeCodexImageGenerationID(id) + ext,
	}, true
}

func codexImageExtensionForMimeType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func sanitizeCodexImageGenerationID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "generated"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "generated"
	}
	return b.String()
}

func (p *codexStreamParser) response() (*codexCompletionResult, error) {
	resp := &agent.Response{
		Content:      p.text.String(),
		Thinking:     p.thinking.String(),
		ThinkingMeta: append([]agent.ThinkingBlock(nil), p.thinkingMeta...),
		ToolCalls:    append([]agent.ToolCall(nil), p.toolCalls...),
		Media:        append([]core.Media(nil), p.media...),
		Usage:        p.usage,
	}
	if strings.TrimSpace(p.responseID) != "" {
		resp.ProviderState = marshalCodexProviderState(p.responseID, p.reasoningRaw)
	}

	switch p.status {
	case codexResponseStatusCompleted:
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: true}, nil
	case codexResponseStatusIncomplete:
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false}, nil
	}
	if strings.TrimSpace(p.responseID) != "" {
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false}, nil
	}
	if resp.Content == "" && resp.Thinking == "" && len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("codex: stream closed before response.completed")
	}
	return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false}, nil
}

func (p *codexStreamParser) captureResponseEnvelope(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var envelope codexCompletedResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if strings.TrimSpace(envelope.ID) != "" {
		p.responseID = strings.TrimSpace(envelope.ID)
	}
}

func (p *codexStreamParser) captureUsage(raw json.RawMessage, cb agent.StreamCallback) error {
	if len(raw) == 0 {
		return nil
	}
	var envelope codexCompletedResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	if strings.TrimSpace(envelope.ID) != "" {
		p.responseID = strings.TrimSpace(envelope.ID)
	}
	if envelope.Usage == nil {
		return nil
	}
	p.usage = core.TokenUsage{
		InputTokens:      envelope.Usage.InputTokens,
		OutputTokens:     envelope.Usage.OutputTokens,
		TotalTokens:      envelope.Usage.TotalTokens,
		CacheReadTokens:  envelope.Usage.InputTokensDetails.CachedTokens,
		CacheWriteTokens: envelope.Usage.InputTokensDetails.CacheWriteTokens,
	}
	if p.usage.TotalTokens == 0 {
		p.usage.TotalTokens = p.usage.InputTokens + p.usage.OutputTokens
	}
	if cb != nil {
		usage := p.usage
		return cb(agent.StreamChunk{Type: "usage", Usage: &usage})
	}
	return nil
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

type codexTurnMode string

const (
	codexTurnModeFullContext            codexTurnMode = "full_context"
	codexTurnModeIncrementalToolResults codexTurnMode = "incremental_tool_results"
	codexTurnModeContinuationOnly       codexTurnMode = "continuation_only"
)

type codexRequestPlan struct {
	mode               codexTurnMode
	instructions       string
	input              []map[string]any
	previousResponseID string
}

func planCodexRequest(messages []agent.Message) codexRequestPlan {
	if previousResponseID, input, ok := planCodexIncrementalToolResults(messages); ok {
		return codexRequestPlan{
			mode:               codexTurnModeIncrementalToolResults,
			instructions:       collectCodexInstructions(messages),
			input:              input,
			previousResponseID: previousResponseID,
		}
	}
	return planFullCodexRequest(messages, true)
}

func planFullCodexRequest(messages []agent.Message, includeReasoningItems bool) codexRequestPlan {
	return codexRequestPlan{
		mode:         codexTurnModeFullContext,
		instructions: collectCodexInstructions(messages),
		input:        codexInputItems(messages, includeReasoningItems),
	}
}

func planCodexContinuation(messages []agent.Message, previousResponseID string) codexRequestPlan {
	return codexRequestPlan{
		mode:               codexTurnModeContinuationOnly,
		instructions:       collectCodexInstructions(messages),
		input:              []map[string]any{},
		previousResponseID: strings.TrimSpace(previousResponseID),
	}
}

func codexInputItems(messages []agent.Message, includeReasoningItems bool) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" || role == "system" {
			continue
		}

		switch role {
		case "user", "assistant":
			if includeReasoningItems && role == "assistant" {
				for _, item := range codexReasoningInputItems(msg.ProviderState) {
					input = append(input, item)
				}
			}
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
	return input
}

func planCodexIncrementalToolResults(messages []agent.Message) (string, []map[string]any, bool) {
	assistantIdx := -1
	var previousResponseID string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			continue
		}
		state, ok := decodeCodexProviderState(msg.ProviderState)
		if !ok {
			return "", nil, false
		}
		assistantIdx = i
		previousResponseID = state.ResponseID
		break
	}
	if assistantIdx < 0 || strings.TrimSpace(previousResponseID) == "" || assistantIdx == len(messages)-1 {
		return "", nil, false
	}
	input := make([]map[string]any, 0, len(messages)-assistantIdx-1)
	for _, msg := range messages[assistantIdx+1:] {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			return "", nil, false
		}
		input = append(input, map[string]any{
			"type":    "function_call_output",
			"call_id": strings.TrimSpace(msg.ToolCallID),
			"output":  strings.TrimSpace(msg.Content),
		})
	}
	if len(input) == 0 {
		return "", nil, false
	}
	return previousResponseID, input, true
}

func codexReasoningInputItems(raw json.RawMessage) []map[string]any {
	state, ok := decodeCodexProviderState(raw)
	if !ok || len(state.ReasoningItems) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(state.ReasoningItems))
	for _, itemRaw := range state.ReasoningItems {
		var item map[string]any
		if len(bytes.TrimSpace(itemRaw)) == 0 {
			continue
		}
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(item["type"])) != "reasoning" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func marshalCodexProviderState(responseID string, reasoningItems []json.RawMessage) json.RawMessage {
	if strings.TrimSpace(responseID) == "" {
		return nil
	}
	items := make([]json.RawMessage, 0, len(reasoningItems))
	seen := map[string]struct{}{}
	for _, item := range reasoningItems {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 {
			continue
		}
		key := string(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, append(json.RawMessage(nil), trimmed...))
	}
	raw, err := json.Marshal(codexProviderState{
		Backend:        "codex",
		ResponseID:     strings.TrimSpace(responseID),
		ReasoningItems: items,
	})
	if err != nil {
		return nil
	}
	return raw
}

func decodeCodexProviderState(raw json.RawMessage) (codexProviderState, bool) {
	var state codexProviderState
	if len(bytes.TrimSpace(raw)) == 0 {
		return codexProviderState{}, false
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return codexProviderState{}, false
	}
	if strings.TrimSpace(state.Backend) != "codex" || strings.TrimSpace(state.ResponseID) == "" {
		return codexProviderState{}, false
	}
	state.ResponseID = strings.TrimSpace(state.ResponseID)
	for i := range state.ReasoningItems {
		state.ReasoningItems[i] = append(json.RawMessage(nil), bytes.TrimSpace(state.ReasoningItems[i])...)
	}
	return state, true
}

type codexResponseAccumulator struct {
	content      strings.Builder
	thinking     strings.Builder
	thinkingMeta []agent.ThinkingBlock
	toolCalls    []agent.ToolCall
	toolCallSet  map[string]struct{}
	media        []core.Media
	mediaSet     map[string]struct{}
	reasoningRaw []json.RawMessage
	reasoningSet map[string]struct{}
	usage        core.TokenUsage
	responseID   string
}

func newCodexResponseAccumulator() *codexResponseAccumulator {
	return &codexResponseAccumulator{
		toolCallSet:  map[string]struct{}{},
		mediaSet:     map[string]struct{}{},
		reasoningSet: map[string]struct{}{},
	}
}

func (a *codexResponseAccumulator) merge(resp *agent.Response, responseID string) {
	if a == nil || resp == nil {
		return
	}
	if resp.Content != "" {
		a.content.WriteString(resp.Content)
	}
	if resp.Thinking != "" {
		a.thinking.WriteString(resp.Thinking)
	}
	a.thinkingMeta = append(a.thinkingMeta, resp.ThinkingMeta...)
	for _, call := range resp.ToolCalls {
		key := strings.Join([]string{strings.TrimSpace(call.ID), strings.TrimSpace(call.Name), string(bytes.TrimSpace(call.Input))}, "\x00")
		if _, ok := a.toolCallSet[key]; ok {
			continue
		}
		a.toolCallSet[key] = struct{}{}
		a.toolCalls = append(a.toolCalls, call)
	}
	for _, media := range resp.Media {
		key := strings.Join([]string{strings.TrimSpace(media.Filename), strings.TrimSpace(media.MimeType), string(media.Data)}, "\x00")
		if _, ok := a.mediaSet[key]; ok {
			continue
		}
		a.mediaSet[key] = struct{}{}
		a.media = append(a.media, media)
	}
	if state, ok := decodeCodexProviderState(resp.ProviderState); ok {
		for _, raw := range state.ReasoningItems {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 {
				continue
			}
			key := string(trimmed)
			if _, ok := a.reasoningSet[key]; ok {
				continue
			}
			a.reasoningSet[key] = struct{}{}
			a.reasoningRaw = append(a.reasoningRaw, append(json.RawMessage(nil), trimmed...))
		}
	}
	if resp.Usage.TotalTokens != 0 || resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		a.usage = resp.Usage
	}
	if strings.TrimSpace(responseID) != "" {
		a.responseID = strings.TrimSpace(responseID)
	}
}

func (a *codexResponseAccumulator) response() *agent.Response {
	if a == nil {
		return &agent.Response{}
	}
	resp := &agent.Response{
		Content:      strings.TrimSpace(a.content.String()),
		Thinking:     strings.TrimSpace(a.thinking.String()),
		ThinkingMeta: append([]agent.ThinkingBlock(nil), a.thinkingMeta...),
		ToolCalls:    append([]agent.ToolCall(nil), a.toolCalls...),
		Media:        append([]core.Media(nil), a.media...),
		Usage:        a.usage,
	}
	if a.responseID != "" {
		resp.ProviderState = marshalCodexProviderState(a.responseID, a.reasoningRaw)
	}
	return resp
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

func isPreviousResponseRejected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "previous_response_id") || strings.Contains(msg, "previous response")
}

func isStoreResponsesRejected(err error) bool {
	if err == nil {
		return false
	}
	var apiErr codexAPIError
	if errors.As(err, &apiErr) && apiErr.statusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "store must be set to false")
}
