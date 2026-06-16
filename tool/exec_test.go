//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type stubExecApprover struct {
	called   int
	approved bool
	request  ExecApprovalRequest
}

func TestRegistryDefinitionsHaveValidJSONParameters(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second).WithSessionStore(newToolTestStore(t))
	for _, def := range registry.Definitions() {
		if !json.Valid(def.Parameters) {
			t.Fatalf("%s parameters are invalid JSON: %s", def.Name, string(def.Parameters))
		}
	}
}

func (s *stubExecApprover) ConfirmExec(_ context.Context, req ExecApprovalRequest) (ExecApprovalDecision, error) {
	s.called++
	s.request = req
	return ExecApprovalDecision{Approved: s.approved}, nil
}

func TestExecContinuationAuthorityRejectsAutoApprovalWidening(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: true}
	registry := NewRegistry(workspace, time.Second).WithExecApprover(approver)
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-read-only",
			RiskClass:      "read_only_review",
			AllowedActions: []string{"read_only", "inspect_code", "report_findings"},
			Status:         session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-read-only",
			ProposalID:     "aprop-read-only",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			AllowedActions: []string{"read_only", "inspect_code", "report_findings"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	ctx := WithContinuationExecAuthority(context.Background(), state)
	_, err := registry.executeWithScopeAndPrincipal(ctx, "exec", json.RawMessage(`{"command":"gh pr create --base main --head fix --title test --body test"}`), sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace}, principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 1001}, session.SessionKey{ChatID: 8801, UserID: 0})
	if err == nil || !strings.Contains(err.Error(), "command exceeds active continuation authority") {
		t.Fatalf("exec err = %v, want continuation authority rejection", err)
	}
	if approver.called != 0 {
		t.Fatalf("approver called = %d, want rejection before proposal approval can widen authority", approver.called)
	}
}

func TestContinuationExecAuthorityAllowsExternalAccountPRCreateWithGrant(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-release-pr",
			RiskClass:      "external_account_pr_create",
			AllowedActions: []string{"github_pr_create", "report_pr_link"},
			Status:         session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:                 "lease-release-pr",
			ProposalID:         "aprop-release-pr",
			Status:             session.ContinuationLeaseStatusActive,
			RemainingTurns:     1,
			LeaseClass:         session.ContinuationLeaseClassCapabilityGrant,
			AllowedActions:     []string{"github_pr_create", "invoke_active_capability_grant", "report_capability_result"},
			CapabilityGrantIDs: []string{"capg-release-pr"},
			ExpiresAt:          now.Add(time.Hour),
		},
	}
	decision := ContinuationExecAuthorityDecisionForCommand(state, "gh pr create --base release/v0.2.5 --head main --title test --body test", now)
	if !decision.Active || !decision.Boundary || !decision.Allowed {
		t.Fatalf("decision = %#v, want external-account PR create allowed by envelope", decision)
	}
	if decision.RequiredAction != "github_pr_create" {
		t.Fatalf("required action = %q, want github_pr_create", decision.RequiredAction)
	}
}

func TestContinuationExecAuthorityAllowsExternalAccountStatusCheckOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-gh-status",
			RiskClass:      "external_account_status_check",
			AllowedActions: []string{"external_account_status_check", "report_release_status"},
			Status:         session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-gh-status",
			ProposalID:     "aprop-gh-status",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			LeaseClass:     session.ContinuationLeaseClassDataAccess,
			AllowedActions: []string{"external_account_status_check", "report_release_status"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	status := ContinuationExecAuthorityDecisionForCommand(state, "gh auth status", now)
	if !status.Active || !status.Boundary || !status.Allowed {
		t.Fatalf("status decision = %#v, want status command allowed", status)
	}
	mutation := ContinuationExecAuthorityDecisionForCommand(state, "gh pr create --base main --head fix --title test --body test", now)
	if !mutation.Active || !mutation.Boundary || mutation.Allowed {
		t.Fatalf("mutation decision = %#v, want PR creation rejected under status-only authority", mutation)
	}
}

