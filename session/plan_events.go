//go:build linux

package session

import "strings"

// NormalizePlanEventKind keeps the ledger permissive while making semantic
// projection conservative: unknown kinds degrade to tool_updated.
func NormalizePlanEventKind(kind PlanEventKind) PlanEventKind {
	value := strings.TrimSpace(string(kind))
	switch PlanEventKind(value) {
	case PlanEventKindPhaseEntered,
		PlanEventKindPhaseCompleted,
		PlanEventKindDirectionChanged,
		PlanEventKindDependencyResolved,
		PlanEventKindBrokerageSeed,
		PlanEventKindRehydrated,
		PlanEventKindToolUpdated:
		return PlanEventKind(value)
	case "":
		return ""
	default:
		return PlanEventKindToolUpdated
	}
}

func (e PlanEvent) SemanticProjection() string {
	kind := NormalizePlanEventKind(e.Kind)
	switch kind {
	case PlanEventKindPhaseEntered,
		PlanEventKindPhaseCompleted,
		PlanEventKindDirectionChanged,
		PlanEventKindDependencyResolved,
		PlanEventKindBrokerageSeed,
		PlanEventKindRehydrated:
		state := NormalizePlanState(e.PlanState)
		parts := []string{string(kind)}
		if state.Explanation != "" {
			parts = append(parts, "summary="+state.Explanation)
		}
		if current := state.CurrentStep(); current != "" {
			parts = append(parts, "current="+current)
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func SemanticPlanEventProjections(events []PlanEvent, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	out := make([]string, 0, limit)
	for _, event := range events {
		projection := strings.TrimSpace(event.SemanticProjection())
		if projection == "" {
			continue
		}
		out = append(out, projection)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s PlanState) CurrentStep() string {
	state := NormalizePlanState(s)
	for _, step := range state.Steps {
		if step.Status == PlanStatusInProgress {
			return step.Step
		}
	}
	for _, step := range state.Steps {
		if step.Status == PlanStatusPending {
			return step.Step
		}
	}
	if len(state.Steps) > 0 {
		return state.Steps[len(state.Steps)-1].Step
	}
	return ""
}
