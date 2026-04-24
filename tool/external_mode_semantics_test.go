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
				Owner:     "idolum-email",
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
			if _, err := store.UpsertToolExposure(session.ToolExposure{ToolName: toolName, Principal: "telegram:1001", Active: true}); err != nil {
				t.Fatalf("UpsertToolExposure() err = %v", err)
			}

			_, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), toolName, json.RawMessage(`{"url":"https://example.com"}`))
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
