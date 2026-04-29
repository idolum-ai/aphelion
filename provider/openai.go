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
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	maxOpenAIResponseBytes = 1 << 20
)

var _ agent.Provider = (*OpenAI)(nil)
var _ agent.ProviderWithOptions = (*OpenAI)(nil)
var _ agent.StreamingProvider = (*OpenAI)(nil)
var _ agent.StreamingProviderWithOptions = (*OpenAI)(nil)

type OpenAIOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	Transport  string
	HTTPClient *http.Client
	UserAgent  string
}

type OpenAI struct {
	chatEndpoint      string
	responsesEndpoint string
	client            *http.Client
	apiKey            string
	model             string
	maxTokens         int
	transport         string
	userAgent         string
}

func NewOpenAI(opts OpenAIOptions) (*OpenAI, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("openai: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openai: model is required")
	}
	transport := core.NormalizeModelTransport(opts.Transport)
	if transport == "" {
		return nil, fmt.Errorf("openai: unsupported transport %q", opts.Transport)
	}
	switch transport {
	case core.ModelTransportAuto, core.ModelTransportOpenAIResponses, core.ModelTransportOpenAIChat:
	default:
		return nil, fmt.Errorf("openai: unsupported transport %q", opts.Transport)
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return &OpenAI{
		chatEndpoint:      baseURL + "/chat/completions",
		responsesEndpoint: baseURL + "/responses",
		client:            client,
		apiKey:            opts.APIKey,
		model:             opts.Model,
		maxTokens:         opts.MaxTokens,
		transport:         transport,
		userAgent:         opts.UserAgent,
	}, nil
}

func (o *OpenAI) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *OpenAI) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if o.resolveTransport(tools, opts) == core.ModelTransportOpenAIResponses {
		return o.completeResponses(ctx, messages, tools, opts)
	}
	return o.completeChat(ctx, messages, tools, opts)
}

func (o *OpenAI) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return o.StreamWithOptions(ctx, messages, tools, agent.CompleteOptions{}, cb)
}

func (o *OpenAI) StreamWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	if o.resolveTransport(tools, opts) == core.ModelTransportOpenAIResponses {
		return o.streamResponses(ctx, messages, tools, opts, cb)
	}
	return o.streamChat(ctx, messages, tools, opts, cb)
}

func (o *OpenAI) resolveTransport(tools []agent.ToolDef, opts agent.CompleteOptions) string {
	switch o.transport {
	case core.ModelTransportOpenAIResponses:
		return core.ModelTransportOpenAIResponses
	case core.ModelTransportOpenAIChat:
		return core.ModelTransportOpenAIChat
	default:
		if shouldUseOpenAIResponses(o.model, tools, opts) {
			return core.ModelTransportOpenAIResponses
		}
		return core.ModelTransportOpenAIChat
	}
}

func (o *OpenAI) completeChat(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := openAIRequest{
		Model:               o.model,
		MaxCompletionTokens: o.maxTokens,
		Messages:            toOpenRouterMessages(messages),
		ReasoningEffort:     openAIReasoningEffort(opts.Reasoning.Effort),
	}
	if defs := toOpenRouterTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.chatEndpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed openRouterResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	return mapOpenRouterResponse(parsed), nil
}

func (o *OpenAI) completeResponses(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := openAIResponsesRequest{
		Model:           o.model,
		Instructions:    openAIResponsesInstructions(messages),
		Input:           openAIResponsesInputItems(messages),
		MaxOutputTokens: o.maxTokens,
		Reasoning:       openAIResponsesReasoning(opts.Reasoning),
		Text:            openAIResponsesText(opts.Verbosity),
		Store:           boolPtr(false),
	}
	if defs := toOpenAIResponsesTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openai: encode responses request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.responsesEndpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: new responses request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: responses request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: read responses response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed openAIResponsesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openai: decode responses response: %w", err)
	}
	return mapOpenAIResponsesResponse(parsed), nil
}

