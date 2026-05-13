//go:build linux

package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestContinuationLeaseActionAccessAndPersistence(t *testing.T) {
	now := time.Date(2026, time.May, 4, 20, 30, 0, 0, time.UTC)
	lease := ContinuationLease{
		ID:               "lease-local-workspace",
		ProposalID:       "aprop-local-workspace",
		Status:           ContinuationLeaseStatusActive,
		MaxTurns:         2,
		RemainingTurns:   2,
		AllowedActions:   []string{"workspace_write", "focused_tests", "git_diff_check"},
		ForbiddenActions: []string{"deploy", "restart"},
		ExpiresAt:        now.Add(time.Hour),
	}

	allowed := CheckContinuationLeaseAction(lease, "workspace-write", now)
	if !allowed.Allowed || allowed.Reason != "allowed" {
		t.Fatalf("workspace action decision = %#v, want allowed", allowed)
	}
	forbidden := CheckContinuationLeaseAction(lease, "deploy", now)
	if forbidden.Allowed || forbidden.Reason != "action_forbidden" {
		t.Fatalf("deploy decision = %#v, want forbidden", forbidden)
	}
	missing := CheckContinuationLeaseAction(lease, "commit", now)
	if missing.Allowed || missing.Reason != "action_not_allowed" {
		t.Fatalf("commit decision = %#v, want not allowed", missing)
	}
	expired := CheckContinuationLeaseAction(lease, "workspace_write", now.Add(2*time.Hour))
	if expired.Allowed || expired.Reason != "lease_inactive_or_expired" {
		t.Fatalf("expired decision = %#v, want inactive/expired", expired)
	}

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := SessionKey{ChatID: 9201, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "telegram_dm:9201"}}
	if err := store.UpdateContinuationState(key, ContinuationState{
		Status:            ContinuationStatusApproved,
		RemainingTurns:    2,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	reloaded, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	persisted := CheckContinuationLeaseAction(reloaded.ContinuationLease, "focused_tests", now)
	if !persisted.Allowed {
		t.Fatalf("persisted decision = %#v, want focused_tests allowed", persisted)
	}
	persistedForbidden := CheckContinuationLeaseAction(reloaded.ContinuationLease, "restart", now)
	if persistedForbidden.Allowed || persistedForbidden.Reason != "action_forbidden" {
		t.Fatalf("persisted forbidden decision = %#v, want restart forbidden", persistedForbidden)
	}
}

func TestContinuationOperatorTitleFieldsSurviveJSONRoundTrip(t *testing.T) {
	action := NormalizeActionProposal(ActionProposal{
		ID:            "aprop-title",
		OperatorTitle: "  Human plan title  ",
		PlanTitle:     "  Canonical plan title  ",
		Summary:       "Do the bounded step.",
	})
	if action.OperatorTitle != "Human plan title" || action.PlanTitle != "Canonical plan title" {
		t.Fatalf("action titles = %q/%q, want trimmed titles", action.OperatorTitle, action.PlanTitle)
	}
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	var decoded ActionProposal
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	if decoded.OperatorTitle != "Human plan title" || decoded.PlanTitle != "Canonical plan title" {
		t.Fatalf("decoded titles = %q/%q, want persisted titles", decoded.OperatorTitle, decoded.PlanTitle)
	}

	var summaryOnly ActionProposal
	if err := json.Unmarshal([]byte(`{"id":"aprop-summary-only","summary":"Summary only"}`), &summaryOnly); err != nil {
		t.Fatalf("unmarshal summary-only action: %v", err)
	}
	summaryOnly = NormalizeActionProposal(summaryOnly)
	if summaryOnly.OperatorTitle != "" || summaryOnly.PlanTitle != "" || summaryOnly.Summary != "Summary only" {
		t.Fatalf("summary-only action = %#v, want title fields optional", summaryOnly)
	}
}

func TestContinuationLeaseClassConstraintsRequireExactActions(t *testing.T) {
	now := time.Date(2026, time.May, 4, 21, 0, 0, 0, time.UTC)
	lease := NormalizeContinuationLease(ContinuationLease{
		ID:             "lease-capability",
		Status:         ContinuationLeaseStatusActive,
		MaxTurns:       1,
		RemainingTurns: 1,
		LeaseClass:     ContinuationLeaseClassCapabilityGrant,
		AllowedActions: []string{"*"},
		ExpiresAt:      now.Add(time.Hour),
	})

	wildcardOnly := CheckContinuationLeaseAction(lease, "grant_set", now)
	if wildcardOnly.Allowed || wildcardOnly.Reason != "lease_class_requires_explicit_action" {
		t.Fatalf("wildcard decision = %#v, want explicit-action denial", wildcardOnly)
	}
	lease.AllowedActions = append(lease.AllowedActions, "grant_set")
	explicit := CheckContinuationLeaseAction(lease, "grant-set", now)
	if !explicit.Allowed || explicit.Reason != "allowed" {
		t.Fatalf("explicit decision = %#v, want allowed", explicit)
	}
	if lease.Constraints["grant"] == "" || lease.Constraints["actions"] == "" {
		t.Fatalf("constraints = %#v, want capability grant defaults", lease.Constraints)
	}
}

func TestContinuationLeaseClassInferenceAndBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		risk    string
		actions []string
		effect  string
		want    ContinuationLeaseClass
	}{
		{name: "data", risk: "data_access", actions: []string{"read_image"}, effect: "inspect one generated artifact", want: ContinuationLeaseClassDataAccess},
		{name: "child", risk: "durable_child_wake", actions: []string{"selected_child_wake"}, effect: "wake image2 once", want: ContinuationLeaseClassChildWake},
		{name: "capability", risk: "capability_grant", actions: []string{"capability_access_check"}, effect: "review grant", want: ContinuationLeaseClassCapabilityGrant},
		{name: "deploy", risk: "deploy", actions: []string{"service_restart"}, effect: "restart and verify", want: ContinuationLeaseClassDeployRestart},
		{name: "workspace", risk: "workspace_write", actions: []string{"focused_tests"}, effect: "patch locally", want: ContinuationLeaseClassLocalWorkspace},
		{name: "local commit", risk: "workspace_commit_then_repo_write_bounded", actions: []string{"git_commit_validated_slices"}, effect: "commit validated local slices", want: ContinuationLeaseClassLocalWorkspace},
		{name: "private data intake", risk: "private_data_intake", actions: nil, effect: "process resource-owner preferences after opt-in", want: ContinuationLeaseClassDataAccess},
		{name: "email and public web read", risk: "external_account_email_read_public_web_read", actions: nil, effect: "read the approved mailbox and public links after profile approval", want: ContinuationLeaseClassDataAccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferContinuationLeaseClass(tc.risk, tc.actions, tc.effect); got != tc.want {
				t.Fatalf("InferContinuationLeaseClass() = %q, want %q", got, tc.want)
			}
			if boundary := ContinuationLeaseClassBoundary(tc.want); boundary == "" || boundary == ContinuationLeaseClassBoundary("") {
				t.Fatalf("boundary for %q = %q, want class-specific boundary", tc.want, boundary)
			}
		})
	}
}

