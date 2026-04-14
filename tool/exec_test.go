//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type stubExecApprover struct {
	called   int
	approved bool
	request  ExecApprovalRequest
}

func (s *stubExecApprover) ConfirmExec(_ context.Context, req ExecApprovalRequest) (ExecApprovalDecision, error) {
	s.called++
	s.request = req
	return ExecApprovalDecision{Approved: s.approved}, nil
}

func setFakeBubblewrapRunner(t *testing.T, registry *Registry) {
	t.Helper()

	dir := t.TempDir()
	fakeBwrapPath := filepath.Join(dir, "bwrap")
	script := `#!/usr/bin/env bash
set -euo pipefail
workdir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --chdir)
      shift
      workdir="$1"
      ;;
    --)
      shift
      break
      ;;
  esac
  shift
done
if [[ -n "$workdir" ]]; then
  cd "$workdir"
fi
exec "$@"
`
	if err := os.WriteFile(fakeBwrapPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake bwrap: %v", err)
	}

	registry.runner = sandbox.NewRunnerWithLookPath(func(_ string) (string, error) {
		return fakeBwrapPath, nil
	})
}

func TestExecSuccess(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)

	out, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"printf 'ok'"}`))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("output = %q, want command output", out)
	}
}

func TestExecDangerousCommandRequiresApproval(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"rm -rf build"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want approval error")
	}
	if !strings.Contains(err.Error(), "requires an approved proposal") {
		t.Fatalf("err = %v, want explicit proposal error", err)
	}
}

func TestExecDangerousCommandUsesApprover(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"rm -rf build"}`),
		sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 7},
	)
	if err == nil {
		t.Fatal("executeWithScopeAndPrincipal() err = nil, want denied approval")
	}
	if approver.called != 1 {
		t.Fatalf("approver called = %d, want 1", approver.called)
	}
	if approver.request.Command != "rm -rf build" {
		t.Fatalf("approver command = %q, want rm -rf build", approver.request.Command)
	}
	if approver.request.Proposal.Kind != "destructive_mutation" {
		t.Fatalf("proposal kind = %q, want destructive_mutation", approver.request.Proposal.Kind)
	}
	if approver.request.SessionKey.ChatID != 7 {
		t.Fatalf("approver session = %+v, want chat id 7", approver.request.SessionKey)
	}
}

func TestExecCapabilityAcquisitionUsesApprover(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"pip install playwright"}`),
		sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 9},
	)
	if err == nil {
		t.Fatal("executeWithScopeAndPrincipal() err = nil, want denied proposal")
	}
	if approver.called != 1 {
		t.Fatalf("approver called = %d, want 1", approver.called)
	}
	if approver.request.Proposal.Kind != "capability_acquisition" {
		t.Fatalf("proposal kind = %q, want capability_acquisition", approver.request.Proposal.Kind)
	}
	if !strings.Contains(approver.request.Proposal.BoundedEffect, "install or update") {
		t.Fatalf("proposal bounded effect = %q, want install/update summary", approver.request.Proposal.BoundedEffect)
	}
}

func TestExecSafeCommandSkipsApprover(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: true}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	out, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"printf 'ok'"}`))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if approver.called != 0 {
		t.Fatalf("approver called = %d, want 0", approver.called)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("output = %q, want command output", out)
	}
}

func TestExecRejectsEscapedWorkdir(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"pwd","workdir":"../outside"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want workspace violation")
	}
}

func TestExecTimeout(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 1*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"sleep 2"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout message", err)
	}
}

func TestExecuteForPrincipalUsesApprovedUserRoot(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	userWorkspaceRoot := filepath.Join(tmp, "users-workspace")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: userWorkspaceRoot,
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal() err = %v", err)
	}

	wantDir := filepath.Join(userWorkspaceRoot, "42")
	wantDir, err = filepath.Abs(wantDir)
	if err != nil {
		t.Fatalf("Abs() err = %v", err)
	}
	if !strings.Contains(out, wantDir) {
		t.Fatalf("output = %q, want pwd under isolated root %q", out, wantDir)
	}
}

func TestExecuteForPrincipalRejectsEscapedWorkdir(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	setFakeBubblewrapRunner(t, registry)
	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"exec",
		json.RawMessage(`{"command":"pwd","workdir":"../outside"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal() err = nil, want workspace violation")
	}
	if !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("err = %v, want workspace escape error", err)
	}
}

func TestExecuteForPrincipalUsesAdminGlobalRoot(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	out, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForPrincipal() err = %v", err)
	}

	wantDir, err := filepath.Abs(globalRoot)
	if err != nil {
		t.Fatalf("Abs() err = %v", err)
	}
	if !strings.Contains(out, wantDir) {
		t.Fatalf("output = %q, want admin root %q", out, wantDir)
	}
}

func TestExecuteForPrincipalRequiresResolver(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal() err = nil, want resolver requirement")
	}
}

func TestExecuteForPrincipalApprovedUserRequiresIsolatedBackend(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver)
	registry.runner = sandbox.NewRunnerWithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	})

	_, err = registry.ExecuteForPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		"exec",
		json.RawMessage(`{"command":"pwd"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForPrincipal() err = nil, want isolated backend requirement")
	}
	if !strings.Contains(err.Error(), "no supported sandbox backend") {
		t.Fatalf("err = %v, want isolated backend error", err)
	}
}

func TestSupportsPrincipal(t *testing.T) {
	t.Parallel()

	base := NewRegistry(t.TempDir(), 2*time.Second)
	if base.SupportsPrincipal(principal.Principal{Role: principal.RoleAdmin}) {
		t.Fatal("SupportsPrincipal(admin) = true, want false without resolver")
	}

	tmp := t.TempDir()
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        filepath.Join(tmp, "global"),
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}

	withSandbox := NewRegistryWithSandbox(filepath.Join(tmp, "global"), 2*time.Second, resolver)
	withSandbox.runner = sandbox.NewRunnerWithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	})
	approved := principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser}
	if withSandbox.SupportsPrincipal(approved) {
		t.Fatal("SupportsPrincipal(approved_user) = true, want false when isolated backend is unavailable")
	}
	setFakeBubblewrapRunner(t, withSandbox)

	if !withSandbox.SupportsPrincipal(principal.Principal{Role: principal.RoleAdmin}) {
		t.Fatal("SupportsPrincipal(admin) = false, want true with resolver")
	}
	if !withSandbox.SupportsPrincipal(approved) {
		t.Fatal("SupportsPrincipal(approved_user) = false, want true with resolver")
	}
}
