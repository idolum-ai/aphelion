//go:build linux

package session

import "testing"

func TestApplyAuthorityContractDisablesAutoApprovalForExternalAccountAction(t *testing.T) {
	t.Parallel()

	autoApprove := true
	got := ApplyAuthorityContractToActionProposal(ActionProposal{
		ID:                  "aprop-pr-metadata",
		RiskClass:           "external_account_action",
		AllowedActions:      []string{"update_pull_request_title"},
		AutoApproveEligible: &autoApprove,
	})
	if got.AutoApproveEligible == nil {
		t.Fatal("AutoApproveEligible = nil, want explicit false")
	}
	if *got.AutoApproveEligible {
		t.Fatalf("AutoApproveEligible = true, want authority contract to force false")
	}
}
