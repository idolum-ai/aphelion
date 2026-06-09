//go:build linux

package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func decodeToolObjectInput(input json.RawMessage, out any, toolName string) error {
	normalized, err := normalizeToolObjectInput(input)
	if err != nil {
		return fmt.Errorf("decode %s input: %w", toolName, err)
	}
	if err := json.Unmarshal(normalized, out); err != nil {
		return fmt.Errorf("decode %s input: %w", toolName, err)
	}
	return nil
}

func normalizeToolObjectInput(input json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("input is not valid JSON")
	}
	var wrapped string
	if err := json.Unmarshal(trimmed, &wrapped); err == nil {
		trimmed = bytes.TrimSpace([]byte(strings.TrimSpace(wrapped)))
		if len(trimmed) == 0 {
			return nil, fmt.Errorf("input string must contain a JSON object")
		}
		if !json.Valid(trimmed) {
			return nil, fmt.Errorf("input string does not contain valid JSON")
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("input must be a JSON object")
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return nil, err
	}
	compact, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(compact), nil
}
