//go:build linux

package core

import "strings"

const reentryRecommendationCallbackPrefix = "reentry:"

const (
	ReentryRecommendationCallbackSelect = "select"
	ReentryRecommendationCallbackIgnore = "ignore"
)

func EncodeReentryRecommendationCallbackData(recommendationID string, candidateID string, action string) string {
	recommendationID = strings.TrimSpace(recommendationID)
	candidateID = strings.TrimSpace(candidateID)
	action = strings.ToLower(strings.TrimSpace(action))
	if recommendationID == "" {
		return ""
	}
	switch action {
	case ReentryRecommendationCallbackIgnore:
		data := reentryRecommendationCallbackPrefix + action + ":" + recommendationID
		if len(data) <= TelegramCallbackDataMaxBytes {
			return data
		}
		return ""
	case ReentryRecommendationCallbackSelect:
		if candidateID == "" {
			return ""
		}
		data := reentryRecommendationCallbackPrefix + action + ":" + recommendationID + ":" + candidateID
		if len(data) <= TelegramCallbackDataMaxBytes {
			return data
		}
		return ""
	default:
		return ""
	}
}

func DecodeReentryRecommendationCallbackData(data string) (recommendationID string, candidateID string, action string, ok bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, reentryRecommendationCallbackPrefix) {
		return "", "", "", false
	}
	payload := strings.TrimPrefix(data, reentryRecommendationCallbackPrefix)
	parts := strings.Split(payload, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", "", false
	}
	action = strings.ToLower(strings.TrimSpace(parts[0]))
	recommendationID = strings.TrimSpace(parts[1])
	if recommendationID == "" {
		return "", "", "", false
	}
	switch action {
	case ReentryRecommendationCallbackIgnore:
		if len(parts) != 2 {
			return "", "", "", false
		}
		return recommendationID, "", action, true
	case ReentryRecommendationCallbackSelect:
		if len(parts) != 3 {
			return "", "", "", false
		}
		candidateID = strings.TrimSpace(parts[2])
		if candidateID == "" {
			return "", "", "", false
		}
		return recommendationID, candidateID, action, true
	default:
		return "", "", "", false
	}
}
