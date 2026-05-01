//go:build linux

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const ContinuationCallbackPrefix = "continuation:"
const TelegramCallbackDataMaxBytes = 64

// ContinuationCallbackAlias returns a compact deterministic identifier for long
// continuation/proposal ids. The full id remains authoritative in persisted
// state; this value is only for Telegram callback_data, which is limited to 64
// bytes.
func ContinuationCallbackAlias(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return "c_" + hex.EncodeToString(sum[:8])
}

func EncodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	if decisionID == "" {
		data := ContinuationCallbackPrefix + action
		if len(data) <= TelegramCallbackDataMaxBytes {
			return data
		}
		return ""
	}
	data := ContinuationCallbackPrefix + decisionID + ":" + action
	if len(data) <= TelegramCallbackDataMaxBytes {
		return data
	}
	alias := ContinuationCallbackAlias(decisionID)
	if alias == "" {
		return ""
	}
	data = ContinuationCallbackPrefix + alias + ":" + action
	if len(data) <= TelegramCallbackDataMaxBytes {
		return data
	}
	return ""
}

func DecodeContinuationCallbackData(data string) (decisionID string, action string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, ContinuationCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, ContinuationCallbackPrefix))
	if payload == "" {
		return "", "", false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) == 1 {
		action = strings.TrimSpace(parts[0])
		if action == "" {
			return "", "", false
		}
		return "", action, true
	}
	decisionID = strings.TrimSpace(parts[0])
	action = strings.TrimSpace(parts[1])
	if decisionID == "" || action == "" {
		return "", "", false
	}
	return decisionID, action, true
}
