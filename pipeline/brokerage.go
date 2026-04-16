//go:build linux

package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (c ExecutionContract) Summary() string {
	return fmt.Sprintf("inspect=%s, question=%s, answer=%s", yesNo(c.NeedsInspection), yesNo(c.NeedsQuestion), yesNo(c.MayAnswerNow))
}

// ParseExecutionContract parses a proposal-like block into a bounded execution
// contract.
func ParseExecutionContract(text string) *ExecutionContract {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if contract, ok := parseExecutionContractJSON(trimmed); ok {
		return &contract
	}

	contract := ExecutionContract{}
	inspectSet := false
	questionSet := false
	answerSet := false
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "INSPECT:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("INSPECT:"):])); ok {
				contract.NeedsInspection = value
				inspectSet = true
			}
		case strings.HasPrefix(upper, "QUESTION:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("QUESTION:"):])); ok {
				contract.NeedsQuestion = value
				questionSet = true
			}
		case strings.HasPrefix(upper, "ANSWER:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("ANSWER:"):])); ok {
				contract.MayAnswerNow = value
				answerSet = true
			}
		}
	}
	if inspectSet && questionSet && answerSet {
		return &contract
	}
	return nil
}

// ParseBrokerageRatification parses a ratification artifact into contract terms.
func ParseBrokerageRatification(text string) (BrokerageRatification, error) {
	parsed := BrokerageRatification{RawText: strings.TrimSpace(text)}
	if parsed.RawText == "" {
		return parsed, fmt.Errorf("empty brokerage ratification")
	}
	if jsonParsed, ok, err := parseBrokerageRatificationJSON(parsed.RawText); ok {
		return jsonParsed, err
	}

	contract := ExecutionContract{}
	inspectSet := false
	questionSet := false
	// answerSet intentionally tracks an explicit answer requirement.
	answerSet := false
	contractKnown := false
	inPlan := false
	for _, rawLine := range strings.Split(parsed.RawText, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "INSPECT:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("INSPECT:"):])); ok {
				contract.NeedsInspection = value
				inspectSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "QUESTION:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("QUESTION:"):])); ok {
				contract.NeedsQuestion = value
				questionSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "ANSWER:"):
			if value, ok := normalizeDirectiveBool(strings.TrimSpace(line[len("ANSWER:"):])); ok {
				contract.MayAnswerNow = value
				answerSet = true
			}
			inPlan = false
			continue
		case strings.HasPrefix(upper, "RATIFICATION:"):
			parsed.Disposition = normalizeRatification(strings.TrimSpace(line[len("RATIFICATION:"):]))
			inPlan = false
			continue
		case strings.HasPrefix(upper, "SIGNAL_JUDGMENT:"):
			parsed.SignalJudgment = normalizeSignalJudgment(strings.TrimSpace(line[len("SIGNAL_JUDGMENT:"):]))
			inPlan = false
			continue
		case upper == "PLAN:":
			inPlan = true
			continue
		case upper == "STEPS:":
			inPlan = true
			continue
		}
		if step := parseBrokeragePlanStep(line); step != "" {
			if !inPlan && parsed.Disposition == "" {
				continue
			}
			parsed.RatifiedSteps = append(parsed.RatifiedSteps, step)
		}
	}
	if inspectSet && questionSet && answerSet {
		parsed.RatifiedContract = contract
		contractKnown = true
	}

	switch {
	case !contractKnown:
		return parsed, fmt.Errorf("missing ratified execution contract")
	case parsed.Disposition == "":
		return parsed, fmt.Errorf("missing ratification disposition")
	case len(parsed.RatifiedSteps) == 0:
		return parsed, fmt.Errorf("missing ratified execution steps")
	default:
		return parsed, nil
	}
}

func parseExecutionContractJSON(raw string) (ExecutionContract, bool) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ExecutionContract{}, false
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return ExecutionContract{}, false
	}
	contract, ok := parseExecutionContractObject(object)
	return contract, ok
}