func TestContinuationExecAuthorityBoundaryKindsAllowAndDeny(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	tests := []struct {
		name           string
		command        string
		allowRiskClass string
		allowActions   []string
		wantAction     string
	}{
		{
			name:           "git push",
			command:        "git push origin fix/continuation-authority-envelope",
			allowRiskClass: "deploy",
			allowActions:   []string{"git_push", "report_release_result"},
			wantAction:     "git_push",
		},
		{
			name:           "remote host",
			command:        "ssh aphelion.example uptime",
			allowRiskClass: "remote_host_operation",
			allowActions:   []string{"remote_host_operation", "report_remote_status"},
			wantAction:     "remote_host_operation",
		},
		{
			name:           "service process",
			command:        "systemctl --user restart aphelion.service",
			allowRiskClass: "deploy",
			allowActions:   []string{"restart_service", "post_restart_verification"},
			wantAction:     "restart_service",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			allowed := ContinuationExecAuthorityDecisionForCommand(
				continuationExecAuthorityTestState(tt.allowRiskClass, tt.allowActions, false, now),
				tt.command,
				now,
			)
			if !allowed.Active || !allowed.Boundary || !allowed.Allowed {
				t.Fatalf("allowed decision = %#v, want boundary command allowed by explicit envelope action", allowed)
			}
			if allowed.RequiredAction != tt.wantAction {
				t.Fatalf("required action = %q, want %q", allowed.RequiredAction, tt.wantAction)
			}

			denied := ContinuationExecAuthorityDecisionForCommand(
				continuationExecAuthorityTestState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now),
				tt.command,
				now,
			)
			if !denied.Active || !denied.Boundary || denied.Allowed {
				t.Fatalf("denied decision = %#v, want boundary command denied by ordinary read-only envelope", denied)
			}
		})
	}
}

func continuationExecAuthorityTestState(riskClass string, allowedActions []string, capabilityGrant bool, now time.Time) session.ContinuationState {
	lease := session.ContinuationLease{
		ID:             "lease-" + strings.ReplaceAll(riskClass, "_", "-"),
		ProposalID:     "aprop-" + strings.ReplaceAll(riskClass, "_", "-"),
		Status:         session.ContinuationLeaseStatusActive,
		RemainingTurns: 1,
		AllowedActions: append([]string(nil), allowedActions...),
		ExpiresAt:      now.Add(time.Hour),
	}
	if capabilityGrant {
		lease.LeaseClass = session.ContinuationLeaseClassCapabilityGrant
		lease.CapabilityGrantIDs = []string{"capg-" + strings.ReplaceAll(riskClass, "_", "-")}
	}
	return session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             lease.ProposalID,
			RiskClass:      riskClass,
			AllowedActions: append([]string(nil), allowedActions...),
			Status:         session.ProposalStatusApproved,
		},
		ContinuationLease: lease,
	}
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
export APHELION_FAKE_BWRAP=1
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

func TestExecAcceptsJSONStringWrappedObjectInput(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	wrapped, err := json.Marshal(`{"command":"printf 'ok'"}`)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	out, err := registry.Execute(context.Background(), "exec", json.RawMessage(wrapped))
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("output = %q, want command output", out)
	}
}

func TestExecAcceptsJSONStringWrappedObjectInputWithEscapedNewline(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	registry := NewRegistry(workspace, 2*time.Second)
	wrapped := stringWrappedJSON(t, `{"command":"printf 'ok\n'"}`)

	out, err := registry.Execute(context.Background(), "exec", wrapped)
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

func TestExecGitCommitRequiresApproval(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), 2*time.Second)
	_, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"git commit -m test"}`))
	if err == nil {
		t.Fatal("Execute() err = nil, want commit approval error")
	}
	if !strings.Contains(err.Error(), "requires an approved proposal") || !strings.Contains(err.Error(), "repository commit") {
		t.Fatalf("err = %v, want repository commit proposal error", err)
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
	if approver.request.Proposal.Kind != "possible_delete_command" {
		t.Fatalf("proposal kind = %q, want possible_delete_command", approver.request.Proposal.Kind)
	}
	if approver.request.SessionKey.ChatID != 7 {
		t.Fatalf("approver session = %+v, want chat id 7", approver.request.SessionKey)
	}
}

func TestExecSearchCommandDangerousNeedleSkipsApproval(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	for _, command := range []string{
		`rg -n "rm -rf|systemctl stop|drop table" .`,
		`rg -n "git push|gh pr merge|systemctl restart|kubectl delete" .`,
		`grep -R "rm -rf build" .`,
		`git grep "drop table users"`,
		`printf '%s\n' 'rm -rf build'`,
		`printf '%s\n' 'git push origin main'`,
	} {
		t.Run(command, func(t *testing.T) {
			_, err := registry.executeWithScopeAndPrincipal(
				context.Background(),
				"exec",
				json.RawMessage(`{"command":`+strconv.Quote(command)+`}`),
				sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
				principal.Principal{Role: principal.RoleAdmin},
				session.SessionKey{ChatID: 7},
			)
			if err == nil {
				return
			}
			if strings.Contains(err.Error(), "requires an approved proposal") || strings.Contains(err.Error(), "proposal denied") {
				t.Fatalf("command %q err = %v, want no approval request for quoted/search text", command, err)
			}
		})
	}
	if approver.called != 0 {
		t.Fatalf("approver called = %d, want no approval for read-only/search needles", approver.called)
	}
}

func TestExecShellCommandStringStillRequiresApproval(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"bash -c 'rm -rf build'"}`),
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
	if approver.request.Proposal.Kind != "possible_delete_command" {
		t.Fatalf("proposal kind = %q, want possible_delete_command", approver.request.Proposal.Kind)
	}
}

