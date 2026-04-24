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
)

func TestToolAuthorityInstallSetShowListAndRegisterGateForExternalTool(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "requires a verified install record") {
		t.Fatalf("register err = %v, want verified install requirement", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"installed","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(installed) err = %v", err)
	}
	if !strings.Contains(out, "status: installed") {
		t.Fatalf("install_set(installed) output = %q, want installed status", out)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1","probe_status":"failed","probe_output":"missing shared libs"}`))
	if err == nil || !strings.Contains(err.Error(), "requires probe_status=passed") {
		t.Fatalf("install_set(verified failed probe) err = %v, want passed probe requirement", err)
	}
	out, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1","probe_status":"passed","probe_output":"self-check ok"}`))
	if err != nil {
		t.Fatalf("install_set(verified) err = %v", err)
	}
	if !strings.Contains(out, "status: verified") || !strings.Contains(out, "probe_status: passed") {
		t.Fatalf("install_set(verified) output = %q, want verified + probe passed", out)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "attested_at:") {
		t.Fatalf("install_show output = %q, want attested_at", showOut)
	}
	listOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_list","status":"verified"}`))
	if err != nil {
		t.Fatalf("install_list err = %v", err)
	}
	if !strings.Contains(listOut, "browse_page status=verified") {
		t.Fatalf("install_list output = %q, want verified browse_page", listOut)
	}
	registerOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`))
	if err != nil {
		t.Fatalf("register after verified install err = %v", err)
	}
	if !strings.Contains(registerOut, "tool_name: browse_page") {
		t.Fatalf("register output = %q, want browse_page registration", registerOut)
	}
}

func TestToolAuthorityProbeRunUpdatesInstallRecordFromManifestProbe(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Probe:     ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"installed","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(installed) err = %v", err)
	}
	probeOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_run err = %v", err)
	}
	if !strings.Contains(probeOut, "probe_status: passed") {
		t.Fatalf("probe_run output = %q, want passed probe status", probeOut)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "last_probed_at:") || !strings.Contains(showOut, "probe_output: stdout:") {
		t.Fatalf("install_show output = %q, want persisted probe evidence", showOut)
	}
}

func TestToolAuthorityProbeRunMarksVerifiedExternalToolStaleOnFailure(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'broken'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Probe:     ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1","probe_status":"passed","probe_output":"self-check ok"}`))
	if err != nil {
		t.Fatalf("install_set(verified) err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "probe output did not contain expected text") {
		t.Fatalf("probe_run failure err = %v, want expected-text mismatch", err)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "status: stale") || !strings.Contains(showOut, "probe_status: failed") {
		t.Fatalf("install_show output = %q, want stale + failed probe after reprobe failure", showOut)
	}
}

func TestToolAuthorityInstallExecuteRunsManifestInstallCommandAndMarksInstalled(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	installScript := filepath.Join(registry.workspace, "install.sh")
	if err := os.WriteFile(installScript, []byte("#!/usr/bin/env bash\necho 'install ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(install.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Install:   ExternalToolManifestInstall{Command: []string{"./install.sh"}},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:tooling-v2"}`))
	if err != nil {
		t.Fatalf("install_set(pending) err = %v", err)
	}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_execute","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_execute err = %v", err)
	}
	if !strings.Contains(out, "status: installed") || !strings.Contains(out, "installed_at:") {
		t.Fatalf("install_execute output = %q, want installed status with timestamp", out)
	}
}

func TestToolAuthorityInstallExecuteMarksRecordFailedOnInstallError(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	installScript := filepath.Join(registry.workspace, "install.sh")
	if err := os.WriteFile(installScript, []byte("#!/usr/bin/env bash\necho 'nope' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(install.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "idolum-email",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Install:   ExternalToolManifestInstall{Command: []string{"./install.sh"}},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:tooling-v2"}`))
	if err != nil {
		t.Fatalf("install_set(pending) err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_execute","tool_name":"browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "install execution failed") {
		t.Fatalf("install_execute failure err = %v, want install execution failure", err)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "status: failed") {
		t.Fatalf("install_show output = %q, want failed status after install error", showOut)
	}
}
