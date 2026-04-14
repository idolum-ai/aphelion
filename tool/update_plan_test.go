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
