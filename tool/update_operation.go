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

func (r *Registry) updateOperation(_ context.Context, input json.RawMessage, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("update_operation requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("update_operation requires session context")
	}

	var in updateOperationInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode update_operation input: %w", err)
		}
	}

	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}

	if operationInputEmpty(in) {
		return renderOperationState("[OPERATION]", current), nil
	}

	state, err := applyOperationInput(current, in)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if state.Active() {
		if strings.TrimSpace(state.ID) == "" {
			if current.ID != "" {
				state.ID = current.ID
			} else {
				state.ID = generatedOperationID("op")
			}
		}
		state.UpdatedAt = now
		if state.Proposal.Active() {
			if strings.TrimSpace(state.Proposal.ID) == "" {
				if current.Proposal.ID != "" {
					state.Proposal.ID = current.Proposal.ID
				} else {
					state.Proposal.ID = generatedOperationID("proposal")
				}
			}
			state.Proposal.UpdatedAt = now
		}
		if state.PhasePlan.Active() {
			if strings.TrimSpace(state.PhasePlan.ID) == "" {
				if current.PhasePlan.ID != "" {
					state.PhasePlan.ID = current.PhasePlan.ID
				} else {
					state.PhasePlan.ID = generatedOperationID("phase-plan")
				}
			}
			state.PhasePlan.UpdatedAt = now
		}
		if state.PlanLease.Active() {
			if strings.TrimSpace(state.PlanLease.ID) == "" {
				if current.PlanLease.ID != "" {
					state.PlanLease.ID = current.PlanLease.ID
				} else {
					state.PlanLease.ID = generatedOperationID("plan-lease")
				}
			}
			state.PlanLease.UpdatedAt = now
		}
	}

	if err := r.store.UpdateOperationState(key, state); err != nil {
		return "", err
	}
	return renderOperationUpdateAck(state, in), nil
}
