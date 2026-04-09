//go:build linux

package sandbox

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

func TestResolverResolveAdminScope(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	resolver, err := NewResolver(
		Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared"),
			UserWorkspaceRoot: filepath.Join(tmp, "workspaces"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	scope, err := resolver.Resolve(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	if scope.WorkingRoot != scope.GlobalRoot {
		t.Fatalf("working root = %q, want global root %q", scope.WorkingRoot, scope.GlobalRoot)
	}
	if scope.Profile.Mode != ModeTrusted {
		t.Fatalf("profile mode = %q, want %q", scope.Profile.Mode, ModeTrusted)
	}
}

func TestResolverResolveApprovedUserScope(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	userWorkspaceRoot := filepath.Join(tmp, "workspaces")
	resolver, err := NewResolver(
		Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared"),
			UserWorkspaceRoot: userWorkspaceRoot,
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	scope, err := resolver.Resolve(principal.Principal{
		TelegramUserID: 42,
		Role:           principal.RoleApprovedUser,
	})
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}

	wantWorkspace := filepath.Join(userWorkspaceRoot, "42")
	wantWorkspace, err = filepath.Abs(wantWorkspace)
	if err != nil {
		t.Fatalf("Abs() err = %v", err)
	}
	if scope.WorkingRoot != wantWorkspace {
		t.Fatalf("working root = %q, want %q", scope.WorkingRoot, wantWorkspace)
	}
	if scope.Profile.Mode != ModeIsolated {
		t.Fatalf("profile mode = %q, want %q", scope.Profile.Mode, ModeIsolated)
	}
}

func TestResolverRejectsApprovedUserWithoutID(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	resolver, err := NewResolver(
		Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared"),
			UserWorkspaceRoot: filepath.Join(tmp, "workspaces"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	_, err = resolver.Resolve(principal.Principal{Role: principal.RoleApprovedUser})
	if err == nil {
		t.Fatal("Resolve() err = nil, want validation error")
	}
}

func TestNewResolverRejectsMissingRoots(t *testing.T) {
	t.Parallel()

	_, err := NewResolver(
		Roots{
			GlobalRoot:        "",
			SharedMemoryRoot:  "/shared",
			UserWorkspaceRoot: "/workspaces",
			UserMemoryRoot:    "/memory",
		},
		DefaultProfiles(),
	)
	if err == nil {
		t.Fatal("NewResolver() err = nil, want missing root validation")
	}
}

func TestDefaultRoots(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	got, err := DefaultRoots(filepath.Join(tmp, "workspace"), filepath.Join(tmp, "state", "sessions.db"))
	if err != nil {
		t.Fatalf("DefaultRoots() err = %v", err)
	}
	if got.GlobalRoot != filepath.Join(tmp, "workspace") {
		t.Fatalf("global root = %q, want %q", got.GlobalRoot, filepath.Join(tmp, "workspace"))
	}
	if got.SharedMemoryRoot != filepath.Join(tmp, "workspace") {
		t.Fatalf("shared memory root = %q, want workspace root", got.SharedMemoryRoot)
	}
	if got.UserWorkspaceRoot != filepath.Join(tmp, "state", "isolated", "workspaces") {
		t.Fatalf("user workspace root = %q", got.UserWorkspaceRoot)
	}
	if got.UserMemoryRoot != filepath.Join(tmp, "state", "isolated", "memory") {
		t.Fatalf("user memory root = %q", got.UserMemoryRoot)
	}
}
