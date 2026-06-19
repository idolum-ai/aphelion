//go:build linux

package effectauth

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

type WorkMode string

const (
	WorkModeReadOnly       WorkMode = "read_only"
	WorkModeWorkspaceWrite WorkMode = "workspace_write"
	WorkModeCommit         WorkMode = "commit"
	WorkModeDeploy         WorkMode = "deploy"
)

// Decision records the canonical effect-authorization result for a command.
// It is intentionally evidence-shaped: callers can use the same record for
// enforcement, projections, and audit without re-deriving authority.
type Decision struct {
	Active                 bool
	Boundary               bool
	Allowed                bool
	Reason                 string
	BoundaryKind           string
	EffectKind             string
	RequiredAction         string
	LeaseID                string
	ProposalID             string
	PhaseID                string
	AuthorityClass         string
	ExternalEffectsAllowed bool
}

type CommandRequest struct {
	State   session.ContinuationState
	Command string
	Now     time.Time
}

type WorkModeRequest struct {
	State    session.ContinuationState
	Mode     WorkMode
	RepoRoot string
	Workdir  string
	Command  string
	Now      time.Time
}

func AuthorizeCommand(req CommandRequest) Decision {
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state := session.NormalizeContinuationState(req.State)
	decision := decisionBase(state, now)
	if !decision.Active {
		return decision
	}
	effect := commandeffect.Classify(req.Command)
	decision.EffectKind = string(effect.Kind)
	boundary, boundaryOK := commandeffect.BoundaryForCommand(req.Command)
	if !boundaryOK {
		if effect.ReadOnlyAllowed() {
			decision.Allowed = true
			decision.Reason = "read_only_effect"
			return decision
		}
		decision.Allowed = true
		decision.Reason = "non_boundary_effect"
		return decision
	}
	decision.Boundary = true
	decision.BoundaryKind = string(boundary.Kind)
	decision.RequiredAction = boundaryRequiredAction(boundary.Kind, req.Command)
	return decisionForBoundary(state, decision, boundary.Kind, req.Command)
}

// AuthorizeWorkModeCommand applies the same continuation-envelope decision used
// by tools and falls back to the generic work-mode policy only when there is no
// active boundary-crossing continuation decision.
func AuthorizeWorkModeCommand(req WorkModeRequest) Decision {
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision := AuthorizeCommand(CommandRequest{
		State:   req.State,
		Command: req.Command,
		Now:     now,
	})
	if decision.Active && decision.Boundary {
		return decision
	}
	return authorizeByWorkMode(req)
}

func DecisionError(decision Decision) error {
	if !decision.Active || !decision.Boundary || decision.Allowed {
		return nil
	}
	required := strings.TrimSpace(decision.RequiredAction)
	if required == "" {
		required = strings.TrimSpace(decision.BoundaryKind)
	}
	return fmt.Errorf("command exceeds active continuation authority: boundary=%s required_action=%s reason=%s lease_id=%s proposal_id=%s phase_id=%s",
		strings.TrimSpace(decision.BoundaryKind),
		required,
		strings.TrimSpace(decision.Reason),
		strings.TrimSpace(decision.LeaseID),
		strings.TrimSpace(decision.ProposalID),
		strings.TrimSpace(decision.PhaseID),
	)
}

func decisionBase(state session.ContinuationState, now time.Time) Decision {
	decision := Decision{
		LeaseID:    strings.TrimSpace(state.ContinuationLease.ID),
		ProposalID: strings.TrimSpace(state.ActionProposal.ID),
	}
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		decision.PhaseID = firstNonEmpty(phase.OperationPhaseID, phase.ID)
		decision.AuthorityClass = strings.TrimSpace(phase.AuthorityClass)
	}
	if decision.AuthorityClass == "" {
		decision.AuthorityClass = strings.TrimSpace(state.ActionProposal.RiskClass)
	}
	if state.Status != session.ContinuationStatusApproved || !state.ContinuationLease.ActiveAt(now) {
		decision.Reason = "no_active_continuation_lease"
		return decision
	}
	compilation := session.CompileContinuationAuthorityContract(state)
	decision.ExternalEffectsAllowed = compilation.Contract.ExternalEffectsAllowed
	if compilation.Invalid() {
		decision.Active = true
		decision.Reason = "invalid_authority_contract"
		return decision
	}
	decision.Active = true
	return decision
}

