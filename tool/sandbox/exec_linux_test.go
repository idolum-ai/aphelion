//go:build linux

package sandbox

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
)

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
