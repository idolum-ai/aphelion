//go:build linux

package provider

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

func (a *Anthropic) buildRequest(messages []agent.Message, tools []agent.ToolDef, stream bool, opts agent.CompleteOptions) anthropicRequest {
	systemPrompt, reqMessages := splitMessages(messages, a.cache)
	toolDefs := toAnthropicTools(tools, a.cache)
	limitAnthropicCacheControls(systemPrompt, reqMessages, toolDefs, maxAnthropicCacheControls)
	reqBody := anthropicRequest{
		Model:     a.model,
		MaxTokens: resolveMaxTokens(a.maxTokens, opts),
		System:    systemPrompt,
		Messages:  toAnthropicMessages(reqMessages),
		Stream:    stream,
	}
	if len(toolDefs) > 0 {
		reqBody.Tools = toolDefs
	}
	if thinking, outputConfig := anthropicThinkingForOptions(a.model, opts.Reasoning, a.maxTokens); thinking != nil {
		reqBody.Thinking = thinking
		reqBody.OutputConfig = outputConfig
	}
	return reqBody
}

const maxAnthropicCacheControls = 4

func limitAnthropicCacheControls(system []anthropicContent, messages []agent.Message, tools []anthropicToolDef, limit int) {
	if limit <= 0 {
		clearAnthropicCacheControls(system, messages, tools)
		return
	}
	count := countAnthropicCacheControls(system, messages, tools)
	for count > limit {
		if clearLastMessageCacheControl(messages) || clearLastSystemCacheControl(system) || clearLastToolCacheControl(tools) {
			count--
			continue
		}
		return
	}
}

func countAnthropicCacheControls(system []anthropicContent, messages []agent.Message, tools []anthropicToolDef) int {
	count := 0
	for _, block := range system {
		if block.CacheControl != nil {
			count++
		}
	}
	for _, msg := range messages {
		for _, block := range msg.SystemBlocks {
			if block.CacheBreakpoint {
				count++
			}
		}
	}
	for _, tool := range tools {
		if tool.CacheControl != nil {
			count++
		}
	}
	return count
}

func clearAnthropicCacheControls(system []anthropicContent, messages []agent.Message, tools []anthropicToolDef) {
	for i := range system {
		system[i].CacheControl = nil
	}
	for i := range messages {
		for j := range messages[i].SystemBlocks {
			messages[i].SystemBlocks[j].CacheBreakpoint = false
		}
	}
	for i := range tools {
		tools[i].CacheControl = nil
	}
}

func clearLastSystemCacheControl(system []anthropicContent) bool {
	for i := len(system) - 1; i >= 0; i-- {
		if system[i].CacheControl != nil {
			system[i].CacheControl = nil
			return true
		}
	}
	return false
}

func clearLastMessageCacheControl(messages []agent.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].SystemBlocks) - 1; j >= 0; j-- {
			if messages[i].SystemBlocks[j].CacheBreakpoint {
				messages[i].SystemBlocks[j].CacheBreakpoint = false
				return true
			}
		}
	}
	return false
}

func clearLastToolCacheControl(tools []anthropicToolDef) bool {
	for i := len(tools) - 1; i >= 0; i-- {
		if tools[i].CacheControl != nil {
			tools[i].CacheControl = nil
			return true
		}
	}
	return false
}

type anthropicRequest struct {
	Model        string                 `json:"model"`
	MaxTokens    int                    `json:"max_tokens,omitempty"`
	System       []anthropicContent     `json:"system,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	Tools        []anthropicToolDef     `json:"tools,omitempty"`
	Thinking     *anthropicThinking     `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
	Stream       bool                   `json:"stream,omitempty"`
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

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
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

func anthropicThinkingForOptions(model string, reasoning agent.ReasoningConfig, maxTokens int) (*anthropicThinking, *anthropicOutputConfig) {
	effort := agent.ReasoningEffort(strings.ToLower(strings.TrimSpace(string(reasoning.Effort))))
	if effort == "" || effort == agent.ReasoningEffortNone {
		return nil, nil
	}

	if anthropicThinkingModeForModel(model) == anthropicThinkingAdaptive {
		return &anthropicThinking{Type: "adaptive"}, &anthropicOutputConfig{
			Effort: anthropicAdaptiveEffort(effort),
		}
	}

	return anthropicManualThinkingForOptions(effort, maxTokens), nil
}

type anthropicThinkingMode string

const (
	anthropicThinkingManual   anthropicThinkingMode = "manual"
	anthropicThinkingAdaptive anthropicThinkingMode = "adaptive"
)

func anthropicThinkingModeForModel(model string) anthropicThinkingMode {
	value := strings.ToLower(strings.TrimSpace(model))
	value = strings.TrimPrefix(value, "anthropic/")
	value = strings.ReplaceAll(value, ".", "-")
	switch {
	case strings.HasPrefix(value, "claude-fable-5"),
		strings.HasPrefix(value, "claude-mythos-5"),
		strings.HasPrefix(value, "claude-sonnet-4-6"),
		strings.HasPrefix(value, "claude-opus-4-5"),
		strings.HasPrefix(value, "claude-opus-4-6"),
		strings.HasPrefix(value, "claude-opus-4-7"),
		strings.HasPrefix(value, "claude-opus-4-8"):
		return anthropicThinkingAdaptive
	case strings.HasPrefix(value, "claude-opus-"):
		if strings.HasPrefix(value, "claude-opus-4-1") {
			return anthropicThinkingManual
		}
		return anthropicThinkingAdaptive
	default:
		return anthropicThinkingManual
	}
}

func anthropicAdaptiveEffort(effort agent.ReasoningEffort) string {
	switch effort {
	case agent.ReasoningEffortLow,
		agent.ReasoningEffortMedium,
		agent.ReasoningEffortHigh:
		return string(effort)
	case agent.ReasoningEffortXHigh:
		return string(agent.ReasoningEffortHigh)
	default:
		return string(agent.ReasoningEffortMedium)
	}
}

func anthropicManualThinkingForOptions(effort agent.ReasoningEffort, maxTokens int) *anthropicThinking {
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
