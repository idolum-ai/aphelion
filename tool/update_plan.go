//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) updatePlan(_ context.Context, input json.RawMessage, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("update_plan requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("update_plan requires session context")
	}

	var in updatePlanInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode update_plan input: %w", err)
		}
	}

	current, err := r.store.PlanState(key)
	if err != nil {
		return "", err
	}

	if in.Plan == nil && strings.TrimSpace(in.Explanation) == "" {
		return renderPlanState("[PLAN]", current), nil
	}

	state, err := applyPlanInput(current, in)
	if err != nil {
		return "", err
	}
	state.UpdatedAt = time.Now().UTC()
	if err := r.store.UpdatePlanStateWithEvent(key, state, session.PlanEventKindToolUpdated); err != nil {
		return "", err
	}
	return renderPlanState("[PLAN_UPDATED]", state), nil
}

func applyPlanInput(current session.PlanState, in updatePlanInput) (session.PlanState, error) {
	current = session.NormalizePlanState(current)
	if in.Merge {
		return mergePlanInput(current, in)
	}

	state := session.PlanState{
		Explanation: strings.TrimSpace(in.Explanation),
		Steps:       make([]session.PlanStep, 0, len(in.Plan)),
	}
	inProgress := 0
	for _, item := range in.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			return session.PlanState{}, fmt.Errorf("update_plan step is required")
		}
		status := session.NormalizePlanStatus(session.PlanStatus(item.Status))
		if status == "" {
			return session.PlanState{}, fmt.Errorf("update_plan status must be pending, in_progress, or completed")
		}
		if status == session.PlanStatusInProgress {
			inProgress++
		}
		state.Steps = append(state.Steps, session.PlanStep{
			Step:   step,
			Status: status,
		})
	}
	if inProgress > 1 {
		return session.PlanState{}, fmt.Errorf("update_plan must have at most one in_progress step")
	}
	return session.NormalizePlanState(state), nil
}

func mergePlanInput(current session.PlanState, in updatePlanInput) (session.PlanState, error) {
	state := session.PlanState{
		Explanation: current.Explanation,
		Steps:       append([]session.PlanStep(nil), current.Steps...),
	}
	if explanation := strings.TrimSpace(in.Explanation); explanation != "" {
		state.Explanation = explanation
	}

	indexByStep := make(map[string]int, len(state.Steps))
	for i, step := range state.Steps {
		indexByStep[step.Step] = i
	}

	for _, item := range in.Plan {
		step := strings.TrimSpace(item.Step)
		if step == "" {
			return session.PlanState{}, fmt.Errorf("update_plan step is required")
		}
		status := session.NormalizePlanStatus(session.PlanStatus(item.Status))
		if status == "" {
			return session.PlanState{}, fmt.Errorf("update_plan status must be pending, in_progress, or completed")
		}

		if idx, ok := indexByStep[step]; ok {
			state.Steps[idx].Status = status
			continue
		}
		state.Steps = append(state.Steps, session.PlanStep{Step: step, Status: status})
		indexByStep[step] = len(state.Steps) - 1
	}

	state = session.NormalizePlanState(state)
	inProgress := 0
	for _, step := range state.Steps {
		if step.Status == session.PlanStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return session.PlanState{}, fmt.Errorf("update_plan must have at most one in_progress step")
	}
	return state, nil
}

func renderPlanState(header string, state session.PlanState) string {
	state = session.NormalizePlanState(state)
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	active := len(state.Steps) > 0
	fmt.Fprintf(&b, "active: %t\n", active)
	if state.Explanation != "" {
		fmt.Fprintf(&b, "explanation: %s\n", state.Explanation)
	}
	if !active {
		b.WriteString("steps: none\n")
		return strings.TrimSpace(b.String())
	}
	for _, step := range state.Steps {
		fmt.Fprintf(&b, "- [%s] %s\n", step.Status, step.Step)
	}
	return strings.TrimSpace(b.String())
}