func TestExecWrappedDangerousCommandsStillRequireApproval(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`sudo -n rm -rf build`,
		`env -i PATH=/usr/bin rm -rf build`,
		`timeout 5 rm -rf build`,
	} {
		t.Run(command, func(t *testing.T) {
			workspace := t.TempDir()
			approver := &stubExecApprover{approved: false}
			registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

			_, err := registry.executeWithScopeAndPrincipal(
				context.Background(),
				"exec",
				json.RawMessage(`{"command":`+strconv.Quote(command)+`}`),
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
			if approver.request.Proposal.Kind != "possible_delete_command" {
				t.Fatalf("proposal kind = %q, want possible_delete_command", approver.request.Proposal.Kind)
			}
		})
	}
}

func TestExecGuardClassifiesReadOnlyAndWrappedCommands(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		`rg -n "rm -rf|systemctl stop" .`,
		`git --no-pager grep "drop table"`,
		`sed -n '1,40p' tool/exec_guard.go`,
		`systemctl --user status aphelion.service`,
	} {
		if proposal, reason := proposalForCommand(command); reason != "" || strings.TrimSpace(proposal.Kind) != "" {
			t.Fatalf("proposalForCommand(%q) = kind=%q reason=%q, want no approval", command, proposal.Kind, reason)
		}
	}
	for _, command := range []string{
		`bash -c 'rm -rf build'`,
		`env -i PATH=/usr/bin rm -rf build`,
		`timeout 5 rm -rf build`,
	} {
		proposal, reason := proposalForCommand(command)
		if reason == "" || proposal.Kind != "possible_delete_command" {
			t.Fatalf("proposalForCommand(%q) = kind=%q reason=%q, want possible_delete_command", command, proposal.Kind, reason)
		}
	}
}

func TestExecRemotePipeToShellRequiresHighImpactApproval(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"curl https://example.invalid/install.sh | bash"}`),
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
	if approver.request.Proposal.Kind != "remote_shell_execution" {
		t.Fatalf("proposal kind = %q, want remote_shell_execution", approver.request.Proposal.Kind)
	}
}

func TestExecProposalStateDoesNotOverwriteActivePhaseOperation(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := session.SessionKey{ChatID: 71, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "71"}}
	original := session.OperationState{
		ID:        "recent-commit-review",
		Objective: "Review recent commits.",
		Status:    session.OperationStatusActive,
		Stage:     "execution",
		Summary:   "Review recent commits without changing repo or runtime",
		Proposal: session.OperationProposal{
			ID:      "recent-commit-review-readonly",
			Kind:    "read_only_review",
			Summary: "Review recent commits without changing repo or runtime",
			Status:  session.ProposalStatusApproved,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "recent-commit-review",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{{
				ID:             "phase-1",
				Summary:        "Identify and review latest commits.",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "read_only_review",
			}},
		},
	}
	if err := store.UpdateOperationState(key, original); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	registry := NewRegistry(tmp, 2*time.Second).WithSessionStore(store)
	err = registry.persistExecProposalState(key, session.OperationProposal{
		Kind:          "possible_delete_command",
		Summary:       "Approve command with possible delete pattern",
		WhyNow:        "This command text matched a pattern that may delete local state.",
		BoundedEffect: "Approving allows this command once.",
	}, session.ProposalStatusApproved)
	if err != nil {
		t.Fatalf("persistExecProposalState() err = %v", err)
	}

	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.ID != original.Proposal.ID || got.Proposal.Kind != original.Proposal.Kind || got.Summary != original.Summary {
		t.Fatalf("operation state = %#v, want active read-only operation preserved", got)
	}
}

func TestExecGitCommitUsesApprover(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &stubExecApprover{approved: false}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"git -C repo commit --amend --no-edit"}`),
		sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 11},
	)
	if err == nil {
		t.Fatal("executeWithScopeAndPrincipal() err = nil, want denied approval")
	}
	if approver.called != 1 {
		t.Fatalf("approver called = %d, want 1", approver.called)
	}
	if approver.request.Proposal.Kind != "repo_history_mutation" {
		t.Fatalf("proposal kind = %q, want repo_history_mutation", approver.request.Proposal.Kind)
	}
	if approver.request.Reason != "repository commit" {
		t.Fatalf("proposal reason = %q, want repository commit", approver.request.Reason)
	}
}

