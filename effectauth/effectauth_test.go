//go:build linux

package effectauth

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestAuthorizeCommandRejectsBoundaryWideningUnderReadOnlyEnvelope(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeCommand(CommandRequest{
		State:   testContinuationState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now),
		Command: "gh pr create --base main --head fix --title test --body test",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want active boundary rejection", decision)
	}
	if decision.RequiredAction != "github_pr_create" {
		t.Fatalf("required action = %q, want github_pr_create", decision.RequiredAction)
	}
	if err := DecisionError(decision); err == nil || !strings.Contains(err.Error(), "command exceeds active continuation authority") {
		t.Fatalf("DecisionError = %v, want authority error", err)
	}
}

func TestAuthorizeCommandAllowsExternalAccountWithGrantCoverage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeCommand(CommandRequest{
		State:   testContinuationState("external_account_pr_create", []string{"github_pr_create", "report_pr_link"}, true, now),
		Command: "gh pr create --base main --head fix --title test --body test",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || !decision.Allowed {
		t.Fatalf("decision = %#v, want external-account command allowed by envelope and grant coverage", decision)
	}
	if decision.RequiredAction != "github_pr_create" {
		t.Fatalf("required action = %q, want github_pr_create", decision.RequiredAction)
	}
}

func TestAuthorizeCommandRejectsExternalAccountWithoutGrantCoverage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeCommand(CommandRequest{
		State:   testContinuationState("external_account_pr_create", []string{"github_pr_create", "report_pr_link"}, false, now),
		Command: "gh pr create --base main --head fix --title test --body test",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want external-account command rejected without grant coverage", decision)
	}
	if decision.Reason != "external_effect_missing_capability_grant" {
		t.Fatalf("reason = %q, want external_effect_missing_capability_grant", decision.Reason)
	}
}

func TestAuthorizeCommandAllowsGitPushOnlyWhenEnvelopeAllowsGitPush(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testContinuationState("repo_publication", []string{"git_push", "report_release_result"}, false, now)
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassRepoPublication
	allowed := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "git push origin release/v0.2.9",
		Now:     now,
	})
	if !allowed.Active || !allowed.Boundary || !allowed.Allowed {
		t.Fatalf("allowed decision = %#v, want git push allowed by envelope", allowed)
	}
	if allowed.RequiredAction != "git_push" {
		t.Fatalf("required action = %q, want git_push", allowed.RequiredAction)
	}

	denied := AuthorizeCommand(CommandRequest{
		State:   testContinuationState("commit", []string{"git_commit", "report_commit"}, false, now),
		Command: "git push origin release/v0.2.9",
		Now:     now,
	})
	if !denied.Active || !denied.Boundary || denied.Allowed {
		t.Fatalf("denied decision = %#v, want git push rejected without git_push action", denied)
	}
}

func TestAuthorizeCommandInvalidAuthorityContractFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testInvalidAuthorityContractState(now)
	decision := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "git push origin release/v0.2.9",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want active boundary denial for invalid authority contract", decision)
	}
	if decision.Reason != reasonInvalidAuthorityContract {
		t.Fatalf("reason = %q, want %q", decision.Reason, reasonInvalidAuthorityContract)
	}
	if decision.RequiredAction != "git_push" {
		t.Fatalf("required action = %q, want git_push", decision.RequiredAction)
	}
	if err := DecisionError(decision); err == nil || !strings.Contains(err.Error(), reasonInvalidAuthorityContract) {
		t.Fatalf("DecisionError = %v, want invalid authority contract error", err)
	}
}

func TestAuthorizeWorkModeCommandDoesNotFallbackForInvalidAuthorityContract(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeWorkModeCommand(WorkModeRequest{
		State:    testInvalidAuthorityContractState(now),
		Mode:     WorkModeWorkspaceWrite,
		RepoRoot: "/repo",
		Workdir:  "/repo",
		Command:  "touch generated.txt",
		Now:      now,
	})
	if !decision.Active || decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want invalid contract to block before work-mode fallback", decision)
	}
	if decision.Reason != reasonInvalidAuthorityContract {
		t.Fatalf("reason = %q, want %q", decision.Reason, reasonInvalidAuthorityContract)
	}
}

