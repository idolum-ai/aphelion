//go:build linux

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

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
