//go:build linux

package telegramcommands

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func encodeModelCallbackData(action modelCallbackAction, slot string, value string) string {
	actionToken := modelCallbackActionToken(action)
	if actionToken == "" {
		return ""
	}
	slotToken := modelSlotToken(slot)
	value = strings.TrimSpace(value)
	switch action {
	case modelCallbackStatus:
		return modelCallbackPrefix + actionToken
	case modelCallbackSlot, modelCallbackChanges, modelCallbackClear:
		return modelCallbackPrefix + actionToken + ":" + slotToken
	case modelCallbackEffort, modelCallbackPreset, modelCallbackSpeed:
		return modelCallbackPrefix + actionToken + ":" + slotToken + ":" + value
	default:
		return ""
	}
}

func decodeModelCallbackData(data string) (modelCallbackAction, string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, modelCallbackPrefix) {
		return "", "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, modelCallbackPrefix))
	if payload == "" {
		return "", "", "", false
	}
	parts := strings.Split(payload, ":")
	action, ok := decodeModelCallbackActionToken(parts[0])
	if !ok {
		return "", "", "", false
	}
	switch action {
	case modelCallbackStatus:
		return action, "", "", len(parts) == 1
	case modelCallbackSlot, modelCallbackChanges, modelCallbackClear:
		if len(parts) != 2 {
			return "", "", "", false
		}
		slot := decodeModelSlotToken(parts[1])
		if slot == "" && action != modelCallbackChanges {
			return "", "", "", false
		}
		return action, slot, "", true
	case modelCallbackEffort, modelCallbackPreset, modelCallbackSpeed:
		if len(parts) != 3 {
			return "", "", "", false
		}
		slot := decodeModelSlotToken(parts[1])
		value := strings.TrimSpace(parts[2])
		if slot == "" || value == "" {
			return "", "", "", false
		}
		return action, slot, value, true
	default:
		return "", "", "", false
	}
}

func modelCallbackActionToken(action modelCallbackAction) string {
	switch action {
	case modelCallbackStatus:
		return "status"
	case modelCallbackSlot:
		return "slot"
	case modelCallbackChanges:
		return "changes"
	case modelCallbackClear:
		return "clear"
	case modelCallbackEffort:
		return "eff"
	case modelCallbackPreset:
		return "preset"
	case modelCallbackSpeed:
		return "speed"
	default:
		return ""
	}
}

func decodeModelCallbackActionToken(token string) (modelCallbackAction, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "status":
		return modelCallbackStatus, true
	case "slot":
		return modelCallbackSlot, true
	case "changes":
		return modelCallbackChanges, true
	case "clear":
		return modelCallbackClear, true
	case "eff":
		return modelCallbackEffort, true
	case "preset":
		return modelCallbackPreset, true
	case "speed":
		return modelCallbackSpeed, true
	default:
		return "", false
	}
}

func modelSlotToken(slot string) string {
	switch core.NormalizeModelSlot(slot) {
	case core.ModelSlotPersona:
		return "p"
	case core.ModelSlotGovernor:
		return "g"
	case core.ModelSlotDoctor:
		return "d"
	case core.ModelSlotChildDefault:
		return "c"
	default:
		return ""
	}
}

func decodeModelSlotToken(token string) string {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "p":
		return core.ModelSlotPersona
	case "g":
		return core.ModelSlotGovernor
	case "d":
		return core.ModelSlotDoctor
	case "c":
		return core.ModelSlotChildDefault
	default:
		return ""
	}
}