func TestDecisionErrorRejectsInvalidAuthorityContractWithoutBoundary(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeCommand(CommandRequest{
		State:   testInvalidAuthorityContractState(now),
		Command: "git status --short",
		Now:     now,
	})
	if !decision.Active || decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want invalid contract denial without boundary metadata", decision)
	}
	if decision.Reason != reasonInvalidAuthorityContract {
		t.Fatalf("reason = %q, want %q", decision.Reason, reasonInvalidAuthorityContract)
	}
	if err := DecisionError(decision); err == nil || !strings.Contains(err.Error(), reasonInvalidAuthorityContract) {
		t.Fatalf("DecisionError = %v, want invalid authority contract error", err)
	}
}

func TestRepoPublicationEnvelopeDoesNotAuthorizeExternalAccountMutation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testContinuationState("repo_publication", []string{"git_push", "report_push_evidence"}, false, now)
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassRepoPublication

	push := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "git push origin release/v0.2.9",
		Now:     now,
	})
	if !push.Active || !push.Boundary || !push.Allowed {
		t.Fatalf("push decision = %#v, want git push allowed by repo-publication envelope", push)
	}
	if push.RequiredAction != "git_push" {
		t.Fatalf("push required action = %q, want git_push", push.RequiredAction)
	}

	pr := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "gh pr create --base main --head release/v0.2.9 --title test --body test",
		Now:     now,
	})
	if !pr.Active || !pr.Boundary || pr.Allowed {
		t.Fatalf("pr decision = %#v, want external-account command rejected by repo-publication envelope", pr)
	}
	if pr.RequiredAction != "github_pr_create" {
		t.Fatalf("pr required action = %q, want github_pr_create", pr.RequiredAction)
	}
	if pr.Reason != "external_effect_missing_capability_grant" {
		t.Fatalf("pr reason = %q, want external_effect_missing_capability_grant", pr.Reason)
	}
}

func TestAuthorizeCommandRejectsExternalAccountWhenContractDisallowsExternalEffects(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeCommand(CommandRequest{
		State:   testContinuationState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now),
		Command: "gh pr create --base main --head fix --title test --body test",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want external-account command rejected by contract", decision)
	}
	if decision.Reason != "external_effect_not_allowed_by_contract" {
		t.Fatalf("reason = %q, want external_effect_not_allowed_by_contract", decision.Reason)
	}
}

func TestAuthorizeCommandAllowsExternalAccountStatusCheckOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testContinuationState("external_account_status_check", []string{"external_account_status_check", "report_release_status"}, false, now)
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassDataAccess
	decision := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "gh auth status",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary || !decision.Allowed {
		t.Fatalf("decision = %#v, want external-account status allowed", decision)
	}
	if decision.RequiredAction != "external_account_status_check" {
		t.Fatalf("required action = %q, want external_account_status_check", decision.RequiredAction)
	}
}

func TestAuthorizeCommandBoundaryKindsAllowAndDeny(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	for _, tt := range []struct {
		name           string
		command        string
		allowRiskClass string
		allowActions   []string
		wantAction     string
	}{
		{
			name:           "remote host",
			command:        "ssh aphelion.example uptime",
			allowRiskClass: "remote_host_operation",
			allowActions:   []string{"ssh", "report_remote_status"},
			wantAction:     "ssh",
		},
		{
			name:           "service process",
			command:        "systemctl --user restart aphelion.service",
			allowRiskClass: "deploy",
			allowActions:   []string{"restart_service", "post_restart_verification"},
			wantAction:     "restart_service",
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			allowed := AuthorizeCommand(CommandRequest{
				State:   testContinuationState(tt.allowRiskClass, tt.allowActions, false, now),
				Command: tt.command,
				Now:     now,
			})
			if !allowed.Active || !allowed.Boundary || !allowed.Allowed {
				t.Fatalf("allowed decision = %#v, want boundary command allowed by explicit envelope action", allowed)
			}
			if allowed.RequiredAction != tt.wantAction {
				t.Fatalf("required action = %q, want %q", allowed.RequiredAction, tt.wantAction)
			}

			denied := AuthorizeCommand(CommandRequest{
				State:   testContinuationState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now),
				Command: tt.command,
				Now:     now,
			})
			if !denied.Active || !denied.Boundary || denied.Allowed {
				t.Fatalf("denied decision = %#v, want boundary command denied by read-only envelope", denied)
			}
		})
	}
}

