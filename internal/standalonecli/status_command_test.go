//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStatusCommandKVDegradesWhenBuildRevisionUnknown(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	metaPath := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(metaPath, []byte(`{"latest_version":"v0.2.2","installed_version":"v0.2.2","checked_at":"2026-06-04T14:38:27Z","source":"test"}`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	info := readVersionInfo()
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
		versions:  map[string]versionInfo{execPath: info},
	}
	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath}, statusCommandOptions{
			Runner:   fake.run,
			Readlink: fake.readlink,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return fake.versions[path], nil
			},
			MetadataPath: metaPath,
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand() err = %v", err)
	}
	for _, want := range []string{
		"action: status",
		"status: degraded",
		"config_path: " + configPath,
		"service_main_pid: 123",
		"service_running_exec: " + execPath,
		"service_binary_matches: false",
		"release_status_class: current",
		"release_installed_version: v0.2.2",
		"next_action: run doctor",
		"running service binary does not match expected binary",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q in %q", want, out)
		}
	}
	if fake.called("restart") || fake.called("install") || fake.called("verify-deploy") {
		t.Fatalf("status command invoked mutating command: %#v", fake.calls)
	}
}

func TestRunStatusCommandJSONDegradedForDuplicateUnits(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=/opt/aphelion ; argv[]=/opt/aphelion }\n",
		unitList:  "aphelion.service loaded active running Aphelion\naphelion-v013-deploy.service loaded failed failed old\n",
		unitFiles: "aphelion.service enabled\naphelion-main-redeploy-1779159152.service disabled\n",
		readlinks: map[string]string{"/proc/123/exe": "/opt/aphelion"},
		versions:  map[string]versionInfo{"/opt/aphelion": {Version: "v0.2.2", VCSRevision: "abc123"}},
	}
	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath, "--format=json"}, statusCommandOptions{
			Runner:   fake.run,
			Readlink: fake.readlink,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return fake.versions[path], nil
			},
			MetadataPath: filepath.Join(t.TempDir(), "missing.json"),
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand(--format=json) err = %v", err)
	}
	var got statusSnapshot
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(status) err = %v; output=%q", err, out)
	}
	if got.Status != "degraded" || got.NextAction != "run doctor" {
		t.Fatalf("status=%q next=%q, want degraded/run doctor", got.Status, got.NextAction)
	}
	if !statusIssueCodePresent(got.IssueRecords, "duplicate_primary_units") || !statusIssueCodePresent(got.IssueRecords, "service_binary_mismatch") {
		t.Fatalf("issue records = %#v, want typed duplicate and binary-mismatch issues", got.IssueRecords)
	}
	wantUnits := strings.Join(got.DuplicateUnits, ",")
	if !strings.Contains(wantUnits, "aphelion-main-redeploy-1779159152.service") || !strings.Contains(wantUnits, "aphelion-v013-deploy.service") {
		t.Fatalf("duplicate units = %#v, want both stale units", got.DuplicateUnits)
	}
}

func TestRunStatusCommandProjectsReleaseUpdateAlongsideServiceConsistency(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	metaPath := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(metaPath, []byte(`{"latest_version":"v0.3.0","installed_version":"v0.2.0","checked_at":"2026-06-04T14:38:27Z","source":"test"}`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	build := versionInfo{Version: "v0.2.0", VCSRevision: "abc123", VCSModified: "false"}
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
	}

	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath, "--format=json"}, statusCommandOptions{
			Runner:       fake.run,
			Readlink:     fake.readlink,
			BuildVersion: build,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return build, nil
			},
			MetadataPath: metaPath,
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand(source metadata) err = %v", err)
	}
	var got statusSnapshot
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(status) err = %v; output=%q", err, out)
	}
	if got.Status != "ready" || got.NextAction != "none" {
		t.Fatalf("status=%q next=%q service=%#v issues=%#v, want ready/none when service matches source despite newer release metadata", got.Status, got.NextAction, got.Service, got.IssueRecords)
	}
	if got.Release.SourceStatus != "release_update_available" {
		t.Fatalf("release source status = %q, want release_update_available", got.Release.SourceStatus)
	}
	if got.Release.CurrentRevision != "abc123" || got.Release.RunningRevision != "abc123" || got.Release.ExpectedRevision != "abc123" {
		t.Fatalf("release revisions = current %q running %q expected %q, want abc123/abc123/abc123", got.Release.CurrentRevision, got.Release.RunningRevision, got.Release.ExpectedRevision)
	}
	if got.Release.StatusClass != "operational_tension" || got.Release.FailureClass != "release_freshness" || got.Release.RetryPolicy != "reinstall_or_restart_service" {
		t.Fatalf("release classification = %#v, want operational release freshness install/restart guidance", got.Release)
	}
	if got.Release.ServiceStatus != "source_service_consistent" || got.Release.ServiceClass != "current" ||
		got.Release.ServiceFailure != "none" || got.Release.ServiceRetry != "none" || got.Release.ServiceNext != "none" {
		t.Fatalf("service axis = %#v, want quiet source service consistency", got.Release)
	}
	if got.Release.FreshnessStatus != "release_update_available" || got.Release.FreshnessClass != "operational_tension" ||
		got.Release.FreshnessFailure != "release_freshness" || got.Release.FreshnessRetry != "reinstall_or_restart_service" {
		t.Fatalf("freshness axis = %#v, want release update availability", got.Release)
	}
	if !strings.Contains(got.Release.NextAction, "newer release") {
		t.Fatalf("release next action = %q, want install guidance", got.Release.NextAction)
	}
	if statusIssueCodePresent(got.IssueRecords, "release_update_available") {
		t.Fatalf("issue records = %#v, did not want release update to degrade coherent source/service status", got.IssueRecords)
	}
}

