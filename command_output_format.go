//go:build linux

package main

import (
	"fmt"
	"strings"
)

const (
	commandOutputHuman = "human"
	commandOutputKV    = "kv"
	commandOutputJSON  = "json"
)

func normalizeCommandOutputFormat(raw string, jsonAlias bool) (string, error) {
	if jsonAlias {
		return commandOutputJSON, nil
	}
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return commandOutputHuman, nil
	}
	switch format {
	case commandOutputHuman, commandOutputKV, commandOutputJSON:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q; use human, kv, or json", raw)
	}
}
