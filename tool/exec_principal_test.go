//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteForPrincipalUsesAdminGlobalRoot(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal() err = %v", err)
	}

	wantDir, err := filepath.Abs(globalRoot)
	if err != nil {
		t.Fatalf("Abs() err = %v", err)
	}
	if !strings.Contains(out, wantDir) {
		t.Fatalf("output = %q, want admin root %q", out, wantDir)
	}
}

func TestExecuteForPrincipalRequiresResolver(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal() err = nil, want resolver requirement")
	}
}

func TestExecuteForPrincipalApprovedUserRequiresIsolatedBackend(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	registry.runner = sandbox.NewRunnerWithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	})

	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal() err = nil, want isolated backend requirement")
	}
	if !strings.Contains(err.Error(), "no supported sandbox backend") {
		t.Fatalf("err = %v, want isolated backend error", err)
	}
}

func TestExecuteForDurableAgentUsesLocalRootsForExec(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			AdminExecRoot:     filepath.Join(tmp, "admin"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared"),
			UserWorkspaceRoot: filepath.Join(tmp, "users", "workspaces"),
			UserMemoryRoot:    filepath.Join(tmp, "users", "memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(tmp, "state"), 0o755); err != nil {
		t.Fatalf("MkdirAll(state) err = %v", err)
	}
	store, err := session.NewSQLiteStore(filepath.Join(tmp, "state", "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	workspaceRoot := filepath.Join(tmp, "durable", "child-alpha", "workspace")
	memoryRoot := filepath.Join(tmp, "durable", "child-alpha", "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "child-alpha",
		ChannelKind:       "scheduled_review",
		Status:            "active",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		NetworkPolicy:     "restricted",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	registry := NewRegistryWithSandbox(filepath.Join(tmp, "admin-workspace"), 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-alpha"},
		session.SessionKey{ChatID: -1},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(exec durable_agent) err = %v", err)
	}
	if !strings.Contains(out, workspaceRoot) {
		t.Fatalf("output = %q, want durable workspace root %q", out, workspaceRoot)
	}
}

func TestExecuteForDurableAgentUsesDefaultLocalRootsWhenAgentHasNoConfiguredRoots(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			AdminExecRoot:     filepath.Join(tmp, "admin"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared"),
			UserWorkspaceRoot: filepath.Join(tmp, "users", "workspaces"),
			UserMemoryRoot:    filepath.Join(tmp, "users", "memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	dbPath := filepath.Join(tmp, "state", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(state) err = %v", err)
	}
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if err := store.UpsertDurableAgent(core.DurableAgent{AgentID: "child-beta", ChannelKind: "scheduled_review", Status: "active", BootstrapLLM: core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"}}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	registry := NewRegistryWithSandbox(filepath.Join(tmp, "admin-workspace"), 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: "child-beta"},
		session.SessionKey{ChatID: -2},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(exec durable_agent) err = %v", err)
	}
	wantWorkspace := filepath.Join(filepath.Dir(dbPath), "durable_agents", "child-beta", "workspace")
	if !strings.Contains(out, wantWorkspace) {
		t.Fatalf("output = %q, want default durable workspace root %q", out, wantWorkspace)
	}
}

func TestSupportsPrincipal(t *testing.T) {
	t.Parallel()

	base := NewRegistry(t.TempDir(), 2*time.Second)
	if base.SupportsPrincipal(principal.Principal{Role: principal.RoleAdmin}) {
		t.Fatal("SupportsPrincipal(admin) = true, want false without resolver")
	}

	tmp := t.TempDir()
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	withSandbox := NewRegistryWithSandbox(filepath.Join(tmp, "global"), 2*time.Second, resolver)
	withSandbox.runner = sandbox.NewRunnerWithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	})
	approved := principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser}
	if withSandbox.SupportsPrincipal(approved) {
		t.Fatal("SupportsPrincipal(approved_user) = true, want false when isolated backend is unavailable")
	}
	setFakeBubblewrapRunner(t, withSandbox)

	if !withSandbox.SupportsPrincipal(principal.Principal{Role: principal.RoleAdmin}) {
		t.Fatal("SupportsPrincipal(admin) = false, want true with resolver")
	}
	if !withSandbox.SupportsPrincipal(approved) {
		t.Fatal("SupportsPrincipal(approved_user) = false, want true with resolver")
	}
}
