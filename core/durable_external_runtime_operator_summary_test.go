//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildWorkAgreementOperatorSummaryNamesAuthorityAndExclusions(t *testing.T) {
	t.Parallel()

	summary, err := BuildWorkAgreementOperatorSummary(WorkAgreement{
		ID:               "wa_daily_audience_update",
		Version:          3,
		AgentID:          "audience-child",
		Title:            "Daily audience update",
		RuntimeKind:      "openclaw",
		PolicyCeilingRef: "policy:audience-child",
		Principals: WorkAgreementPrincipals{
			AuthorityPrincipal:      "admin:aphelion",
			ReviewPrincipal:         "customer:audience-owner",
			ResourceOwnerPrincipals: []string{"customer:audience-owner"},
		},
		Schedule:            ScheduleSpec{Kind: "cron", Expression: "0 13 * * *", Timezone: "America/New_York"},
		ReviewPolicy:        ReviewPolicy{DefaultOutbound: "draft_only", SendRequires: "review_principal"},
		ConditionalGrantIDs: []string{"grant_gmail_read"},
	}, []ConditionalGrant{{
		ID:                   "grant_gmail_read",
		WorkAgreementID:      "wa_daily_audience_update",
		WorkAgreementVersion: 3,
		Capability:           "gmail_read",
		Tool:                 "gog",
		Actions:              []string{"gmail.search", "gmail.read"},
		CredentialScope:      "gmail:audience",
		Conditions:           ConditionalGrantConditions{Triggers: []string{"schedule:wa_daily_audience_update"}},
		Materializes:         GrantMaterialization{LeaseKind: ExternalRuntimeLeaseKindToolInvocation, TTLSeconds: 900, ReviewRoute: "review_principal", SingleUse: true},
	}}, DurableExternalRuntimeSpec{
		Kind:      "openclaw",
		Mode:      ExternalRuntimeModeOneshot,
		StateRoot: "/var/lib/aphelion/children/audience/openclaw",
		DependencyRoots: []DependencyRoot{
			{Kind: "node_modules", Path: "/var/lib/aphelion/children/audience/openclaw/deps/node_modules", Writable: true},
		},
		Source:         RuntimeSourceRef{Kind: "git", Repo: "https://github.com/openclaw/openclaw", Ref: "c7295e417d5daec76c18fb452d117f7b8eadc4d6"},
		NetworkClasses: []string{"provider_egress"},
	})
	if err != nil {
		t.Fatalf("BuildWorkAgreementOperatorSummary() err = %v", err)
	}
	if summary.AuthorityPrincipal != "admin:aphelion" || summary.ReviewPrincipal != "customer:audience-owner" {
		t.Fatalf("summary principals = %#v", summary)
	}
	assertStringContains(t, summary.AuthorizedWork, "gmail_read")
	assertStringContains(t, summary.Surfaces, "credential:gmail:audience")
	assertStringContains(t, summary.Surfaces, "dependency:node_modules")
	assertStringContains(t, summary.ExplicitExclusions, "no ambient runtime authority")
}

func TestBuildWorkAgreementAmendmentOperatorSummaryPreservesExistingLeaseFence(t *testing.T) {
	t.Parallel()

	summary, err := BuildWorkAgreementAmendmentOperatorSummary(WorkAgreementAmendment{
		ID:              "amend_01J",
		WorkAgreementID: "wa_daily_audience_update",
		FromVersion:     3,
		ProposedVersion: 4,
		ProposedBy:      "durable_agent:audience-child",
		ChangeClass:     []string{"credential_scope", "review_route"},
		Diff:            map[string]json.RawMessage{"credential_scope": json.RawMessage(`"gmail:expanded"`)},
		RiskDelta:       map[string]json.RawMessage{"risk": json.RawMessage(`"wider email access"`)},
	})
	if err != nil {
		t.Fatalf("BuildWorkAgreementAmendmentOperatorSummary() err = %v", err)
	}
	if summary.FromVersion != 3 || summary.ProposedVersion != 4 {
		t.Fatalf("summary = %#v, want version transition", summary)
	}
	assertStringContains(t, summary.ReviewFocus, "credential scope")
	assertStringContains(t, summary.ExplicitNoChange, "existing leases stay fenced")
}

func assertStringContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if strings.Contains(value, want) {
			return
		}
	}
	t.Fatalf("values = %#v, want item containing %q", values, want)
}
