//go:build linux

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultAnthropicVersion  = "2023-06-01"
)

var _ agent.Provider = (*Anthropic)(nil)
var _ agent.StreamingProvider = (*Anthropic)(nil)
var _ agent.ProviderWithOptions = (*Anthropic)(nil)

// AnthropicOptions configures the Anthropic provider client.
type AnthropicOptions struct {
	APIKey           string
	Model            string
	MaxTokens        int
	CacheStrategy    string
	CacheTTL         string
	HTTPClient       *http.Client
	BaseURL          string
	AnthropicVersion string
	UserAgent        string
}

// Anthropic implements agent.Provider against the Anthropic Messages API.
type Anthropic struct {
	endpoint  string
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	version   string
	userAgent string
	cache     anthropicCachePolicy
}

// NewAnthropic creates a new Anthropic client.
func NewAnthropic(opts AnthropicOptions) (*Anthropic, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("anthropic: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := opts.BaseURL
	if endpoint == "" {
		endpoint = defaultAnthropicEndpoint
	}
	version := opts.AnthropicVersion
	if version == "" {
		version = defaultAnthropicVersion
	}
	cache, err := newAnthropicCachePolicy(opts.CacheStrategy, opts.CacheTTL)
	if err != nil {
		return nil, err
	}

	return &Anthropic{
		endpoint:  endpoint,
		client:    client,
		apiKey:    opts.APIKey,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
		version:   version,
		userAgent: opts.UserAgent,
		cache:     cache,
	}, nil
}

// Complete sends the assembled history to Anthropic and returns the response.
func (a *Anthropic) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return a.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (a *Anthropic) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := a.buildRequest(messages, tools, false, opts)
	resp, err := a.doRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var anthRes anthropicResponse
	if err := json.Unmarshal(body, &anthRes); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return mapAnthropicResponse(anthRes, opts.Reasoning.Summary), nil
}

func (a *Anthropic) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := a.buildRequest(messages, tools, true, agent.CompleteOptions{})
	resp, err := a.doRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("anthropic: read response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newAnthropicStreamParser(cb)
	for event := range internal.ParseSSE(resp.Body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event); err != nil {
			return nil, err
		}
	}
	return parser.response(), parser.err()
}

