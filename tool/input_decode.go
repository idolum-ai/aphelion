//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeToolInput(input json.RawMessage) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" || trimmed[0] != '"' {
		return input, nil
	}
	var wrapped string
	if err := json.Unmarshal(input, &wrapped); err != nil {
		return input, nil
	}
	wrapped = strings.TrimSpace(wrapped)
	if wrapped == "" {
		return input, nil
	}
	switch wrapped[0] {
	case '{', '[':
	default:
		return input, nil
	}
	if !json.Valid([]byte(wrapped)) {
		return nil, fmt.Errorf("invalid tool arguments: JSON-string-wrapped structured value is invalid JSON")
	}
	return json.RawMessage(wrapped), nil
}
