//go:build linux

package runtime

import "github.com/idolum-ai/aphelion/session"

func mergeSessionPlanState(inMemory session.PlanState, persisted session.PlanState) session.PlanState {
	inMemory = session.NormalizePlanState(inMemory)
	persisted = session.NormalizePlanState(persisted)

	switch {
	case persisted.UpdatedAt.After(inMemory.UpdatedAt):
		return persisted
	case inMemory.UpdatedAt.After(persisted.UpdatedAt):
		return inMemory
	case len(persisted.Steps) > 0 || persisted.Explanation != "":
		return persisted
	default:
		return inMemory
	}
}

func mergeSessionOperationState(inMemory session.OperationState, persisted session.OperationState) session.OperationState {
	inMemory = session.NormalizeOperationState(inMemory)
	persisted = session.NormalizeOperationState(persisted)

	switch {
	case persisted.UpdatedAt.After(inMemory.UpdatedAt):
		return persisted
	case inMemory.UpdatedAt.After(persisted.UpdatedAt):
		return inMemory
	case persisted.Active():
		return persisted
	default:
		return inMemory
	}
}
