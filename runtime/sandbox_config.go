//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func SandboxProfilesFromConfig(cfg config.SandboxConfig) sandbox.Profiles {
	defaults := sandbox.DefaultProfiles()
	return sandbox.Profiles{
		Admin:        sandboxProfileFromConfig(defaults.Admin, cfg.Profiles.Admin),
		ApprovedUser: sandboxProfileFromConfig(defaults.ApprovedUser, cfg.Profiles.ApprovedUser),
		DurableAgent: sandboxProfileFromConfig(defaults.DurableAgent, cfg.Profiles.DurableAgent),
	}
}

func sandboxProfileFromConfig(base sandbox.Profile, cfg config.SandboxProfileConfig) sandbox.Profile {
	if cfg.Mode == "" &&
		cfg.Network == "" &&
		!cfg.ReadonlyRoot &&
		len(cfg.WritablePaths) == 0 &&
		len(cfg.ReadonlyPaths) == 0 &&
		len(cfg.HiddenPaths) == 0 {
		return base
	}
	out := sandbox.Profile{
		Mode:          sandbox.Mode(cfg.Mode),
		ReadonlyRoot:  cfg.ReadonlyRoot,
		WritablePaths: append([]string(nil), cfg.WritablePaths...),
		ReadonlyPaths: append([]string(nil), cfg.ReadonlyPaths...),
		HiddenPaths:   append([]string(nil), cfg.HiddenPaths...),
		Network:       sandbox.NetworkPolicy(cfg.Network),
	}
	if out.Mode == "" {
		out.Mode = base.Mode
	}
	if out.Network == "" {
		out.Network = base.Network
	}
	if len(out.WritablePaths) == 0 {
		out.WritablePaths = append([]string(nil), base.WritablePaths...)
	}
	if len(out.ReadonlyPaths) == 0 {
		out.ReadonlyPaths = append([]string(nil), base.ReadonlyPaths...)
	}
	if len(out.HiddenPaths) == 0 {
		out.HiddenPaths = append([]string(nil), base.HiddenPaths...)
	}
	return out
}
