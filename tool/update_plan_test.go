//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefinitionsIncludeUpdatePlanToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "update_plan") {
		t.Fatalf("definitions without store = %#v, do not want update_plan", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "update_plan") {
		t.Fatalf("definitions with store = %#v, want update_plan", names)
	}
}

func TestUpdatePlanToolPersistsAndShowsPlanState(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_plan",
		json.RawMessage(`{
			"explanation":"Break this into execution steps.",
			"plan":[
				{"step":"Inspect the relevant files.","status":"in_progress"},
				{"step":"Patch the issue.","status":"pending"}
			]
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_plan) err = %v", err)
	}
	if !strings.Contains(out, "[PLAN_UPDATED]") || !strings.Contains(out, "Inspect the relevant files.") {
		t.Fatalf("update output = %q, want updated plan summary", out)
	}

	planState, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState() err = %v", err)
	}
	if planState.Explanation != "Break this into execution steps." {
		t.Fatalf("Explanation = %q, want persisted value", planState.Explanation)
	}
	if len(planState.Steps) != 2 || planState.Steps[0].Status != session.PlanStatusInProgress {
		t.Fatalf("Steps = %#v, want persisted in_progress plan", planState.Steps)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) == 0 || events[0].Kind != session.PlanEventKindToolUpdated {
		t.Fatalf("PlanEvents = %#v, want tool_updated event", events)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_plan",
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(show update_plan) err = %v", err)
	}
	if !strings.Contains(showOut, "[PLAN]") || !strings.Contains(showOut, "Patch the issue.") {
		t.Fatalf("show output = %q, want current plan state", showOut)
	}
}

func TestUpdatePlanToolMergePreservesExistingStepsAndAppendsNewWork(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()

	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Existing plan.",
		Steps: []session.PlanStep{
			{Step: "Inspect the current files.", Status: session.PlanStatusInProgress},
			{Step: "Patch the issue.", Status: session.PlanStatusPending},
		},
	}); err != nil {
		t.Fatalf("UpdatePlanState(seed) err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"update_plan",
		json.RawMessage(`{
			"merge": true,
			"explanation": "Execution is underway.",
			"plan":[
				{"step":"Inspect the current files.","status":"completed"},
				{"step":"Patch the issue.","status":"in_progress"},
				{"step":"Run focused tests.","status":"pending"}
			]
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(update_plan merge) err = %v", err)
	}
	if !strings.Contains(out, "Run focused tests.") {
		t.Fatalf("merge output = %q, want appended step", out)
	}

	planState, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState() err = %v", err)
	}
	if planState.Explanation != "Execution is underway." {
		t.Fatalf("Explanation = %q, want merged explanation", planState.Explanation)
	}
	if len(planState.Steps) != 3 {
		t.Fatalf("steps len = %d, want 3", len(planState.Steps))
	}
	if planState.Steps[0].Status != session.PlanStatusCompleted {
		t.Fatalf("first step status = %q, want completed", planState.Steps[0].Status)
	}
	if planState.Steps[1].Status != session.PlanStatusInProgress {
		t.Fatalf("second step status = %q, want in_progress", planState.Steps[1].Status)
	}
	if planState.Steps[2].Step != "Run focused tests." {
		t.Fatalf("third step = %#v, want appended new step", planState.Steps[2])
	}
}

func TestUpdatePlanToolRejectsMultipleInProgressSteps(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"update_plan",
		json.RawMessage(`{
			"plan":[
				{"step":"Inspect.","status":"in_progress"},
				{"step":"Patch.","status":"in_progress"}
			]
		}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(update_plan) err = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "at most one in_progress") {
		t.Fatalf("err = %v, want in_progress validation", err)
	}
}
