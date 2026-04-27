//go:build linux

package core

import "strings"

const streamControlCallbackPrefix = "stream:"

type StreamControlAction string

const StreamControlActionStop StreamControlAction = "stop"

func EncodeStreamControlCallbackData(streamID string, action StreamControlAction) string {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return ""
	}
	action = normalizeStreamControlAction(string(action))
	if action == "" {
		return ""
	}
	return streamControlCallbackPrefix + streamID + ":" + string(action)
}

func DecodeStreamControlCallbackData(data string) (streamID string, action StreamControlAction, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, streamControlCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, streamControlCallbackPrefix))
	if payload == "" {
		return "", "", false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	streamID = strings.TrimSpace(parts[0])
	action = normalizeStreamControlAction(parts[1])
	if streamID == "" || action == "" {
		return "", "", false
	}
	return streamID, action, true
}

func normalizeStreamControlAction(raw string) StreamControlAction {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(StreamControlActionStop):
		return StreamControlActionStop
	default:
		return ""
	}
}