func mapAnthropicResponse(res anthropicResponse, summaryMode agent.ReasoningSummaryMode) *agent.Response {
	var text strings.Builder
	var thinkingSummary strings.Builder
	var thinkingBlocks []agent.ThinkingBlock
	var toolCalls []agent.ToolCall
	for _, block := range res.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			summary := firstNonEmpty(block.Thinking, block.Text)
			thinkingSummary.WriteString(summary)
			thinkingBlocks = append(thinkingBlocks, agent.ThinkingBlock{
				Type:      block.Type,
				Content:   summary,
				Signature: block.Signature,
				Raw:       mustMarshalRaw(block),
			})
		case "tool_use", "tool_call":
			if block.ID == "" || block.Name == "" {
				continue
			}
			toolCalls = append(toolCalls, agent.ToolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}
	summary := summarizeThinking(strings.TrimSpace(thinkingSummary.String()), summaryMode)

	usage := core.TokenUsage{
		InputTokens:      res.Usage.InputTokens,
		OutputTokens:     res.Usage.OutputTokens,
		TotalTokens:      res.Usage.TotalTokens,
		CacheReadTokens:  res.Usage.CacheReadInputTokens,
		CacheWriteTokens: res.Usage.CacheCreationInputTokens,
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return &agent.Response{
		Content:      text.String(),
		Thinking:     summary,
		ThinkingMeta: thinkingBlocks,
		ToolCalls:    toolCalls,
		Usage:        usage,
	}
}

func (a *Anthropic) buildRequest(messages []agent.Message, tools []agent.ToolDef, stream bool, opts agent.CompleteOptions) anthropicRequest {
	systemPrompt, reqMessages := splitMessages(messages, a.cache)
	reqBody := anthropicRequest{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    systemPrompt,
		Messages:  toAnthropicMessages(reqMessages),
		Stream:    stream,
	}
	if toolDefs := toAnthropicTools(tools, a.cache); len(toolDefs) > 0 {
		reqBody.Tools = toolDefs
	}
	if thinking := anthropicThinkingForOptions(opts.Reasoning, a.maxTokens); thinking != nil {
		reqBody.Thinking = thinking
	}
	return reqBody
}

func (a *Anthropic) doRequest(ctx context.Context, reqBody anthropicRequest) (*http.Response, error) {
	buf := bytes.Buffer{}
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("anthropic: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("Anthropic-Version", a.version)
	if a.userAgent != "" {
		req.Header.Set("User-Agent", a.userAgent)
	}

	return a.client.Do(req)
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	System    []anthropicContent `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicToolDef `json:"tools,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

type anthropicToolDef struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
	Usage   anthropicUsage     `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	TotalTokens              int64 `json:"total_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type anthropicContent struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	Source       any                    `json:"source,omitempty"`
	Thinking     string                 `json:"thinking,omitempty"`
	Signature    string                 `json:"signature,omitempty"`
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      json.RawMessage        `json:"content,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Message      anthropicStreamMessage  `json:"message,omitempty"`
	Usage        anthropicUsage          `json:"usage,omitempty"`
	ContentBlock anthropicContent        `json:"content_block,omitempty"`
	Delta        anthropicStreamDelta    `json:"delta,omitempty"`
	Error        *anthropicStreamFailure `json:"error,omitempty"`
}

type anthropicStreamMessage struct {
	Usage anthropicUsage `json:"usage,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicStreamFailure struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type anthropicCachePolicy struct {
	Strategy string
	TTL      string
}

func newAnthropicCachePolicy(strategy string, ttl string) (anthropicCachePolicy, error) {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy == "" {
		strategy = "explicit"
	}
	switch strategy {
	case "auto", "explicit", "hybrid", "off":
	default:
		return anthropicCachePolicy{}, fmt.Errorf("anthropic: cache strategy must be one of auto|explicit|hybrid|off")
	}
	ttl = strings.ToLower(strings.TrimSpace(ttl))
	if ttl == "" {
		ttl = "5m"
	}
	switch ttl {
	case "5m", "1h":
	default:
		return anthropicCachePolicy{}, fmt.Errorf("anthropic: cache ttl must be one of 5m|1h")
	}
	return anthropicCachePolicy{Strategy: strategy, TTL: ttl}, nil
}

func (p anthropicCachePolicy) cacheControl() *anthropicCacheControl {
	if p.Strategy == "off" {
		return nil
	}
	return &anthropicCacheControl{Type: "ephemeral", TTL: p.TTL}
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicStreamBlock struct {
	kind       string
	text       strings.Builder
	toolID     string
	toolName   string
	toolInput  strings.Builder
	initialRaw json.RawMessage
}

type anthropicStreamParser struct {
	cb          agent.StreamCallback
	text        strings.Builder
	toolCalls   []agent.ToolCall
	usage       core.TokenUsage
	blocks      map[int]*anthropicStreamBlock
	order       []int
	callbackErr error
	parseErr    error
}

func newAnthropicStreamParser(cb agent.StreamCallback) *anthropicStreamParser {
	return &anthropicStreamParser{
		cb:     cb,
		blocks: make(map[int]*anthropicStreamBlock),
	}
}

func toAnthropicMessages(messages []agent.Message) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		role := normalizeRole(msg.Role)
		if role == "" {
			continue
		}
		out = append(out, anthropicMessage{
			Role:    role,
			Content: messageToContent(msg),
		})
	}
	return out
}

func normalizeRole(role string) string {
	swt := strings.ToLower(strings.TrimSpace(role))
	switch swt {
	case "user", "assistant":
		return swt
	case "tool":
		return "user"
	default:
		return ""
	}
}

func messageToContent(msg agent.Message) []anthropicContent {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	if role == "tool" {
		block := anthropicContent{
			Type:    "tool_result",
			IsError: strings.HasPrefix(msg.Content, "tool_error:"),
		}
		if msg.ToolCallID != "" {
			block.ToolUseID = msg.ToolCallID
		}
		if msg.Content != "" {
			block.Content = rawString(msg.Content)
		}
		return []anthropicContent{block}
	}
	content := make([]anthropicContent, 0, 1+len(msg.ToolCalls))
	for _, block := range msg.ThinkingMeta {
		if thinkingBlock, ok := thinkingBlockToAnthropic(block); ok {
			content = append(content, thinkingBlock)
		}
	}
	for _, media := range msg.Media {
		if mediaBlock, ok := mediaToAnthropicContent(media); ok {
			content = append(content, mediaBlock)
		}
	}
	if msg.Content != "" {
		content = append(content, anthropicContent{Type: "text", Text: msg.Content})
	}
	for _, call := range msg.ToolCalls {
		content = append(content, anthropicContent{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Name,
			Input: call.Input,
		})
	}
	if len(content) == 0 {
		content = append(content, anthropicContent{Type: "text", Text: ""})
	}
	return content
}

func mediaToAnthropicContent(media core.Media) (anthropicContent, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") || len(media.Data) == 0 {
		return anthropicContent{}, false
	}
	return anthropicContent{
		Type: "image",
		Source: anthropicImageSource{
			Type:      "base64",
			MediaType: mimeType,
			Data:      base64.StdEncoding.EncodeToString(media.Data),
		},
	}, true
}

