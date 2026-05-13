//go:build linux

package session

import (
	"encoding/json"
	"strings"
)

func encodePlanState(state PlanState) string {
	normalized := NormalizePlanState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func encodeOperationState(state OperationState) string {
	normalized := NormalizeOperationState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodePlanState(raw string) PlanState {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlanState{}
	}
	var state PlanState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return PlanState{}
	}
	return NormalizePlanState(state)
}

func decodeOperationState(raw string) OperationState {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OperationState{}
	}
	var state OperationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return OperationState{}
	}
	return NormalizeOperationState(state)
}

func encodeContinuationState(state ContinuationState) string {
	normalized := NormalizeContinuationState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodeContinuationState(raw string) ContinuationState {
	state, err := decodeContinuationStateStrict(raw)
	if err != nil {
		return ContinuationState{}
	}
	return state
}

func decodeContinuationStateStrict(raw string) (ContinuationState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ContinuationState{}, nil
	}
	var state ContinuationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ContinuationState{}, err
	}
	return NormalizeContinuationState(state), nil
}

func parseRenderedPlanState(raw string) (PlanState, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlanState{}, false
	}

	var (
		state  PlanState
		header bool
	)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[PLAN"):
			header = true
		case strings.HasPrefix(strings.ToLower(line), "explanation:"):
			state.Explanation = strings.TrimSpace(strings.TrimPrefix(line, "explanation:"))
		case strings.HasPrefix(line, "- ["):
			end := strings.Index(line, "]")
			if end <= 3 {
				continue
			}
			status := NormalizePlanStatus(PlanStatus(line[3:end]))
			step := strings.TrimSpace(line[end+1:])
			if status == "" || step == "" {
				continue
			}
			state.Steps = append(state.Steps, PlanStep{Step: step, Status: status})
		}
	}
	state = NormalizePlanState(state)
	if !header || (len(state.Steps) == 0 && state.Explanation == "") {
		return PlanState{}, false
	}
	return state, true
}
