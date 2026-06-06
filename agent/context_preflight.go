//go:build linux

package agent

import (
	"fmt"
	"strings"
)

const (
	providerContextRecentToolChars    = 4000
	providerContextOlderToolChars     = 800
	providerContextTotalToolChars     = 60000
	providerContextOversizedToolChars = 16000
	defaultContextMaxRatio            = 0.90
	defaultContextHardRatio           = 1.10
)

type contextPreflight struct {
	Messages           []Message
	EstimatedTokens    int
	ContextWindow      int
	MaxTokens          int
	HardTokens         int
	Compacted          bool
	OriginalTokens     int
	CompactedTokens    int
	OriginalToolChars  int
	CompactedToolChars int
}

type ContextBudgetError struct {
	EstimatedTokens int
	HardTokens      int
	ContextWindow   int
}

func (e ContextBudgetError) Error() string {
	return fmt.Sprintf("context_budget_exceeded: estimated_input_tokens=%d hard_limit_tokens=%d context_window=%d", e.EstimatedTokens, e.HardTokens, e.ContextWindow)
}

func prepareProviderMessages(messages []Message, tools []ToolDef, opts *CompleteOptions) ([]Message, contextPreflight, error) {
	preflight := contextPreflight{Messages: messages}
	if opts == nil || opts.ContextBudget == nil || opts.ContextBudget.ContextWindow <= 0 {
		preflight.EstimatedTokens = estimateProviderRequestTokens(messages, tools)
		return messages, preflight, nil
	}

	budget := opts.ContextBudget
	preflight.ContextWindow = budget.ContextWindow
	maxRatio := budget.MaxRatio
	if maxRatio <= 0 {
		maxRatio = defaultContextMaxRatio
	}
	hardRatio := budget.HardRatio
	if hardRatio <= 0 {
		hardRatio = defaultContextHardRatio
	}
	if hardRatio < maxRatio {
		hardRatio = maxRatio
	}
	preflight.MaxTokens = int(float64(budget.ContextWindow) * maxRatio)
	preflight.HardTokens = int(float64(budget.ContextWindow) * hardRatio)
	if preflight.MaxTokens <= 0 {
		preflight.MaxTokens = budget.ContextWindow
	}
	if preflight.HardTokens <= 0 {
		preflight.HardTokens = budget.ContextWindow
	}

	estimated := estimateProviderRequestTokens(messages, tools)
	preflight.EstimatedTokens = estimated
	if estimated <= preflight.MaxTokens && !hasOversizedToolOutputForProviderContext(messages) {
		return messages, preflight, nil
	}

	compacted := CompactToolResultMessagesForProviderContext(messages)
	compactedTokens := estimateProviderRequestTokens(compacted, tools)
	originalToolChars := toolMessageChars(messages)
	compactedToolChars := toolMessageChars(compacted)
	preflight.Messages = compacted
	preflight.EstimatedTokens = compactedTokens
	preflight.Compacted = compactedToolChars < originalToolChars
	preflight.OriginalTokens = estimated
	preflight.CompactedTokens = compactedTokens
	preflight.OriginalToolChars = originalToolChars
	preflight.CompactedToolChars = compactedToolChars
	if compactedTokens > preflight.HardTokens {
		return compacted, preflight, ContextBudgetError{
			EstimatedTokens: compactedTokens,
			HardTokens:      preflight.HardTokens,
			ContextWindow:   budget.ContextWindow,
		}
	}
	return compacted, preflight, nil
}

func CompactToolResultMessagesForProviderContext(messages []Message) []Message {
	out := append([]Message(nil), messages...)
	totalToolChars := 0
	for i := len(out) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(out[i].Role), "tool") {
			continue
		}
		limit := providerContextRecentToolChars
		if totalToolChars >= providerContextTotalToolChars {
			limit = providerContextOlderToolChars
		}
		out[i].Content = CompactProviderContextText(out[i].Content, limit)
		totalToolChars += len(out[i].Content)
	}
	return out
}

func hasOversizedToolOutputForProviderContext(messages []Message) bool {
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			continue
		}
		if len(strings.TrimSpace(msg.Content)) > providerContextOversizedToolChars {
			return true
		}
	}
	return false
}

func CompactProviderContextText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	head := limit * 2 / 3
	tail := limit - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	if head+tail >= len(text) {
		return text
	}
	return strings.TrimSpace(text[:head]) +
		fmt.Sprintf("\n\n[tool output compacted for provider context: original_chars=%d omitted_chars=%d]\n\n", len(text), len(text)-head-tail) +
		strings.TrimSpace(text[len(text)-tail:])
}

func estimateProviderRequestTokens(messages []Message, tools []ToolDef) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	for _, tool := range tools {
		total += estimateTextTokens(tool.Name)
		total += estimateTextTokens(tool.Description)
		total += estimateTextTokens(string(tool.Parameters))
	}
	return total
}

func estimateMessageTokens(msg Message) int {
	total := estimateTextTokens(msg.Role)
	total += estimateTextTokens(msg.Content)
	total += estimateTextTokens(msg.Thinking)
	total += estimateTextTokens(string(msg.ProviderState))
	for _, block := range msg.ThinkingMeta {
		total += estimateTextTokens(block.Type)
		total += estimateTextTokens(block.Content)
		total += estimateTextTokens(block.Signature)
		total += estimateTextTokens(string(block.Raw))
	}
	for _, call := range msg.ToolCalls {
		total += estimateTextTokens(call.ID)
		total += estimateTextTokens(call.Name)
		total += estimateTextTokens(string(call.Input))
	}
	total += estimateTextTokens(msg.ToolCallID)
	total += estimateTextTokens(msg.ToolName)
	return total + 4
}

func estimateTextTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(trimmed)/4 + 1
}

func toolMessageChars(messages []Message) int {
	total := 0
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") {
			total += len(strings.TrimSpace(msg.Content))
		}
	}
	return total
}
