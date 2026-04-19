//go:build linux

package core

import (
	"strconv"
	"strings"
)

type DeliberationControlAction string

const (
	DeliberationControlActionStop   DeliberationControlAction = "stop"
	DeliberationControlActionDetach DeliberationControlAction = "detach"

	deliberationControlCallbackPrefix = "deliberation:"
)

func EncodeDeliberationControlCallbackData(runID int64, action DeliberationControlAction) string {
	if runID <= 0 {
		return ""
	}
	action = normalizeDeliberationControlAction(string(action))
	if action == "" {
		return ""
	}
	return deliberationControlCallbackPrefix + strconv.FormatInt(runID, 10) + ":" + string(action)
}

func DecodeDeliberationControlCallbackData(data string) (runID int64, action DeliberationControlAction, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, deliberationControlCallbackPrefix) {
		return 0, "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, deliberationControlCallbackPrefix))
	if payload == "" {
		return 0, "", false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	parsedRunID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || parsedRunID <= 0 {
		return 0, "", false
	}
	normalizedAction := normalizeDeliberationControlAction(parts[1])
	if normalizedAction == "" {
		return 0, "", false
	}
	return parsedRunID, normalizedAction, true
}

func normalizeDeliberationControlAction(raw string) DeliberationControlAction {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(DeliberationControlActionStop):
		return DeliberationControlActionStop
	case string(DeliberationControlActionDetach):
		return DeliberationControlActionDetach
	default:
		return ""
	}
}
