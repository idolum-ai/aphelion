//go:build linux

package session

import "testing"

func TestCompileActionProposalAuthorityContractRejectsFetchUnderWorkspaceWrite(t *testing.T) {
	t.Parallel()

	compilation := CompileActionProposalAuthorityContract(ActionProposal{
		Summary:       "Fresh external-read/fetch approval for origin/main.",
		BoundedEffect: "Run git fetch origin main --prune, then report fetched ref evidence.",
		RiskClass:     "workspace_write",
		AllowedActions: []string{
			"git_fetch_origin_main_prune",
			"git_rev_parse_origin_main",
			"report_fetch_evidence",
		},
		ForbiddenActions: []string{
			"external_effect_without_separate_grant",
			"commit",
			"git_push",
			"deploy",
			"restart_service",
		},
	})

	if compilation.Valid() {
		t.Fatalf("compilation = %#v, want invalid fetch/workspace_write contract", compilation)
	}
	if len(compilation.Contradictions) == 0 {
		t.Fatalf("compilation = %#v, want external-effect contradiction", compilation)
	}
	got := compilation.Contradictions[0]
	if got.Reason != AuthorityContradictionReasonProposalRequiresForbiddenExternalEffect {
		t.Fatalf("contradiction reason = %q, want %q", got.Reason, AuthorityContradictionReasonProposalRequiresForbiddenExternalEffect)
	}
	if got.AllowedAction != "git_fetch_origin_main_prune" {
		t.Fatalf("allowed action = %q, want git_fetch_origin_main_prune", got.AllowedAction)
	}
	if got.ForbiddenAction != "external_effect_without_separate_grant" {
		t.Fatalf("forbidden action = %q, want external_effect_without_separate_grant", got.ForbiddenAction)
	}
}

func TestCompileActionProposalAuthorityContractAllowsExplicitExternalReadFetch(t *testing.T) {
	t.Parallel()

	compilation := CompileActionProposalAuthorityContract(ActionProposal{
		Summary:       "Fetch origin/main and report remote ref evidence.",
		BoundedEffect: "Run only git fetch origin main --prune and read refs after it.",
		RiskClass:     AuthorityClassExternalRead,
		AllowedActions: []string{
			"git_fetch_origin_main_prune",
			"git_rev_parse_origin_main",
			"report_fetch_evidence",
		},
		ForbiddenActions: []string{
			"workspace_write",
			"commit",
			"git_push",
			"deploy",
			"restart_service",
		},
	})

	if !compilation.Valid() {
		t.Fatalf("compilation = %#v, want valid external_read fetch contract", compilation)
	}
	if compilation.Contract.Key != AuthorityClassExternalRead {
		t.Fatalf("contract key = %q, want %q", compilation.Contract.Key, AuthorityClassExternalRead)
	}
	if !compilation.Contract.ExternalEffectsAllowed {
		t.Fatalf("external effects allowed = false, want true")
	}
	for _, want := range []string{"fetch", "git_fetch", "external_network_contact"} {
		if !authorityPreflightStringSliceContains(compilation.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", compilation.AllowedActions, want)
		}
	}
}

func TestCompileActionProposalAuthorityContractRejectsExternalReadWithWorkspaceWrite(t *testing.T) {
	t.Parallel()

	compilation := CompileActionProposalAuthorityContract(ActionProposal{
		Summary:        "Fetch then edit files.",
		RiskClass:      AuthorityClassExternalRead,
		AllowedActions: []string{"git_fetch", "edit_files"},
	})

	if compilation.Valid() {
		t.Fatalf("compilation = %#v, want invalid external_read workspace write blend", compilation)
	}
	var foundExact bool
	for _, contradiction := range compilation.Contradictions {
		if contradiction.AllowedAction == "edit_files" && contradiction.ForbiddenAction == "edit_files" && contradiction.Reason == "allowed_action_exactly_forbidden" {
			foundExact = true
		}
	}
	if !foundExact {
		t.Fatalf("contradictions = %#v, want exact workspace_write exclusion", compilation.Contradictions)
	}
}

func authorityPreflightStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
