//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type fakeSandboxStageResolver struct {
	isolatedStage sandbox.Stage
	networkStatus sandbox.NetworkBackendStatus
}

func (r fakeSandboxStageResolver) Stage(scope sandbox.Scope) sandbox.Stage {
	if scope.Profile.Mode == sandbox.ModeTrusted {
		return sandbox.StageTrustedHost
	}
	if r.isolatedStage != "" {
		return r.isolatedStage
	}
	return sandbox.StageIsolatedBwrap
}

func (r fakeSandboxStageResolver) NetworkBackendStatus(context.Context) sandbox.NetworkBackendStatus {
	if r.networkStatus.Name != "" || r.networkStatus.Available || r.networkStatus.Reason != "" {
		return r.networkStatus
	}
	return sandbox.NetworkBackendStatus{Name: "fake", Available: true}
}

func TestSandboxReadinessReportsUnavailableIsolatedBackend(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	activeRoles := map[string]bool{"admin": true, "approved_user": true, "durable_agent": true}
	snapshot := sandboxReadinessSnapshotFromConfig(&cfg, time.Now().UTC(), fakeSandboxStageResolver{isolatedStage: sandbox.StageUnavailable}, activeRoles)

	if !sandboxReadinessHasIssue(snapshot, "approved_user", "sandbox_backend_unavailable") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want approved_user unavailable backend", snapshot.Issues)
	}
	if !sandboxReadinessHasIssue(snapshot, "durable_agent", "sandbox_backend_unavailable") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want durable_agent unavailable backend", snapshot.Issues)
	}
	if sandboxReadinessHasIssue(snapshot, "admin", "sandbox_backend_unavailable") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, did not expect admin unavailable backend", snapshot.Issues)
	}
}

func TestSandboxReadinessIgnoresDormantNonAdminProfiles(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	snapshot := sandboxReadinessSnapshotFromConfig(&cfg, time.Now().UTC(), fakeSandboxStageResolver{isolatedStage: sandbox.StageUnavailable}, sandboxStaticActiveRoles(&cfg))
	if len(snapshot.Issues) != 0 {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want no dormant non-admin profile warnings", snapshot.Issues)
	}
}

func TestSandboxReadinessReportsUnenforcedNetworkAllowlist(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sandbox.Profiles.ApprovedUser.Network = "allowlist"
	cfg.Sandbox.Profiles.ApprovedUser.NetworkAllow = []string{"api.openai.com:443"}
	activeRoles := map[string]bool{"approved_user": true}
	snapshot := sandboxReadinessSnapshotFromConfig(&cfg, time.Now().UTC(), fakeSandboxStageResolver{
		isolatedStage: sandbox.StageIsolatedBwrap,
		networkStatus: sandbox.NetworkBackendStatus{
			Name:   "fake",
			Reason: "missing capability",
		},
	}, activeRoles)

	if !sandboxReadinessHasIssue(snapshot, "approved_user", "sandbox_network_allowlist_backend_unavailable") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want approved_user network allowlist warning", snapshot.Issues)
	}
	if sandboxReadinessHasIssue(snapshot, "approved_user", "sandbox_backend_unavailable") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, did not expect backend warning with fake bwrap stage", snapshot.Issues)
	}
}

func TestSandboxReadinessReportsTrustedNonAdminAndTrustedNetworkDeny(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Sandbox.Profiles.Admin.Network = "deny"
	cfg.Sandbox.Profiles.ApprovedUser.Mode = "trusted"
	cfg.Sandbox.Profiles.ApprovedUser.Network = "allowlist"
	cfg.Sandbox.Profiles.DurableAgent.Mode = "trusted"
	cfg.Sandbox.Profiles.DurableAgent.Network = "allowlist"
	activeRoles := map[string]bool{"admin": true, "approved_user": true, "durable_agent": true}
	snapshot := sandboxReadinessSnapshotFromConfig(&cfg, time.Now().UTC(), fakeSandboxStageResolver{}, activeRoles)

	if !sandboxReadinessHasIssue(snapshot, "admin", "trusted_network_policy_unenforced") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want admin trusted network warning", snapshot.Issues)
	}
	if !sandboxReadinessHasIssue(snapshot, "approved_user", "non_admin_trusted_sandbox") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want approved_user trusted host warning", snapshot.Issues)
	}
	if !sandboxReadinessHasIssue(snapshot, "durable_agent", "non_admin_trusted_sandbox") {
		t.Fatalf("SandboxReadinessSnapshot issues = %#v, want durable_agent trusted host warning", snapshot.Issues)
	}
}

func sandboxReadinessHasIssue(snapshot core.SandboxReadinessSnapshot, role string, code string) bool {
	for _, issue := range snapshot.Issues {
		if issue.Role == role && issue.Code == code {
			return true
		}
	}
	return false
}
