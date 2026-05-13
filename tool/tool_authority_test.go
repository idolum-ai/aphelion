//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
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
	if containsString(names, "tool_request") || containsString(names, "search_web") {
		t.Fatalf("definitions without store = %#v, do not want removed tool surfaces", names)
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
	if containsString(names, "tool_request") || containsString(names, "search_web") {
		t.Fatalf("definitions with store = %#v, do not want removed tool surfaces", names)
	}
}

func TestToolAuthorityDefinitionDoesNotAdvertiseDeprecatedInlineProbeFields(t *testing.T) {
	t.Parallel()

	store := newToolTestStore(t)
	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	var raw json.RawMessage
	for _, def := range registry.Definitions() {
		if def.Name == "tool_authority" {
			raw = def.Parameters
			break
		}
	}
	if len(raw) == 0 {
		t.Fatal("tool_authority definition not found")
	}
	if strings.Contains(string(raw), "probe_status") || strings.Contains(string(raw), "probe_output") {
		t.Fatalf("tool_authority schema advertises inline probe fields: %s", string(raw))
	}
}

func TestToolAuthorityRegisterAndGrantAccessFlow(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})

	registerOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{
			"action":"register",
			"tool_name":"browse_page",
			"implementation_ref":"external:browse_page"
		}`),
	)
	if err != nil {
		t.Fatalf("register err = %v", err)
	}
	if !strings.Contains(registerOut, "[REGISTERED_TOOL]") || !strings.Contains(registerOut, "registered: true") {
		t.Fatalf("register output = %q, want registered tool summary", registerOut)
	}

	accessOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{"action":"access_check","tool_name":"browse_page","principal":"telegram:1001"}`),
	)
	if err != nil {
		t.Fatalf("access_check(before grant) err = %v", err)
	}
	if !strings.Contains(accessOut, "allowed: false") {
		t.Fatalf("access_check(before grant) output = %q, want allowed false", accessOut)
	}

	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	accessOut, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		key,
		"tool_authority",
		json.RawMessage(`{"action":"access_check","tool_name":"browse_page","principal":"telegram:1001"}`),
	)
	if err != nil {
		t.Fatalf("access_check(after grant) err = %v", err)
	}
	if !strings.Contains(accessOut, "allowed: true") || !strings.Contains(accessOut, "capability_grant_active: true") {
		t.Fatalf("access_check(after grant) output = %q, want grant-backed access", accessOut)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !executionEventTypeExists(events, core.ExecutionEventToolRegistered) {
		t.Fatalf("missing %s event", core.ExecutionEventToolRegistered)
	}
}

func TestToolAuthorityRegisterRejectsUnknownRuntimeTool(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"tool_authority",
		json.RawMessage(`{
			"action":"register",
			"tool_name":"imaginary_tool",
			"implementation_ref":"tool/imaginary_tool.go"
		}`),
	)
	if err == nil {
		t.Fatal("register err = nil, want unknown-tool rejection")
	}
	if !strings.Contains(err.Error(), "not a known runtime tool definition") {
		t.Fatalf("err = %v, want known runtime tool definition error", err)
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
		json.RawMessage(`{"action":"registered_list"}`),
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
