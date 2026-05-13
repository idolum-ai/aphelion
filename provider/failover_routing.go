//go:build linux

package provider

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
)

func (c *FailoverChain) nextCompleteFailoverIndex(idx int, err error, messages []agent.Message) (int, bool) {
	if c == nil || idx < 0 || idx >= len(c.entries) {
		return 0, false
	}
	if shouldFallbackAfterOpenAIFamilyCapacity(err, c.entries[idx].name) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	if shouldFallbackAfterToolResultRejection(err, c.entries[idx].name, messages) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	if shouldFallbackAfterContextWindowError(err, c.entries[idx].name, messages) {
		nextIdx := nextNonOpenAIProviderIndex(c.entries, idx)
		return nextIdx, nextIdx > idx && nextIdx < len(c.entries)
	}
	nextIdx := idx + 1
	nextName := ""
	if nextIdx < len(c.entries) {
		nextName = c.entries[nextIdx].name
	}
	return nextIdx, shouldFailoverOnError(err) || shouldFallbackToNextEntry(err, c.entries[idx].name, nextName)
}

func (c *FailoverChain) startIndexForFailover(opts agent.CompleteOptions) int {
	if c == nil || opts.ProviderFailover == nil {
		return 0
	}
	preferred := strings.TrimSpace(opts.ProviderFailover.PreferredProvider)
	if preferred == "" {
		return 0
	}
	for idx, entry := range c.entries {
		if strings.EqualFold(strings.TrimSpace(entry.name), preferred) {
			return idx
		}
	}
	return 0
}

func (c *FailoverChain) rememberFailoverPreference(opts agent.CompleteOptions, idx int, nextIdx int, err error, messages []agent.Message, startIdx int) {
	if c == nil || opts.ProviderFailover == nil || idx < 0 || idx >= len(c.entries) || nextIdx < 0 || nextIdx >= len(c.entries) {
		return
	}
	if reason := stickyFailoverReason(err, c.entries[idx].name, messages, startIdx > 0); reason != "" {
		opts.ProviderFailover.PreferredProvider = strings.TrimSpace(c.entries[nextIdx].name)
		opts.ProviderFailover.Reason = reason
	}
}

func stickyFailoverReason(err error, current string, messages []agent.Message, alreadyOnFallback bool) string {
	switch {
	case isProviderBufferLimitError(err):
		return "provider_buffer_limit"
	case shouldFallbackAfterOpenAIFamilyCapacity(err, current):
		return "openai_family_capacity"
	case shouldFallbackAfterToolResultRejection(err, current, messages):
		return "tool_result_rejection"
	case shouldFallbackAfterContextWindowError(err, current, messages):
		return "context_window_after_tools"
	case alreadyOnFallback && shouldFailoverOnError(err):
		return "fallback_provider_failed"
	default:
		return ""
	}
}

func (c *FailoverChain) recordSuccess(idx int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if idx < 0 || idx >= len(c.entries) {
		return
	}
	c.state.ActiveProvider = c.entries[idx].name
	c.state.FallbackActive = idx > 0
}

func nextNonOpenAIProviderIndex(entries []failoverEntry, idx int) int {
	for i := idx + 1; i < len(entries); i++ {
		switch providerFamilyName(entries[i].name) {
		case "codex", "openai":
			continue
		default:
			return i
		}
	}
	return len(entries)
}

func providerFamilyName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[:idx]
	}
	return name
}
