//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

const continuationIntentSchemaVersion = "1"

func parseContinuationIntentContract(raw string) (session.ContinuationIntent, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return session.ContinuationIntent{}, false
	}
	if parsed, ok := parseContinuationIntentContractJSON(trimmed); ok {
		return normalizeParsedContinuationIntent(parsed), true
	}
	if parsed, ok := parseContinuationIntentContractLines(trimmed); ok {
		return normalizeParsedContinuationIntent(parsed), true
	}
	return session.ContinuationIntent{}, false
}

func parseContinuationIntentContractLines(raw string) (session.ContinuationIntent, bool) {
	intent := session.ContinuationIntent{}
	schemaVersion := ""
	schemaSet := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := splitContinuationDirective(line)
		if !ok {
			continue
		}
		switch key {
		case "CONTINUATION_SCHEMA_VERSION", "CONTINUATION_SCHEMA":
			schemaVersion = normalizeContinuationSchemaVersion(value)
			schemaSet = true
		case "CONTINUATION_INTENT", "CONTINUATION_DECISION":
			intent.Decision = parseContinuationIntentDecision(value)
		case "CONTINUATION_RATIONALE":
			intent.Rationale = strings.TrimSpace(value)
		case "CONTINUATION_NEXT_STEP":
			intent.NextStep = strings.TrimSpace(value)
		case "CONTINUATION_CONSTRAINTS":
			intent.Constraints = strings.TrimSpace(value)
		case "CONTINUATION_CONFIDENCE":
			intent.Confidence = parseContinuationConfidence(value)
		case "CONTINUATION_RATIFIED":
			ratified, boolOK := parseContinuationBool(value)
			if !boolOK {
				return session.ContinuationIntent{}, false
			}
			intent.Ratified = ratified
		}
	}
	if !schemaSet || schemaVersion != continuationIntentSchemaVersion {
		return session.ContinuationIntent{}, false
	}
	if intent.Decision == "" {
		return session.ContinuationIntent{}, false
	}
	return intent, true
}

func splitContinuationDirective(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.ToUpper(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func parseContinuationIntentContractJSON(raw string) (session.ContinuationIntent, bool) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return session.ContinuationIntent{}, false
	}
	root, ok := payload.(map[string]any)
	if !ok {
		return session.ContinuationIntent{}, false
	}

	contract := root
	if nested, ok := root["continuation"]; ok {
		nestedMap, nestedOK := nested.(map[string]any)
		if !nestedOK {
			return session.ContinuationIntent{}, false
		}
		contract = nestedMap
	}

	if normalizeContinuationSchemaVersion(firstStringFromMap(contract, "schema_version", "continuation_schema_version", "schema")) != continuationIntentSchemaVersion {
		return session.ContinuationIntent{}, false
	}

	intent := session.ContinuationIntent{
		Decision:    parseContinuationIntentDecision(firstStringFromMap(contract, "intent", "decision", "continuation_intent")),
		Rationale:   strings.TrimSpace(firstStringFromMap(contract, "rationale", "continuation_rationale")),
		NextStep:    strings.TrimSpace(firstStringFromMap(contract, "next_step", "continuation_next_step")),
		Constraints: strings.TrimSpace(firstStringFromMap(contract, "constraints", "continuation_constraints")),
		Confidence:  parseContinuationConfidence(firstStringFromMap(contract, "confidence", "continuation_confidence")),
	}

	if ratified, ok := firstBoolFromMap(contract, "ratified", "continuation_ratified"); ok {
		intent.Ratified = ratified
	}

	if intent.Decision == "" {
		return session.ContinuationIntent{}, false
	}
	return intent, true
}

func normalizeParsedContinuationIntent(intent session.ContinuationIntent) session.ContinuationIntent {
	intent.Decision = parseContinuationIntentDecision(string(intent.Decision))
	intent.Rationale = clampContinuationText(strings.TrimSpace(intent.Rationale), 220)
	intent.NextStep = clampContinuationText(strings.TrimSpace(intent.NextStep), 220)
	intent.Constraints = clampContinuationText(strings.TrimSpace(intent.Constraints), 220)
	intent.Confidence = parseContinuationConfidence(intent.Confidence)
	return intent
}

func parseContinuationIntentDecision(raw string) session.ContinuationIntentDecision {
	switch normalizeContinuationEnum(raw) {
	case string(session.ContinuationIntentDecisionContinue):
		return session.ContinuationIntentDecisionContinue
	case string(session.ContinuationIntentDecisionHold):
		return session.ContinuationIntentDecisionHold
	case string(session.ContinuationIntentDecisionStop):
		return session.ContinuationIntentDecisionStop
	default:
		return ""
	}
}

func parseContinuationConfidence(raw string) string {
	switch normalizeContinuationEnum(raw) {
	case "low", "medium", "high":
		return normalizeContinuationEnum(raw)
	default:
		return ""
	}
}

func parseContinuationBool(raw string) (bool, bool) {
	switch normalizeContinuationEnum(raw) {
	case "yes", "true", "1":
		return true, true
	case "no", "false", "0":
		return false, true
	default:
		return false, false
	}
}

func normalizeContinuationSchemaVersion(raw string) string {
	trimmed := strings.Trim(strings.TrimSpace(raw), "\"'")
	if trimmed == "" {
		return ""
	}
	if trimmed == continuationIntentSchemaVersion {
		return continuationIntentSchemaVersion
	}
	return ""
}

func normalizeContinuationEnum(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func firstStringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case float64:
			return strings.TrimSpace(fmt.Sprintf("%.0f", typed))
		case int:
			return fmt.Sprintf("%d", typed)
		case int64:
			return fmt.Sprintf("%d", typed)
		case json.Number:
			return strings.TrimSpace(typed.String())
		default:
			coerced := strings.TrimSpace(fmt.Sprint(typed))
			if coerced != "" && coerced != "<nil>" {
				return coerced
			}
		}
	}
	return ""
}

func firstBoolFromMap(values map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			return parseContinuationBool(typed)
		case float64:
			if typed == 1 {
				return true, true
			}
			if typed == 0 {
				return false, true
			}
		}
	}
	return false, false
}
