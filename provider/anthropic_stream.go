//go:build linux

package provider

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal"
)

type anthropicStreamEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Message      anthropicStreamMessage  `json:"message,omitempty"`
	Usage        anthropicUsage          `json:"usage,omitempty"`
	ContentBlock anthropicContent        `json:"content_block,omitempty"`
	Delta        anthropicStreamDelta    `json:"delta,omitempty"`
	Error        *anthropicStreamFailure `json:"error,omitempty"`
}

type anthropicStreamMessage struct {
	Usage anthropicUsage `json:"usage,omitempty"`
}

type anthropicStreamDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicStreamFailure struct {
	Type    string `json:"type,omitempty"`
	Message string `json:"message,omitempty"`
}

type anthropicStreamBlock struct {
	kind       string
	text       strings.Builder
	toolID     string
	toolName   string
	toolInput  strings.Builder
	initialRaw json.RawMessage
}

type anthropicStreamParser struct {
	cb          agent.StreamCallback
	text        strings.Builder
	toolCalls   []agent.ToolCall
	usage       core.TokenUsage
	blocks      map[int]*anthropicStreamBlock
	order       []int
	callbackErr error
	parseErr    error
}

func newAnthropicStreamParser(cb agent.StreamCallback) *anthropicStreamParser {
	return &anthropicStreamParser{
		cb:     cb,
		blocks: make(map[int]*anthropicStreamBlock),
	}
}

func (p *anthropicStreamParser) consume(event internal.Event) error {
	if p.parseErr != nil {
		return p.parseErr
	}
	if strings.TrimSpace(event.Data) == "" {
		return nil
	}

	var payload anthropicStreamEvent
	if err := json.Unmarshal([]byte(event.Data), &payload); err != nil {
		p.parseErr = fmt.Errorf("anthropic: decode stream event: %w", err)
		return p.parseErr
	}
	eventType := strings.TrimSpace(payload.Type)
	if eventType == "" {
		eventType = strings.TrimSpace(event.Type)
	}

	switch eventType {
	case "ping", "":
		return nil
	case "error":
		message := "anthropic stream error"
		if payload.Error != nil && strings.TrimSpace(payload.Error.Message) != "" {
			message = payload.Error.Message
		}
		p.parseErr = fmt.Errorf("anthropic: %s", message)
		return p.parseErr
	case "message_start":
		p.captureUsage(payload.Message.Usage)
	case "message_delta":
		p.captureUsage(payload.Usage)
	case "content_block_start":
		block := p.ensureBlock(payload.Index)
		block.kind = payload.ContentBlock.Type
		switch payload.ContentBlock.Type {
		case "text":
			if payload.ContentBlock.Text != "" {
				block.text.WriteString(payload.ContentBlock.Text)
				p.text.WriteString(payload.ContentBlock.Text)
				p.emitText(payload.ContentBlock.Text)
			}
		case "tool_use", "tool_call":
			block.toolID = payload.ContentBlock.ID
			block.toolName = payload.ContentBlock.Name
			if len(payload.ContentBlock.Input) > 0 {
				block.initialRaw = payload.ContentBlock.Input
			}
		}
	case "content_block_delta":
		block := p.ensureBlock(payload.Index)
		switch payload.Delta.Type {
		case "text_delta":
			if payload.Delta.Text == "" {
				return nil
			}
			block.text.WriteString(payload.Delta.Text)
			p.text.WriteString(payload.Delta.Text)
			p.emitText(payload.Delta.Text)
		case "input_json_delta":
			block.toolInput.WriteString(payload.Delta.PartialJSON)
		}
	case "content_block_stop":
		block := p.blocks[payload.Index]
		if block == nil {
			return nil
		}
		if block.kind == "tool_use" || block.kind == "tool_call" {
			p.toolCalls = append(p.toolCalls, agent.ToolCall{
				ID:    block.toolID,
				Name:  block.toolName,
				Input: finalizeToolInput(block.initialRaw, block.toolInput.String()),
			})
		}
	case "message_stop":
		return nil
	}
	return nil
}

func (p *anthropicStreamParser) response() *agent.Response {
	content := p.text.String()
	if content == "" && len(p.order) > 0 {
		sort.Ints(p.order)
		var joined strings.Builder
		for _, idx := range p.order {
			block := p.blocks[idx]
			if block == nil || block.kind != "text" {
				continue
			}
			joined.WriteString(block.text.String())
		}
		content = joined.String()
	}
	usage := p.usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return &agent.Response{
		Content:   content,
		ToolCalls: append([]agent.ToolCall(nil), p.toolCalls...),
		Usage:     usage,
	}
}

func (p *anthropicStreamParser) err() error {
	if p.callbackErr != nil {
		return p.callbackErr
	}
	return p.parseErr
}

func (p *anthropicStreamParser) ensureBlock(index int) *anthropicStreamBlock {
	if block, ok := p.blocks[index]; ok {
		return block
	}
	block := &anthropicStreamBlock{}
	p.blocks[index] = block
	p.order = append(p.order, index)
	return block
}

func (p *anthropicStreamParser) captureUsage(usage anthropicUsage) {
	if usage.InputTokens != 0 {
		p.usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		p.usage.OutputTokens = usage.OutputTokens
	}
	if usage.TotalTokens != 0 {
		p.usage.TotalTokens = usage.TotalTokens
	}
	if usage.CacheReadInputTokens != 0 {
		p.usage.CacheReadTokens = usage.CacheReadInputTokens
	}
	if usage.CacheCreationInputTokens != 0 {
		p.usage.CacheWriteTokens = usage.CacheCreationInputTokens
	}
}

func (p *anthropicStreamParser) emitText(text string) {
	if p.cb == nil || text == "" || p.callbackErr != nil {
		return
	}
	if err := p.cb(agent.StreamChunk{Type: "text", Text: text}); err != nil {
		p.callbackErr = err
	}
}
