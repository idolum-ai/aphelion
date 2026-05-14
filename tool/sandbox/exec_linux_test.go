//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

type fakeNetworkBackend struct {
	status      NetworkBackendStatus
	prepareFunc func(context.Context, CompiledNetworkPolicy) (*NetworkLease, error)
}

func (b fakeNetworkBackend) Status(context.Context) NetworkBackendStatus {
	if strings.TrimSpace(b.status.Name) == "" {
		return NetworkBackendStatus{Name: "fake", Available: true}
	}
	return b.status
}

func (b fakeNetworkBackend) Prepare(ctx context.Context, policy CompiledNetworkPolicy) (*NetworkLease, error) {
	if b.prepareFunc != nil {
		return b.prepareFunc(ctx, policy)
	}
	status := b.Status(ctx)
	if !status.Available {
		return nil, fmt.Errorf("sandbox network allowlist backend unavailable: %s", status.Reason)
	}
	return &NetworkLease{
		Evidence: NetworkExecutionEvidence{
			Policy:       string(NetworkAllowlist),
			Backend:      status.Name,
			Destinations: policy.DestinationStrings(),
			Rules:        policy.RuleStrings(),
		},
	}, nil
}

func buildScope(t *testing.T, role principal.Role) Scope {
	t.Helper()

	tmp := t.TempDir()
	if role == principal.RoleDurableAgent {
		scope, err := DurableAgentScope(
			"family-group",
			filepath.Join(tmp, "global"),
			filepath.Join(tmp, "workspaces", "family-group"),
			filepath.Join(tmp, "memory", "family-group"),
			"restricted",
		)
		if err != nil {
			t.Fatalf("DurableAgentScope() err = %v", err)
		}
		return scope
	}

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

	p := principal.Principal{Role: role}
	if role == principal.RoleApprovedUser {
		p.TelegramUserID = 42
	}
	scope, err := resolver.Resolve(p)
	if err != nil {
		t.Fatalf("Resolve() err = %v", err)
	}
	return scope
}

func TestRunnerStageSelection(t *testing.T) {
	t.Parallel()

	adminScope := buildScope(t, principal.RoleAdmin)
	approvedScope := buildScope(t, principal.RoleApprovedUser)
	durableScope := buildScope(t, principal.RoleDurableAgent)

	withBwrap := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})
	if got := withBwrap.Stage(adminScope); got != StageTrustedHost {
		t.Fatalf("admin stage = %q, want %q", got, StageTrustedHost)
	}
	if got := withBwrap.Stage(approvedScope); got != StageIsolatedBwrap {
		t.Fatalf("approved stage = %q, want %q", got, StageIsolatedBwrap)
	}
	if got := withBwrap.Stage(durableScope); got != StageIsolatedBwrap {
		t.Fatalf("durable stage = %q, want %q", got, StageIsolatedBwrap)
	}

	withoutBwrap := NewRunnerWithLookPath(func(string) (string, error) {
		return "", filepath.ErrBadPattern
	})
	if got := withoutBwrap.Stage(approvedScope); got != StageUnavailable {
		t.Fatalf("approved stage without bubblewrap = %q, want %q", got, StageUnavailable)
	}
	if got := withoutBwrap.Stage(durableScope); got != StageUnavailable {
		t.Fatalf("durable stage without bubblewrap = %q, want %q", got, StageUnavailable)
	}
}

