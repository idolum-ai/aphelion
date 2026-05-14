//go:build linux

package sandbox

import (
	"fmt"

	"github.com/idolum-ai/aphelion/principal"
)

type Mode string

const (
	ModeTrusted  Mode = "trusted"
	ModeIsolated Mode = "isolated"
)

type NetworkPolicy string

const (
	NetworkAllowlist NetworkPolicy = "allowlist"
	NetworkDeny      NetworkPolicy = "deny"
)

type Profile struct {
	Mode          Mode
	ReadonlyRoot  bool
	WritablePaths []string
	ReadonlyPaths []string
	HiddenPaths   []string
	Network       NetworkPolicy
	NetworkAllow  []NetworkDestination
}

type Profiles struct {
	Admin        Profile
	ApprovedUser Profile
	DurableAgent Profile
}

func DefaultProfiles() Profiles {
	return Profiles{
		Admin: Profile{
			Mode:         ModeTrusted,
			ReadonlyRoot: false,
			Network:      NetworkAllowlist,
		},
		ApprovedUser: Profile{
			Mode:          ModeIsolated,
			ReadonlyRoot:  true,
			WritablePaths: []string{"{user_workspace}", "{user_memory}", "/tmp"},
			ReadonlyPaths: []string{"{global_root}", "{shared_memory_root}"},
			HiddenPaths: []string{
				"~/.aphelion/aphelion.toml",
				"~/.config/aphelion/config.toml",
				"~/.ssh",
				"~/.gnupg",
			},
			Network: NetworkDeny,
		},
		DurableAgent: Profile{
			Mode:          ModeIsolated,
			ReadonlyRoot:  true,
			WritablePaths: []string{"{working_root}", "{shared_memory_root}", "/tmp"},
			ReadonlyPaths: []string{"{global_root}"},
			HiddenPaths: []string{
				"~/.aphelion/aphelion.toml",
				"~/.config/aphelion/config.toml",
				"~/.ssh",
				"~/.gnupg",
			},
			Network: NetworkDeny,
		},
	}
}

func (p Profiles) ForRole(role principal.Role) (Profile, error) {
	switch role {
	case principal.RoleAdmin:
		return p.Admin, nil
	case principal.RoleApprovedUser:
		return p.ApprovedUser, nil
	case principal.RoleDurableAgent:
		return p.DurableAgent, nil
	default:
		return Profile{}, fmt.Errorf("unsupported role %q", role)
	}
}