func thinkingBlockToAnthropic(block agent.ThinkingBlock) (anthropicContent, bool) {
	if len(block.Raw) > 0 {
		var decoded anthropicContent
		if err := json.Unmarshal(block.Raw, &decoded); err == nil {
			return decoded, true
		}
	}

	kind := strings.TrimSpace(block.Type)
	if kind == "" {
		kind = "thinking"
	}
	if kind != "thinking" && kind != "redacted_thinking" {
		return anthropicContent{}, false
	}
	return anthropicContent{
		Type:      kind,
		Thinking:  block.Content,
		Signature: block.Signature,
	}, true
}

func rawString(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

func toAnthropicTools(tools []agent.ToolDef, cache anthropicCachePolicy) []anthropicToolDef {
	out := make([]anthropicToolDef, 0, len(tools))
	for _, t := range tools {
		if t.Name == "" || len(t.Parameters) == 0 {
			continue
		}
		out = append(out, anthropicToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	if len(out) > 0 {
		out[len(out)-1].CacheControl = cache.cacheControl()
	}
	return out
}

func anthropicThinkingForOptions(reasoning agent.ReasoningConfig, maxTokens int) *anthropicThinking {
	effort := agent.ReasoningEffort(strings.ToLower(strings.TrimSpace(string(reasoning.Effort))))
	if effort == "" || effort == agent.ReasoningEffortNone {
		return nil
	}

	usable := maxTokens - 1
	if usable < 1024 {
		return nil
	}

	ratio := 0.5
	switch effort {
	case agent.ReasoningEffortLow:
		ratio = 0.25
	case agent.ReasoningEffortMedium:
		ratio = 0.5
	case agent.ReasoningEffortHigh:
		ratio = 0.75
	case agent.ReasoningEffortXHigh:
		ratio = 0.9
	default:
		ratio = 0.5
	}
	budget := int(float64(maxTokens) * ratio)
	if budget < 1024 {
		budget = 1024
	}
	if budget >= maxTokens {
		budget = usable
	}
	if budget < 1024 {
		return nil
	}
	return &anthropicThinking{
		Type:         "enabled",
		BudgetTokens: budget,
	}
}

func splitMessages(messages []agent.Message, cache anthropicCachePolicy) ([]anthropicContent, []agent.Message) {
	var systemParts []anthropicContent
	out := make([]agent.Message, 0, len(messages))
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			systemParts = append(systemParts, systemMessageToContent(msg, cache)...)
			continue
		}
		out = append(out, msg)
	}
	return systemParts, out
}

func systemMessageToContent(msg agent.Message, cache anthropicCachePolicy) []anthropicContent {
	if len(msg.SystemBlocks) > 0 {
		out := make([]anthropicContent, 0, len(msg.SystemBlocks))
		for _, block := range msg.SystemBlocks {
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			content := anthropicContent{
				Type: "text",
				Text: text,
			}
			if block.CacheBreakpoint {
				content.CacheControl = cache.cacheControl()
			}
			out = append(out, content)
		}
		return out
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return nil
	}
	return []anthropicContent{{
		Type: "text",
		Text: text,
	}}
}

func mustMarshalRaw(v anthropicContent) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func summarizeThinking(raw string, mode agent.ReasoningSummaryMode) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	switch agent.ReasoningSummaryMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case agent.ReasoningSummaryNone:
		return ""
	case agent.ReasoningSummaryCompact:
		return truncateSummary(raw, 1800)
	default:
		return truncateSummary(raw, 600)
	}
}

