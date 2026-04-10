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
)

const (
	defaultAnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	defaultAnthropicVersion  = "2023-06-01"
)

var _ agent.Provider = (*Anthropic)(nil)

// AnthropicOptions configures the Anthropic provider client.
type AnthropicOptions struct {
	APIKey           string
	Model            string
	MaxTokens        int
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

	return &Anthropic{
		endpoint:  endpoint,
		client:    client,
		apiKey:    opts.APIKey,
		model:     opts.Model,
		maxTokens: opts.MaxTokens,
		version:   version,
		userAgent: opts.UserAgent,
	}, nil
}

// Complete sends the assembled history to Anthropic and returns the response.
func (a *Anthropic) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	systemPrompt, reqMessages := splitMessages(messages)
	reqBody := anthropicRequest{
		Model:     a.model,
		MaxTokens: a.maxTokens,
		System:    systemPrompt,
		Messages:  toAnthropicMessages(reqMessages),
	}
	if toolDefs := toAnthropicTools(tools); len(toolDefs) > 0 {
		reqBody.Tools = toolDefs
	}

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

	resp, err := a.client.Do(req)
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

	return mapAnthropicResponse(anthRes), nil
}

func mapAnthropicResponse(res anthropicResponse) *agent.Response {
	var text strings.Builder
	var toolCalls []agent.ToolCall
	for _, block := range res.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
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
		Content:   text.String(),
		ToolCalls: toolCalls,
		Usage:     usage,
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens,omitempty"`
	System    []anthropicContent `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicToolDef `json:"tools,omitempty"`
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
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty"`
	Input        json.RawMessage        `json:"input,omitempty"`
	ToolUseID    string                 `json:"tool_use_id,omitempty"`
	Content      json.RawMessage        `json:"content,omitempty"`
	IsError      bool                   `json:"is_error,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"`
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

func rawString(v string) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

func toAnthropicTools(tools []agent.ToolDef) []anthropicToolDef {
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
		out[len(out)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}
	return out
}

func splitMessages(messages []agent.Message) ([]anthropicContent, []agent.Message) {
	var systemParts []anthropicContent
	out := make([]agent.Message, 0, len(messages))
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			systemParts = append(systemParts, systemMessageToContent(msg)...)
			continue
		}
		out = append(out, msg)
	}
	return systemParts, out
}

func systemMessageToContent(msg agent.Message) []anthropicContent {
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
				content.CacheControl = &anthropicCacheControl{Type: "ephemeral"}
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