func TestExecBoundaryCrossingCommandsRequireApproval(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
		kind    string
		reason  string
	}{
		{name: "git_push", command: "git push origin main", kind: "repo_history_mutation", reason: "repository push"},
		{name: "git_push_with_global_option", command: "git -C repo push --force origin branch", kind: "repo_history_mutation", reason: "repository push"},
		{name: "gh_pr_create", command: "gh pr create --fill", kind: "external_account_command", reason: "external account command"},
		{name: "gh_pr_merge", command: "gh pr merge 208", kind: "external_account_command", reason: "external account command"},
		{name: "aws", command: "aws sts get-caller-identity", kind: "external_account_command", reason: "external account command"},
		{name: "gcloud", command: "gcloud auth print-access-token", kind: "external_account_command", reason: "external account command"},
		{name: "az", command: "az account show", kind: "external_account_command", reason: "external account command"},
		{name: "op", command: "op item get production-token", kind: "external_account_command", reason: "external account command"},
		{name: "ssh", command: "ssh host.example uptime", kind: "remote_host_operation", reason: "remote host operation"},
		{name: "scp", command: "scp notes.txt host.example:/tmp/notes.txt", kind: "remote_host_operation", reason: "remote host operation"},
		{name: "rsync", command: "rsync -av . host.example:/tmp/work", kind: "remote_host_operation", reason: "remote host operation"},
		{name: "systemctl_restart", command: "systemctl --user restart aphelion.service", kind: "service_process_change", reason: "service/process change"},
		{name: "systemctl_start", command: "systemctl start aphelion.service", kind: "service_process_change", reason: "service/process change"},
		{name: "systemctl_reload", command: "systemctl reload aphelion.service", kind: "service_process_change", reason: "service/process change"},
		{name: "systemctl_enable", command: "systemctl enable aphelion.service", kind: "service_process_change", reason: "service/process change"},
		{name: "systemctl_daemon_reload", command: "systemctl --user daemon-reload", kind: "service_process_change", reason: "service/process change"},
		{name: "docker", command: "docker ps", kind: "service_process_change", reason: "service/process change"},
		{name: "kubectl", command: "kubectl get pods", kind: "service_process_change", reason: "service/process change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			proposal, reason := proposalForCommand(tc.command)
			if reason != tc.reason || proposal.Kind != tc.kind {
				t.Fatalf("proposalForCommand(%q) = kind=%q reason=%q, want %q/%q", tc.command, proposal.Kind, reason, tc.kind, tc.reason)
			}
		})
	}
}

func TestExecInterruptionCommandKindsStaySpecific(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
		kind    string
		reason  string
	}{
		{name: "systemctl_stop", command: "systemctl --user stop aphelion.service", kind: "service_interruption_command", reason: "stop or disable system service"},
		{name: "systemctl_disable", command: "systemctl disable aphelion.service", kind: "service_interruption_command", reason: "stop or disable system service"},
		{name: "systemctl_mask", command: "systemctl mask aphelion.service", kind: "service_interruption_command", reason: "stop or disable system service"},
		{name: "kill_all", command: "kill -9 -1", kind: "process_interruption_command", reason: "kill all processes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			proposal, reason := proposalForCommand(tc.command)
			if reason != tc.reason || proposal.Kind != tc.kind {
				t.Fatalf("proposalForCommand(%q) = kind=%q reason=%q, want %q/%q", tc.command, proposal.Kind, reason, tc.kind, tc.reason)
			}
		})
	}
}

func TestExecBoundaryCrossingCommandsUseApprover(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
		kind    string
		reason  string
	}{
		{name: "git_push", command: "git push origin main", kind: "repo_history_mutation", reason: "repository push"},
		{name: "gh_pr_merge", command: "gh pr merge 208", kind: "external_account_command", reason: "external account command"},
		{name: "systemctl_restart", command: "systemctl --user restart aphelion.service", kind: "service_process_change", reason: "service/process change"},
		{name: "kubectl", command: "kubectl apply -f deploy.yaml", kind: "service_process_change", reason: "service/process change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			workspace := t.TempDir()
			approver := &stubExecApprover{approved: false}
			registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

			_, err := registry.executeWithScopeAndPrincipal(
				context.Background(),
				"exec",
				json.RawMessage(`{"command":`+strconv.Quote(tc.command)+`}`),
				sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
				principal.Principal{Role: principal.RoleAdmin},
				session.SessionKey{ChatID: 12},
			)
			if err == nil {
				t.Fatal("executeWithScopeAndPrincipal() err = nil, want denied approval")
			}
			if approver.called != 1 {
				t.Fatalf("approver called = %d, want 1", approver.called)
			}
			if approver.request.Proposal.Kind != tc.kind {
				t.Fatalf("proposal kind = %q, want %q", approver.request.Proposal.Kind, tc.kind)
			}
			if approver.request.Reason != tc.reason {
				t.Fatalf("proposal reason = %q, want %q", approver.request.Reason, tc.reason)
			}
		})
	}
}

