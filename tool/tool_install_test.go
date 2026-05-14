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

func TestToolAuthorityInstallSetShowListAndRegisterGateForExternalTool(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Probe:     ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
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
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1","probe_status":"passed","probe_output":"self-check ok"}`))
	if err == nil || !strings.Contains(err.Error(), "no longer accepts probe_status or probe_output") {
		t.Fatalf("install_set(verified with inline probe) err = %v, want inline probe rejection", err)
	}
	auditOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("audit_run err = %v", err)
	}
	if !strings.Contains(auditOut, "status: passed") {
		t.Fatalf("audit_run output = %q, want passed status", auditOut)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err == nil || !strings.Contains(err.Error(), "requires a passed runtime-authored probe_run record") {
		t.Fatalf("install_set(verified without runtime probe) err = %v, want runtime-authored probe requirement", err)
	}
	probeOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_run err = %v", err)
	}
	if !strings.Contains(probeOut, "probe_status: passed") {
		t.Fatalf("probe_run output = %q, want passed probe status", probeOut)
	}
	out, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(verified) err = %v", err)
	}
	if !strings.Contains(out, "status: verified") || !strings.Contains(out, "probe_status: passed") || !strings.Contains(out, "baseline_fingerprint: sha256:") || !strings.Contains(out, "current_fingerprint: sha256:") {
		t.Fatalf("install_set(verified) output = %q, want verified + probe passed", out)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "attested_at:") || !strings.Contains(showOut, "baseline_fingerprint: sha256:") {
		t.Fatalf("install_show output = %q, want attested_at + fingerprint", showOut)
	}
	auditShowOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("audit_show err = %v", err)
	}
	if !strings.Contains(auditShowOut, "status: passed") || !strings.Contains(auditShowOut, "baseline_fingerprint: sha256:") {
		t.Fatalf("audit_show output = %q, want passed audit + fingerprint", auditShowOut)
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

func TestToolAuthorityInstallShowMarksExternalToolStaleOnFingerprintDrift(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"ok\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		IO:        ExternalToolManifestIO{OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`)},
		Probe:     ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	for _, input := range []string{
		`{"action":"install_set","tool_name":"browse_page","status":"installed","installer":"aphelion","install_ref":"workspace:tooling-v1"}`,
		`{"action":"audit_run","tool_name":"browse_page"}`,
		`{"action":"probe_run","tool_name":"browse_page"}`,
		`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1"}`,
		`{"action":"register","tool_name":"browse_page","implementation_ref":"external:browse_page"}`,
	} {
		if _, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "tool_authority", json.RawMessage(input)); err != nil {
			t.Fatalf("tool_authority input %s err = %v", input, err)
		}
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	beforeOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("browse_page before drift err = %v", err)
	}
	if beforeOut != `{"summary":"ok"}` {
		t.Fatalf("browse_page before drift output = %q, want manifest output", beforeOut)
	}
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{\"summary\":\"mutated\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh mutated) err = %v", err)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show after drift err = %v", err)
	}
	for _, needle := range []string{"status: stale", "baseline_fingerprint: sha256:", "current_fingerprint: sha256:", "drift_source: workspace_drift", "stale_reason: workspace_drift:"} {
		if !strings.Contains(showOut, needle) {
			t.Fatalf("install_show after drift output = %q, want %q", showOut, needle)
		}
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "external tool \"browse_page\" is stale: workspace_drift:") {
		t.Fatalf("browse_page after drift err = %v, want stale drift rejection", err)
	}
}

