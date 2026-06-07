//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type updatePlanToolProvider struct {
	callCount int
}

func TestMergeSessionContinuationStateTerminalLeaseBeatsStalePending(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	inMemory := session.ContinuationState{
		Status:            session.ContinuationStatusPending,
		DecisionID:        "phase-stale",
		DecisionMessageID: 17492,
		RemainingTurns:    1,
		UpdatedAt:         now.Add(time.Minute),
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-stale",
			Status:         session.ContinuationLeaseStatusPending,
			RemainingTurns: 1,
			UpdatedAt:      now.Add(time.Minute),
		},
	}
	persisted := inMemory
	persisted.Status = session.ContinuationStatusIdle
	persisted.DecisionID = ""
	persisted.DecisionMessageID = 0
	persisted.RemainingTurns = 0
	persisted.UpdatedAt = now
	persisted.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	persisted.ContinuationLease.RemainingTurns = 0
	persisted.ContinuationLease.ConsumedAt = now
	persisted.ContinuationLease.UpdatedAt = now

	got := mergeSessionContinuationState(inMemory, persisted)
	if got.Status != session.ContinuationStatusIdle ||
		got.DecisionID != "" ||
		got.DecisionMessageID != 0 ||
		got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("mergeSessionContinuationState() = %#v, want consumed persisted state", got)
	}
}

func TestMergeSessionContinuationStateKeepsEmptyStateEmpty(t *testing.T) {
	t.Parallel()

	got := mergeSessionContinuationState(session.ContinuationState{}, session.ContinuationState{})
	if got.Kind != "" || !got.UpdatedAt.IsZero() || continuationStateHasDurableRecord(got) {
		t.Fatalf("mergeSessionContinuationState(empty) = %#v, want empty state", got)
	}
}

func (p *updatePlanToolProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.callCount++
	if p.callCount == 1 {
		for _, def := range tools {
			if def.Name == "update_plan" {
				return &agent.Response{
					ToolCalls: []agent.ToolCall{{
						ID:   "plan-1",
						Name: "update_plan",
						Input: json.RawMessage(`{
							"explanation":"Inspect before editing.",
							"plan":[
								{"step":"Inspect the relevant files.","status":"in_progress"},
								{"step":"Patch the issue.","status":"pending"}
							]
						}`),
					}},
				}, nil
			}
		}
		return &agent.Response{Content: "update_plan unavailable"}, nil
	}
	return &agent.Response{Content: "done"}, nil
}

func TestHandleInboundPersistsPlanUpdatesFromToolAndReinjectsPlan(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &updatePlanToolProvider{}
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     912,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Please work through this carefully.",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound(first) err = %v", err)
	}

	key := session.SessionKey{
		ChatID: 912,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "912"},
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.PlanState.Explanation != "Inspect before editing." {
		t.Fatalf("Explanation = %q, want persisted plan explanation", sess.PlanState.Explanation)
	}
	if len(sess.PlanState.Steps) != 2 || sess.PlanState.Steps[0].Status != "in_progress" {
		t.Fatalf("PlanState = %#v, want persisted in_progress steps", sess.PlanState)
	}
	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) == 0 || events[0].Kind != session.PlanEventKindToolUpdated {
		t.Fatalf("PlanEvents = %#v, want tool_updated event", events)
	}

	probe := &fakeProvider{replyText: "second"}
	rt, err = New(cfg, store, probe, tools, sender)
	if err != nil {
		t.Fatalf("New(second) err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     912,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "continue",
		MessageID:  2,
	}); err != nil {
		t.Fatalf("HandleInbound(second) err = %v", err)
	}

	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.seenGovernorSystem) == 0 {
		t.Fatal("seenGovernorSystem empty, want prompt with current plan state")
	}
	if !strings.Contains(probe.seenGovernorSystem[0], "## Current Plan State") {
		t.Fatalf("governor prompt missing current plan state: %q", probe.seenGovernorSystem[0])
	}
	if !strings.Contains(probe.seenGovernorSystem[0], "[in_progress] Inspect the relevant files.") {
		t.Fatalf("governor prompt missing persisted plan step: %q", probe.seenGovernorSystem[0])
	}
}
