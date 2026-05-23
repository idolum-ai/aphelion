//go:build linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParsesGitHubAppConfigAndHidesKeyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(keyPath, []byte("test key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	configPath := filepath.Join(dir, "config.toml")
	raw := minimalGitHubConfig(`
[github]
enabled = true

[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
permissions = ["Contents:READ", "metadata:read"]
`)
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !cfg.GitHub.Enabled || len(cfg.GitHub.Apps) != 1 {
		t.Fatalf("github config = %#v, want one enabled app", cfg.GitHub)
	}
	app := cfg.GitHub.Apps[0]
	if app.PrivateKeyFile != keyPath {
		t.Fatalf("private_key_file = %q, want %q", app.PrivateKeyFile, keyPath)
	}
	if len(app.Permissions) != 2 || app.Permissions[0] != "contents:read" {
		t.Fatalf("permissions = %#v, want normalized permissions", app.Permissions)
	}
	if !containsGitHubConfigString(cfg.Sandbox.Profiles.ApprovedUser.HiddenPaths, keyPath) || !containsGitHubConfigString(cfg.Sandbox.Profiles.DurableAgent.HiddenPaths, keyPath) {
		t.Fatalf("hidden paths missing github app key: approved=%#v durable=%#v", cfg.Sandbox.Profiles.ApprovedUser.HiddenPaths, cfg.Sandbox.Profiles.DurableAgent.HiddenPaths)
	}
}

func TestLoadRejectsGitHubAppUnsafeOrBroadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(keyPath, []byte("test key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "missing repositories without broad flag",
			body: `
[github]
enabled = true
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
permissions = ["contents:read"]
`,
			wantErr: "repositories must not be empty",
		},
		{
			name: "missing permissions without broad flag",
			body: `
[github]
enabled = true
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
`,
			wantErr: "permissions must not be empty",
		},
		{
			name: "duplicate names",
			body: `
[github]
enabled = true
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
permissions = ["contents:read"]
[[github.apps]]
name = "Maintenance"
app_id = 124
installation_id = 457
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
permissions = ["contents:read"]
`,
			wantErr: "duplicates another GitHub App name",
		},
		{
			name: "invalid repository",
			body: `
[github]
enabled = true
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["not-a-repo"]
permissions = ["contents:read"]
`,
			wantErr: "invalid repository",
		},
		{
			name: "plaintext api url",
			body: `
[github]
enabled = true
api_base_url = "http://api.github.test"
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
permissions = ["contents:read"]
`,
			wantErr: "must use https",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "_")+".toml")
			if err := os.WriteFile(configPath, []byte(minimalGitHubConfig(tc.body)), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(configPath)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRejectsGitHubAppKeyReadableByGroupOrOther(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(keyPath, []byte("test key"), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod key: %v", err)
	}
	configPath := filepath.Join(dir, "config.toml")
	raw := minimalGitHubConfig(`
[github]
enabled = true
[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "./github-app.pem"
repositories = ["idolum-ai/aphelion"]
permissions = ["contents:read"]
`)
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "must not be readable") {
		t.Fatalf("Load() err = %v, want unsafe key permission error", err)
	}
}

func minimalGitHubConfig(extra string) string {
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
` + extra
}

func containsGitHubConfigString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