func TestToolAuthorityProbeRunUpdatesInstallRecordFromManifestProbe(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
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
	if !strings.Contains(probeOut, "rationale: probe_run passed against the declared probe command") || !strings.Contains(probeOut, "artifact_ref: file_path ") || !strings.Contains(probeOut, "probe.sh") {
		t.Fatalf("probe_run output = %q, want rendered rationale + probe script ref", probeOut)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "last_probed_at:") || !strings.Contains(showOut, "probe_output: stdout:") {
		t.Fatalf("install_show output = %q, want persisted probe evidence", showOut)
	}
	probeShowOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_show err = %v", err)
	}
	if !strings.Contains(probeShowOut, "status: passed") {
		t.Fatalf("probe_show output = %q, want passed canonical probe record", probeShowOut)
	}
	if !strings.Contains(probeShowOut, "rationale: probe_run passed against the declared probe command") || !strings.Contains(probeShowOut, "artifact_ref: file_path ") {
		t.Fatalf("probe_show output = %q, want rendered traceability", probeShowOut)
	}
	probeListOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_list"}`))
	if err != nil {
		t.Fatalf("probe_list err = %v", err)
	}
	if !strings.Contains(probeListOut, "browse_page status=passed") || !strings.Contains(probeListOut, "why=probe_run passed against the declared probe command") || !strings.Contains(probeListOut, "refs=1") {
		t.Fatalf("probe_list output = %q, want browse_page canonical probe row with traceability", probeListOut)
	}
}

func TestToolAuthorityProbeRunMarksVerifiedExternalToolStaleOnFailure(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
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
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("audit_run err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_run initial pass err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(verified) err = %v", err)
	}
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'broken'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh broken) err = %v", err)
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
		Owner:     "child-alpha",
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
	if !strings.Contains(out, "rationale: install_execute ran the manifest install command") || !strings.Contains(out, "artifact_ref: file_path ") || !strings.Contains(out, "install.sh") {
		t.Fatalf("install_execute output = %q, want rendered rationale + install script ref", out)
	}
	installListOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_list"}`))
	if err != nil {
		t.Fatalf("install_list err = %v", err)
	}
	if !strings.Contains(installListOut, "browse_page status=installed") || !strings.Contains(installListOut, "why=install_execute ran the manifest install command") || !strings.Contains(installListOut, "refs=1") {
		t.Fatalf("install_list output = %q, want compact traceability", installListOut)
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
		Owner:     "child-alpha",
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
	if !strings.Contains(showOut, "status: failed") || !strings.Contains(showOut, "consecutive_failures: 1") {
		t.Fatalf("install_show output = %q, want failed status with failure count after install error", showOut)
	}
	if !strings.Contains(showOut, "rationale: install_execute failed while running the manifest install command") || !strings.Contains(showOut, "artifact_ref: file_path ") || !strings.Contains(showOut, "install.sh") {
		t.Fatalf("install_show output = %q, want rendered rationale + install script ref", showOut)
	}
}

func TestToolAuthorityProcessPolicyCeilingsBlockInstallProbeAndInvoke(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	for name, body := range map[string]string{
		"install.sh": "#!/usr/bin/env bash\necho install ok\n",
		"probe.sh":   "#!/usr/bin/env bash\necho probe ok\n",
		"run.sh":     "#!/usr/bin/env bash\necho '{}'\n",
	} {
		if err := os.WriteFile(filepath.Join(registry.workspace, name), []byte(body), 0o755); err != nil {
			t.Fatalf("WriteFile(%s) err = %v", name, err)
		}
	}
	manifest := ExternalToolManifest{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
		Install:   ExternalToolManifestInstall{Command: []string{"./install.sh"}},
		Probe:     ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
		Constraints: ExternalToolManifestConstraints{
			Network: "allowlist",
		},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"pending","installer":"aphelion","install_ref":"workspace:policy"}`)); err != nil {
		t.Fatalf("install_set pending err = %v", err)
	}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"install_execute","tool_name":"browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "policy_violation: process-mode network=\"allowlist\" requires constraints.network_targets") {
		t.Fatalf("install_execute err = %v, want policy violation", err)
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "status: failed") || !strings.Contains(showOut, "drift_source: policy_violation") {
		t.Fatalf("install_show output = %q, want governed policy failure", showOut)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "browse_page", ImplementationRef: "external:browse_page", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	grantToolInvoke(t, store, "browse_page", "telegram:1001")
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "browse_page", json.RawMessage(`{"url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "policy_violation: process-mode network=\"allowlist\" requires constraints.network_targets") {
		t.Fatalf("browse_page invoke err = %v, want governed policy violation", err)
	}
}