func (o *OpenAI) streamChat(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := openAIRequest{
		Model:               o.model,
		MaxCompletionTokens: o.maxTokens,
		Messages:            toOpenRouterMessages(messages),
		ReasoningEffort:     openAIReasoningEffort(opts.Reasoning.Effort),
		Stream:              true,
		StreamOptions:       &openAIStreamOptions{IncludeUsage: true},
	}
	if defs := toOpenRouterTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openai: encode stream request: %w", err)
	}
	resp, err := o.doJSONRequest(ctx, o.chatEndpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
		if readErr != nil {
			return nil, fmt.Errorf("openai: read stream response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newOpenAIChatStreamParser(cb)
	for event := range internal.ParseSSE(resp.Body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event.Data); err != nil {
			return parser.response(), err
		}
	}
	if err := ctx.Err(); err != nil {
		return parser.response(), err
	}
	return parser.response(), parser.err()
}

func (o *OpenAI) streamResponses(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := openAIResponsesRequest{
		Model:           o.model,
		Instructions:    openAIResponsesInstructions(messages),
		Input:           openAIResponsesInputItems(messages),
		MaxOutputTokens: o.maxTokens,
		Reasoning:       openAIResponsesReasoning(opts.Reasoning),
		Text:            openAIResponsesText(opts.Verbosity),
		Stream:          true,
		Store:           boolPtr(false),
	}
	if defs := toOpenAIResponsesTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openai: encode responses stream request: %w", err)
	}
	resp, err := o.doJSONRequest(ctx, o.responsesEndpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: responses stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOpenAIResponseBytes))
		if readErr != nil {
			return nil, fmt.Errorf("openai: read responses stream response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newOpenAIResponsesStreamParser(cb)
	for event := range internal.ParseSSE(resp.Body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event); err != nil {
			return parser.response(), err
		}
	}
	if err := ctx.Err(); err != nil {
		return parser.response(), err
	}
	return parser.response(), parser.err()
}

func (o *OpenAI) doJSONRequest(ctx context.Context, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}
	return o.client.Do(req)
}

type openAIRequest struct {
	Model               string               `json:"model"`
	Messages            []openRouterMessage  `json:"messages"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Tools               []openRouterTool     `json:"tools,omitempty"`
	ToolChoice          string               `json:"tool_choice,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Stream              bool                 `json:"stream,omitempty"`
	StreamOptions       *openAIStreamOptions `json:"stream_options,omitempty"`
}

func openAIReasoningEffort(effort agent.ReasoningEffort) string {
	switch effort {
	case agent.ReasoningEffortNone,
		agent.ReasoningEffortLow,
		agent.ReasoningEffortMedium,
		agent.ReasoningEffortHigh,
		agent.ReasoningEffortXHigh:
		return string(effort)
	default:
		return ""
	}
}

type openAIResponsesRequest struct {
	Model           string                     `json:"model"`
	Instructions    string                     `json:"instructions,omitempty"`
	Input           []map[string]any           `json:"input"`
	MaxOutputTokens int                        `json:"max_output_tokens,omitempty"`
	Tools           []map[string]any           `json:"tools,omitempty"`
	ToolChoice      string                     `json:"tool_choice,omitempty"`
	Reasoning       map[string]any             `json:"reasoning,omitempty"`
	Text            *openAIResponsesTextConfig `json:"text,omitempty"`
	Stream          bool                       `json:"stream,omitempty"`
	Store           *bool                      `json:"store,omitempty"`
}

type openAIResponsesTextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type openAIResponsesResponse struct {
	ID         string                      `json:"id"`
	OutputText string                      `json:"output_text"`
	Output     []openAIResponsesOutputItem `json:"output"`
	Usage      openAIResponsesUsage        `json:"usage"`
	Error      *openAIStreamFailure        `json:"error,omitempty"`
}

type openAIResponsesOutputItem struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type openAIResponsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type openAIResponsesStreamEvent struct {
	Type     string                  `json:"type"`
	Delta    string                  `json:"delta,omitempty"`
	Text     string                  `json:"text,omitempty"`
	Response openAIResponsesResponse `json:"response,omitempty"`
	Error    *openAIStreamFailure    `json:"error,omitempty"`
}

type openAIChatStreamEvent struct {
	Type    string               `json:"type,omitempty"`
	Choices []openAIChatChoice   `json:"choices,omitempty"`
	Usage   openRouterUsage      `json:"usage,omitempty"`
	Error   *openAIStreamFailure `json:"error,omitempty"`
}

type openAIChatChoice struct {
	Delta openAIChatDelta `json:"delta"`
}

type openAIChatDelta struct {
	Content string `json:"content,omitempty"`
}

