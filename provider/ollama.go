//go:build linux

package provider

import (
	"bufio"
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
	defaultOllamaBaseURL   = "http://localhost:11434"
	maxOllamaResponseBytes = 1 << 20
)

var _ agent.Provider = (*Ollama)(nil)
var _ agent.ProviderWithOptions = (*Ollama)(nil)
var _ agent.StreamingProvider = (*Ollama)(nil)
var _ agent.StreamingProviderWithOptions = (*Ollama)(nil)

type OllamaOptions struct {
	BaseURL    string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
	UserAgent  string
}

type Ollama struct {
	endpoint  string
	client    *http.Client
	model     string
	maxTokens int
	userAgent string
}

func NewOllama(opts OllamaOptions) (*Ollama, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("ollama: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	return &Ollama{
		endpoint:  baseURL + "/api/chat",
		client:    client,
		model:     strings.TrimSpace(opts.Model),
		maxTokens: opts.MaxTokens,
		userAgent: opts.UserAgent,
	}, nil
}

func (o *Ollama) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return o.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (o *Ollama) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := ollamaRequest{
		Model:    o.model,
		Messages: toOllamaMessages(messages),
		Tools:    toOllamaTools(tools),
		Stream:   false,
		Options:  ollamaOptions{NumPredict: resolveMaxTokens(o.maxTokens, opts)},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("ollama: encode request: %w", err)
	}
	resp, err := o.doJSONRequest(ctx, &buf)
	if err != nil {
		return nil, fmt.Errorf("ollama: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOllamaResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("ollama: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("ollama: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed ollamaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ollama: decode response: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("ollama: %s", parsed.Error)
	}
	return mapOllamaResponse(parsed), nil
}

func (o *Ollama) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return o.StreamWithOptions(ctx, messages, tools, agent.CompleteOptions{}, cb)
}

func (o *Ollama) StreamWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := ollamaRequest{
		Model:    o.model,
		Messages: toOllamaMessages(messages),
		Tools:    toOllamaTools(tools),
		Stream:   true,
		Options:  ollamaOptions{NumPredict: resolveMaxTokens(o.maxTokens, opts)},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("ollama: encode stream request: %w", err)
	}
	resp, err := o.doJSONRequest(ctx, &buf)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOllamaResponseBytes))
		if readErr != nil {
			return nil, fmt.Errorf("ollama: read stream response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("ollama: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newOllamaStreamParser(cb)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxOllamaResponseBytes)
	for scanner.Scan() {
		if err := parser.consume(scanner.Bytes()); err != nil {
			return parser.response(), err
		}
	}
	if err := scanner.Err(); err != nil {
		return parser.response(), fmt.Errorf("ollama: read stream: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return parser.response(), err
	}
	return parser.response(), parser.err()
}

func (o *Ollama) doJSONRequest(ctx context.Context, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.userAgent != "" {
		req.Header.Set("User-Agent", o.userAgent)
	}
	return o.client.Do(req)
}

type ollamaRequest struct {
	Model    string           `json:"model"`
	Messages []ollamaMessage  `json:"messages"`
	Tools    []openRouterTool `json:"tools,omitempty"`
	Stream   bool             `json:"stream"`
	Options  ollamaOptions    `json:"options,omitempty"`
}

type ollamaOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaResponse struct {
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	PromptEvalCount int64         `json:"prompt_eval_count"`
	EvalCount       int64         `json:"eval_count"`
	Error           string        `json:"error,omitempty"`
}

type ollamaToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func toOllamaMessages(messages []agent.Message) []ollamaMessage {
	out := make([]ollamaMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" {
			continue
		}
		entry := ollamaMessage{Role: role}
		switch role {
		case "assistant":
			entry.Content = msg.Content
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Name)
				if name == "" {
					continue
				}
				entry.ToolCalls = append(entry.ToolCalls, ollamaToolCall{
					ID:   strings.TrimSpace(call.ID),
					Type: "function",
					Function: ollamaToolCallFunction{
						Name:      name,
						Arguments: normalizeOllamaArguments(call.Input),
					},
				})
			}
		case "tool":
			entry.Role = "tool"
			entry.Content = msg.Content
		default:
			entry.Content = msg.Content
			for _, media := range msg.Media {
				if image, ok := mediaToOllamaImage(media); ok {
					entry.Images = append(entry.Images, image)
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

func mediaToOllamaImage(media core.Media) (string, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if len(media.Data) == 0 || !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(media.Data), true
}

func toOllamaTools(tools []agent.ToolDef) []openRouterTool {
	return toOpenRouterTools(tools)
}

func mapOllamaResponse(res ollamaResponse) *agent.Response {
	resp := &agent.Response{
		Content:   res.Message.Content,
		ToolCalls: mapOllamaToolCalls(res.Message.ToolCalls),
		Usage: core.TokenUsage{
			InputTokens:  res.PromptEvalCount,
			OutputTokens: res.EvalCount,
			TotalTokens:  res.PromptEvalCount + res.EvalCount,
		},
	}
	return resp
}

func mapOllamaToolCalls(calls []ollamaToolCall) []agent.ToolCall {
	out := make([]agent.ToolCall, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		input := normalizeOllamaArguments(call.Function.Arguments)
		id := strings.TrimSpace(call.ID)
		if id == "" {
			id = fmt.Sprintf("ollama_call_%d", len(out)+1)
		}
		out = append(out, agent.ToolCall{
			ID:    id,
			Name:  name,
			Input: input,
		})
	}
	return out
}

func normalizeOllamaArguments(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`)
	}
	if json.Valid(trimmed) {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err == nil {
			nested := bytes.TrimSpace([]byte(encoded))
			if len(nested) > 0 && json.Valid(nested) {
				return json.RawMessage(nested)
			}
		}
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(string(trimmed))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

type ollamaStreamParser struct {
	cb          agent.StreamCallback
	text        strings.Builder
	toolCalls   []agent.ToolCall
	usage       core.TokenUsage
	callbackErr error
	parseErr    error
}

func newOllamaStreamParser(cb agent.StreamCallback) *ollamaStreamParser {
	return &ollamaStreamParser{cb: cb}
}

func (p *ollamaStreamParser) consume(line []byte) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	var payload ollamaResponse
	if err := json.Unmarshal(line, &payload); err != nil {
		p.parseErr = fmt.Errorf("ollama: decode stream event: %w", err)
		return p.parseErr
	}
	if payload.Error != "" {
		p.parseErr = fmt.Errorf("ollama: stream error: %s", payload.Error)
		return p.parseErr
	}
	if payload.Message.Content != "" {
		p.text.WriteString(payload.Message.Content)
		p.emitText(payload.Message.Content)
	}
	for _, call := range mapOllamaToolCalls(payload.Message.ToolCalls) {
		p.toolCalls = append(p.toolCalls, call)
		p.emitToolCall(call)
	}
	if payload.PromptEvalCount != 0 || payload.EvalCount != 0 {
		p.usage.InputTokens = payload.PromptEvalCount
		p.usage.OutputTokens = payload.EvalCount
		p.usage.TotalTokens = payload.PromptEvalCount + payload.EvalCount
	}
	return p.err()
}

func (p *ollamaStreamParser) response() *agent.Response {
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return &agent.Response{
		Content:   p.text.String(),
		ToolCalls: append([]agent.ToolCall(nil), p.toolCalls...),
		Usage:     usage,
	}
}

func (p *ollamaStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *ollamaStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}

func (p *ollamaStreamParser) emitToolCall(call agent.ToolCall) {
	if p.cb == nil || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "tool_call", ToolCall: &call}); err != nil {
		p.callbackErr = err
	}
}