type timedOutExecApprover struct {
	request ExecApprovalRequest
}

func (t *timedOutExecApprover) ConfirmExec(_ context.Context, req ExecApprovalRequest) (ExecApprovalDecision, error) {
	t.request = req
	return ExecApprovalDecision{
		Approved:             false,
		DecisionID:           "commit-decision-1",
		Choice:               "deny",
		TimedOut:             true,
		DefaultChoice:        "deny",
		RequiredApprovalKind: "proposal_approval",
	}, nil
}

func TestExecGitCommitDeniedErrorExplainsNestedApproval(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	approver := &timedOutExecApprover{}
	registry := NewRegistry(workspace, 2*time.Second).WithExecApprover(approver)

	_, err := registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"git commit -m test"}`),
		sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 7},
	)
	if err == nil {
		t.Fatal("executeWithScopeAndPrincipal() err = nil, want repository commit denial diagnostic")
	}
	got := err.Error()
	for _, want := range []string{
		"proposal denied: repository commit",
		"gate: repository_commit",
		"required_approval_kind: proposal_approval",
		"required_approval_status: expired",
		"required_approval_default: deny",
		"denial_reason: timeout",
		"decision_id: commit-decision-1",
		"decision_choice: deny",
		"continuation_approval_covered: false",
		"git commit opens a separate repository-history proposal gate",
		"next_action: approve the specific git commit proposal card",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error = %q, want %q", got, want)
		}
	}
}

func TestExecGitCommitTimeoutPersistsExpiredProposalState(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := session.SessionKey{ChatID: 7, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "7"}}
	registry := NewRegistry(workspace, 2*time.Second).
		WithSessionStore(store).
		WithExecApprover(&timedOutExecApprover{})

	_, err = registry.executeWithScopeAndPrincipal(
		context.Background(),
		"exec",
		json.RawMessage(`{"command":"git commit -m test"}`),
		sandbox.Scope{WorkingRoot: workspace, SharedMemoryRoot: workspace},
		principal.Principal{Role: principal.RoleAdmin},
		key,
	)
	if err == nil {
		t.Fatal("executeWithScopeAndPrincipal() err = nil, want repository commit denial")
	}

	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Kind != "repo_history_mutation" || got.Proposal.Status != session.ProposalStatusExpired {
		t.Fatalf("proposal = kind:%q status:%q, want repo_history_mutation/expired", got.Proposal.Kind, got.Proposal.Status)
	}
	if !strings.Contains(got.Summary, "Repository commit blocked: approval timed out/default-denied") || !strings.Contains(got.Summary, "request and approve a fresh git commit proposal") {
		t.Fatalf("summary = %q, want causal repository commit timeout next-action", got.Summary)
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

func TestExecuteForAdminAllowsEscapedWorkdirWithApproval(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	outsideRoot := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
		t.Fatalf("mkdir outside root: %v", err)
	}
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

	approver := &stubExecApprover{approved: true}
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithExecApprover(approver)
	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 7},
		"exec",
		json.RawMessage(`{"command":"pwd","workdir":`+strconv.Quote(outsideRoot)+`}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v", err)
	}
	if !strings.Contains(out, outsideRoot) {
		t.Fatalf("output = %q, want pwd under approved outside root %q", out, outsideRoot)
	}
	if approver.called != 1 {
		t.Fatalf("approver called = %d, want 1", approver.called)
	}
	if approver.request.Proposal.Kind != "workspace_escape" {
		t.Fatalf("proposal kind = %q, want workspace_escape", approver.request.Proposal.Kind)
	}
}

func TestExecuteForAdminRejectsEscapedWorkdirWithoutApproval(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	outsideRoot := filepath.Join(tmp, "outside")
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
	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		session.SessionKey{ChatID: 7},
		"exec",
		json.RawMessage(`{"command":"pwd","workdir":`+strconv.Quote(outsideRoot)+`}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal() err = nil, want approval requirement")
	}
	if !strings.Contains(err.Error(), "approved proposal") {
		t.Fatalf("err = %v, want approval requirement", err)
	}
}