type openAIStreamFailure struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func shouldUseOpenAIResponses(model string, tools []agent.ToolDef, opts agent.CompleteOptions) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5") {
		return false
	}
	if openAIResponsesVerbosity(opts.Verbosity) != "" {
		return true
	}
	return opts.Reasoning.Effort != "" &&
		opts.Reasoning.Effort != agent.ReasoningEffortNone &&
		len(toOpenRouterTools(tools)) > 0
}

func openAIResponsesInstructions(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if text == "" && len(msg.SystemBlocks) > 0 {
			blockParts := make([]string, 0, len(msg.SystemBlocks))
			for _, block := range msg.SystemBlocks {
				if blockText := strings.TrimSpace(block.Text); blockText != "" {
					blockParts = append(blockParts, blockText)
				}
			}
			text = strings.Join(blockParts, "\n\n")
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func openAIResponsesInputItems(messages []agent.Message) []map[string]any {
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" || role == "system" {
			continue
		}
		switch role {
		case "user", "assistant":
			if item, ok := openAIResponsesMessageInputItem(role, msg); ok {
				input = append(input, item)
			}
			if role == "assistant" {
				for _, call := range msg.ToolCalls {
					input = append(input, map[string]any{
						"type":      "function_call",
						"name":      call.Name,
						"arguments": normalizeOpenAIResponsesArguments(call.Input),
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

func openAIResponsesMessageInputItem(role string, msg agent.Message) (map[string]any, bool) {
	content := make([]map[string]any, 0, len(msg.Media)+1)
	for _, media := range msg.Media {
		if part, ok := mediaToOpenAIResponsesInputItem(media); ok {
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

func mediaToOpenAIResponsesInputItem(media core.Media) (map[string]any, bool) {
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

func toOpenAIResponsesTools(tools []agent.ToolDef) []map[string]any {
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

func openAIResponsesReasoning(cfg agent.ReasoningConfig) map[string]any {
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

func openAIResponsesText(verbosity agent.Verbosity) *openAIResponsesTextConfig {
	value := openAIResponsesVerbosity(verbosity)
	if value == "" {
		return nil
	}
	return &openAIResponsesTextConfig{Verbosity: value}
}

func openAIResponsesVerbosity(verbosity agent.Verbosity) string {
	switch verbosity {
	case agent.VerbosityLow, agent.VerbosityMedium, agent.VerbosityHigh:
		return string(verbosity)
	default:
		return ""
	}
}

func mapOpenAIResponsesResponse(res openAIResponsesResponse) *agent.Response {
	resp := &agent.Response{}
	var text strings.Builder
	var thinking strings.Builder
	for _, item := range res.Output {
		switch strings.TrimSpace(item.Type) {
		case "message":
			for _, part := range item.Content {
				switch strings.TrimSpace(part.Type) {
				case "output_text", "text":
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			name := strings.TrimSpace(item.Name)
			if name == "" {
				continue
			}
			resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
				ID:    strings.TrimSpace(item.CallID),
				Name:  name,
				Input: json.RawMessage(normalizeOpenAIResponsesArguments(item.Arguments)),
			})
		case "reasoning":
			for _, summary := range item.Summary {
				if summaryText := strings.TrimSpace(summary.Text); summaryText != "" {
					if thinking.Len() > 0 {
						thinking.WriteString("\n")
					}
					thinking.WriteString(summaryText)
					resp.ThinkingMeta = append(resp.ThinkingMeta, agent.ThinkingBlock{
						Type:    "summary_text",
						Content: summaryText,
					})
				}
			}
		}
	}
	resp.Content = text.String()
	if strings.TrimSpace(resp.Content) == "" {
		resp.Content = strings.TrimSpace(res.OutputText)
	}
	resp.Thinking = thinking.String()
	resp.Usage = core.TokenUsage{
		InputTokens:      res.Usage.InputTokens,
		OutputTokens:     res.Usage.OutputTokens,
		TotalTokens:      res.Usage.TotalTokens,
		CacheReadTokens:  res.Usage.InputTokensDetails.CachedTokens,
		CacheWriteTokens: res.Usage.InputTokensDetails.CacheWriteTokens,
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	return resp
}

func normalizeOpenAIResponsesArguments(raw json.RawMessage) string {
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
			if len(nested) == 0 {
				return "{}"
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

type openAIChatStreamParser struct {
	cb          agent.StreamCallback
	text        strings.Builder
	usage       core.TokenUsage
	callbackErr error
	parseErr    error
}

func newOpenAIChatStreamParser(cb agent.StreamCallback) *openAIChatStreamParser {
	return &openAIChatStreamParser{cb: cb}
}

func (p *openAIChatStreamParser) consume(data string) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if strings.TrimSpace(data) == "" {
		return nil
	}
	var payload openAIChatStreamEvent
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		p.parseErr = fmt.Errorf("openai: decode chat stream event: %w", err)
		return p.parseErr
	}
	if payload.Error != nil {
		p.parseErr = fmt.Errorf("openai: stream error: %s", openAIStreamFailureMessage(payload.Error))
		return p.parseErr
	}
	p.captureUsage(payload.Usage)
	for _, choice := range payload.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		p.text.WriteString(choice.Delta.Content)
		p.emitText(choice.Delta.Content)
	}
	return p.err()
}

func (p *openAIChatStreamParser) response() *agent.Response {
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return &agent.Response{Content: p.text.String(), Usage: usage}
}

func (p *openAIChatStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *openAIChatStreamParser) captureUsage(usage openRouterUsage) {
	if usage.PromptTokens != 0 {
		p.usage.InputTokens = usage.PromptTokens
	}
	if usage.CompletionTokens != 0 {
		p.usage.OutputTokens = usage.CompletionTokens
	}
	if usage.TotalTokens != 0 {
		p.usage.TotalTokens = usage.TotalTokens
	}
	if usage.PromptTokensDetails.CachedTokens != 0 {
		p.usage.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
	}
	if usage.PromptTokensDetails.CacheWriteTokens != 0 {
		p.usage.CacheWriteTokens = usage.PromptTokensDetails.CacheWriteTokens
	}
}

func (p *openAIChatStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}

type openAIResponsesStreamParser struct {
	cb          agent.StreamCallback
	text        strings.Builder
	doneText    string
	completed   *openAIResponsesResponse
	callbackErr error
	parseErr    error
}

func newOpenAIResponsesStreamParser(cb agent.StreamCallback) *openAIResponsesStreamParser {
	return &openAIResponsesStreamParser{cb: cb}
}

func (p *openAIResponsesStreamParser) consume(event internal.Event) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if strings.TrimSpace(event.Data) == "" {
		return nil
	}
	var payload openAIResponsesStreamEvent
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		p.parseErr = fmt.Errorf("openai: decode responses stream event: %w", err)
		return p.parseErr
	}
	eventType := strings.TrimSpace(payload.Type)
	if eventType == "" {
		eventType = strings.TrimSpace(event.Type)
	}
	switch eventType {
	case "", "response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.content_part.done", "response.output_item.done":
		return p.err()
	case "response.output_text.delta":
		if payload.Delta == "" {
			return p.err()
		}
		p.text.WriteString(payload.Delta)
		p.emitText(payload.Delta)
	case "response.output_text.done":
		p.doneText = payload.Text
	case "response.completed":
		completed := payload.Response
		p.completed = &completed
	case "response.failed", "response.incomplete", "error":
		failure := payload.Error
		if failure == nil && payload.Response.Error != nil {
			failure = payload.Response.Error
		}
		p.parseErr = fmt.Errorf("openai: stream error: %s", openAIStreamFailureMessage(failure))
		return p.parseErr
	}
	return p.err()
}

func (p *openAIResponsesStreamParser) response() *agent.Response {
	resp := &agent.Response{}
	if p.completed != nil {
		resp = mapOpenAIResponsesResponse(*p.completed)
	}
	content := p.text.String()
	if strings.TrimSpace(content) == "" {
		content = p.doneText
	}
	if strings.TrimSpace(content) != "" {
		resp.Content = content
	}
	return resp
}

func (p *openAIResponsesStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *openAIResponsesStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}

func openAIStreamFailureMessage(failure *openAIStreamFailure) string {
	if failure == nil {
		return "unknown stream error"
	}
	if msg := strings.TrimSpace(failure.Message); msg != "" {
		return msg
	}
	if code := strings.TrimSpace(failure.Code); code != "" {
		return code
	}
	if typ := strings.TrimSpace(failure.Type); typ != "" {
		return typ
	}
	return "unknown stream error"
}

func boolPtr(v bool) *bool {
	return &v
}
