//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDefinitionsForPrincipalFiltersExternalToolByExposure(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertToolExposure(session.ToolExposure{ToolName: "browse_page", Principal: "idolum-email", Active: true}); err != nil {
		t.Fatalf("UpsertToolExposure() err = %v", err)
	}

	exposed := registry.DefinitionsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "idolum-email"})
	if !toolDefExists(exposed, "browse_page") {
		t.Fatalf("DefinitionsForPrincipal(exposed) missing browse_page: %#v", exposed)
	}
	hidden := registry.DefinitionsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "other-agent"})
	if toolDefExists(hidden, "browse_page") {
		t.Fatalf("DefinitionsForPrincipal(unexposed) included browse_page: %#v", hidden)
	}
}

func TestExternalToolRequiresRegistrationAndExposureAtInvocation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unregistered browse_page err = %v, want not registered", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("unexposed browse_page err = %v, want not exposed", err)
	}
	if _, err := store.UpsertToolExposure(session.ToolExposure{ToolName: "browse_page", Principal: "telegram:1001", Active: true}); err != nil {
		t.Fatalf("UpsertToolExposure() err = %v", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("exposed browse_page err = %v", err)
	}
	if out != `{"summary":"ok"}` {
		t.Fatalf("out = %q, want manifest-backed output", out)
	}
}

func TestManifestForPrincipalIncludesOnlyExposedExternalTools(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "container", Entry: "ghcr.io/idolum/email-browser-tool:pilot"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertToolExposure(session.ToolExposure{ToolName: "browse_page", Principal: "idolum-email", Active: true}); err != nil {
		t.Fatalf("UpsertToolExposure() err = %v", err)
	}

	visible := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "idolum-email"})
	if !strings.Contains(visible, "- browse_page: external tool owned by idolum-email") {
		t.Fatalf("visible manifest = %q, want exposed external tool", visible)
	}
	if !strings.Contains(visible, "executable: false") {
		t.Fatalf("visible manifest = %q, want non-executable container notice", visible)
	}
	hidden := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "other-agent"})
	if strings.Contains(hidden, "browse_page") {
		t.Fatalf("hidden manifest = %q, do not want unexposed external tool", hidden)
	}
}

func TestToolAuthorityRegisterRejectsNonAuthorityManagedKnownTool(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"register","tool_name":"memory","implementation_ref":"noop"}`))
	if err == nil {
		t.Fatal("register err = nil, want non-authority-managed rejection")
	}
	if !strings.Contains(err.Error(), "not an authority-managed runtime tool") {
		t.Fatalf("err = %v, want non-authority-managed rejection", err)
	}
}
