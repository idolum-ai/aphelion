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
	return out, nil
}

// NewMessagesForTurn converts user input + generated assistant/tool messages into persisted rows.
func NewMessagesForTurn(userText string, generated []agent.Message, turnIndex int) ([]Message, error) {
	out := []Message{{
		Role:         "user",
		Content:      userText,
		ContentChars: len(userText),
		TurnIndex:    turnIndex,
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