func parseExecutionContractObject(object map[string]any) (ExecutionContract, bool) {
	for _, key := range []string{"contract", "execution_contract", "ratified_contract", "suggested_contract"} {
		nested, ok := object[key]
		if !ok {
			continue
		}
		nestedObject, ok := nested.(map[string]any)
		if !ok {
			continue
		}
		if contract, ok := parseExecutionContractObject(nestedObject); ok {
			return contract, true
		}
	}

	inspect, inspectOK := readBoolFromMap(object, "inspect", "needs_inspection")
	question, questionOK := readBoolFromMap(object, "question", "needs_question")
	answer, answerOK := readBoolFromMap(object, "answer", "answer_now", "may_answer_now")
	if !inspectOK || !questionOK || !answerOK {
		return ExecutionContract{}, false
	}
	return ExecutionContract{
		NeedsInspection: inspect,
		NeedsQuestion:   question,
		MayAnswerNow:    answer,
	}, true
}

func parseBrokerageRatificationJSON(raw string) (BrokerageRatification, bool, error) {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return BrokerageRatification{}, false, nil
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return BrokerageRatification{}, false, nil
	}

	parsed := BrokerageRatification{RawText: strings.TrimSpace(raw)}
	if contract, ok := parseExecutionContractObject(object); ok {
		parsed.RatifiedContract = contract
	}
	if ratification, ok := readStringFromMap(object, "ratification", "disposition"); ok {
		parsed.Disposition = normalizeRatification(ratification)
	}
	if signal, ok := readStringFromMap(object, "signal_judgment", "signalJudgment", "signal"); ok {
		parsed.SignalJudgment = normalizeSignalJudgment(signal)
	}
	parsed.RatifiedSteps = parsePlanStepsFromMap(object)

	switch {
	case parsed.RatifiedContract == (ExecutionContract{}):
		return parsed, true, fmt.Errorf("missing ratified execution contract")
	case parsed.Disposition == "":
		return parsed, true, fmt.Errorf("missing ratification disposition")
	case len(parsed.RatifiedSteps) == 0:
		return parsed, true, fmt.Errorf("missing ratified execution steps")
	default:
		return parsed, true, nil
	}
}

func parsePlanStepsFromMap(object map[string]any) []string {
	for _, key := range []string{"plan", "steps", "ratified_steps"} {
		value, ok := object[key]
		if !ok {
			continue
		}
		steps := parsePlanSteps(value)
		if len(steps) > 0 {
			return steps
		}
	}
	return nil
}

func parsePlanSteps(value any) []string {
	out := []string{}
	appendStep := func(step string) {
		step = strings.TrimSpace(step)
		if step == "" {
			return
		}
		out = append(out, step)
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			switch concrete := item.(type) {
			case string:
				appendStep(concrete)
			default:
				appendStep(fmt.Sprint(concrete))
			}
		}
	case []string:
		for _, step := range typed {
			appendStep(step)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if step := parseBrokeragePlanStep(line); step != "" {
				appendStep(step)
				continue
			}
			appendStep(line)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readBoolFromMap(object map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		if parsed, ok := normalizeBoolValue(value); ok {
			return parsed, true
		}
	}
	return false, false
}

func readStringFromMap(object map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := object[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed, true
			}
		default:
			trimmed := strings.TrimSpace(fmt.Sprint(typed))
			if trimmed != "" && trimmed != "<nil>" {
				return trimmed, true
			}
		}
	}
	return "", false
}

func normalizeBoolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		return normalizeDirectiveBool(typed)
	case float64:
		switch typed {
		case 0:
			return false, true
		case 1:
			return true, true
		}
	}
	return false, false
}

func normalizeDirectiveBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "required", "needed", "y":
		return true, true
	case "no", "false", "not_required", "not needed", "n":
		return false, true
	default:
		return false, false
	}
}

func normalizeRatification(raw string) RatificationDisposition {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case string(RatificationAccept), string(RatificationAdapt), string(RatificationReject):
		return RatificationDisposition(value)
	default:
		return ""
	}
}

func normalizeSignalJudgment(raw string) SignalJudgment {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "", string(SignalJudgmentConfirmed), string(SignalJudgmentOverridden), string(SignalJudgmentNotMaterial):
		return SignalJudgment(value)
	default:
		return ""
	}
}

func parseBrokeragePlanStep(line string) string {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
		return strings.TrimSpace(line[2:])
	}
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return ""
	}
	for _, ch := range line[:dot] {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return strings.TrimSpace(line[dot+2:])
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