func TestAuthorizeWorkModeCommandFallsBackWithoutActiveEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    WorkMode
		command string
		want    bool
	}{
		{name: "read only inspection", mode: WorkModeReadOnly, command: "git status --short", want: true},
		{name: "read only rejects commit", mode: WorkModeReadOnly, command: "git commit -am test", want: false},
		{name: "commit mode allows commit", mode: WorkModeCommit, command: "git commit -am test", want: true},
		{name: "commit mode rejects push", mode: WorkModeCommit, command: "git push origin main", want: false},
		{name: "deploy mode allows service", mode: WorkModeDeploy, command: "systemctl --user restart aphelion.service", want: true},
		{name: "write mode rejects service", mode: WorkModeWorkspaceWrite, command: "systemctl --user restart aphelion.service", want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decision := AuthorizeWorkModeCommand(WorkModeRequest{
				Mode:     tt.mode,
				RepoRoot: "/repo",
				Workdir:  "/repo",
				Command:  tt.command,
			})
			if decision.Allowed != tt.want {
				t.Fatalf("decision = %#v, want allowed=%v", decision, tt.want)
			}
		})
	}
}

func TestAuthorizeWorkModeCommandRejectsWorkdirOutsideRepoRoot(t *testing.T) {
	t.Parallel()

	decision := AuthorizeWorkModeCommand(WorkModeRequest{
		Mode:     WorkModeWorkspaceWrite,
		RepoRoot: "/repo",
		Workdir:  "/tmp/outside",
		Command:  "touch generated.txt",
	})
	if decision.Allowed {
		t.Fatalf("decision = %#v, want workdir outside repo root rejected", decision)
	}
	if decision.Reason != "workdir_outside_repo_root" {
		t.Fatalf("reason = %q, want workdir_outside_repo_root", decision.Reason)
	}
}

func TestAuthorizeWorkModeCommandActiveEnvelopeOverridesFallback(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	decision := AuthorizeWorkModeCommand(WorkModeRequest{
		State:    testContinuationState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now),
		Mode:     WorkModeDeploy,
		RepoRoot: "/repo",
		Workdir:  "/repo",
		Command:  "gh pr create --base main --head fix --title test --body test",
		Now:      now,
	})
	if !decision.Active || !decision.Boundary || decision.Allowed {
		t.Fatalf("decision = %#v, want active read-only envelope to reject deploy-mode external account command", decision)
	}
}

func TestAuthorizeCommandActiveEnvelopeRejectsUnboundedAndNonBoundarySideEffects(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testContinuationState("read_only_review", []string{"read_only", "inspect_code", "report_findings"}, false, now)
	for _, command := range []string{
		"git status --short && mkdir out",
		"touch generated.txt",
		"cat README.md > generated.txt",
		"curl -X POST https://example.com/hook",
		"sqlite3 state.db 'delete from runs'",
		"eval 'git push origin main'",
		`echo "$(git push origin main)"`,
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			decision := AuthorizeCommand(CommandRequest{State: state, Command: command, Now: now})
			if !decision.Active {
				t.Fatalf("decision = %#v, want active continuation envelope", decision)
			}
			if decision.Allowed {
				t.Fatalf("decision = %#v, want active envelope to deny non-read-only or dynamically unbounded side effect", decision)
			}
		})
	}
}