func truncateSummary(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= limit || limit <= 0 {
		return raw
	}
	if limit <= 3 {
		return raw[:limit]
	}
	return raw[:limit-3] + "..."
}

func (p *anthropicStreamParser) consume(event internal.Event) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if strings.TrimSpace(event.Data) == "" {
		return nil
	}

	var payload anthropicStreamEvent
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		p.parseErr = fmt.Errorf("anthropic: decode stream event: %w", err)
		return p.parseErr
	}
	eventType := strings.TrimSpace(payload.Type)
	if eventType == "" {
		eventType = strings.TrimSpace(event.Type)
	}

	switch eventType {
	case "ping", "":
		return nil
	case "error":
		message := "anthropic stream error"
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			message = payload.Error.Message
		}
		p.parseErr = fmt.Errorf("anthropic: %s", message)
		return p.parseErr
	case "message_start":
		p.captureUsage(payload.Message.Usage)
	case "message_delta":
		p.captureUsage(payload.Usage)
	case "content_block_start":
		block := p.ensureBlock(payload.Index)
		block.kind = payload.ContentBlock.Type
		switch payload.ContentBlock.Type {
		case "text":
			if payload.ContentBlock.Text != "" {
				block.text.WriteString(payload.ContentBlock.Text)
				p.text.WriteString(payload.ContentBlock.Text)
				p.emitText(payload.ContentBlock.Text)
			}
		case "tool_use", "tool_call":
			block.toolID = payload.ContentBlock.ID
			block.toolName = payload.ContentBlock.Name
			if len(payload.ContentBlock.Input) > 0 {
				block.initialRaw = payload.ContentBlock.Input
			}
		}
	case "content_block_delta":
		block := p.ensureBlock(payload.Index)
		switch payload.Delta.Type {
		case "text_delta":
			if payload.Delta.Text == "" {
				return nil
			}
			block.text.WriteString(payload.Delta.Text)
			p.text.WriteString(payload.Delta.Text)
			p.emitText(payload.Delta.Text)
		case "input_json_delta":
			block.toolInput.WriteString(payload.Delta.PartialJSON)
		}
	case "content_block_stop":
		block := p.blocks[payload.Index]
		if block == nil {
			return nil
		}
		if block.kind == "tool_use" || block.kind == "tool_call" {
			p.toolCalls = append(p.toolCalls, agent.ToolCall{
				ID:    block.toolID,
				Name:  block.toolName,
				Input: finalizeToolInput(block.initialRaw, block.toolInput.String()),
			})
		}
	case "message_stop":
		return nil
	}
	return nil
}

func (p *anthropicStreamParser) response() *agent.Response {
	content := p.text.String()
	if content == "" && len(p.order) > 0 {
		sort.Ints(p.order)
		var joined strings.Builder
		for _, idx := range p.order {
			block := p.blocks[idx]
			if block == nil || block.kind != "text" {
				continue
			}
			joined.WriteString(block.text.String())
		}
		content = joined.String()
	}
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return &agent.Response{
		Content:   content,
		ToolCalls: append([]agent.ToolCall(nil), p.toolCalls...),
		Usage:     usage,
	}
}

func (p *anthropicStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *anthropicStreamParser) ensureBlock(index int) *anthropicStreamBlock {
	if block, ok := p.blocks[index]; ok {
		return block
	}
	block := &anthropicStreamBlock{}
	p.blocks[index] = block
	p.order = append(p.order, index)
	return block
}

func (p *anthropicStreamParser) captureUsage(usage anthropicUsage) {
	if usage.InputTokens != 0 {
		p.usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		p.usage.OutputTokens = usage.OutputTokens
	}
	if usage.TotalTokens != 0 {
		p.usage.TotalTokens = usage.TotalTokens
	}
	if usage.CacheReadInputTokens != 0 {
		p.usage.CacheReadTokens = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens != 0 {
		p.usage.CacheWriteTokens = usage.CacheCreationInputTokens
	}
}

func (p *anthropicStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}

func finalizeToolInput(initial json.RawMessage, partial string) json.RawMessage {
	trimmed := strings.TrimSpace(partial)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	if len(initial) > 0 {
		return initial
	}
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	raw, _ := json.Marshal(trimmed)
	return json.RawMessage(raw)
}

type apiError struct {
	statusCode int
	message    string
}

func (e apiError) Error() string {
	return e.message
}

func (e apiError) StatusCode() int {
	return e.statusCode
}
