//go:build linux

package provider

import (
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

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
		InputTokens:         res.Usage.InputTokens,
		OutputTokens:        res.Usage.OutputTokens,
		TotalTokens:         res.Usage.TotalTokens,
		CacheReadTokens:     res.Usage.CacheReadInputTokens,
		CacheWriteTokens:    res.Usage.CacheCreationInputTokens,
		CacheCreationTokens: res.Usage.CacheCreationInputTokens,
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
		FinishReason: strings.TrimSpace(res.StopReason),
	}
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason,omitempty"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	TotalTokens              int64 `json:"total_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
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
