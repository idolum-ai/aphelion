//go:build linux

package sandbox

import (
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

func TestProfilesForRole(t *testing.T) {
	t.Parallel()

	profiles := DefaultProfiles()

	admin, err := profiles.ForRole(principal.RoleAdmin)
	if err != nil {
		t.Fatalf("ForRole(admin) err = %v", err)
	}
	if admin.Mode != ModeTrusted {
		t.Fatalf("admin mode = %q, want %q", admin.Mode, ModeTrusted)
	}

	approved, err := profiles.ForRole(principal.RoleApprovedUser)
	if err != nil {
		t.Fatalf("ForRole(approved_user) err = %v", err)
	}
	if approved.Mode != ModeIsolated {
		t.Fatalf("approved mode = %q, want %q", approved.Mode, ModeIsolated)
	}

	if _, err := profiles.ForRole(principal.Role("unknown")); err == nil {
		t.Fatal("ForRole(unknown) err = nil, want error")
	}
}