func TestRunStatusCommandKeepsBinaryMismatchVisibleWithMalformedMetadata(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	metaPath := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(metaPath, []byte(`{"latest_version":`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	build := versionInfo{Version: "v0.2.0", VCSRevision: "abc123", VCSModified: "false"}
	running := versionInfo{Version: "v0.1.0", VCSRevision: "def456", VCSModified: "false"}
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
	}

	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath, "--format=json"}, statusCommandOptions{
			Runner:       fake.run,
			Readlink:     fake.readlink,
			BuildVersion: build,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return running, nil
			},
			MetadataPath: metaPath,
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand(malformed metadata) err = %v", err)
	}
	var got statusSnapshot
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(status) err = %v; output=%q", err, out)
	}
	if got.Status != "degraded" || got.NextAction != "run doctor" {
		t.Fatalf("status=%q next=%q, want degraded/run doctor", got.Status, got.NextAction)
	}
	if got.Release.SourceStatus != "source_install_revision_mismatch" || got.Release.ServiceStatus != "source_install_revision_mismatch" {
		t.Fatalf("release/service status = %q/%q, want source_install_revision_mismatch", got.Release.SourceStatus, got.Release.ServiceStatus)
	}
	if got.Release.FreshnessStatus != "release_metadata_unreadable" {
		t.Fatalf("freshness status = %q, want release_metadata_unreadable", got.Release.FreshnessStatus)
	}
	if got.Release.StatusClass != "operational_tension" || got.Release.FailureClass != "source_install_revision_mismatch" ||
		got.Release.RetryPolicy != "reinstall_or_restart_service" {
		t.Fatalf("release classification = %#v, want concrete service mismatch to drive overall", got.Release)
	}
	if !statusIssueCodePresent(got.IssueRecords, "service_binary_mismatch") || !statusIssueCodePresent(got.IssueRecords, "release_metadata_unreadable") {
		t.Fatalf("issue records = %#v, want both service mismatch and metadata unreadable", got.IssueRecords)
	}
}

func TestRunStatusCommandCurrentSourceInstallHasQuietClassification(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	metaPath := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(metaPath, []byte(`{"latest_version":"v0.2.0","installed_version":"v0.2.0","checked_at":"2026-06-04T14:38:27Z","source":"test"}`), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	build := versionInfo{Version: "v0.2.0", VCSRevision: "abc123", VCSModified: "false"}
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
	}

	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath, "--format=json"}, statusCommandOptions{
			Runner:       fake.run,
			Readlink:     fake.readlink,
			BuildVersion: build,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return build, nil
			},
			MetadataPath: metaPath,
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand(current source) err = %v", err)
	}
	var got statusSnapshot
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(status) err = %v; output=%q", err, out)
	}
	if got.Status != "ready" || got.NextAction != "none" || len(got.IssueRecords) != 0 {
		t.Fatalf("status=%q next=%q issues=%#v, want quiet ready status", got.Status, got.NextAction, got.IssueRecords)
	}
	if got.Release.StatusClass != "current" || got.Release.FailureClass != "none" || got.Release.RetryPolicy != "none" || got.Release.NextAction != "none" {
		t.Fatalf("release classification = %#v, want quiet current classification", got.Release)
	}
	if got.Release.ServiceStatus != "source_service_consistent" || got.Release.ServiceClass != "current" ||
		got.Release.ServiceFailure != "none" || got.Release.ServiceRetry != "none" || got.Release.ServiceNext != "none" {
		t.Fatalf("service axis = %#v, want quiet current classification", got.Release)
	}
	if got.Release.FreshnessStatus != "release_status_current" || got.Release.FreshnessClass != "current" ||
		got.Release.FreshnessFailure != "none" || got.Release.FreshnessRetry != "none" || got.Release.FreshnessNext != "none" {
		t.Fatalf("freshness axis = %#v, want quiet current classification", got.Release)
	}
}

