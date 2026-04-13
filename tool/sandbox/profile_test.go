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

	durable, err := profiles.ForRole(principal.RoleDurableAgent)
	if err != nil {
		t.Fatalf("ForRole(durable_agent) err = %v", err)
	}
	if durable.Mode != ModeIsolated {
		t.Fatalf("durable_agent mode = %q, want %q", durable.Mode, ModeIsolated)
	}

	if _, err := profiles.ForRole(principal.Role("unknown")); err == nil {
		t.Fatal("ForRole(unknown) err = nil, want error")
	}
}