func TestRunnerRunPassesStdin(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleAdmin)
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(working root) err = %v", err)
	}
	runner := NewRunner()

	result, err := runner.Run(context.Background(), ExecRequest{
		Scope:   scope,
		Command: "cat",
		Workdir: scope.WorkingRoot,
		Stdin:   []byte("external input"),
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if result.Stdout != "external input" {
		t.Fatalf("stdout = %q, want stdin echoed", result.Stdout)
	}
}

func TestRunnerPlanForApprovedIncludesBubblewrapAndChdir(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})

	plan, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "pwd",
		Workdir: scope.WorkingRoot,
	})
	if err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if plan.Stage != StageIsolatedBwrap {
		t.Fatalf("stage = %q, want %q", plan.Stage, StageIsolatedBwrap)
	}
	if plan.Binary != "/usr/bin/bwrap" {
		t.Fatalf("binary = %q, want /usr/bin/bwrap", plan.Binary)
	}

	args := strings.Join(plan.Args, " ")
	if !strings.Contains(args, "--chdir "+scope.WorkingRoot) {
		t.Fatalf("args missing chdir to working root: %v", plan.Args)
	}
	if !strings.Contains(args, "--bind "+scope.WorkingRoot+" "+scope.WorkingRoot) {
		t.Fatalf("args missing writable bind for working root: %v", plan.Args)
	}
	if !strings.Contains(args, "--ro-bind "+scope.GlobalRoot+" "+scope.GlobalRoot) {
		t.Fatalf("args missing readonly bind for global root: %v", plan.Args)
	}
	if !strings.Contains(args, "--unshare-user") || !strings.Contains(args, "--uid 65534") || !strings.Contains(args, "--gid 65534") {
		t.Fatalf("args missing user namespace remap: %v", plan.Args)
	}
	if !strings.Contains(args, "--cap-drop ALL") {
		t.Fatalf("args missing cap drop: %v", plan.Args)
	}
	if !strings.Contains(args, "--unshare-net") {
		t.Fatalf("args missing network namespace isolation: %v", plan.Args)
	}
	if !strings.Contains(args, "--clearenv") || !strings.Contains(args, "--setenv HOME "+scope.UserWorkspace) {
		t.Fatalf("args missing isolated environment setup: %v", plan.Args)
	}
	if len(plan.Env) != 0 {
		t.Fatalf("env = %#v, want empty host env for isolated runner", plan.Env)
	}
}

func TestRunnerPlanRejectsHiddenPathShadowingWritableRoot(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	scope.Profile.HiddenPaths = []string{"{user_workspace}"}

	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})

	_, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "pwd",
		Workdir: scope.WorkingRoot,
	})
	if err == nil {
		t.Fatal("Plan() err = nil, want hidden path conflict")
	}
	if !strings.Contains(err.Error(), "conflicts with exposed root") {
		t.Fatalf("err = %v, want hidden path conflict", err)
	}
}

func TestRunnerPlanRejectsIsolatedNetworkAllowlistWithoutBackend(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	scope.Profile.Network = NetworkAllowlist
	scope.Profile.NetworkAllow = MustParseNetworkDestinations([]string{"example.com:443"})

	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}).WithNetworkBackend(fakeNetworkBackend{status: NetworkBackendStatus{Name: "fake", Reason: "not configured"}})

	_, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "curl https://example.com",
		Workdir: scope.WorkingRoot,
	})
	if err == nil {
		t.Fatal("Plan() err = nil, want unavailable network allowlist rejection")
	}
	if !strings.Contains(err.Error(), "network allowlist backend unavailable") {
		t.Fatalf("err = %v, want network allowlist enforcement rejection", err)
	}
}

func TestRunnerPlanForIsolatedNetworkAllowlistUsesBackendContract(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	scope.Profile.Network = NetworkAllowlist
	scope.Profile.NetworkAllow = MustParseNetworkDestinations([]string{"example.com:443"})

	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	}).WithNetworkBackend(fakeNetworkBackend{status: NetworkBackendStatus{Name: "fake", Available: true}}).
		WithNetworkResolver(func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		})

	plan, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "curl https://example.com",
		Workdir: scope.WorkingRoot,
	})
	if err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if plan.Network == nil || plan.Network.Backend != "fake" {
		t.Fatalf("plan network = %#v, want fake backend evidence", plan.Network)
	}
	args := strings.Join(plan.Args, " ")
	if strings.Contains(args, "--unshare-net") {
		t.Fatalf("allowlist plan args include network denial instead of backend-managed network: %v", plan.Args)
	}
	if got, want := strings.Join(plan.Network.Rules, ","), "93.184.216.34/32:443"; got != want {
		t.Fatalf("network rules = %q, want %q", got, want)
	}
}

