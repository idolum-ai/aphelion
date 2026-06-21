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

func TestExternalContainerAndWorkspaceRunnerModesAreNotProcessExecutable(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"container", "workspace_runner"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			registry, store := newDurableAgentToolRegistry(t)
			if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
				t.Fatalf("MkdirAll(workspace) err = %v", err)
			}
			marker := filepath.Join(registry.workspace, "executed.txt")
			script := filepath.Join(registry.workspace, "run.sh")
			if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\nprintf executed > executed.txt\necho '{\"summary\":\"ran\"}'\n"), 0o755); err != nil {
				t.Fatalf("WriteFile(run.sh) err = %v", err)
			}
			toolName := mode + "_tool"
			manifest := ExternalToolManifest{
				Name:      toolName,
				Owner:     "child-alpha",
				Execution: ExternalToolManifestExecution{Mode: mode, Entry: "./run.sh"},
				Constraints: ExternalToolManifestConstraints{
					Network: "none",
				},
			}
			if (defaultExternalToolExecutor{}).Supports(manifest) {
				t.Fatalf("defaultExternalToolExecutor.Supports(%q) = true, want false", mode)
			}
			if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
				t.Fatalf("WithExternalToolManifests() err = %v", err)
			}
			if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: toolName, ImplementationRef: "external:" + toolName, Registered: true}); err != nil {
				t.Fatalf("UpsertRegisteredTool() err = %v", err)
			}
			grantToolInvoke(t, store, toolName, "telegram:1001")

			actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
			key := adminSessionKey()
			ctx := authorityRunContextForPrincipal(t, store, key, actor)
			_, err := registry.ExecuteForSessionPrincipal(ctx, actor, key, toolName, json.RawMessage(`{"url":"https://example.com"}`))
			if err == nil {
				t.Fatal("ExecuteForSessionPrincipal() err = nil, want non-executable error")
			}
			if !strings.Contains(err.Error(), "present in the manifest but not yet executable") {
				t.Fatalf("err = %v, want clean non-executable error", err)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("process-looking %s manifest executed marker file; stat err = %v", mode, statErr)
			}
		})
	}
}

func TestExternalContainerModeUsesContainerAuditAndDrift(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	healthScript := filepath.Join(registry.workspace, "health.sh")
	if err := os.WriteFile(healthScript, []byte("#!/usr/bin/env bash\necho 'container healthy'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(health.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	manifest := ExternalToolManifest{
		Name:      "container_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "container", Entry: "example/container-tool:1"},
		Container: ExternalToolManifestContainer{
			Image:    "example/container-tool:1",
			Digest:   "sha256:111",
			BuildRef: "oci:container-tool@sha256:111",
			Healthcheck: ExternalToolManifestContainerHealth{
				Command:                []string{"./health.sh"},
				ExpectedOutputContains: "container healthy",
			},
		},
		Probe: ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
		Constraints: ExternalToolManifestConstraints{
			Network: "none",
		},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	for _, input := range []string{
		`{"action":"install_set","tool_name":"container_tool","status":"installed","installer":"aphelion","install_ref":"oci:container-tool@sha256:111"}`,
		`{"action":"audit_run","tool_name":"container_tool"}`,
		`{"action":"probe_run","tool_name":"container_tool"}`,
		`{"action":"install_set","tool_name":"container_tool","status":"verified","installer":"aphelion","install_ref":"oci:container-tool@sha256:111"}`,
	} {
		if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(input)); err != nil {
			t.Fatalf("tool_authority input %s err = %v", input, err)
		}
	}
	auditOut, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"audit_show","tool_name":"container_tool"}`))
	if err != nil {
		t.Fatalf("audit_show err = %v", err)
	}
	if !strings.Contains(auditOut, "container_image: example/container-tool:1") || !strings.Contains(auditOut, "rationale: audit_run resolved the declared container image and health check") {
		t.Fatalf("audit_show output = %q, want container audit evidence", auditOut)
	}

	registry.externalManifests[0].Container.Digest = "sha256:222"
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"container_tool"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "status: stale") || !strings.Contains(showOut, "drift_source: container_drift") || !strings.Contains(showOut, "stale_reason: container_drift:") {
		t.Fatalf("install_show output = %q, want typed container drift", showOut)
	}
}