func TestAuthorityInferenceIgnoresNegatedDeployInReadOnlyChildInspection(t *testing.T) {
	proposal := ApplyAuthorityContractToActionProposal(ActionProposal{
		RiskClass: "read_only_child_adapter_environment_inspection",
		AllowedActions: []string{
			"inspect_durable_agent_state",
			"inspect_external_channel_adapter_state",
			"inspect_execution_events_for_mailbox_adapter_command",
			"inspect_binary_path_metadata",
			"inspect_nonsecret_environment_metadata",
			"report_mismatch_and_repair_options",
		},
		ForbiddenActions: []string{
			"read_or_print_secret_values",
			"read_mailbox_contents",
			"run_mailbox_adapter_query",
			"edit_config",
			"deploy",
			"restart",
		},
		BoundedEffect: "Read local non-secret child state, adapter config, execution events, binary path metadata, and sanitized command metadata. No mailbox content/query, OAuth, file mutation, credential exposure, config edits, deploy, or restart.",
	})

	if actionListMatches(proposal.AllowedActions, "deploy") || actionListMatches(proposal.AllowedActions, "restart") {
		t.Fatalf("allowed actions = %#v, want no deploy/restart injected from negated prose", proposal.AllowedActions)
	}
	if !actionListMatches(proposal.ForbiddenActions, "deploy") || !actionListMatches(proposal.ForbiddenActions, "restart") {
		t.Fatalf("forbidden actions = %#v, want deploy/restart preserved as forbiddens", proposal.ForbiddenActions)
	}
	if got := InferContinuationLeaseClass(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect); got == ContinuationLeaseClassDeployRestart {
		t.Fatalf("InferContinuationLeaseClass() = %q, want non-deploy class", got)
	}
}