func decisionForBoundary(state session.ContinuationState, decision Decision, kind commandeffect.BoundaryKind, command string) Decision {
	switch kind {
	case commandeffect.BoundaryExternalAccount:
		if externalAccountStatusCommand(command) {
			return decisionRequireAnyAction(state, decision, []string{
				"external_account_status_check",
				"external_account_auth_status",
				"read_only_auth_status_check",
				"credential_metadata_check",
				"credential_metadata",
				"token_health_check",
			})
		}
		if !decision.ExternalEffectsAllowed {
			decision.Reason = "external_effect_not_allowed_by_contract"
			return decision
		}
		if !hasCapabilityGrantCoverage(state) {
			decision.Reason = "external_effect_missing_capability_grant"
			return decision
		}
		return decisionRequireAnyAction(state, decision, externalAccountMutationActions(command))
	case commandeffect.BoundaryGitCommit:
		return decisionRequireAnyAction(state, decision, []string{"git_commit", "commit", "repo_history_mutation"})
	case commandeffect.BoundaryGitPush:
		return decisionRequireAnyAction(state, decision, []string{"git_push", "push_remote", "deploy", "run_deploy"})
	case commandeffect.BoundaryRemoteHostOperation:
		return decisionRequireAnyAction(state, decision, []string{"remote_host_operation", "ssh", "scp", "rsync", "deploy", "run_deploy"})
	case commandeffect.BoundaryServiceProcessChange:
		return decisionRequireAnyAction(state, decision, []string{"service_process_change", "restart_service", "service_restart", "systemctl_restart", "deploy", "run_deploy"})
	default:
		decision.Reason = "unhandled_boundary_effect"
		return decision
	}
}

func decisionRequireAnyAction(state session.ContinuationState, decision Decision, actions []string) Decision {
	for _, action := range actions {
		action = normalizeAuthorityAction(action)
		if action == "" {
			continue
		}
		if decision.RequiredAction == "" {
			decision.RequiredAction = action
		}
		if authorityActionAllowed(state, action) {
			decision.Allowed = true
			decision.Reason = "allowed_by_continuation_envelope"
			decision.RequiredAction = action
			return decision
		}
	}
	decision.Reason = "action_not_allowed_by_continuation_envelope"
	return decision
}

func authorityActionAllowed(state session.ContinuationState, action string) bool {
	action = normalizeAuthorityAction(action)
	if action == "" {
		return false
	}
	for _, candidate := range authorityActions(state) {
		if normalizeAuthorityAction(candidate) == action {
			return true
		}
	}
	return false
}

func authorityActions(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	values := append([]string(nil), state.ActionProposal.AllowedActions...)
	values = append(values, state.ContinuationLease.AllowedActions...)
	values = append(values, strings.TrimSpace(state.ActionProposal.RiskClass))
	values = append(values, strings.TrimSpace(string(state.ContinuationLease.LeaseClass)))
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		values = append(values, phase.AllowedActions...)
		values = append(values, strings.TrimSpace(phase.AuthorityClass))
	}
	if compilation := session.CompileContinuationAuthorityContract(state); compilation.Valid() {
		values = append(values, compilation.AllowedActions...)
		values = append(values, strings.TrimSpace(compilation.Contract.Key))
	}
	return values
}

func hasCapabilityGrantCoverage(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if len(state.ContinuationLease.CapabilityGrantIDs) > 0 || len(state.ContinuationLease.RequiredCapabilityGrants) > 0 {
		return true
	}
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok && len(phase.RequiredCapabilityGrants) > 0 {
		return true
	}
	return false
}

