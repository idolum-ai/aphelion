//go:build linux

package turn

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestRunPersistStageOrdersCallbacksAndCommits(t *testing.T) {
	t.Parallel()

	var order []string
	var savedMessages []session.Message
	sess := &session.Session{
		TurnCount:         3,
		PlanState:         session.PlanState{Explanation: "in-memory"},
		OperationState:    session.OperationState{ID: "in-memory"},
		LastFloorText:     "old floor",
		LastFloorMetadata: "old metadata",
	}
	outHistory := []agent.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "draft"},
		{Role: "tool", Content: "tool result"},
	}

	got, err := RunPersistStage(context.Background(), PersistStageInput{
		LedgerText:      "user text",
		OutHistory:      outHistory,
		HistoryInputLen: 1,
		Session:         sess,
		ReplyText:       "scene text",
		FloorText:       "floor text",
		FloorMetadata:   "floor metadata",
		Usage:           core.TokenUsage{TotalTokens: 9},
	}, PersistStageCallbacks{
		BuildMessages: func(ledgerText string, generated []agent.Message, turnIndex int) ([]session.Message, error) {
			order = append(order, "build")
			if ledgerText != "user text" {
				t.Fatalf("ledgerText = %q, want user text", ledgerText)
			}
			if turnIndex != 3 {
				t.Fatalf("turnIndex = %d, want 3", turnIndex)
			}
			if len(generated) != 2 {
				t.Fatalf("generated len = %d, want 2", len(generated))
			}
			return []session.Message{
				{Role: "user", Content: ledgerText, TurnIndex: turnIndex},
				{Role: "assistant", Content: "draft scene", TurnIndex: turnIndex},
			}, nil
		},
		ApplyScene: func(messages []session.Message, sceneText string) []session.Message {
			order = append(order, "scene")
			messages[len(messages)-1].Content = sceneText
			return messages
		},
		ApplyFloor: func(messages []session.Message, floorText string, floorMetadata string) []session.Message {
			order = append(order, "floor")
			messages[len(messages)-1].FloorContent = floorText
			messages[len(messages)-1].FloorMetadata = floorMetadata
			return messages
		},
		LoadPlanState: func(context.Context) (session.PlanState, error) {
			order = append(order, "plan_load")
			return session.PlanState{Explanation: "persisted"}, nil
		},
		MergePlanState: func(inMemory session.PlanState, persisted session.PlanState) session.PlanState {
			order = append(order, "plan_merge")
			if inMemory.Explanation != "in-memory" {
				t.Fatalf("inMemory plan = %#v", inMemory)
			}
			return persisted
		},
		LoadOperationState: func(context.Context) (session.OperationState, error) {
			order = append(order, "op_load")
			return session.OperationState{ID: "persisted"}, nil
		},
		MergeOperationState: func(inMemory session.OperationState, persisted session.OperationState) session.OperationState {
			order = append(order, "op_merge")
			if inMemory.ID != "in-memory" {
				t.Fatalf("inMemory operation = %#v", inMemory)
			}
			return persisted
		},
		Save: func(_ context.Context, saveSess *session.Session, newMessages []session.Message, usage core.TokenUsage) error {
			order = append(order, "save")
			if usage.TotalTokens != 9 {
				t.Fatalf("usage = %#v, want total=9", usage)
			}
			if saveSess.PlanState.Explanation != "persisted" {
				t.Fatalf("session plan = %#v, want persisted", saveSess.PlanState)
			}
			if saveSess.OperationState.ID != "persisted" {
				t.Fatalf("session operation = %#v, want persisted", saveSess.OperationState)
			}
			savedMessages = append([]session.Message(nil), newMessages...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunPersistStage() err = %v", err)
	}
	if !got.Committed {
		t.Fatal("Committed = false, want true")
	}
	if want := []string{"build", "scene", "floor", "plan_load", "plan_merge", "op_load", "op_merge", "save"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %#v, want %#v", order, want)
	}
	if got.NewMessages == nil || len(got.NewMessages) != 2 {
		t.Fatalf("NewMessages = %#v, want 2 messages", got.NewMessages)
	}
	if savedMessages[len(savedMessages)-1].Content != "scene text" {
		t.Fatalf("saved assistant content = %q, want scene text", savedMessages[len(savedMessages)-1].Content)
	}
	if savedMessages[len(savedMessages)-1].FloorContent != "floor text" {
		t.Fatalf("saved floor content = %q, want floor text", savedMessages[len(savedMessages)-1].FloorContent)
	}
	if savedMessages[len(savedMessages)-1].FloorMetadata != "floor metadata" {
		t.Fatalf("saved floor metadata = %q, want floor metadata", savedMessages[len(savedMessages)-1].FloorMetadata)
	}
	if sess.LastFloorText != "floor text" {
		t.Fatalf("session.LastFloorText = %q, want floor text", sess.LastFloorText)
	}
	if sess.LastFloorMetadata != "floor metadata" {
		t.Fatalf("session.LastFloorMetadata = %q, want floor metadata", sess.LastFloorMetadata)
	}
}

func TestRunPersistStageInvalidHistoryWindowUsesConvertPrefix(t *testing.T) {
	t.Parallel()

	_, err := RunPersistStage(context.Background(), PersistStageInput{
		OutHistory:      []agent.Message{{Role: "assistant", Content: "x"}},
		HistoryInputLen: 2,
		Session:         &session.Session{},
		ErrorContext: PersistStageErrorContext{
			ConvertMessages: "convert durable group messages",
		},
	}, PersistStageCallbacks{
		BuildMessages: func(string, []agent.Message, int) ([]session.Message, error) {
			t.Fatal("BuildMessages callback should not run")
			return nil, nil
		},
		Save: func(context.Context, *session.Session, []session.Message, core.TokenUsage) error {
			t.Fatal("Save callback should not run")
			return nil
		},
	})
	if err == nil {
		t.Fatal("RunPersistStage() err = nil, want invalid window error")
	}
	if !strings.Contains(err.Error(), "convert durable group messages") {
		t.Fatalf("err = %v, want custom convert prefix", err)
	}
	if !strings.Contains(err.Error(), "invalid governor output history window") {
		t.Fatalf("err = %v, want invalid history window detail", err)
	}
}

func TestRunPersistStageBuildMessagesErrorUsesConvertPrefix(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("convert failed")
	_, err := RunPersistStage(context.Background(), PersistStageInput{
		OutHistory:      []agent.Message{{Role: "assistant", Content: "x"}},
		HistoryInputLen: 0,
		Session:         &session.Session{},
	}, PersistStageCallbacks{
		BuildMessages: func(string, []agent.Message, int) ([]session.Message, error) {
			return nil, wantErr
		},
		Save: func(context.Context, *session.Session, []session.Message, core.TokenUsage) error {
			t.Fatal("Save callback should not run")
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunPersistStage() err = %v, want %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "convert new messages") {
		t.Fatalf("err = %v, want default convert prefix", err)
	}
}

func TestRunPersistStagePlanLoadErrorUsesPrefix(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("plan load failed")
	_, err := RunPersistStage(context.Background(), PersistStageInput{
		OutHistory:      []agent.Message{{Role: "assistant", Content: "x"}},
		HistoryInputLen: 0,
		Session:         &session.Session{},
		ErrorContext: PersistStageErrorContext{
			LoadPlanState: "load durable group plan state before save",
		},
	}, PersistStageCallbacks{
		BuildMessages: func(string, []agent.Message, int) ([]session.Message, error) {
			return []session.Message{{Role: "assistant", Content: "x"}}, nil
		},
		LoadPlanState: func(context.Context) (session.PlanState, error) {
			return session.PlanState{}, wantErr
		},
		Save: func(context.Context, *session.Session, []session.Message, core.TokenUsage) error {
			t.Fatal("Save callback should not run")
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunPersistStage() err = %v, want %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "load durable group plan state before save") {
		t.Fatalf("err = %v, want custom plan prefix", err)
	}
}

func TestRunPersistStageOperationLoadErrorUsesPrefix(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("operation load failed")
	_, err := RunPersistStage(context.Background(), PersistStageInput{
		OutHistory:      []agent.Message{{Role: "assistant", Content: "x"}},
		HistoryInputLen: 0,
		Session:         &session.Session{},
	}, PersistStageCallbacks{
		BuildMessages: func(string, []agent.Message, int) ([]session.Message, error) {
			return []session.Message{{Role: "assistant", Content: "x"}}, nil
		},
		LoadPlanState: func(context.Context) (session.PlanState, error) {
			return session.PlanState{}, nil
		},
		LoadOperationState: func(context.Context) (session.OperationState, error) {
			return session.OperationState{}, wantErr
		},
		Save: func(context.Context, *session.Session, []session.Message, core.TokenUsage) error {
			t.Fatal("Save callback should not run")
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunPersistStage() err = %v, want %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "load operation state before save") {
		t.Fatalf("err = %v, want default operation prefix", err)
	}
}

func TestRunPersistStageSaveErrorUsesPrefix(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("save failed")
	_, err := RunPersistStage(context.Background(), PersistStageInput{
		OutHistory:      []agent.Message{{Role: "assistant", Content: "x"}},
		HistoryInputLen: 0,
		Session:         &session.Session{},
		ErrorContext: PersistStageErrorContext{
			SaveSession: "save durable group session",
		},
	}, PersistStageCallbacks{
		BuildMessages: func(string, []agent.Message, int) ([]session.Message, error) {
			return []session.Message{{Role: "assistant", Content: "x"}}, nil
		},
		Save: func(context.Context, *session.Session, []session.Message, core.TokenUsage) error {
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunPersistStage() err = %v, want %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "save durable group session") {
		t.Fatalf("err = %v, want custom save prefix", err)
	}
}
