//go:build linux

package governorbackend

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

func consumeCodexStream(body io.Reader, cb agent.StreamCallback) (*codexCompletionResult, error) {
	parser := &codexStreamParser{}
	for event := range internal.ParseSSE(body) {
		if strings.EqualFold(strings.TrimSpace(event.Data), "[DONE]") {
			break
		}
		if err := parser.consume(event, cb); err != nil {
			return nil, err
		}
	}
	return parser.response()
}

type codexUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64 `json:"cached_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
}

type codexStreamEnvelope struct {
	Type         string          `json:"type"`
	Delta        string          `json:"delta"`
	Item         json.RawMessage `json:"item"`
	Response     json.RawMessage `json:"response"`
	SummaryIndex *int            `json:"summary_index"`
	ContentIndex *int            `json:"content_index"`
}

type codexCompletedResponse struct {
	ID                string      `json:"id"`
	Status            string      `json:"status"`
	Usage             *codexUsage `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type codexOutputItem struct {
	Type          string          `json:"type"`
	ID            string          `json:"id"`
	CallID        string          `json:"call_id"`
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments"`
	Status        string          `json:"status"`
	RevisedPrompt string          `json:"revised_prompt"`
	Result        string          `json:"result"`
	Summary       []struct {
		Text string `json:"text"`
	} `json:"summary"`
}

type codexProviderState struct {
	Backend        string            `json:"backend"`
	ResponseID     string            `json:"response_id"`
	ReasoningItems []json.RawMessage `json:"reasoning_items,omitempty"`
}

type codexFailedResponse struct {
	Error *codexResponseError `json:"error"`
}

type codexResponseError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type codexStreamParser struct {
	text         strings.Builder
	thinking     strings.Builder
	thinkingMeta []agent.ThinkingBlock
	toolCalls    []agent.ToolCall
	media        []core.Media
	reasoningRaw []json.RawMessage
	usage        core.TokenUsage
	responseID   string
	status       codexResponseStatus
}

func (p *codexStreamParser) consume(event internal.Event, cb agent.StreamCallback) error {
	var env codexStreamEnvelope
	if err := json.Unmarshal([]byte(event.Data), &env); err != nil {
		return fmt.Errorf("codex: decode stream event: %w", err)
	}

	kind := strings.TrimSpace(env.Type)
	if kind == "" {
		kind = strings.TrimSpace(event.Type)
	}
	switch kind {
	case "response.created":
		p.captureResponseEnvelope(env.Response)
		return nil
	case "response.output_text.delta":
		p.text.WriteString(env.Delta)
		if cb != nil && strings.TrimSpace(env.Delta) != "" {
			return cb(agent.StreamChunk{Type: "text", Text: env.Delta})
		}
		return nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		p.thinking.WriteString(env.Delta)
		return nil
	case "response.output_item.done":
		if len(env.Item) == 0 {
			return nil
		}
		var item codexOutputItem
		if err := json.Unmarshal(env.Item, &item); err != nil {
			return fmt.Errorf("codex: decode stream item: %w", err)
		}
		switch item.Type {
		case "function_call":
			call := agent.ToolCall{
				ID:    strings.TrimSpace(item.CallID),
				Name:  strings.TrimSpace(item.Name),
				Input: json.RawMessage(normalizeArguments(json.RawMessage(item.Arguments))),
			}
			p.toolCalls = append(p.toolCalls, call)
			if cb != nil {
				return cb(agent.StreamChunk{Type: "tool_call", ToolCall: &call})
			}
		case "reasoning":
			if raw := bytes.TrimSpace(env.Item); len(raw) > 0 {
				p.reasoningRaw = append(p.reasoningRaw, append(json.RawMessage(nil), raw...))
			}
			for _, summary := range item.Summary {
				if text := strings.TrimSpace(summary.Text); text != "" {
					if p.thinking.Len() > 0 {
						p.thinking.WriteString("\n")
					}
					p.thinking.WriteString(text)
					p.thinkingMeta = append(p.thinkingMeta, agent.ThinkingBlock{
						Type:    "summary_text",
						Content: text,
					})
				}
			}
		case "image_generation_call":
			if media, ok := codexImageGenerationCallMedia(item.ID, item.Result); ok {
				p.media = append(p.media, media)
			}
		case "message":
			// Text is normally streamed via output_text.delta; ignore here to avoid duplication.
		}
		return nil
	case "response.completed":
		p.status = codexResponseStatusCompleted
		if err := p.captureUsage(env.Response, cb); err != nil {
			return err
		}
		return nil
	case "response.incomplete":
		p.status = codexResponseStatusIncomplete
		if err := p.captureUsage(env.Response, cb); err != nil {
			return err
		}
		return nil
	case "response.failed":
		var failed codexFailedResponse
		if len(env.Response) > 0 && json.Unmarshal(env.Response, &failed) == nil {
			if failed.Error != nil {
				return newCodexFailedError(failed.Error.Type, failed.Error.Code, failed.Error.Message)
			}
		}
		return newCodexFailedError("", "", "response.failed event received")
	default:
		return nil
	}
}

