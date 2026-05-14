//go:build linux

package runtime

import (
	"fmt"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func SandboxProfilesFromConfig(cfg config.SandboxConfig) (sandbox.Profiles, error) {
	defaults := sandbox.DefaultProfiles()
	admin, err := sandboxProfileFromConfig(defaults.Admin, cfg.Profiles.Admin)
	if err != nil {
		return sandbox.Profiles{}, fmt.Errorf("sandbox.profiles.admin: %w", err)
	}
	approvedUser, err := sandboxProfileFromConfig(defaults.ApprovedUser, cfg.Profiles.ApprovedUser)
	if err != nil {
		return sandbox.Profiles{}, fmt.Errorf("sandbox.profiles.approved_user: %w", err)
	}
	durableAgent, err := sandboxProfileFromConfig(defaults.DurableAgent, cfg.Profiles.DurableAgent)
	if err != nil {
		return sandbox.Profiles{}, fmt.Errorf("sandbox.profiles.durable_agent: %w", err)
	}
	return sandbox.Profiles{Admin: admin, ApprovedUser: approvedUser, DurableAgent: durableAgent}, nil
}

func sandboxProfileFromConfig(base sandbox.Profile, cfg config.SandboxProfileConfig) (sandbox.Profile, error) {
	if cfg.Mode == "" &&
		cfg.Network == "" &&
		!cfg.ReadonlyRoot &&
		len(cfg.WritablePaths) == 0 &&
		len(cfg.ReadonlyPaths) == 0 &&
		len(cfg.HiddenPaths) == 0 &&
		len(cfg.NetworkAllow) == 0 {
		return base, nil
	}
	networkAllow, err := sandbox.ParseNetworkDestinations(cfg.NetworkAllow)
	if err != nil {
		return sandbox.Profile{}, err
	}
	out := sandbox.Profile{
		Mode:          sandbox.Mode(cfg.Mode),
		ReadonlyRoot:  cfg.ReadonlyRoot,
		WritablePaths: append([]string(nil), cfg.WritablePaths...),
		ReadonlyPaths: append([]string(nil), cfg.ReadonlyPaths...),
		HiddenPaths:   append([]string(nil), cfg.HiddenPaths...),
		Network:       sandbox.NetworkPolicy(cfg.Network),
		NetworkAllow:  networkAllow,
	}
	if out.Mode == "" {
		out.Mode = base.Mode
	}
	if out.Network == "" {
		out.Network = base.Network
	}
	if len(out.NetworkAllow) == 0 {
		out.NetworkAllow = append([]sandbox.NetworkDestination(nil), base.NetworkAllow...)
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
	return out, nil
}
