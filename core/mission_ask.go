//go:build linux

package core

import (
	"strings"
)

const missionAskCallbackPrefix = "missionask:"

const (
	MissionAskCallbackAsk    = "ask"
	MissionAskCallbackIgnore = "ignore"
)

func EncodeMissionAskCallbackData(promptID string, action string) string {
	promptID = strings.TrimSpace(promptID)
	action = strings.ToLower(strings.TrimSpace(action))
	if promptID == "" {
		return ""
	}
	switch action {
	case MissionAskCallbackAsk, MissionAskCallbackIgnore:
		return missionAskCallbackPrefix + action + ":" + promptID
	default:
		return ""
	}
}

func DecodeMissionAskCallbackData(data string) (promptID string, action string, ok bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, missionAskCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimPrefix(data, missionAskCallbackPrefix)
	rawAction, rawID, found := strings.Cut(payload, ":")
	if !found {
		return "", "", false
	}
	action = strings.ToLower(strings.TrimSpace(rawAction))
	promptID = strings.TrimSpace(rawID)
	if promptID == "" {
		return "", "", false
	}
	switch action {
	case MissionAskCallbackAsk, MissionAskCallbackIgnore:
		return promptID, action, true
	default:
		return "", "", false
	}
}
