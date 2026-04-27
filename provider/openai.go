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
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	maxOpenAIResponseBytes = 1 << 20
)

var _ agent.Provider = (*OpenAI)(nil)
var _ agent.ProviderWithOptions = (*OpenAI)(nil)

type OpenAIOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
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
	userAgent         string
}

func NewOpenAI(opts OpenAIOptions) (*OpenAI, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("openai: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("openai: model is required")
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
		userAgent:         opts.UserAgent,
	}, nil
}

func (o *OpenAI) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *OpenAI) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if shouldUseOpenAIResponses(o.model, tools, opts) {
		return o.completeResponses(ctx, messages, tools, opts)
	}
	return o.completeChat(ctx, messages, tools, opts)
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

type openAIRequest struct {
	Model               string              `json:"model"`
	Messages            []openRouterMessage `json:"messages"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Tools               []openRouterTool    `json:"tools,omitempty"`
	ToolChoice          string              `json:"tool_choice,omitempty"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
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
	Model           string           `json:"model"`
	Instructions    string           `json:"instructions,omitempty"`
	Input           []map[string]any `json:"input"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Tools           []map[string]any `json:"tools,omitempty"`
	ToolChoice      string           `json:"tool_choice,omitempty"`
	Reasoning       map[string]any   `json:"reasoning,omitempty"`
}

type openAIResponsesResponse struct {
	ID         string                      `json:"id"`
	OutputText string                      `json:"output_text"`
	Output     []openAIResponsesOutputItem `json:"output"`
	Usage      openAIResponsesUsage        `json:"usage"`
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

func shouldUseOpenAIResponses(model string, tools []agent.ToolDef, opts agent.CompleteOptions) bool {
	if opts.Reasoning.Effort == "" || opts.Reasoning.Effort == agent.ReasoningEffortNone || len(toOpenRouterTools(tools)) == 0 {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5")
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