func TestRunnerPlanForDurableAgentIncludesBubblewrapAndChildRoots(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleDurableAgent)
	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})

	plan, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "pwd",
		Workdir: scope.WorkingRoot,
	})
	if err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if plan.Stage != StageIsolatedBwrap {
		t.Fatalf("stage = %q, want %q", plan.Stage, StageIsolatedBwrap)
	}

	args := strings.Join(plan.Args, " ")
	if !strings.Contains(args, "--bind "+scope.WorkingRoot+" "+scope.WorkingRoot) {
		t.Fatalf("args missing writable bind for working root: %v", plan.Args)
	}
	if !strings.Contains(args, "--bind "+scope.SharedMemoryRoot+" "+scope.SharedMemoryRoot) {
		t.Fatalf("args missing writable bind for shared memory root: %v", plan.Args)
	}
	if !strings.Contains(args, "--ro-bind "+scope.GlobalRoot+" "+scope.GlobalRoot) {
		t.Fatalf("args missing readonly bind for global root: %v", plan.Args)
	}
	if !strings.Contains(args, "--clearenv") || !strings.Contains(args, "--setenv HOME "+scope.UserWorkspace) {
		t.Fatalf("args missing isolated environment setup: %v", plan.Args)
	}
	if !strings.Contains(args, "--unshare-net") {
		t.Fatalf("args missing network namespace isolation for restricted durable agent: %v", plan.Args)
	}
	if len(plan.Env) != 0 {
		t.Fatalf("env = %#v, want empty host env for isolated runner", plan.Env)
	}
}

func TestRunnerPlanApprovedFailsWithoutBubblewrap(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleApprovedUser)
	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "", filepath.ErrBadPattern
	})

	_, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "pwd",
		Workdir: scope.WorkingRoot,
	})
	if err == nil {
		t.Fatal("Plan() err = nil, want unavailable backend error")
	}
}

func TestRunnerPlanIncludesResolvedResolvConfSymlinkTarget(t *testing.T) {
	t.Parallel()

	target, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil {
		t.Skipf("/etc/resolv.conf target unavailable: %v", err)
	}
	resolverRoot := filepath.Dir(filepath.Clean(target))
	if resolverRoot == "/" || resolverRoot == "/etc" {
		t.Skipf("/etc/resolv.conf does not require an extra resolver bind: %s", target)
	}

	scope := buildScope(t, principal.RoleDurableAgent)
	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})

	plan, err := runner.Plan(ExecRequest{
		Scope:   scope,
		Command: "getent hosts chatgpt.com",
		Workdir: scope.WorkingRoot,
	})
	if err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	args := strings.Join(plan.Args, " ")
	if !strings.Contains(args, "--ro-bind "+resolverRoot+" "+resolverRoot) {
		t.Fatalf("args missing resolver root bind %q for /etc/resolv.conf target %q: %v", resolverRoot, target, plan.Args)
	}
}

func TestRunnerPlanIncludesExtraBindPaths(t *testing.T) {
	t.Parallel()

	scope := buildScope(t, principal.RoleDurableAgent)
	extraRO := filepath.Join(t.TempDir(), "codex-home")
	extraRW := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(extraRO, 0o755); err != nil {
		t.Fatalf("MkdirAll(extraRO) err = %v", err)
	}
	if err := os.MkdirAll(extraRW, 0o755); err != nil {
		t.Fatalf("MkdirAll(extraRW) err = %v", err)
	}

	runner := NewRunnerWithLookPath(func(string) (string, error) {
		return "/usr/bin/bwrap", nil
	})

	extraBindSource := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(extraBindSource, 0o755); err != nil {
		t.Fatalf("MkdirAll(extraBindSource) err = %v", err)
	}

	plan, err := runner.Plan(ExecRequest{
		Scope:              scope,
		Command:            "pwd",
		Workdir:            scope.WorkingRoot,
		ExtraReadonlyPaths: []string{extraRO},
		ExtraWritablePaths: []string{extraRW},
		ExtraReadonlyBinds: []BindPath{{Source: extraBindSource, Target: "/usr/local/bin"}},
		ExtraEnv:           map[string]string{"MAILBOX_ADAPTER_SECRET": "test-secret", "XDG_CONFIG_HOME": "/host-config"},
	})
	if err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	args := strings.Join(plan.Args, " ")
	if !strings.Contains(args, "--ro-bind "+extraRO+" "+extraRO) {
		t.Fatalf("args missing extra readonly bind %q: %v", extraRO, plan.Args)
	}
	if !strings.Contains(args, "--bind "+extraRW+" "+extraRW) {
		t.Fatalf("args missing extra writable bind %q: %v", extraRW, plan.Args)
	}
	if !strings.Contains(args, "--ro-bind "+extraBindSource+" /usr/local/bin") {
		t.Fatalf("args missing extra readonly mapped bind %q -> /usr/local/bin: %v", extraBindSource, plan.Args)
	}
	if !strings.Contains(args, "--setenv MAILBOX_ADAPTER_SECRET test-secret") || !strings.Contains(args, "--setenv XDG_CONFIG_HOME /host-config") {
		t.Fatalf("args missing extra env: %v", plan.Args)
	}
}