func boundaryRequiredAction(kind commandeffect.BoundaryKind, command string) string {
	switch kind {
	case commandeffect.BoundaryExternalAccount:
		if actions := externalAccountMutationActions(command); len(actions) > 0 {
			return actions[0]
		}
		if externalAccountStatusCommand(command) {
			return "external_account_status_check"
		}
		return "external_account_action"
	case commandeffect.BoundaryGitCommit:
		return "git_commit"
	case commandeffect.BoundaryGitPush:
		return "git_push"
	case commandeffect.BoundaryRemoteHostOperation:
		return "remote_host_operation"
	case commandeffect.BoundaryServiceProcessChange:
		return "service_process_change"
	default:
		return string(kind)
	}
}

func externalAccountMutationActions(command string) []string {
	command = normalizeAuthorityAction(command)
	switch {
	case strings.Contains(command, "gh_pr_create"),
		strings.Contains(command, "gh_pr_new"),
		strings.Contains(command, "gh_pr_open"),
		strings.Contains(command, "pull_request_create"),
		strings.Contains(command, "create_github_pr"):
		return []string{"github_pr_create", "pull_request_create", "open_pull_request", "create_github_pr"}
	case strings.Contains(command, "gh_pr_edit"),
		strings.Contains(command, "gh_pr_update"),
		strings.Contains(command, "pull_request_update"),
		strings.Contains(command, "update_pull_request"):
		return []string{"github_pr_update", "pull_request_update", "github_pr_metadata_update", "pull_request_metadata_update"}
	default:
		return []string{"external_account_action"}
	}
}

func externalAccountStatusCommand(command string) bool {
	command = normalizeAuthorityAction(command)
	return strings.Contains(command, "gh_auth_status") ||
		strings.Contains(command, "gh_api_user") ||
		strings.Contains(command, "gh_api_rate_limit") ||
		strings.Contains(command, "aws_sts_get_caller_identity") ||
		strings.Contains(command, "gcloud_auth_list") ||
		strings.Contains(command, "az_account_show") ||
		strings.Contains(command, "op_account_get")
}

func authorizeByWorkMode(req WorkModeRequest) Decision {
	compact := commandeffect.NormalizeCommand(req.Command)
	if compact == "" {
		return Decision{Allowed: false, Reason: "empty_command"}
	}
	effect := commandeffect.Classify(compact)
	decision := Decision{
		Allowed:    true,
		Reason:     "allowed_by_work_mode",
		EffectKind: string(effect.Kind),
	}
	if boundary, ok := commandeffect.BoundaryForCommand(compact); ok {
		decision.Boundary = true
		decision.BoundaryKind = string(boundary.Kind)
		decision.RequiredAction = boundaryRequiredAction(boundary.Kind, compact)
	}
	if req.Mode == WorkModeReadOnly {
		if effect.ReadOnlyAllowed() {
			decision.Reason = "read_only_work_mode"
			return decision
		}
		decision.Allowed = false
		decision.Reason = "effect_exceeds_read_only_work_mode"
		return decision
	}
	if effect.Kind == commandeffect.KindRepoHistory && effect.Reason == commandeffect.ReasonGitPush {
		decision.Allowed = false
		decision.Reason = "git_push_requires_continuation_envelope"
		return decision
	}
	if effect.Kind == commandeffect.KindService && req.Mode != WorkModeDeploy {
		decision.Allowed = false
		decision.Reason = "service_change_requires_deploy_work_mode"
		return decision
	}
	if effect.Kind == commandeffect.KindRepoHistory && effect.Reason == commandeffect.ReasonGitCommit && req.Mode != WorkModeCommit && req.Mode != WorkModeDeploy {
		decision.Allowed = false
		decision.Reason = "git_commit_requires_commit_work_mode"
		return decision
	}
	if effect.Kind == commandeffect.KindHighImpactStorage {
		decision.Allowed = false
		decision.Reason = "high_impact_storage_not_allowed_by_work_mode"
		return decision
	}
	if !commandWithinWorkRoot(req.RepoRoot, req.Workdir) {
		decision.Allowed = false
		decision.Reason = "workdir_outside_repo_root"
		return decision
	}
	return decision
}

func commandWithinWorkRoot(root string, workdir string) bool {
	root = strings.TrimSpace(root)
	workdir = strings.TrimSpace(workdir)
	if root == "" || workdir == "" {
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(workdir))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

func normalizeAuthorityAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
