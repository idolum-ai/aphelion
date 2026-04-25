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
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestExternalProcessExecutorRunsManifestBackedTool(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\nread INPUT\necho '{\"summary\":\"ok\",\"seen\":true}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO: ExternalToolManifestIO{
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"},"seen":{"type":"boolean"}},"required":["summary"]}`),
		},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if out != `{"summary":"ok","seen":true}` {
		t.Fatalf("out = %q, want structured json output", out)
	}
}

func TestExternalProcessExecutorUsesSandboxRunnerForApprovedUser(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 42}
	scope, err := registry.sandbox.Resolve(actor)
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(working root) err = %v", err)
	}
	script := filepath.Join(scope.WorkingRoot, "run.sh")
	if err := os.WriteFile(script, []byte(`#!/usr/bin/env bash
if [[ "${APHELION_FAKE_BWRAP:-}" == "1" ]]; then
  echo '{"summary":"sandbox-runner"}'
else
  echo '{"summary":"direct-host"}'
fi
`), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
	}
	_, err = registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, scope)
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:42")

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, adminSessionKey(), "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v", err)
	}
	if out != `{"summary":"sandbox-runner"}` {
		t.Fatalf("out = %q, want sandbox runner path", out)
	}
}

func TestExternalProcessExecutorRejectsInvalidInputAgainstSchema(t *testing.T) {
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
		IO:        ExternalToolManifestIO{InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`)},
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	seedVerifiedExternalToolLifecycle(t, registry, store, manifest, sandbox.Scope{WorkingRoot: registry.workspace})
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), "browse_page", json.RawMessage(`{"goal":"summarize"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want input-schema rejection")
	}
	if !strings.Contains(err.Error(), `missing required field "url"`) {
		t.Fatalf("err = %v, want input-schema rejection", err)
	}
}

func TestExternalProcessExecutorRejectsInvalidOutputAgainstSchema(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	script := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho '{\"summary\":7}'\n"), 0o755); err != nil {
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
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")

	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want output-schema rejection")
	}
	if !strings.Contains(err.Error(), "output.summary must be a string") {
		t.Fatalf("err = %v, want output-schema rejection", err)
	}
}
