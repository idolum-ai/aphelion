//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefinitionsIncludeToolAuthorityWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "tool_authority") {
		t.Fatalf("definitions without store = %#v, do not want tool_authority", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "tool_authority") {
		t.Fatalf("definitions with store = %#v, want tool_authority", names)
	}
}

func TestToolAuthorityProposalRegisterExposeFlow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	submitOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"proposal_submit",
			"proposal_id":"tp-1",
			"proposed_by":"idolum-email",
			"tool_name":"search_web",
			"why_now":"Inbox-only analysis cannot evaluate external postings.",
			"contract":{
				"inputs":{"query":"string","limit":"int<=5"},
				"outputs":[{"title":"string","url":"string","snippet":"string"}],
				"constraints":["read_only","no_clickthrough","max_3_queries_per_task"]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("proposal_submit err = %v", err)
	}
	if !strings.Contains(submitOut, "[TOOL_PROPOSAL]") || !strings.Contains(submitOut, "review_status: proposed") {
		t.Fatalf("proposal_submit output = %q, want proposal summary", submitOut)
	}

	reviewOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"proposal_review",
			"proposal_id":"tp-1",
			"review_status":"approved"
		}`),
	)
	if err != nil {
		t.Fatalf("proposal_review err = %v", err)
	}
	if !strings.Contains(reviewOut, "[TOOL_PROPOSAL_UPDATED]") || !strings.Contains(reviewOut, "review_status: approved") {
		t.Fatalf("proposal_review output = %q, want approved status", reviewOut)
	}

	registerOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"register",
			"proposal_id":"tp-1",
			"implementation_ref":"tool/search_web.go"
		}`),
	)
	if err != nil {
		t.Fatalf("register err = %v", err)
	}
	if !strings.Contains(registerOut, "[REGISTERED_TOOL]") || !strings.Contains(registerOut, "registered: true") {
		t.Fatalf("register output = %q, want registered tool summary", registerOut)
	}

	exposeOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"exposure_set",
			"tool_name":"search_web",
			"principal":"idolum-email",
			"active":true
		}`),
	)
	if err != nil {
		t.Fatalf("exposure_set(active=true) err = %v", err)
	}
	if !strings.Contains(exposeOut, "[TOOL_EXPOSURE]") || !strings.Contains(exposeOut, "active: true") {
		t.Fatalf("exposure_set(active=true) output = %q, want active exposure", exposeOut)
	}

	accessOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"access_check",
			"tool_name":"search_web",
			"principal":"idolum-email"
		}`),
	)
	if err != nil {
		t.Fatalf("access_check(active) err = %v", err)
	}
	if !strings.Contains(accessOut, "allowed: true") {
		t.Fatalf("access_check(active) output = %q, want allowed true", accessOut)
	}

	if _, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"exposure_set",
			"tool_name":"search_web",
			"principal":"idolum-email",
			"active":false
		}`),
	); err != nil {
		t.Fatalf("exposure_set(active=false) err = %v", err)
	}

	accessOut, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"access_check",
			"tool_name":"search_web",
			"principal":"idolum-email"
		}`),
	)
	if err != nil {
		t.Fatalf("access_check(inactive) err = %v", err)
	}
	if !strings.Contains(accessOut, "allowed: false") {
		t.Fatalf("access_check(inactive) output = %q, want allowed false", accessOut)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !executionEventTypeExists(events, core.ExecutionEventToolProposalCreated) {
		t.Fatalf("missing %s event", core.ExecutionEventToolProposalCreated)
	}
	if !executionEventTypeExists(events, core.ExecutionEventToolProposalReviewed) {
		t.Fatalf("missing %s event", core.ExecutionEventToolProposalReviewed)
	}
	if !executionEventTypeExists(events, core.ExecutionEventToolRegistered) {
		t.Fatalf("missing %s event", core.ExecutionEventToolRegistered)
	}
	if !executionEventTypeExists(events, core.ExecutionEventToolExposureChanged) {
		t.Fatalf("missing %s event", core.ExecutionEventToolExposureChanged)
	}
}

func TestToolAuthorityRejectsExposureWhenToolNotRegistered(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"tool_authority",
		json.RawMessage(`{
			"action":"exposure_set",
			"tool_name":"search_web",
			"principal":"idolum-email",
			"active":true
		}`),
	)
	if err == nil {
		t.Fatal("exposure_set err = nil, want unregistered-tool error")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v, want unregistered tool error", err)
	}
}

func TestToolAuthorityIsAdminOnly(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 2002},
		adminSessionKey(),
		"tool_authority",
		json.RawMessage(`{"action":"proposal_list"}`),
	)
	if err == nil {
		t.Fatal("tool_authority err = nil, want admin-only denial")
	}
	if !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("err = %v, want admin-only denial", err)
	}
}

func executionEventTypeExists(events []session.ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == strings.TrimSpace(eventType) {
			return true
		}
	}
	return false
}