func TestRunStatusCommandRejectsHumanFormat(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	if err := runStatusCommandWithOptions([]string{"--config", configPath, "--format=human"}, statusCommandOptions{}); err == nil {
		t.Fatal("runStatusCommand(--format=human) err = nil, want unsupported format")
	} else if !strings.Contains(err.Error(), "use kv or json") {
		t.Fatalf("err = %v, want kv/json guidance", err)
	}
}

type statusFakeService struct {
	show      string
	unitList  string
	unitFiles string
	readlinks map[string]string
	versions  map[string]versionInfo
	calls     []string
}

func (f *statusFakeService) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if name == "systemctl" && strings.Join(args, " ") == "--user list-units --all --no-legend --plain" {
		return []byte(f.unitList), nil
	}
	if name == "systemctl" && strings.Join(args, " ") == "--user list-unit-files --no-legend --plain" {
		return []byte(f.unitFiles), nil
	}
	if name == "systemctl" && strings.Contains(strings.Join(args, " "), "--user show aphelion") {
		return []byte(f.show), nil
	}
	return []byte(""), nil
}

func (f *statusFakeService) readlink(path string) (string, error) {
	return f.readlinks[path], nil
}

func (f *statusFakeService) called(fragment string) bool {
	for _, call := range f.calls {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

func writeMinimalStatusConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "aphelion.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

[agent]
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"

[tools]
external_manifest_dir = "./external-tools"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func TestRunStatusCommandDegradesWhenVersionOrRevisionUnknown(t *testing.T) {
	configPath := writeMinimalStatusConfig(t)
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
		versions:  map[string]versionInfo{execPath: {Version: "", VCSRevision: ""}},
	}
	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath}, statusCommandOptions{
			Runner:   fake.run,
			Readlink: fake.readlink,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return fake.versions[path], nil
			},
			MetadataPath: filepath.Join(t.TempDir(), "missing.json"),
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand() err = %v", err)
	}
	for _, want := range []string{"status: degraded", "service_binary_matches: false", "running service binary does not match expected binary"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q in %q", want, out)
		}
	}
}

func TestRunStatusCommandReturnsDegradedPacketForConfigLoadFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "missing.toml")
	execPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	info := readVersionInfo()
	fake := statusFakeService{
		show:      "MainPID=123\nExecStart={ path=" + execPath + " ; argv[]=" + execPath + " --config " + configPath + " }\n",
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		readlinks: map[string]string{"/proc/123/exe": execPath},
		versions:  map[string]versionInfo{execPath: info},
	}
	out, err := captureStandaloneStdout(t, func() error {
		return runStatusCommandWithOptions([]string{"--config", configPath, "--format=json"}, statusCommandOptions{
			Runner:   fake.run,
			Readlink: fake.readlink,
			ExecVersion: func(ctx context.Context, path string) (versionInfo, error) {
				return fake.versions[path], nil
			},
			MetadataPath: filepath.Join(t.TempDir(), "missing-release.json"),
		})
	})
	if err != nil {
		t.Fatalf("runStatusCommand(config failure) err = %v, want degraded packet", err)
	}
	var got statusSnapshot
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal(status) err = %v; output=%q", err, out)
	}
	if got.Status != "degraded" || got.ConfigPath != configPath {
		t.Fatalf("status=%q config=%q, want degraded config path %q", got.Status, got.ConfigPath, configPath)
	}
	if !strings.Contains(strings.Join(got.Issues, ";"), "config load failed") {
		t.Fatalf("issues = %#v, want config load failed", got.Issues)
	}
}

func statusIssueCodePresent(records []statusIssue, code string) bool {
	for _, record := range records {
		if record.Code == code {
			return true
		}
	}
	return false
}
