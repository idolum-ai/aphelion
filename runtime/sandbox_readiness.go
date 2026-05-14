//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type sandboxStageResolver interface {
	Stage(sandbox.Scope) sandbox.Stage
	NetworkBackendStatus(context.Context) sandbox.NetworkBackendStatus
}

type sandboxReadinessProfile struct {
	role    string
	profile sandbox.Profile
}

func SandboxReadinessSnapshot(cfg *config.Config) core.SandboxReadinessSnapshot {
	return sandboxReadinessSnapshotFromConfig(cfg, time.Now().UTC(), sandbox.NewRunner(), sandboxStaticActiveRoles(cfg))
}

func (r *Runtime) sandboxReadinessSnapshot(now time.Time) core.SandboxReadinessSnapshot {
	if r == nil || r.cfg == nil {
		return core.SandboxReadinessSnapshot{GeneratedAt: coalesceSandboxReadinessTime(now)}
	}
	activeRoles := sandboxStaticActiveRoles(r.cfg)
	if r.store != nil {
		if agents, err := r.store.ListDurableAgents(); err == nil && len(agents) > 0 {
			activeRoles["durable_agent"] = true
		}
	}
	return sandboxReadinessSnapshotFromConfig(r.cfg, now, sandbox.NewRunner(), activeRoles)
}

func sandboxReadinessSnapshotFromConfig(cfg *config.Config, now time.Time, runner sandboxStageResolver, activeRoles map[string]bool) core.SandboxReadinessSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	snapshot := core.SandboxReadinessSnapshot{GeneratedAt: now.UTC()}
	if cfg == nil {
		return snapshot
	}
	if runner == nil {
		runner = sandbox.NewRunner()
	}
	if activeRoles == nil {
		activeRoles = sandboxStaticActiveRoles(cfg)
	}
	profiles, err := SandboxProfilesFromConfig(cfg.Sandbox)
	if err != nil {
		snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
			Code:             "sandbox_profile_invalid",
			Severity:         "error",
			Summary:          fmt.Sprintf("sandbox profiles are invalid: %v", err),
			NextRepairAction: "Fix the sandbox profile config and run --check-config again.",
		})
		return snapshot
	}
	networkStatus := runner.NetworkBackendStatus(context.Background())
	for _, entry := range sandboxReadinessProfiles(profiles) {
		profile := entry.profile
		role := strings.TrimSpace(entry.role)
		if !activeRoles[role] {
			continue
		}
		mode := strings.TrimSpace(string(profile.Mode))
		network := strings.TrimSpace(string(profile.Network))
		stage := runner.Stage(sandbox.Scope{Profile: profile})
		if profile.Mode == sandbox.ModeIsolated && stage == sandbox.StageUnavailable {
			snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
				Role:             role,
				Mode:             mode,
				Network:          network,
				Code:             "sandbox_backend_unavailable",
				Severity:         "error",
				Summary:          fmt.Sprintf("%s requests isolated execution, but bubblewrap is unavailable.", role),
				NextRepairAction: "Install bubblewrap or deliberately change this profile to trusted only if that authority is intended.",
			})
		}
		if profile.Mode == sandbox.ModeIsolated && profile.Network == sandbox.NetworkAllowlist {
			if len(profile.NetworkAllow) == 0 {
				snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
					Role:             role,
					Mode:             mode,
					Network:          network,
					Code:             "sandbox_network_allowlist_empty",
					Severity:         "error",
					Summary:          fmt.Sprintf("%s requests a sandbox network allowlist without destinations.", role),
					NextRepairAction: "Set sandbox profile network_allow to explicit host:port, ip:port, or cidr:port destinations.",
				})
			}
			if !networkStatus.Available {
				snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
					Role:             role,
					Mode:             mode,
					Network:          network,
					Code:             "sandbox_network_allowlist_backend_unavailable",
					Severity:         "warning",
					Summary:          fmt.Sprintf("%s requests a sandbox network allowlist, but the %s backend is unavailable: %s.", role, networkStatus.Name, networkStatus.Reason),
					NextRepairAction: "Install the host networking prerequisites or use network=deny for isolated execution.",
				})
			}
		}
		if profile.Mode == sandbox.ModeTrusted && role != "admin" {
			snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
				Role:             role,
				Mode:             mode,
				Network:          network,
				Code:             "non_admin_trusted_sandbox",
				Severity:         "warning",
				Summary:          fmt.Sprintf("%s is configured for trusted host execution.", role),
				NextRepairAction: "Use an isolated profile for non-admin execution unless this role is intentionally host-trusted.",
			})
		}
		if profile.Mode == sandbox.ModeTrusted && profile.Network == sandbox.NetworkDeny {
			snapshot.Issues = append(snapshot.Issues, core.SandboxReadinessIssue{
				Role:             role,
				Mode:             mode,
				Network:          network,
				Code:             "trusted_network_policy_unenforced",
				Severity:         "warning",
				Summary:          fmt.Sprintf("%s requests network=deny on trusted host execution, which does not isolate networking.", role),
				NextRepairAction: "Use mode=isolated with network=deny when network isolation is required.",
			})
		}
	}
	return snapshot
}

func coalesceSandboxReadinessTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func sandboxStaticActiveRoles(cfg *config.Config) map[string]bool {
	active := map[string]bool{"admin": true}
	if cfg == nil {
		return active
	}
	if len(cfg.Principals.Telegram.ApprovedUserIDs) > 0 {
		active["approved_user"] = true
	}
	if len(cfg.Telegram.DurableGroups) > 0 || len(cfg.Telegram.ChildBots) > 0 {
		active["durable_agent"] = true
	}
	return active
}

func sandboxReadinessProfiles(profiles sandbox.Profiles) []sandboxReadinessProfile {
	return []sandboxReadinessProfile{
		{role: "admin", profile: profiles.Admin},
		{role: "approved_user", profile: profiles.ApprovedUser},
		{role: "durable_agent", profile: profiles.DurableAgent},
	}
}
