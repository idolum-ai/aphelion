//go:build linux

package agent

import (
	"fmt"
	"strings"
)

const (
	providerContextRecentToolChars    = 4000
	providerContextOlderToolChars     = 800
	providerContextDigestAfterChars   = 12000
	providerContextToolDigestChars    = 1200
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
	Admission          contextAdmissionReport
}

type contextAdmissionReport struct {
	ToolEvidenceLayers    int
	ToolEvidenceCompacted int
	ToolEvidenceDigested  int
	SuppressedLayers      int
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

	compacted, admission := projectToolResultMessagesForProviderContext(messages)
	compactedTokens := estimateProviderRequestTokens(compacted, tools)
	originalToolChars := toolMessageChars(messages)
	compactedToolChars := toolMessageChars(compacted)
	preflight.Messages = compacted
	preflight.Admission = admission
	preflight.EstimatedTokens = compactedTokens
	preflight.Compacted = compactedToolChars < originalToolChars
	preflight.OriginalTokens = estimated
	preflight.CompactedTokens = compactedTokens
	preflight.OriginalToolChars = originalToolChars
	preflight.CompactedToolChars = compactedToolChars
	if compactedTokens > preflight.HardTokens {
		preflight.Admission.SuppressedLayers++
		return compacted, preflight, ContextBudgetError{
			EstimatedTokens: compactedTokens,
			HardTokens:      preflight.HardTokens,
			ContextWindow:   budget.ContextWindow,
		}
	}
	return compacted, preflight, nil
}

func CompactToolResultMessagesForProviderContext(messages []Message) []Message {
	out, _ := projectToolResultMessagesForProviderContext(messages)
	return out
}

func projectToolResultMessagesForProviderContext(messages []Message) ([]Message, contextAdmissionReport) {
	out := append([]Message(nil), messages...)
	totalToolChars := 0
	var admission contextAdmissionReport
	for i := len(out) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(out[i].Role), "tool") {
			continue
		}
		admission.ToolEvidenceLayers++
		limit := providerContextRecentToolChars
		if totalToolChars >= providerContextTotalToolChars {
			limit = providerContextOlderToolChars
		}
		original := strings.TrimSpace(out[i].Content)
		if shouldDigestToolResultForProviderContext(out[i], totalToolChars) {
			out[i].Content = RenderToolResultDigestForProviderContext(out[i], providerContextToolDigestChars)
			admission.ToolEvidenceDigested++
		} else {
			out[i].Content = CompactProviderContextText(original, limit)
			if strings.TrimSpace(out[i].Content) != original {
				admission.ToolEvidenceCompacted++
			}
		}
		totalToolChars += len(strings.TrimSpace(out[i].Content))
	}
	return out, admission
}

func shouldDigestToolResultForProviderContext(msg Message, projectedToolChars int) bool {
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return false
	}
	if len(text) > providerContextOversizedToolChars {
		return true
	}
	return projectedToolChars >= providerContextDigestAfterChars && len(text) > providerContextOlderToolChars
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

func RenderToolResultDigestForProviderContext(msg Message, maxChars int) string {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ""
	}
	lines := []string{
		"[tool_result_digest]",
		"projection_kind: tool_result_digest",
		"source: provider_context_projection",
		"raw_evidence: retained_in_session_history",
	}
	if id := strings.TrimSpace(msg.ToolCallID); id != "" {
		lines = append(lines, "tool_call_id: "+id)
	}
	if name := strings.TrimSpace(msg.ToolName); name != "" {
		lines = append(lines, "tool_name: "+name)
	}
	lines = append(lines,
		fmt.Sprintf("original_chars: %d", len(content)),
		"key_facts:",
	)
	for _, fact := range toolResultDigestFacts(content, 4) {
		lines = append(lines, "- "+fact)
	}
	lines = append(lines, "[/tool_result_digest]")
	return clampProviderProjectionText(strings.Join(lines, "\n"), maxChars)
}

func toolResultDigestFacts(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	seen := map[string]struct{}{}
	lines := make([]string, 0, limit)
	add := func(raw string) {
		if len(lines) >= limit {
			return
		}
		line := compactDigestLine(raw, 220)
		if line == "" {
			return
		}
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		lines = append(lines, line)
	}
	parts := strings.Split(text, "\n")
	for _, part := range parts {
		add(part)
	}
	for i := len(parts) - 1; i >= 0 && len(lines) < limit; i-- {
		add(parts[i])
	}
	if len(lines) == 0 {
		add(text)
	}
	return lines
}

func compactDigestLine(text string, maxChars int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	return clampProviderProjectionText(text, maxChars)
}

func clampProviderProjectionText(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return text[:maxChars]
	}
	return strings.TrimSpace(text[:maxChars-3]) + "..."
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
