//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestSandboxProfilesFromConfig(t *testing.T) {
	t.Parallel()

	profiles, err := SandboxProfilesFromConfig(config.SandboxConfig{
		Profiles: config.SandboxProfilesConfig{
			Admin: config.SandboxProfileConfig{
				Mode:    "trusted",
				Network: "allowlist",
			},
			ApprovedUser: config.SandboxProfileConfig{
				Mode:          "isolated",
				ReadonlyRoot:  true,
				WritablePaths: []string{"{user_workspace}"},
				ReadonlyPaths: []string{"{global_root}"},
				HiddenPaths:   []string{"~/.ssh"},
				Network:       "deny",
			},
			DurableAgent: config.SandboxProfileConfig{
				Mode:          "isolated",
				WritablePaths: []string{"{working_root}"},
				Network:       "allowlist",
				NetworkAllow:  []string{"api.openai.com:443"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SandboxProfilesFromConfig() err = %v", err)
	}

	approved, err := profiles.ForRole(principal.RoleApprovedUser)
	if err != nil {
		t.Fatalf("ForRole(approved_user) err = %v", err)
	}
	if approved.Mode != sandbox.ModeIsolated || !approved.ReadonlyRoot || approved.Network != sandbox.NetworkDeny {
		t.Fatalf("approved profile = %#v", approved)
	}
	if len(approved.WritablePaths) != 1 || approved.WritablePaths[0] != "{user_workspace}" {
		t.Fatalf("approved writable paths = %#v", approved.WritablePaths)
	}

	durable, err := profiles.ForRole(principal.RoleDurableAgent)
	if err != nil {
		t.Fatalf("ForRole(durable_agent) err = %v", err)
	}
	if durable.Network != sandbox.NetworkAllowlist {
		t.Fatalf("durable network = %q, want allowlist", durable.Network)
	}
	if len(durable.NetworkAllow) != 1 || durable.NetworkAllow[0].Canonical() != "api.openai.com:443" {
		t.Fatalf("durable network allow = %#v, want api.openai.com:443", durable.NetworkAllow)
	}
}
