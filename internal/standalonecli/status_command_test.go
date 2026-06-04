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

func TestRunStatusCommandKVReady(t *testing.T) {
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
		"status: ready",
		"config_path: " + configPath,
		"service_main_pid: 123",
		"service_running_exec: " + execPath,
		"service_binary_matches: true",
		"release_installed_version: v0.2.2",
		"next_action: none",
		"issues: none",
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
	wantUnits := strings.Join(got.DuplicateUnits, ",")
	if !strings.Contains(wantUnits, "aphelion-main-redeploy-1779159152.service") || !strings.Contains(wantUnits, "aphelion-v013-deploy.service") {
		t.Fatalf("duplicate units = %#v, want both stale units", got.DuplicateUnits)
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