func TestToolAuthorityAuditRunFailsWhenExecutionEntryIsMissing(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./missing-run.sh"},
	}})
	if err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"installed","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(installed) err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_run","tool_name":"browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "entry path does not exist") {
		t.Fatalf("audit_run missing entry err = %v, want missing entry path", err)
	}
	auditShowOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("audit_show err = %v", err)
	}
	if !strings.Contains(auditShowOut, "status: failed") || !strings.Contains(auditShowOut, "consecutive_failures: 1") {
		t.Fatalf("audit_show output = %q, want failed audit with failure count", auditShowOut)
	}
	if !strings.Contains(auditShowOut, "rationale: audit_run could not resolve the declared execution entry") || !strings.Contains(auditShowOut, "artifact_ref: file_path ") || !strings.Contains(auditShowOut, "missing-run.sh") {
		t.Fatalf("audit_show output = %q, want rendered rationale + missing entry ref", auditShowOut)
	}
	auditListOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_list"}`))
	if err != nil {
		t.Fatalf("audit_list err = %v", err)
	}
	if !strings.Contains(auditListOut, "browse_page status=failed") || !strings.Contains(auditListOut, "why=audit_run could not resolve the declared execution entry") || !strings.Contains(auditListOut, "refs=1") {
		t.Fatalf("audit_list output = %q, want compact traceability", auditListOut)
	}
}

func TestToolAuthorityAuditRunChecksRuntimeLoadability(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\nif then\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"installed","installer":"aphelion","install_ref":"workspace:syntax"}`)); err != nil {
		t.Fatalf("install_set(installed) err = %v", err)
	}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", json.RawMessage(`{"action":"audit_run","tool_name":"browse_page"}`))
	if err == nil || !strings.Contains(err.Error(), "loadability check failed") {
		t.Fatalf("audit_run err = %v, want shell loadability failure", err)
	}
}

func TestToolAuthorityRepeatedFailedProbeEscalatesStaleToFailed(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	if err := os.MkdirAll(registry.workspace, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	runScript := filepath.Join(registry.workspace, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(run.sh) err = %v", err)
	}
	probeScript := filepath.Join(registry.workspace, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh) err = %v", err)
	}
	_, err := registry.WithExternalToolManifests([]ExternalToolManifest{{
		Name:      "browse_page",
		Owner:     "child-alpha",
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
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"audit_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("audit_run err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_run initial pass err = %v", err)
	}
	_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_set","tool_name":"browse_page","status":"verified","installer":"aphelion","install_ref":"workspace:tooling-v1"}`))
	if err != nil {
		t.Fatalf("install_set(verified) err = %v", err)
	}
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'broken'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(probe.sh broken) err = %v", err)
	}
	for i := 0; i < 3; i++ {
		_, err = registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_run","tool_name":"browse_page"}`))
		if err == nil || !strings.Contains(err.Error(), "probe output did not contain expected text") {
			t.Fatalf("probe_run #%d err = %v, want expected-text mismatch", i+1, err)
		}
	}
	showOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"install_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("install_show err = %v", err)
	}
	if !strings.Contains(showOut, "status: failed") {
		t.Fatalf("install_show output = %q, want failed after repeated probe failures", showOut)
	}
	probeShowOut, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin}, key, "tool_authority", json.RawMessage(`{"action":"probe_show","tool_name":"browse_page"}`))
	if err != nil {
		t.Fatalf("probe_show err = %v", err)
	}
	if !strings.Contains(probeShowOut, "consecutive_failures: 3") {
		t.Fatalf("probe_show output = %q, want three consecutive failures", probeShowOut)
	}
	if !strings.Contains(probeShowOut, "rationale: probe_run failed against the declared probe command") || !strings.Contains(probeShowOut, "artifact_ref: file_path ") || !strings.Contains(probeShowOut, "probe.sh") {
		t.Fatalf("probe_show output = %q, want rendered rationale + probe ref", probeShowOut)
	}
}
