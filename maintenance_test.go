//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestRunPathsCommandReportsEffectiveRootsAndFiles(t *testing.T) {
	root, configPath := writeMaintenanceConfig(t)

	if err := os.WriteFile(filepath.Join(root, "agent", "AGENTS.md"), []byte("agent rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent", "IDOLUM.md"), []byte("idolum"), 0o600); err != nil {
		t.Fatalf("write IDOLUM.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "agent", "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	output := captureStdout(t, func() {
		if err := runPathsCommand([]string{"--config", configPath}); err != nil {
			t.Fatalf("runPathsCommand() err = %v", err)
		}
	})

	for _, want := range []string{
		"config_path: " + configPath,
		"prompt_root: " + filepath.Join(root, "agent"),
		"exec_root: " + filepath.Join(root, "workspace"),
		"loaded_bootstrap_files:",
		"AGENTS.md",
		"loaded_dynamic_files:",
		"MEMORY.md",
		"loaded_idolum_stable_files:",
		"IDOLUM.md",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunResetCommandClearsRuntimeAndMemoryButKeepsConstitution(t *testing.T) {
	root, configPath := writeMaintenanceConfig(t)

	agentRoot := filepath.Join(root, "agent")
	userWorkspaceRoot := filepath.Join(root, "users-workspace")
	userMemoryRoot := filepath.Join(root, "users-memory")
	if err := os.WriteFile(filepath.Join(agentRoot, "AGENTS.md"), []byte("agent rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "IDOLUM.md"), []byte("idolum"), 0o600); err != nil {
		t.Fatalf("write IDOLUM.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "HEARTBEAT.md"), []byte("heartbeat"), 0o600); err != nil {
		t.Fatalf("write HEARTBEAT.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(agentRoot, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir daily notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "memory", time.Now().UTC().Format("2006-01-02")+".md"), []byte("note"), 0o600); err != nil {
		t.Fatalf("write daily note: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(userWorkspaceRoot, "42"), 0o755); err != nil {
		t.Fatalf("mkdir user workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userWorkspaceRoot, "42", "scratch.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write user workspace file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(userMemoryRoot, "42"), 0o755); err != nil {
		t.Fatalf("mkdir user memory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userMemoryRoot, "42", "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write user memory file: %v", err)
	}

	store, err := session.NewSQLiteStore(filepath.Join(root, "state", "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	sess, err := store.Load(session.SessionKey{ChatID: 123, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []session.Message{{
		Role:         "assistant",
		Content:      "hello",
		ContentChars: 5,
		TurnIndex:    1,
	}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	if err := runResetCommand([]string{"--config", configPath, "--scope", "all"}); err != nil {
		t.Fatalf("runResetCommand() err = %v", err)
	}

	for _, mustExist := range []string{
		filepath.Join(agentRoot, "AGENTS.md"),
		filepath.Join(agentRoot, "IDOLUM.md"),
	} {
		if _, err := os.Stat(mustExist); err != nil {
			t.Fatalf("expected %s to survive reset: %v", mustExist, err)
		}
	}
	for _, mustBeGone := range []string{
		filepath.Join(agentRoot, "MEMORY.md"),
		filepath.Join(agentRoot, "HEARTBEAT.md"),
		filepath.Join(agentRoot, "memory"),
	} {
		if _, err := os.Stat(mustBeGone); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, stat err=%v", mustBeGone, err)
		}
	}

	if entries, err := os.ReadDir(userWorkspaceRoot); err != nil {
		t.Fatalf("ReadDir(userWorkspaceRoot) err = %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("user workspace entries = %d, want 0", len(entries))
	}
	if entries, err := os.ReadDir(userMemoryRoot); err != nil {
		t.Fatalf("ReadDir(userMemoryRoot) err = %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("user memory entries = %d, want 0", len(entries))
	}

	store, err = session.NewSQLiteStore(filepath.Join(root, "state", "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() after reset err = %v", err)
	}
	defer store.Close()
	active, err := store.ListActive(365 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("ListActive() err = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active sessions after reset = %#v, want none", active)
	}
}

func writeMaintenanceConfig(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	configPath := filepath.Join(root, "aphelion.toml")
	raw := fmt.Sprintf(`
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

[sessions]
db_path = %q
idle_expiry = "24h"

[agent]
prompt_root = %q
exec_root = %q
shared_memory_root = %q
user_workspace_root = %q
user_memory_root = %q
bootstrap_files = ["AGENTS.md"]
dynamic_files = ["MEMORY.md", "HEARTBEAT.md"]
daily_notes = true
daily_notes_dir = "memory"
`, filepath.Join(root, "state", "sessions.db"), filepath.Join(root, "agent"), filepath.Join(root, "workspace"), filepath.Join(root, "agent"), filepath.Join(root, "users-workspace"), filepath.Join(root, "users-memory"))
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatalf("mkdir state: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agent"), 0o755); err != nil {
		t.Fatalf("mkdir agent: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "users-workspace"), 0o755); err != nil {
		t.Fatalf("mkdir users-workspace: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "users-memory"), 0o755); err != nil {
		t.Fatalf("mkdir users-memory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root, configPath
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() err = %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	return <-done
}