func TestAuthorityInferenceIgnoresNegatedDeployInCredentialRecovery(t *testing.T) {
	proposal := ApplyAuthorityContractToActionProposal(ActionProposal{
		RiskClass: "credential_recovery",
		AllowedActions: []string{
			"create_child_scoped_mailbox_adapter_materialization_if_approved",
			"copy_or_bind_existing_host_mailbox_credentials_without_printing_values",
			"adjust_child_mailbox_adapter_wrapper_or_grant_contract_if_needed",
			"run_child_sandbox_external_account_auth_status_only",
			"report_repair_evidence",
		},
		ForbiddenActions: []string{
			"read_or_print_secret_values",
			"run_mailbox_adapter_query",
			"read_mailbox_contents",
			"read_mailbox_labels_or_messages",
			"start_oauth_flow",
			"mutate_google_account",
			"send_email",
			"archive_delete_or_modify_email",
			"deploy",
			"restart",
		},
		BoundedEffect: "May create or adjust child-scoped mailbox adapter credential materialization, wrapper/env, or grant contract so only intended host credentials are accessible to the child. May run one non-mailbox auth/config status smoke. No mailbox content/label/inbox/message query, no OAuth, no account mutation, no public/external contact, no email actions, no deploy/restart unless separately approved.",
	})

	if actionListMatches(proposal.AllowedActions, "deploy") || actionListMatches(proposal.AllowedActions, "restart") {
		t.Fatalf("allowed actions = %#v, want no deploy/restart injected from negated credential-recovery prose", proposal.AllowedActions)
	}
	if got := InferContinuationLeaseClass(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect); got == ContinuationLeaseClassDeployRestart {
		t.Fatalf("InferContinuationLeaseClass() = %q, want non-deploy class", got)
	}
}

func TestAuthorityContractMapsLocalSecretMetadataReadToReadOnlyDataAccess(t *testing.T) {
	contract, ok := AuthorityContractForToken(AuthorityClassLocalSecretMetadataReadLiveConfigRead)
	if !ok {
		t.Fatal("AuthorityContractForToken(metadata) ok = false, want true")
	}
	if contract.LeaseClass != ContinuationLeaseClassDataAccess || contract.WorkAction != AuthorityWorkActionReadOnly {
		t.Fatalf("contract = %#v, want data-access/read-only", contract)
	}
	if got := InferContinuationLeaseClass(AuthorityClassLocalSecretMetadataReadLiveConfigRead, nil, "metadata-only config preflight"); got != ContinuationLeaseClassDataAccess {
		t.Fatalf("InferContinuationLeaseClass() = %q, want %q", got, ContinuationLeaseClassDataAccess)
	}
	proposal := ApplyAuthorityContractToActionProposal(ActionProposal{
		RiskClass:     AuthorityClassLocalSecretMetadataReadLiveConfigRead,
		Summary:       "Metadata-only preflight; prior diagnostic mentioned workspace_write mismatch.",
		BoundedEffect: "Inspect token-file metadata only.",
	})
	if !actionListMatches(proposal.AllowedActions, AuthorityWorkActionReadOnly) {
		t.Fatalf("allowed actions = %#v, want read_only", proposal.AllowedActions)
	}
	if !actionListMatches(proposal.ForbiddenActions, "telegram_api_call") || !actionListMatches(proposal.ForbiddenActions, "read_token_contents") {
		t.Fatalf("forbidden actions = %#v, want live-effect and token-content denials", proposal.ForbiddenActions)
	}
}

