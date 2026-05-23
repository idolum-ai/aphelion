//go:build linux

package maintenancecli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSandboxNetCheckCommandKV(t *testing.T) {
	t.Parallel()

	configPath := writeSandboxNetTestConfig(t)
	out, err := captureStdout(t, func() error {
		return runSandboxNetCommand([]string{"check", "--config", configPath, "--format=kv"})
	})
	if err != nil {
		t.Fatalf("runSandboxNetCommand(check) err = %v", err)
	}
	for _, want := range []string{
		"action: sandbox-net check",
		"backend:",
		"backend_available:",
		"profile_admin_mode: trusted",
		"profile_approved_user_network: deny",
		"profile_durable_agent_network: deny",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sandbox-net check output missing %q:\n%s", want, out)
		}
	}
}

func TestRunSandboxNetCheckRejectsInvalidAllowlistConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "aphelion.toml")
	raw := minimalSandboxNetTestConfig() + `
[sandbox.profiles.approved_user]
mode = "isolated"
network = "allowlist"
network_allow = ["example.com"]
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := captureStdout(t, func() error {
		return runSandboxNetCommand([]string{"check", "--config", configPath, "--format=kv"})
	})
	if err == nil {
		t.Fatal("runSandboxNetCommand(check invalid) err = nil, want config validation error")
	}
	if !strings.Contains(err.Error(), "sandbox.profiles.approved_user.network_allow[0]") {
		t.Fatalf("err = %v, want network_allow validation", err)
	}
}

func TestRunSandboxNetHelperServeRejectsInvalidMode(t *testing.T) {
	t.Parallel()

	err := runSandboxNetCommand([]string{"helper", "serve", "--socket-mode", "not-octal"})
	if err == nil {
		t.Fatal("runSandboxNetCommand(helper serve) err = nil, want invalid mode rejection")
	}
	if !strings.Contains(err.Error(), "must be octal") {
		t.Fatalf("err = %v, want octal mode rejection", err)
	}
}

func writeSandboxNetTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "aphelion.toml")
	if err := os.WriteFile(configPath, []byte(minimalSandboxNetTestConfig()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func minimalSandboxNetTestConfig() string {
	return `
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
`
}
