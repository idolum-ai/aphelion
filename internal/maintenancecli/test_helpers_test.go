//go:build linux

package maintenancecli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var captureStdoutMu sync.Mutex

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	captureStdoutMu.Lock()
	defer captureStdoutMu.Unlock()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe() err = %v", err)
	}
	os.Stdout = w
	err = fn()
	if closeErr := w.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	os.Stdout = old
	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, r); copyErr != nil && err == nil {
		err = copyErr
	}
	if closeErr := r.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return buf.String(), err
}

func writeMaintenanceConfig(t *testing.T, root string) string {
	t.Helper()
	cfgPath := filepath.Join(root, "aphelion.toml")
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatalf("MkdirAll(state) err = %v", err)
	}
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
`
	if err := os.WriteFile(cfgPath, []byte(configRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(config) err = %v", err)
	}
	return cfgPath
}
