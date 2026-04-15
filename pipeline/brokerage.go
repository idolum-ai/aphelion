//go:build linux

package pipeline

import (
	"fmt"
	"strings"
)

const (
	turnModeAnswerNow        = "answer_now"
	turnModeInspectThenReply = "inspect_then_answer"
	turnModeAskThenWait      = "ask_then_wait"
	turnModeDecline          = "decline"
	turnModeSilent           = "silent"
)

func (c ExecutionContract) Summary() string {
	return fmt.Sprintf("inspect=%s, question=%s, answer=%s", yesNo(c.NeedsInspection), yesNo(c.NeedsQuestion), yesNo(c.MayAnswerNow))
}

// ParseExecutionContract parses a proposal-like block into a bounded execution
// contract.
func ParseExecutionContract(text string) *ExecutionContract {
	contract := ExecutionContract{}
	inspectSet := false
	questionSet := false
	answerSet := false
	for _, line := range strings.Split(text, "\n") {
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
		case strings.HasPrefix(upper, "MODE:"):
			if legacy, ok := executionContractFromTurnMode(strings.TrimSpace(line[len("MODE:"):])); ok {
				return legacy
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
		case strings.HasPrefix(upper, "MODE:"):
			if legacy, ok := executionContractFromTurnMode(strings.TrimSpace(line[len("MODE:"):])); ok {
				contract = *legacy
				inspectSet = true
				questionSet = true
				answerSet = true
				contractKnown = true
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
		}
		if !inPlan {
			continue
		}
		if step := parseBrokeragePlanStep(line); step != "" {
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

func normalizeTurnMode(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case turnModeAnswerNow, turnModeInspectThenReply, turnModeAskThenWait, turnModeDecline, turnModeSilent:
		return value
	default:
		return ""
	}
}

func executionContractFromTurnMode(raw string) (*ExecutionContract, bool) {
	switch normalizeTurnMode(raw) {
	case turnModeAnswerNow, turnModeDecline:
		return &ExecutionContract{MayAnswerNow: true}, true
	case turnModeInspectThenReply:
		return &ExecutionContract{NeedsInspection: true, MayAnswerNow: true}, true
	case turnModeAskThenWait:
		return &ExecutionContract{NeedsQuestion: true}, true
	case turnModeSilent:
		return &ExecutionContract{}, true
	default:
		return nil, false
	}
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