func codexImageGenerationCallMedia(id string, result string) (core.Media, bool) {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return core.Media{}, false
	}
	bytes, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil || len(bytes) == 0 {
		return core.Media{}, false
	}
	mimeType := http.DetectContentType(bytes)
	ext := codexImageExtensionForMimeType(mimeType)
	return core.Media{
		Type:     "image",
		Data:     bytes,
		MimeType: mimeType,
		Filename: "image-generation-call-" + sanitizeCodexImageGenerationID(id) + ext,
	}, true
}

func codexImageExtensionForMimeType(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

func sanitizeCodexImageGenerationID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "generated"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "generated"
	}
	return b.String()
}

func (p *codexStreamParser) response() (*codexCompletionResult, error) {
	resp := &agent.Response{
		Content:      p.text.String(),
		Thinking:     p.thinking.String(),
		ThinkingMeta: append([]agent.ThinkingBlock(nil), p.thinkingMeta...),
		ToolCalls:    append([]agent.ToolCall(nil), p.toolCalls...),
		Media:        append([]core.Media(nil), p.media...),
		Usage:        p.usage,
	}
	if strings.TrimSpace(p.responseID) != "" {
		resp.ProviderState = marshalCodexProviderState(p.responseID, p.reasoningRaw)
	}

	switch p.status {
	case codexResponseStatusCompleted:
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: true}, nil
	case codexResponseStatusIncomplete:
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false, IncompleteReason: codexIncompleteReasonStatusClosed}, nil
	}
	if strings.TrimSpace(p.responseID) != "" {
		return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false, IncompleteReason: codexIncompleteReasonStreamClosed}, nil
	}
	if resp.Content == "" && resp.Thinking == "" && len(resp.ToolCalls) == 0 {
		return nil, fmt.Errorf("codex: stream closed before response.completed")
	}
	return &codexCompletionResult{Response: resp, ResponseID: p.responseID, Complete: false, IncompleteReason: codexIncompleteReasonPartialStreamClosed}, nil
}

func (p *codexStreamParser) captureResponseEnvelope(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var envelope codexCompletedResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return
	}
	if strings.TrimSpace(envelope.ID) != "" {
		p.responseID = strings.TrimSpace(envelope.ID)
	}
}

func (p *codexStreamParser) captureUsage(raw json.RawMessage, cb agent.StreamCallback) error {
	if len(raw) == 0 {
		return nil
	}
	var envelope codexCompletedResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	if strings.TrimSpace(envelope.ID) != "" {
		p.responseID = strings.TrimSpace(envelope.ID)
	}
	if envelope.Usage == nil {
		return nil
	}
	p.usage = core.TokenUsage{
		InputTokens:      envelope.Usage.InputTokens,
		OutputTokens:     envelope.Usage.OutputTokens,
		TotalTokens:      envelope.Usage.TotalTokens,
		CacheReadTokens:  envelope.Usage.InputTokensDetails.CachedTokens,
		CacheWriteTokens: envelope.Usage.InputTokensDetails.CacheWriteTokens,
	}
	if p.usage.TotalTokens == 0 {
		p.usage.TotalTokens = p.usage.InputTokens + p.usage.OutputTokens
	}
	if cb != nil {
		usage := p.usage
		return cb(agent.StreamChunk{Type: "usage", Usage: &usage})
	}
	return nil
}

func (a *codexResponseAccumulator) response() *agent.Response {
	if a == nil {
		return &agent.Response{}
	}
	resp := &agent.Response{
		Content:      strings.TrimSpace(a.content.String()),
		Thinking:     strings.TrimSpace(a.thinking.String()),
		ThinkingMeta: append([]agent.ThinkingBlock(nil), a.thinkingMeta...),
		ToolCalls:    append([]agent.ToolCall(nil), a.toolCalls...),
		Media:        append([]core.Media(nil), a.media...),
		Usage:        a.usage,
	}
	if a.responseID != "" {
		resp.ProviderState = marshalCodexProviderState(a.responseID, a.reasoningRaw)
	}
	return resp
}
