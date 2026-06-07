//go:build linux

package tool

import (
	"encoding/json"
	"strings"
)

func normalizeToolInput(input json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed[0] != '"' {
		return input
	}
	var wrapped string
	if err := json.Unmarshal(input, &wrapped); err != nil {
		return input
	}
	wrapped = strings.TrimSpace(wrapped)
	if wrapped == "" {
		return input
	}
	switch wrapped[0] {
	case '{', '[':
	default:
		return input
	}
	if !json.Valid([]byte(wrapped)) {
		return input
	}
	return json.RawMessage(wrapped)
}
