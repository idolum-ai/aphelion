//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDefinitionsForPrincipalFiltersExternalToolByGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "child-alpha",
		ChannelKind:       "external_channel",
		Status:            "active",
		LocalStorageRoots: []string{registry.workspace, filepath.Join(registry.workspace, "memory")},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "child-alpha")

	granted := registry.DefinitionsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"})
	if !toolDefExists(granted, "browse_page") {
		t.Fatalf("DefinitionsForPrincipal(granted) missing browse_page: %#v", granted)
	}
	hidden := registry.DefinitionsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "other-agent"})
	if toolDefExists(hidden, "browse_page") {
		t.Fatalf("DefinitionsForPrincipal(ungranted) included browse_page: %#v", hidden)
	}
}

func TestExternalToolRequiresRegistrationAndGrantAtInvocation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
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
	if err == nil || !strings.Contains(err.Error(), "queued review request") {
		t.Fatalf("ungranted browse_page err = %v, want queued missing-grant review", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("granted browse_page err = %v", err)
	}
	if out != `{"summary":"ok"}` {
		t.Fatalf("out = %q, want manifest-backed output", out)
	}
}

func TestManifestForPrincipalIncludesOnlyGrantedExternalTools(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "container", Entry: "ghcr.io/idolum/child-browser-tool:pilot"},
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "child-alpha")

	visible := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"})
	if !strings.Contains(visible, "- browse_page: external tool owned by child-alpha") {
		t.Fatalf("visible manifest = %q, want granted external tool", visible)
	}
	if !strings.Contains(visible, "executable: false") {
		t.Fatalf("visible manifest = %q, want non-executable container notice", visible)
	}
	hidden := registry.ManifestForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "other-agent"})
	if strings.Contains(hidden, "browse_page") {
		t.Fatalf("hidden manifest = %q, do not want ungranted external tool", hidden)
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

func TestExternalToolGrantToolInvocationScopeBlocksSelectorBeforeExecution(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.py")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env python3
import json
import pathlib
import sys
payload=json.load(sys.stdin)
with pathlib.Path('executions.jsonl').open('a', encoding='utf-8') as f:
    f.write(json.dumps(payload, sort_keys=True)+'\n')
print(json.dumps({'summary':'ok','action':payload.get('action'),'username':payload.get('username')}, sort_keys=True))
`), 0o755); err != nil {
		t.Fatalf("WriteFile(run.py) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "public-feed-readonly",
		Owner:     "child-public-feed",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.py"},
		IO: ExternalToolManifestIO{
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"username":{"type":"string"}},"required":["action"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"action":{"type":"string"},"username":{"type":"string"}},"required":["summary"]}`),
		},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: manifest.Name, ImplementationRef: "external:" + manifest.Name, Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "child-public-feed",
		ChannelKind:       "external_channel",
		Status:            "active",
		LocalStorageRoots: []string{registry.workspace, filepath.Join(registry.workspace, "memory")},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-x-profile-scope",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "durable_agent:child-public-feed",
		Kind:           session.CapabilityKindTool,
		TargetResource: manifest.Name,
		AllowedActions: []string{"invoke"},
		Constraints:    `{"tool_invocation":{"actions":{"public_profile_metadata_read":{"selectors":{"username":["example_handle"]}}}}}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-public-feed"}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, manifest.Name, json.RawMessage(`{"action":"public_profile_metadata_read","username":"example_handle"}`))
	if err != nil {
		t.Fatalf("allowed scoped invoke err = %v", err)
	}
	if !strings.Contains(out, `"summary": "ok"`) && !strings.Contains(out, `"summary":"ok"`) {
		t.Fatalf("allowed scoped invoke output = %q, want script output", out)
	}
	_, err = registry.ExecuteForSessionPrincipal(ctx, actor, key, manifest.Name, json.RawMessage(`{"action":"public_profile_metadata_read","username":"other"}`))
	if err == nil || !strings.Contains(err.Error(), "selector") || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("blocked scoped invoke err = %v, want selector not allowed", err)
	}

	raw, err := os.ReadFile(filepath.Join(registry.workspace, "executions.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(executions.jsonl) err = %v", err)
	}
	if got := strings.Count(string(raw), "public_profile_metadata_read"); got != 1 {
		t.Fatalf("executions log = %q, want exactly one external execution", string(raw))
	}
	grant, ok, err := store.CapabilityGrant("capg-x-profile-scope")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || grant.InvocationCount != 2 || grant.FailureCount != 1 {
		t.Fatalf("grant counters = %#v ok=%t, want one successful invocation and one blocked attempt", grant, ok)
	}
}
