//go:build linux

package turn

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

// PersistStageErrorContext controls persisted error-prefix semantics.
type PersistStageErrorContext struct {
	ConvertMessages string
	LoadPlanState   string
	LoadOperation   string
	SaveSession     string
}

// PersistStageInput captures persist-stage orchestration facts.
type PersistStageInput struct {
	LedgerText      string
	OutHistory      []agent.Message
	HistoryInputLen int
	Session         *session.Session
	ReplyText       string
	FloorText       string
	FloorMetadata   string
	Usage           core.TokenUsage
	ErrorContext    PersistStageErrorContext
}

// PersistStageCallbacks provides side-effectful persistence callbacks.
type PersistStageCallbacks struct {
	BuildMessages       func(ledgerText string, generated []agent.Message, turnIndex int) ([]session.Message, error)
	ApplyScene          func(messages []session.Message, sceneText string) []session.Message
	ApplyFloor          func(messages []session.Message, floorText string, floorMetadata string) []session.Message
	LoadPlanState       func(ctx context.Context) (session.PlanState, error)
	MergePlanState      func(inMemory session.PlanState, persisted session.PlanState) session.PlanState
	LoadOperationState  func(ctx context.Context) (session.OperationState, error)
	MergeOperationState func(inMemory session.OperationState, persisted session.OperationState) session.OperationState
	Save                func(ctx context.Context, sess *session.Session, newMessages []session.Message, usage core.TokenUsage) error
}

// PersistStageResult captures persist-stage output facts.
type PersistStageResult struct {
	Committed   bool
	NewMessages []session.Message
}

// RunPersistStage applies persist-stage ordering for one turn result.
func RunPersistStage(ctx context.Context, input PersistStageInput, callbacks PersistStageCallbacks) (PersistStageResult, error) {
	out := PersistStageResult{}
	convertErrPrefix := input.ErrorContext.ConvertMessages
	if convertErrPrefix == "" {
		convertErrPrefix = "convert new messages"
	}
	planStateErrPrefix := input.ErrorContext.LoadPlanState
	if planStateErrPrefix == "" {
		planStateErrPrefix = "load plan state before save"
	}
	operationStateErrPrefix := input.ErrorContext.LoadOperation
	if operationStateErrPrefix == "" {
		operationStateErrPrefix = "load operation state before save"
	}
	saveErrPrefix := input.ErrorContext.SaveSession
	if saveErrPrefix == "" {
		saveErrPrefix = "save session"
	}
	if input.Session == nil {
		return out, fmt.Errorf("%s: %w", saveErrPrefix, fmt.Errorf("session is required"))
	}
	if len(input.OutHistory) < input.HistoryInputLen {
		return out, fmt.Errorf("%s: %w", convertErrPrefix, fmt.Errorf("invalid governor output history window"))
	}
	if callbacks.BuildMessages == nil {
		return out, fmt.Errorf("%s: %w", convertErrPrefix, fmt.Errorf("build messages callback is required"))
	}
	generated := input.OutHistory[input.HistoryInputLen:]
	newMessages, err := callbacks.BuildMessages(input.LedgerText, generated, input.Session.TurnCount)
	if err != nil {
		return out, fmt.Errorf("%s: %w", convertErrPrefix, err)
	}
	if callbacks.ApplyScene != nil {
		newMessages = callbacks.ApplyScene(newMessages, input.ReplyText)
	}
	if callbacks.ApplyFloor != nil {
		newMessages = callbacks.ApplyFloor(newMessages, input.FloorText, input.FloorMetadata)
	}
	input.Session.LastFloorText = input.FloorText
	input.Session.LastFloorMetadata = input.FloorMetadata

	if callbacks.LoadPlanState != nil {
		planState, planErr := callbacks.LoadPlanState(ctx)
		if planErr != nil {
			return out, fmt.Errorf("%s: %w", planStateErrPrefix, planErr)
		}
		if callbacks.MergePlanState != nil {
			input.Session.PlanState = callbacks.MergePlanState(input.Session.PlanState, planState)
		} else {
			input.Session.PlanState = planState
		}
	}
	if callbacks.LoadOperationState != nil {
		operationState, operationErr := callbacks.LoadOperationState(ctx)
		if operationErr != nil {
			return out, fmt.Errorf("%s: %w", operationStateErrPrefix, operationErr)
		}
		if callbacks.MergeOperationState != nil {
			input.Session.OperationState = callbacks.MergeOperationState(input.Session.OperationState, operationState)
		} else {
			input.Session.OperationState = operationState
		}
	}
	if callbacks.Save == nil {
		return out, fmt.Errorf("%s: %w", saveErrPrefix, fmt.Errorf("save callback is required"))
	}
	if err := callbacks.Save(ctx, input.Session, newMessages, input.Usage); err != nil {
		return out, fmt.Errorf("%s: %w", saveErrPrefix, err)
	}
	out.Committed = true
	out.NewMessages = newMessages
	return out, nil
}
