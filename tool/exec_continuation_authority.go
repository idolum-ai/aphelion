//go:build linux

package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/session"
)

type continuationExecAuthorityContextKey struct{}

// ContinuationExecAuthorityDecision records whether an active continuation
// envelope permits a boundary-crossing command.
type ContinuationExecAuthorityDecision struct {
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

func WithContinuationExecAuthority(ctx context.Context, state session.ContinuationState) context.Context {
	return context.WithValue(ctx, continuationExecAuthorityContextKey{}, session.NormalizeContinuationState(state))
}

func ContinuationExecAuthorityFromContext(ctx context.Context) (session.ContinuationState, bool) {
	if ctx == nil {
		return session.ContinuationState{}, false
	}
	state, ok := ctx.Value(continuationExecAuthorityContextKey{}).(session.ContinuationState)
	if !ok {
		return session.ContinuationState{}, false
	}
	return session.NormalizeContinuationState(state), true
}

func ContinuationExecAuthorityDecisionForCommand(state session.ContinuationState, command string, now time.Time) ContinuationExecAuthorityDecision {
	state = session.NormalizeContinuationState(state)
	decision := continuationExecDecisionBase(state, now)
	if !decision.Active {
		return decision
	}
	effect := commandeffect.Classify(command)
	decision.EffectKind = string(effect.Kind)
	boundary, boundaryOK := commandeffect.BoundaryForCommand(command)
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
	decision.RequiredAction = continuationBoundaryRequiredAction(boundary.Kind, command)
	return continuationExecDecisionForBoundary(state, decision, boundary.Kind, command)
}

func ContinuationExecAuthorityError(decision ContinuationExecAuthorityDecision) error {
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

func continuationExecDecisionBase(state session.ContinuationState, now time.Time) ContinuationExecAuthorityDecision {
	decision := ContinuationExecAuthorityDecision{
		LeaseID:    strings.TrimSpace(state.ContinuationLease.ID),
		ProposalID: strings.TrimSpace(state.ActionProposal.ID),
	}
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		decision.PhaseID = firstNonEmptyToolString(phase.OperationPhaseID, phase.ID)
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

func continuationExecDecisionForBoundary(state session.ContinuationState, decision ContinuationExecAuthorityDecision, kind commandeffect.BoundaryKind, command string) ContinuationExecAuthorityDecision {
	switch kind {
	case commandeffect.BoundaryExternalAccount:
		if continuationExternalAccountStatusCommand(command) {
			return continuationExecDecisionRequireAnyAction(state, decision, []string{
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
		if !continuationHasCapabilityGrantCoverage(state) {
			decision.Reason = "external_effect_missing_capability_grant"
			return decision
		}
		return continuationExecDecisionRequireAnyAction(state, decision, continuationExternalAccountMutationActions(command))
	case commandeffect.BoundaryGitCommit:
		return continuationExecDecisionRequireAnyAction(state, decision, []string{"git_commit", "commit", "repo_history_mutation"})
	case commandeffect.BoundaryGitPush:
		return continuationExecDecisionRequireAnyAction(state, decision, []string{"git_push", "push_remote", "deploy", "run_deploy"})
	case commandeffect.BoundaryRemoteHostOperation:
		return continuationExecDecisionRequireAnyAction(state, decision, []string{"remote_host_operation", "ssh", "scp", "rsync", "deploy", "run_deploy"})
	case commandeffect.BoundaryServiceProcessChange:
		return continuationExecDecisionRequireAnyAction(state, decision, []string{"service_process_change", "restart_service", "service_restart", "systemctl_restart", "deploy", "run_deploy"})
	default:
		decision.Reason = "unhandled_boundary_effect"
		return decision
	}
}

func continuationExecDecisionRequireAnyAction(state session.ContinuationState, decision ContinuationExecAuthorityDecision, actions []string) ContinuationExecAuthorityDecision {
	for _, action := range actions {
		action = normalizeToolAuthorityAction(action)
		if action == "" {
			continue
		}
		if decision.RequiredAction == "" {
			decision.RequiredAction = action
		}
		if continuationAuthorityActionAllowed(state, action) {
			decision.Allowed = true
			decision.Reason = "allowed_by_continuation_envelope"
			decision.RequiredAction = action
			return decision
		}
	}
	decision.Reason = "action_not_allowed_by_continuation_envelope"
	return decision
}

func continuationAuthorityActionAllowed(state session.ContinuationState, action string) bool {
	action = normalizeToolAuthorityAction(action)
	if action == "" {
		return false
	}
	for _, candidate := range continuationAuthorityActions(state) {
		if normalizeToolAuthorityAction(candidate) == action {
			return true
		}
	}
	return false
}

func continuationAuthorityActions(state session.ContinuationState) []string {
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

func continuationHasCapabilityGrantCoverage(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if len(state.ContinuationLease.CapabilityGrantIDs) > 0 || len(state.ContinuationLease.RequiredCapabilityGrants) > 0 {
		return true
	}
	if phase, ok := session.CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok && len(phase.RequiredCapabilityGrants) > 0 {
		return true
	}
	return false
}

func continuationBoundaryRequiredAction(kind commandeffect.BoundaryKind, command string) string {
	switch kind {
	case commandeffect.BoundaryExternalAccount:
		if actions := continuationExternalAccountMutationActions(command); len(actions) > 0 {
			return actions[0]
		}
		if continuationExternalAccountStatusCommand(command) {
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

func continuationExternalAccountMutationActions(command string) []string {
	command = normalizeToolAuthorityAction(command)
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

func continuationExternalAccountStatusCommand(command string) bool {
	command = normalizeToolAuthorityAction(command)
	return strings.Contains(command, "gh_auth_status") ||
		strings.Contains(command, "gh_api_user") ||
		strings.Contains(command, "gh_api_rate_limit") ||
		strings.Contains(command, "aws_sts_get_caller_identity") ||
		strings.Contains(command, "gcloud_auth_list") ||
		strings.Contains(command, "az_account_show") ||
		strings.Contains(command, "op_account_get")
}

func normalizeToolAuthorityAction(value string) string {
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

func firstNonEmptyToolString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
