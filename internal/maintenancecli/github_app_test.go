//go:build linux

package maintenancecli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestRunGitHubAppStatusRedactsKeyAndOmitsToken(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := writeGitHubAppMaintenanceConfig(t, root, "https://api.github.com")
	out, err := captureStdout(t, func() error {
		return runGitHubAppCommand([]string{"status", "--config", configPath, "--format", "json"})
	})
	if err != nil {
		t.Fatalf("runGitHubAppCommand(status) err = %v", err)
	}
	if !strings.Contains(out, `"private_key_file": "github-app.pem"`) {
		t.Fatalf("status output = %s, want key basename", out)
	}
	if strings.Contains(out, filepath.Join(root, "github-app.pem")) || strings.Contains(out, "token-for-test") || strings.Contains(out, "password=") {
		t.Fatalf("status output leaked secret detail: %s", out)
	}
}

func TestRunGitHubAppTokenRequiresExplicitShowToken(t *testing.T) {
	t.Parallel()
	err := runGitHubAppCommand([]string{"token"})
	if err == nil || !strings.Contains(err.Error(), "--show-token") {
		t.Fatalf("runGitHubAppCommand(token) err = %v, want --show-token refusal", err)
	}
}

func TestRunGitHubAppTokenMintsPrintsAndRecordsRedactedEvent(t *testing.T) {
	root := t.TempDir()
	oldHTTPClient := githubAppHTTPClient
	githubAppHTTPClient = &http.Client{Transport: githubAppRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/456/access_tokens" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"token":"token-for-test","expires_at":"2026-05-23T13:00:00Z","permissions":{"contents":"read"},"repositories":[{"full_name":"idolum-ai/aphelion"}]}`)),
		}, nil
	})}
	t.Cleanup(func() { githubAppHTTPClient = oldHTTPClient })
	configPath := writeGitHubAppMaintenanceConfig(t, root, "https://api.github.test")
	out, err := captureStdout(t, func() error {
		return runGitHubAppCommand([]string{"token", "--config", configPath, "--app", "maintenance", "--show-token", "--format", "git-credential"})
	})
	if err != nil {
		t.Fatalf("runGitHubAppCommand(token) err = %v", err)
	}
	if !strings.Contains(out, "username=x-access-token") || !strings.Contains(out, "password=token-for-test") {
		t.Fatalf("token output = %q, want git credential payload", out)
	}
	store, err := session.NewSQLiteStore(filepath.Join(root, "state", "sessions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	events, err := store.ExecutionEventsBySession(maintenanceRepairKey(), 0, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 1 || events[0].EventType != "github_app.token.minted" || events[0].Status != "minted" {
		t.Fatalf("events = %#v, want minted github app event", events)
	}
	if strings.Contains(events[0].PayloadJSON, "token-for-test") || !strings.Contains(events[0].PayloadJSON, `"token_redacted":true`) {
		t.Fatalf("event payload = %s, want redacted token evidence", events[0].PayloadJSON)
	}
}

type githubAppRoundTripFunc func(*http.Request) (*http.Response, error)

func (f githubAppRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeGitHubAppMaintenanceConfig(t *testing.T, root string, apiBaseURL string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatalf("MkdirAll(state) err = %v", err)
	}
	keyPath := filepath.Join(root, "github-app.pem")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() err = %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	cfgPath := filepath.Join(root, "aphelion.toml")
	configRaw := `
[telegram]
bot_token = "token"

[principals.telegram]
admin_user_ids = [1]

[providers.anthropic]
api_key = "anthropic-key"

[sessions]
db_path = "` + filepath.ToSlash(filepath.Join(root, "state", "sessions.db")) + `"

[agent]
prompt_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
exec_root = "` + filepath.ToSlash(filepath.Join(root, "workspace")) + `"
shared_memory_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
user_workspace_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "workspaces")) + `"
user_memory_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "memory")) + `"

[github]
enabled = true
api_base_url = "` + apiBaseURL + `"

[[github.apps]]
name = "maintenance"
app_id = 123
installation_id = 456
private_key_file = "` + filepath.ToSlash(keyPath) + `"
repositories = ["idolum-ai/aphelion"]
permissions = ["metadata:read", "contents:read"]
`
	if err := os.WriteFile(cfgPath, []byte(configRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(config) err = %v", err)
	}
	return cfgPath
}
