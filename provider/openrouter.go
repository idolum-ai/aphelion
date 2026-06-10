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
)

const (
	defaultOpenRouterBaseURL   = "https://openrouter.ai/api/v1"
	maxOpenRouterResponseBytes = 1 << 20
)

var _ agent.Provider = (*OpenRouter)(nil)
var _ agent.ProviderWithOptions = (*OpenRouter)(nil)

type OpenRouterOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
	UserAgent  string
}

type OpenRouter struct {
	endpoint  string
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	userAgent string
}

func NewOpenRouter(opts OpenRouterOptions) (*OpenRouter, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("openrouter: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openrouter: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOpenRouterBaseURL
	}
	return &OpenRouter{
		endpoint:  baseURL + "/chat/completions",
		client:    client,
		apiKey:    opts.APIKey,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
		userAgent: opts.UserAgent,
	}, nil
}

func (o *OpenRouter) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *OpenRouter) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := openRouterRequest{
		Model:     o.model,
		MaxTokens: resolveMaxTokens(o.maxTokens, opts),
		Messages:  toOpenRouterMessages(messages),
	}
	if defs := toOpenRouterTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
		reqBody.ToolChoice = "auto"
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("openrouter: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("openrouter: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("openrouter: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("openrouter: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed openRouterResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("openrouter: decode response: %w", err)
	}
	return mapOpenRouterResponse(parsed), nil
}

type openRouterRequest struct {
	Model      string              `json:"model"`
	Messages   []openRouterMessage `json:"messages"`
	MaxTokens  int                 `json:"max_tokens,omitempty"`
	Tools      []openRouterTool    `json:"tools,omitempty"`
	ToolChoice string              `json:"tool_choice,omitempty"`
}

type openRouterMessage struct {
	Role       string               `json:"role"`
	Content    any                  `json:"content,omitempty"`
	ToolCalls  []openRouterToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type openRouterTool struct {
	Type     string                 `json:"type"`
	Function openRouterToolFunction `json:"function"`
}

type openRouterToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openRouterToolCall struct {
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function openRouterToolCallTarget `json:"function"`
}

type openRouterToolCallTarget struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type openRouterResponse struct {
	Choices []openRouterChoice `json:"choices"`
	Usage   openRouterUsage    `json:"usage"`
}

type openRouterChoice struct {
	Message      openRouterResponseMessage `json:"message"`
	FinishReason string                    `json:"finish_reason,omitempty"`
}

type openRouterResponseMessage struct {
	Content   json.RawMessage      `json:"content"`
	ToolCalls []openRouterToolCall `json:"tool_calls"`
}

type openRouterUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func toOpenRouterMessages(messages []agent.Message) []openRouterMessage {
	out := make([]openRouterMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			continue
		}
		entry := openRouterMessage{Role: role}
		switch role {
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				entry.ToolCalls = make([]openRouterToolCall, 0, len(msg.ToolCalls))
				for _, call := range msg.ToolCalls {
					entry.ToolCalls = append(entry.ToolCalls, openRouterToolCall{
						ID:   call.ID,
						Type: "function",
						Function: openRouterToolCallTarget{
							Name:      call.Name,
							Arguments: string(call.Input),
						},
					})
				}
			}
			if strings.TrimSpace(msg.Content) != "" {
				entry.Content = msg.Content
			}
		case "tool":
			entry.ToolCallID = msg.ToolCallID
			entry.Content = msg.Content
		default:
			entry.Content = openRouterContentFromMessage(msg)
		}
		out = append(out, entry)
	}
	return out
}

func openRouterContentFromMessage(msg agent.Message) any {
	if len(msg.Media) == 0 {
		return msg.Content
	}
	parts := make([]map[string]any, 0, len(msg.Media)+1)
	for _, media := range msg.Media {
		if imagePart, ok := mediaToOpenRouterPart(media); ok {
			parts = append(parts, imagePart)
		}
	}
	if strings.TrimSpace(msg.Content) != "" || len(parts) == 0 {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": msg.Content,
		})
	}
	return parts
}

func mediaToOpenRouterPart(media core.Media) (map[string]any, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") || len(media.Data) == 0 {
		return nil, false
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(media.Data))
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": dataURL,
		},
	}, true
}

func toOpenRouterTools(tools []agent.ToolDef) []openRouterTool {
	out := make([]openRouterTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			continue
		}
		out = append(out, openRouterTool{
			Type: "function",
			Function: openRouterToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return out
}

func mapOpenRouterResponse(res openRouterResponse) *agent.Response {
	resp := &agent.Response{}
	if len(res.Choices) > 0 {
		choice := res.Choices[0]
		msg := choice.Message
		resp.FinishReason = strings.TrimSpace(choice.FinishReason)
		resp.Content = decodeOpenRouterContent(msg.Content)
		resp.ToolCalls = make([]agent.ToolCall, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if strings.TrimSpace(call.Function.Name) == "" {
				continue
			}
			input := json.RawMessage(strings.TrimSpace(call.Function.Arguments))
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
				ID:    call.ID,
				Name:  call.Function.Name,
				Input: input,
			})
		}
	}
	resp.Usage = core.TokenUsage{
		InputTokens:      res.Usage.PromptTokens,
		OutputTokens:     res.Usage.CompletionTokens,
		TotalTokens:      res.Usage.TotalTokens,
		CacheReadTokens:  res.Usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: res.Usage.PromptTokensDetails.CacheWriteTokens,
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.InputTokens + resp.Usage.OutputTokens
	}
	return resp
}

func decodeOpenRouterContent(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return string(raw)
}