func TestAuthorityInterpretationClaimUsesStructuredFieldsBeforeProse(t *testing.T) {
	claim, ok := AuthorityInterpretationClaimFor(
		"read_only_child_adapter_environment_inspection",
		[]string{"inspect_durable_agent_state", "inspect_nonsecret_environment_metadata"},
		"Read local non-secret child state only. No deploy or restart.",
	)
	if !ok {
		t.Fatal("AuthorityInterpretationClaimFor() ok = false, want read-only claim")
	}
	if claim.Intent != "authority_classification" || claim.AuthorityClass != "read_only_review" {
		t.Fatalf("claim = %#v, want read_only_review authority classification", claim)
	}
	if claim.Source != "structured_authority_fields" {
		t.Fatalf("claim.Source = %q, want structured_authority_fields", claim.Source)
	}
}

func TestAuthorityInterpretationClaimDoesNotClassifyFromBoundedEffectProse(t *testing.T) {
	claim, ok := AuthorityInterpretationClaimFor(
		"",
		nil,
		"Deploy and restart the live service after pushing the branch.",
	)
	if ok {
		t.Fatalf("AuthorityInterpretationClaimFor() = %#v, want no claim from bounded_effect prose", claim)
	}
	if got := InferContinuationLeaseClass("", nil, "Deploy and restart the live service."); got != "" {
		t.Fatalf("InferContinuationLeaseClass() = %q, want no lease class from bounded_effect prose", got)
	}
}

func TestAuthorityInterpretationClaimTreatsCompoundCommitAsLocalAuthority(t *testing.T) {
	claim, ok := AuthorityInterpretationClaimFor(
		"workspace_commit_then_repo_write_bounded",
		[]string{"git_commit_validated_slices"},
		"Commit the validated local slice; do not push, deploy, or restart.",
	)
	if !ok {
		t.Fatal("AuthorityInterpretationClaimFor() ok = false, want commit claim")
	}
	if claim.AuthorityClass != "commit" {
		t.Fatalf("claim.AuthorityClass = %q, want commit", claim.AuthorityClass)
	}
	contract, ok := AuthorityContractFor("workspace_commit_then_repo_write_bounded", []string{"git_commit_validated_slices"}, "")
	if !ok {
		t.Fatal("AuthorityContractFor(compound commit) ok = false, want contract")
	}
	if contract.LeaseClass != ContinuationLeaseClassLocalWorkspace || contract.WorkAction != AuthorityWorkActionCommit {
		t.Fatalf("contract = %#v, want local workspace commit authority", contract)
	}
}

func TestAuthorityContractMapsExternalAccountStatusCheckAliasToReadOnlyDataAccess(t *testing.T) {
	contract, ok := AuthorityContractForToken("external_account_status_check")
	if !ok {
		t.Fatal("AuthorityContractForToken(external_account_status_check) ok = false, want true")
	}
	if contract.LeaseClass != ContinuationLeaseClassDataAccess || contract.WorkAction != AuthorityWorkActionReadOnly {
		t.Fatalf("contract = %#v, want data-access/read-only", contract)
	}
	if got := InferContinuationLeaseClass("external_account_status_check", nil, "status check only"); got != ContinuationLeaseClassDataAccess {
		t.Fatalf("InferContinuationLeaseClass() = %q, want %q", got, ContinuationLeaseClassDataAccess)
	}
	proposal := ApplyAuthorityContractToActionProposal(ActionProposal{RiskClass: "external_account_status_check", Summary: "Check external account status only."})
	if !actionListMatches(proposal.AllowedActions, AuthorityWorkActionReadOnly) {
		t.Fatalf("allowed actions = %#v, want read_only", proposal.AllowedActions)
	}
}