func TestAuthorizeCommandRequiresEveryCompoundEffect(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	for _, tc := range []struct {
		name    string
		state   session.ContinuationState
		command string
	}{
		{
			name:    "external account approval does not cover preceding push",
			state:   testContinuationState("external_account_pr_create", []string{"github_pr_create", "report_pr_link"}, true, now),
			command: "git push origin main && gh pr create --fill",
		},
		{
			name:    "service approval does not cover preceding push",
			state:   testContinuationState("deploy", []string{"restart_service", "post_restart_verification"}, false, now),
			command: "git push origin main && systemctl --user restart aphelion.service",
		},
		{
			name:    "repo publication approval does not cover pull request metadata",
			state:   testContinuationState("repo_publication", []string{"git_push", "report_push_evidence"}, false, now),
			command: "git push origin main && gh pr create --fill",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if strings.TrimSpace(tc.state.ActionProposal.RiskClass) == "repo_publication" {
				tc.state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassRepoPublication
			}
			decision := AuthorizeCommand(CommandRequest{State: tc.state, Command: tc.command, Now: now})
			if !decision.Active {
				t.Fatalf("decision = %#v, want active continuation envelope", decision)
			}
			if decision.Allowed {
				t.Fatalf("decision = %#v, want compound command denied until every effect is independently authorized", decision)
			}
		})
	}
}

func TestAuthorizeCommandDeployClassDoesNotAuthorizeRepoPublication(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := testContinuationState("deploy", []string{"deploy", "run_deploy", "restart_service", "run_verify_deploy"}, false, now)
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassDeployRestart
	decision := AuthorizeCommand(CommandRequest{
		State:   state,
		Command: "git push origin release/v0.2.9",
		Now:     now,
	})
	if !decision.Active || !decision.Boundary {
		t.Fatalf("decision = %#v, want active git-push boundary decision", decision)
	}
	if decision.Allowed {
		t.Fatalf("decision = %#v, want deploy/restart authority to deny repository publication without repo_publication + git_push", decision)
	}
}

func TestAuthorizeCommandRequiresExactEffectAction(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	commitState := testContinuationState("commit", []string{"git_commit", "report_commit_evidence"}, false, now)
	for _, command := range []string{
		"git reset --hard HEAD~1",
		"git clean -fd",
		"git branch -D stale",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			decision := AuthorizeCommand(CommandRequest{State: commitState, Command: command, Now: now})
			if !decision.Active || decision.Allowed {
				t.Fatalf("decision = %#v, want git_commit authority to deny distinct repo-history action", decision)
			}
		})
	}

	sshState := testContinuationState("remote_host_operation", []string{"scp", "report_remote_status"}, false, now)
	ssh := AuthorizeCommand(CommandRequest{State: sshState, Command: "ssh aphelion.example uptime", Now: now})
	if !ssh.Active || ssh.Allowed {
		t.Fatalf("decision = %#v, want scp action not to authorize ssh action", ssh)
	}

	serviceState := testContinuationState("deploy", []string{"systemctl_enable", "post_enable_check"}, false, now)
	restart := AuthorizeCommand(CommandRequest{State: serviceState, Command: "systemctl restart aphelion.service", Now: now})
	if !restart.Active || restart.Allowed {
		t.Fatalf("decision = %#v, want systemctl_enable not to authorize systemctl_restart", restart)
	}
}

func testInvalidAuthorityContractState(now time.Time) session.ContinuationState {
	state := testContinuationState("commit", []string{"git_push", "report_push_evidence"}, false, now)
	state.ActionProposal.ForbiddenActions = []string{"git_push"}
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassRepoPublication
	state.ContinuationLease.ForbiddenActions = []string{"git_push"}
	return state
}

func testContinuationState(riskClass string, allowedActions []string, capabilityGrant bool, now time.Time) session.ContinuationState {
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
