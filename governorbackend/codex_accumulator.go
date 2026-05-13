//go:build linux

package governorbackend

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
)

type codexResponseAccumulator struct {
	content      strings.Builder
	thinking     strings.Builder
	thinkingMeta []agent.ThinkingBlock
	toolCalls    []agent.ToolCall
	toolCallSet  map[string]struct{}
	media        []core.Media
	mediaSet     map[string]struct{}
	reasoningRaw []json.RawMessage
	reasoningSet map[string]struct{}
	usage        core.TokenUsage
	responseID   string
}

func newCodexResponseAccumulator() *codexResponseAccumulator {
	return &codexResponseAccumulator{
		toolCallSet:  map[string]struct{}{},
		mediaSet:     map[string]struct{}{},
		reasoningSet: map[string]struct{}{},
	}
}

func (a *codexResponseAccumulator) merge(resp *agent.Response, responseID string) {
	if a == nil || resp == nil {
		return
	}
	if resp.Content != "" {
		a.content.WriteString(resp.Content)
	}
	if resp.Thinking != "" {
		a.thinking.WriteString(resp.Thinking)
	}
	a.thinkingMeta = append(a.thinkingMeta, resp.ThinkingMeta...)
	for _, call := range resp.ToolCalls {
		key := strings.Join([]string{strings.TrimSpace(call.ID), strings.TrimSpace(call.Name), string(bytes.TrimSpace(call.Input))}, "\x00")
		if _, ok := a.toolCallSet[key]; ok {
			continue
		}
		a.toolCallSet[key] = struct{}{}
		a.toolCalls = append(a.toolCalls, call)
	}
	for _, media := range resp.Media {
		key := strings.Join([]string{strings.TrimSpace(media.Filename), strings.TrimSpace(media.MimeType), string(media.Data)}, "\x00")
		if _, ok := a.mediaSet[key]; ok {
			continue
		}
		a.mediaSet[key] = struct{}{}
		a.media = append(a.media, media)
	}
	if state, ok := decodeCodexProviderState(resp.ProviderState); ok {
		for _, raw := range state.ReasoningItems {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) == 0 {
				continue
			}
			key := string(trimmed)
			if _, ok := a.reasoningSet[key]; ok {
				continue
			}
			a.reasoningSet[key] = struct{}{}
			a.reasoningRaw = append(a.reasoningRaw, append(json.RawMessage(nil), trimmed...))
		}
	}
	if resp.Usage.TotalTokens != 0 || resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		a.usage = resp.Usage
	}
	if strings.TrimSpace(responseID) != "" {
		a.responseID = strings.TrimSpace(responseID)
	}
}

func (a *codexResponseAccumulator) hasPartial() bool {
	if a == nil {
		return false
	}
	return strings.TrimSpace(a.content.String()) != "" ||
		strings.TrimSpace(a.thinking.String()) != "" ||
		len(a.toolCalls) > 0 ||
		len(a.media) > 0 ||
		strings.TrimSpace(a.responseID) != ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
