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
	"net/url"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

const (
	defaultGeminiBaseURL   = "https://generativelanguage.googleapis.com/v1beta"
	maxGeminiResponseBytes = 1 << 20
)

var _ agent.Provider = (*Gemini)(nil)
var _ agent.ProviderWithOptions = (*Gemini)(nil)
var _ agent.StreamingProvider = (*Gemini)(nil)
var _ agent.StreamingProviderWithOptions = (*Gemini)(nil)

type GeminiOptions struct {
	APIKey     string
	BaseURL    string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
	UserAgent  string
}

type Gemini struct {
	baseURL   string
	client    *http.Client
	apiKey    string
	model     string
	maxTokens int
	userAgent string
}

func NewGemini(opts GeminiOptions) (*Gemini, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("gemini: api key is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return nil, fmt.Errorf("gemini: model is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}
	return &Gemini{
		baseURL:   baseURL,
		client:    client,
		apiKey:    opts.APIKey,
		model:     strings.TrimSpace(opts.Model),
		maxTokens: opts.MaxTokens,
		userAgent: opts.UserAgent,
	}, nil
}

func (g *Gemini) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	return g.CompleteWithOptions(ctx, messages, tools, agent.CompleteOptions{})
}

func (g *Gemini) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	reqBody := geminiRequest{
		Contents:          toGeminiContents(messages),
		SystemInstruction: geminiSystemInstruction(messages),
		GenerationConfig:  geminiGenerationConfig{MaxOutputTokens: resolveMaxTokens(g.maxTokens, opts)},
	}
	if defs := toGeminiTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("gemini: encode request: %w", err)
	}

	resp, err := g.doJSONRequest(ctx, g.endpoint("generateContent"), &buf)
	if err != nil {
		return nil, fmt.Errorf("gemini: request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGeminiResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("gemini: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("gemini: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	var parsed geminiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}
	if parsed.Error != nil {
		msg := strings.TrimSpace(parsed.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(parsed.Error.Status)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("gemini: %s", msg)
	}
	return mapGeminiResponse(parsed), nil
}

func (g *Gemini) Stream(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, cb agent.StreamCallback) (*agent.Response, error) {
	return g.StreamWithOptions(ctx, messages, tools, agent.CompleteOptions{}, cb)
}

func (g *Gemini) StreamWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions, cb agent.StreamCallback) (*agent.Response, error) {
	reqBody := geminiRequest{
		Contents:          toGeminiContents(messages),
		SystemInstruction: geminiSystemInstruction(messages),
		GenerationConfig:  geminiGenerationConfig{MaxOutputTokens: g.maxTokens},
	}
	if defs := toGeminiTools(tools); len(defs) > 0 {
		reqBody.Tools = defs
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(reqBody); err != nil {
		return nil, fmt.Errorf("gemini: encode stream request: %w", err)
	}
	resp, err := g.doJSONRequest(ctx, g.endpoint("streamGenerateContent"), &buf)
	if err != nil {
		return nil, fmt.Errorf("gemini: stream request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxGeminiResponseBytes))
		if readErr != nil {
			return nil, fmt.Errorf("gemini: read stream response: %w", readErr)
		}
		return nil, apiError{
			statusCode: resp.StatusCode,
			message:    fmt.Sprintf("gemini: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
	}

	parser := newGeminiStreamParser(cb)
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

func (g *Gemini) doJSONRequest(ctx context.Context, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(g.apiKey) != "" {
		req.Header.Set("x-goog-api-key", g.apiKey)
	}
	if g.userAgent != "" {
		req.Header.Set("User-Agent", g.userAgent)
	}
	return g.client.Do(req)
}

func (g *Gemini) endpoint(method string) string {
	escapedModel := url.PathEscape(strings.TrimSpace(g.model))
	u := fmt.Sprintf("%s/models/%s:%s", g.baseURL, escapedModel, method)
	values := url.Values{}
	if method == "streamGenerateContent" {
		values.Set("alt", "sse")
	}
	if len(values) == 0 {
		return u
	}
	return u + "?" + values.Encode()
}

type geminiRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiSystemContent   `json:"systemInstruction,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiSystemContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Usage      geminiUsage       `json:"usageMetadata"`
	Error      *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiUsage struct {
	PromptTokenCount     int64 `json:"promptTokenCount"`
	CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	TotalTokenCount      int64 `json:"totalTokenCount"`
	CachedContentTokens  int64 `json:"cachedContentTokenCount"`
}

func geminiSystemInstruction(messages []agent.Message) *geminiSystemContent {
	parts := make([]geminiPart, 0, len(messages))
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
			parts = append(parts, geminiPart{Text: text})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return &geminiSystemContent{Parts: parts}
}

func toGeminiContents(messages []agent.Message) []geminiContent {
	out := make([]geminiContent, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "" || role == "system" {
			continue
		}
		if role == "assistant" {
			if content, ok := geminiProviderStateContent(msg.ProviderState); ok {
				out = append(out, content)
				continue
			}
		}
		if content, ok := geminiContentFromMessage(role, msg); ok {
			out = append(out, content)
		}
	}
	return out
}

func geminiProviderStateContent(raw json.RawMessage) (geminiContent, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return geminiContent{}, false
	}
	var content geminiContent
	if err := json.Unmarshal(raw, &content); err != nil || len(content.Parts) == 0 {
		return geminiContent{}, false
	}
	if strings.TrimSpace(content.Role) == "" {
		content.Role = "model"
	}
	return content, true
}

func geminiContentFromMessage(role string, msg agent.Message) (geminiContent, bool) {
	switch role {
	case "assistant":
		parts := make([]geminiPart, 0, len(msg.ToolCalls)+1)
		if text := strings.TrimSpace(msg.Content); text != "" {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		for _, call := range msg.ToolCalls {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				continue
			}
			parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{
				Name: name,
				Args: normalizeGeminiJSONArgs(call.Input),
			}})
		}
		if len(parts) == 0 {
			return geminiContent{}, false
		}
		return geminiContent{Role: "model", Parts: parts}, true
	case "tool":
		name := strings.TrimSpace(msg.ToolName)
		if name == "" {
			name = strings.TrimSpace(msg.ToolCallID)
		}
		if name == "" {
			name = "tool"
		}
		return geminiContent{
			Role: "function",
			Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name: name,
					Response: map[string]any{
						"result": strings.TrimSpace(msg.Content),
					},
				},
			}},
		}, true
	default:
		parts := make([]geminiPart, 0, len(msg.Media)+1)
		for _, media := range msg.Media {
			if part, ok := mediaToGeminiPart(media); ok {
				parts = append(parts, part)
			}
		}
		if strings.TrimSpace(msg.Content) != "" || len(parts) == 0 {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		return geminiContent{Role: "user", Parts: parts}, true
	}
}

func mediaToGeminiPart(media core.Media) (geminiPart, bool) {
	mimeType := strings.TrimSpace(media.MimeType)
	if mimeType == "" && len(media.Data) > 0 {
		mimeType = http.DetectContentType(media.Data)
	}
	if len(media.Data) == 0 || mimeType == "" {
		return geminiPart{}, false
	}
	mimeType = strings.ToLower(mimeType)
	switch {
	case strings.HasPrefix(mimeType, "image/"),
		strings.HasPrefix(mimeType, "audio/"),
		strings.HasPrefix(mimeType, "video/"),
		strings.HasPrefix(mimeType, "text/"),
		mimeType == "application/pdf":
	default:
		return geminiPart{}, false
	}
	return geminiPart{InlineData: &geminiInlineData{
		MimeType: mimeType,
		Data:     base64.StdEncoding.EncodeToString(media.Data),
	}}, true
}

func toGeminiTools(tools []agent.ToolDef) []geminiTool {
	declarations := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		decl := geminiFunctionDeclaration{
			Name:        name,
			Description: strings.TrimSpace(tool.Description),
		}
		if len(bytes.TrimSpace(tool.Parameters)) > 0 {
			decl.Parameters = json.RawMessage(tool.Parameters)
		}
		declarations = append(declarations, decl)
	}
	if len(declarations) == 0 {
		return nil
	}
	return []geminiTool{{FunctionDeclarations: declarations}}
}

func mapGeminiResponse(res geminiResponse) *agent.Response {
	resp := &agent.Response{}
	if len(res.Candidates) > 0 {
		content := res.Candidates[0].Content
		resp.ProviderState, _ = json.Marshal(content)
		resp.Content, resp.ToolCalls, resp.Thinking, resp.ThinkingMeta = mapGeminiContent(content)
	}
	resp.Usage = mapGeminiUsage(res.Usage)
	return resp
}

func mapGeminiContent(content geminiContent) (string, []agent.ToolCall, string, []agent.ThinkingBlock) {
	var text strings.Builder
	var thinking strings.Builder
	var thinkingMeta []agent.ThinkingBlock
	var calls []agent.ToolCall
	for _, part := range content.Parts {
		if part.Thought {
			if strings.TrimSpace(part.Text) != "" {
				if thinking.Len() > 0 {
					thinking.WriteString("\n")
				}
				thinking.WriteString(part.Text)
				thinkingMeta = append(thinkingMeta, agent.ThinkingBlock{
					Type:      "gemini_thought",
					Content:   part.Text,
					Signature: part.ThoughtSignature,
				})
			}
			continue
		}
		if part.Text != "" {
			text.WriteString(part.Text)
		}
		if part.FunctionCall != nil && strings.TrimSpace(part.FunctionCall.Name) != "" {
			id := fmt.Sprintf("gemini_call_%d", len(calls)+1)
			calls = append(calls, agent.ToolCall{
				ID:    id,
				Name:  strings.TrimSpace(part.FunctionCall.Name),
				Input: normalizeGeminiJSONArgs(part.FunctionCall.Args),
			})
		}
	}
	return text.String(), calls, thinking.String(), thinkingMeta
}

func mapGeminiUsage(usage geminiUsage) core.TokenUsage {
	out := core.TokenUsage{
		InputTokens:     usage.PromptTokenCount,
		OutputTokens:    usage.CandidatesTokenCount,
		TotalTokens:     usage.TotalTokenCount,
		CacheReadTokens: usage.CachedContentTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func normalizeGeminiJSONArgs(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`)
	}
	if json.Valid(trimmed) {
		return json.RawMessage(trimmed)
	}
	encoded, err := json.Marshal(string(trimmed))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

type geminiStreamParser struct {
	cb           agent.StreamCallback
	text         strings.Builder
	thinking     strings.Builder
	thinkingMeta []agent.ThinkingBlock
	toolCalls    []agent.ToolCall
	usage        core.TokenUsage
	stateContent geminiContent
	callbackErr  error
	parseErr     error
}

func newGeminiStreamParser(cb agent.StreamCallback) *geminiStreamParser {
	return &geminiStreamParser{cb: cb}
}

func (p *geminiStreamParser) consume(data string) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if strings.TrimSpace(data) == "" {
		return nil
	}
	var payload geminiResponse
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		p.parseErr = fmt.Errorf("gemini: decode stream event: %w", err)
		return p.parseErr
	}
	if payload.Error != nil {
		msg := strings.TrimSpace(payload.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(payload.Error.Status)
		}
		if msg == "" {
			msg = "unknown stream error"
		}
		p.parseErr = fmt.Errorf("gemini: stream error: %s", msg)
		return p.parseErr
	}
	if usage := mapGeminiUsage(payload.Usage); usage.TotalTokens != 0 || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		p.usage = usage
	}
	if len(payload.Candidates) == 0 {
		return p.err()
	}
	content := payload.Candidates[0].Content
	p.appendProviderState(content)
	text, calls, thinking, thinkingMeta := mapGeminiContent(content)
	if text != "" {
		p.text.WriteString(text)
		p.emitText(text)
	}
	if thinking != "" {
		if p.thinking.Len() > 0 {
			p.thinking.WriteString("\n")
		}
		p.thinking.WriteString(thinking)
		p.thinkingMeta = append(p.thinkingMeta, thinkingMeta...)
	}
	for _, call := range calls {
		call.ID = fmt.Sprintf("gemini_call_%d", len(p.toolCalls)+1)
		p.toolCalls = append(p.toolCalls, call)
		p.emitToolCall(call)
	}
	return p.err()
}

func (p *geminiStreamParser) response() *agent.Response {
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	providerState, _ := json.Marshal(p.stateContent)
	if len(p.stateContent.Parts) == 0 {
		providerState = nil
	}
	return &agent.Response{
		Content:       p.text.String(),
		Thinking:      p.thinking.String(),
		ThinkingMeta:  append([]agent.ThinkingBlock(nil), p.thinkingMeta...),
		ProviderState: append(json.RawMessage(nil), providerState...),
		ToolCalls:     append([]agent.ToolCall(nil), p.toolCalls...),
		Usage:         usage,
	}
}

func (p *geminiStreamParser) appendProviderState(content geminiContent) {
	if len(content.Parts) == 0 {
		return
	}
	if strings.TrimSpace(p.stateContent.Role) == "" {
		p.stateContent.Role = firstNonEmpty(strings.TrimSpace(content.Role), "model")
	}
	p.stateContent.Parts = append(p.stateContent.Parts, content.Parts...)
}

func (p *geminiStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *geminiStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}

func (p *geminiStreamParser) emitToolCall(call agent.ToolCall) {
	if p.cb == nil || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "tool_call", ToolCall: &call}); err != nil {
		p.callbackErr = err
	}
}
