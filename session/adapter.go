//go:build linux

package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
)

// ToAgentHistory converts persisted session messages into agent turn history.
func ToAgentHistory(messages []Message) ([]agent.Message, error) {
	out := make([]agent.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Compacted {
			continue
		}

		entry := agent.Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolID,
			ToolName:   msg.ToolName,
		}
		if strings.TrimSpace(msg.ToolCalls) != "" {
			if err := json.Unmarshal([]byte(msg.ToolCalls), &entry.ToolCalls); err != nil {
				return nil, fmt.Errorf("decode tool calls for message %d: %w", msg.ID, err)
			}
		}
		out = append(out, entry)
	}
	return repairAgentHistory(out), nil
}

// NewMessagesForTurn converts user input + generated assistant/tool messages into persisted rows.
func NewMessagesForTurn(userText string, generated []agent.Message, turnIndex int) ([]Message, error) {
	return NewMessagesForTurnWithContext(userText, generated, turnIndex, TurnMessageContext{})
}

// NewMessagesForTurnWithContext converts user input + generated assistant/tool messages into persisted rows with explicit turn provenance.
func NewMessagesForTurnWithContext(userText string, generated []agent.Message, turnIndex int, ctx TurnMessageContext) ([]Message, error) {
	out := []Message{{
		Role:              "user",
		Content:           userText,
		ContentChars:      len(userText),
		TurnIndex:         turnIndex,
		ActorUserID:       ctx.ActorUserID,
		ActorRole:         strings.TrimSpace(ctx.ActorRole),
		EventOrigin:       strings.TrimSpace(ctx.EventOrigin),
		EventOriginDetail: strings.TrimSpace(ctx.EventOriginDetail),
	}}
	toolNames := make(map[string]string)

	for _, msg := range generated {
		entry := Message{
			Role:         msg.Role,
			Content:      msg.Content,
			Thinking:     strings.TrimSpace(msg.Thinking),
			ContentChars: len(msg.Content),
			TurnIndex:    turnIndex,
			ToolID:       msg.ToolCallID,
			ToolName:     strings.TrimSpace(msg.ToolName),
		}

		if len(msg.ToolCalls) > 0 {
			entry.Content = ""
			entry.ContentChars = 0
			raw, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return nil, fmt.Errorf("encode tool calls: %w", err)
			}
			entry.ToolCalls = string(raw)
			for _, call := range msg.ToolCalls {
				if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
					continue
				}
				toolNames[strings.TrimSpace(call.ID)] = strings.TrimSpace(call.Name)
			}
		}

		if msg.Role == "tool" {
			if entry.ToolName == "" {
				entry.ToolName = toolNames[strings.TrimSpace(msg.ToolCallID)]
			}
			if entry.ToolName == "" {
				entry.ToolName = toolNameFromContent(msg.Content)
			}
		}

		out = append(out, entry)
	}

	return out, nil
}

func toolNameFromContent(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return "exec"
}

func repairAgentHistory(history []agent.Message) []agent.Message {
	repaired := make([]agent.Message, 0, len(history))
	pending := newPendingToolCalls()
	for _, msg := range history {
		switch msg.Role {
		case "assistant":
			if pending.hasPending() {
				repaired = append(repaired, pending.flushMissing()...)
			}
			msg.ToolCalls = sanitizeToolCalls(msg.ToolCalls)
			repaired = append(repaired, msg)
			pending.add(msg.ToolCalls)
		case "tool":
			toolMsg, ok := pending.match(msg)
			if !ok {
				continue
			}
			repaired = append(repaired, toolMsg)
		default:
			if pending.hasPending() {
				repaired = append(repaired, pending.flushMissing()...)
			}
			repaired = append(repaired, msg)
		}
	}
	if pending.hasPending() {
		repaired = append(repaired, pending.flushMissing()...)
	}
	return repaired
}

func sanitizeToolCalls(calls []agent.ToolCall) []agent.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]agent.ToolCall, 0, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
			continue
		}
		out = append(out, call)
	}
	return out
}

type pendingToolCalls struct {
	order []agent.ToolCall
	byID  map[string]agent.ToolCall
}

func newPendingToolCalls() *pendingToolCalls {
	return &pendingToolCalls{byID: make(map[string]agent.ToolCall)}
}

func (p *pendingToolCalls) hasPending() bool {
	return len(p.order) > 0
}

func (p *pendingToolCalls) add(calls []agent.ToolCall) {
	for _, call := range calls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		p.order = append(p.order, call)
		p.byID[id] = call
	}
}

func (p *pendingToolCalls) match(msg agent.Message) (agent.Message, bool) {
	if len(p.order) == 0 {
		return agent.Message{}, false
	}

	repaired := msg
	repaired.ToolCallID = strings.TrimSpace(repaired.ToolCallID)
	repaired.ToolName = strings.TrimSpace(repaired.ToolName)

	if repaired.ToolCallID == "" {
		switch len(p.order) {
		case 1:
			repaired.ToolCallID = p.order[0].ID
		default:
			if repaired.ToolName != "" {
				for _, call := range p.order {
					if strings.TrimSpace(call.Name) == repaired.ToolName {
						repaired.ToolCallID = call.ID
						break
					}
				}
			}
		}
	}

	call, ok := p.byID[repaired.ToolCallID]
	if !ok {
		return agent.Message{}, false
	}
	repaired.ToolName = call.Name
	p.remove(call.ID)
	return repaired, true
}

func (p *pendingToolCalls) flushMissing() []agent.Message {
	if len(p.order) == 0 {
		return nil
	}
	out := make([]agent.Message, 0, len(p.order))
	for _, call := range p.order {
		out = append(out, agent.Message{
			Role:       "tool",
			Content:    "tool_error: missing tool result in persisted transcript",
			ToolCallID: call.ID,
			ToolName:   call.Name,
		})
	}
	p.order = nil
	p.byID = make(map[string]agent.ToolCall)
	return out
}

func (p *pendingToolCalls) remove(id string) {
	delete(p.byID, id)
	for idx, call := range p.order {
		if call.ID != id {
			continue
		}
		p.order = append(p.order[:idx], p.order[idx+1:]...)
		return
	}
}
