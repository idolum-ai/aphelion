//go:build linux

package tool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDurableAgentPrincipalFallbackEnablesChildRunToolIdentity(t *testing.T) {
	registry := NewRegistryWithSandbox(t.TempDir(), time.Second, mustSandboxResolver(t)).WithDurableAgentPrincipalFallback()
	p := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	if !registry.SupportsPrincipal(p) {
		t.Fatal("SupportsPrincipal(durable agent) = false, want fallback support for child-run registry")
	}
	scope, err := registry.scopeForPrincipalToolExecution(p)
	if err != nil {
		t.Fatalf("scopeForPrincipalToolExecution() err = %v", err)
	}
	if scope.Principal.Role != principal.RoleDurableAgent || scope.Principal.DurableAgentID != "child-alpha" {
		t.Fatalf("scope principal = %#v, want durable child identity", scope.Principal)
	}
}

func TestDurableAgentExternalProcessToolRequiresIsolatedSandbox(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentPrincipalFallback()
	registry.WithRunner(sandbox.NewRunnerWithLookPath(func(string) (string, error) {
		return "", errors.New("bubblewrap missing")
	}))
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	marker := filepath.Join(registry.workspace, "host-ran.txt")
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\nprintf host > host-ran.txt\necho '{\"summary\":\"host\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
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
	grantToolInvoke(t, store, "browse_page", "durable_agent:child-alpha")

	actor := principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"}
	if toolDefExists(registry.DefinitionsForPrincipal(actor), "browse_page") {
		t.Fatal("DefinitionsForPrincipal included process external tool without isolated sandbox")
	}
	key := adminSessionKey()
	ctx := authorityRunContextForPrincipal(t, store, key, actor)
	_, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if !errors.Is(err, ErrSandboxRequired) {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v, want ErrSandboxRequired", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("external process marker exists despite missing sandbox; stat err = %v", statErr)
	}
}

func mustSandboxResolver(t *testing.T) *sandbox.Resolver {
	t.Helper()
	resolver, err := sandbox.NewResolver(sandbox.Roots{GlobalRoot: t.TempDir(), AdminExecRoot: t.TempDir(), SharedMemoryRoot: t.TempDir(), UserWorkspaceRoot: t.TempDir(), UserMemoryRoot: t.TempDir()}, sandbox.DefaultProfiles())
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}
