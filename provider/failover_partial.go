//go:build linux

package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
)

type partialProviderError interface {
	PartialProviderResponse() *agent.Response
	PartialProviderResponseID() string
	PartialProviderReason() string
}

func appendPartialProviderRecoveryMessage(messages []agent.Message, provider string, err error) []agent.Message {
	partial, responseID, reason, ok := partialProviderSnapshot(err)
	if !ok {
		return messages
	}
	note := renderPartialProviderRecoveryNote(provider, responseID, reason, partial)
	if strings.TrimSpace(note) == "" {
		return messages
	}
	out := append([]agent.Message(nil), messages...)
	out = append(out, agent.Message{
		Role:    "user",
		Content: note,
	})
	return out
}

func partialProviderSnapshot(err error) (*agent.Response, string, string, bool) {
	if err == nil {
		return nil, "", "", false
	}
	var partialErr partialProviderError
	if !errors.As(err, &partialErr) {
		return nil, "", "", false
	}
	partial := partialErr.PartialProviderResponse()
	responseID := strings.TrimSpace(partialErr.PartialProviderResponseID())
	reason := strings.TrimSpace(partialErr.PartialProviderReason())
	if partial == nil && responseID == "" && reason == "" {
		return nil, "", "", false
	}
	return partial, responseID, reason, true
}

func renderPartialProviderRecoveryNote(provider string, responseID string, reason string, partial *agent.Response) string {
	var b strings.Builder
	b.WriteString("Provider recovery note: ")
	b.WriteString(firstNonEmpty(strings.TrimSpace(provider), "primary provider"))
	b.WriteString(" produced an incomplete response before failing. Treat this as partial, non-authoritative evidence while completing the user's request.")
	if reason = strings.TrimSpace(reason); reason != "" {
		b.WriteString("\nreason: ")
		b.WriteString(reason)
	}
	if responseID = strings.TrimSpace(responseID); responseID != "" {
		b.WriteString("\nresponse_id: ")
		b.WriteString(responseID)
	}
	if partial == nil {
		return b.String()
	}
	if text := strings.TrimSpace(partial.Content); text != "" {
		b.WriteString("\npartial_text:\n")
		b.WriteString(compactProviderFallbackText(text, providerFallbackRecentToolChars))
	}
	if len(partial.ToolCalls) > 0 {
		b.WriteString("\npartial_tool_calls:")
		for _, call := range partial.ToolCalls {
			b.WriteString("\n- ")
			b.WriteString(firstNonEmpty(strings.TrimSpace(call.Name), "tool"))
			if id := strings.TrimSpace(call.ID); id != "" {
				b.WriteString(" id=")
				b.WriteString(id)
			}
			if input := strings.TrimSpace(string(call.Input)); input != "" {
				b.WriteString(" input=")
				b.WriteString(compactProviderFallbackText(input, providerFallbackOlderToolChars))
			}
		}
	}
	return b.String()
}

const (
	providerFallbackRecentToolChars = 4000
	providerFallbackOlderToolChars  = 800
	providerFallbackTotalToolChars  = 60000
)

func compactToolResultMessagesForProviderFallback(messages []agent.Message) []agent.Message {
	out := append([]agent.Message(nil), messages...)
	totalToolChars := 0
	for i := len(out) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(out[i].Role), "tool") {
			continue
		}
		limit := providerFallbackRecentToolChars
		if totalToolChars >= providerFallbackTotalToolChars {
			limit = providerFallbackOlderToolChars
		}
		out[i].Content = compactProviderFallbackText(out[i].Content, limit)
		totalToolChars += len(out[i].Content)
	}
	return out
}

func compactProviderFallbackText(text string, limit int) string {
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
