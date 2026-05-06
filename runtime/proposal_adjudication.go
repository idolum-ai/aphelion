//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationProposalRiskFindings(proposal session.ActionProposal) []core.RuntimeFinding {
	proposal = session.NormalizeActionProposal(proposal)
	findings := make([]core.RuntimeFinding, 0, 3)
	if proposalMayDelete(proposal) {
		findings = append(findings, core.RuntimeFinding{
			Kind:             "may_delete",
			EvidenceStatus:   "declared_by_proposal",
			Detail:           "proposal includes explicit delete/remove authority",
			RequiredBehavior: "Label this as may delete when surfaced to the operator; do not call it destructive unless the contract itself uses that word.",
		})
	}
	if proposalMayRestartOrDeploy(proposal) {
		findings = append(findings, core.RuntimeFinding{
			Kind:             "may_restart_or_deploy",
			EvidenceStatus:   "declared_by_proposal",
			Detail:           "proposal includes deploy or restart authority",
			RequiredBehavior: "Surface as may restart/deploy and keep approval boundaries explicit.",
		})
	}
	if proposalMayExternalEffect(proposal) {
		findings = append(findings, core.RuntimeFinding{
			Kind:             "may_external_effect",
			EvidenceStatus:   "declared_by_proposal",
			Detail:           "proposal includes possible external side effects",
			RequiredBehavior: "Surface the concrete external effect rather than a generic danger label.",
		})
	}
	return findings
}

func continuationProposalRiskAdjudication(state session.ContinuationState) core.RuntimeAdjudication {
	state = session.NormalizeContinuationState(state)
	findings := continuationProposalRiskFindings(state.ActionProposal)
	return core.NormalizeRuntimeAdjudication(core.RuntimeAdjudication{
		Kind:          "proposal_risk",
		Surface:       "continuation_operator_card",
		SubjectID:     strings.TrimSpace(state.ActionProposal.ID),
		OperatorLabel: "Proposal risk notes",
		Findings:      findings,
		VisibleAction: "operator_label",
	})
}

func proposalMayDelete(proposal session.ActionProposal) bool {
	return proposalHasStructuredRiskToken(proposal, "delete", "remove", "drop", "truncate", "rm_rf", "delete_data") ||
		proposalTextHasPositiveRisk(proposal.BoundedEffect, "delete", "remove", "drop table", "truncate", "rm -rf", "delete from")
}

func proposalMayRestartOrDeploy(proposal session.ActionProposal) bool {
	if session.InferContinuationLeaseClass(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect) == session.ContinuationLeaseClassDeployRestart {
		return true
	}
	return proposalHasStructuredRiskToken(proposal, "deploy", "restart", "system_change")
}

func proposalMayExternalEffect(proposal session.ActionProposal) bool {
	return proposalHasStructuredRiskToken(proposal, "send_email", "contact", "purchase", "push", "publish", "external_account") ||
		proposalTextHasPositiveRisk(proposal.BoundedEffect, "send email", "contact user", "make purchase", "push to", "publish")
}

func proposalHasStructuredRiskToken(proposal session.ActionProposal, tokens ...string) bool {
	values := make([]string, 0, len(proposal.AllowedActions)+1)
	values = append(values, proposal.RiskClass)
	values = append(values, proposal.AllowedActions...)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		for _, token := range tokens {
			token = strings.ToLower(strings.TrimSpace(token))
			if token != "" && strings.Contains(value, token) {
				return true
			}
		}
	}
	return false
}

func proposalTextHasPositiveRisk(text string, markers ...string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range markers {
		marker = strings.ToLower(strings.TrimSpace(marker))
		if marker == "" {
			continue
		}
		searchFrom := 0
		for {
			idx := strings.Index(lower[searchFrom:], marker)
			if idx < 0 {
				break
			}
			idx += searchFrom
			prefixStart := idx - 36
			if prefixStart < 0 {
				prefixStart = 0
			}
			prefix := strings.TrimSpace(lower[prefixStart:idx])
			if !containsAnyRiskNegation(prefix) {
				return true
			}
			searchFrom = idx + len(marker)
			if searchFrom >= len(lower) {
				break
			}
		}
	}
	return false
}

func containsAnyRiskNegation(prefix string) bool {
	for _, marker := range []string{"no ", "not ", "without ", "do not ", "don't ", "never ", "avoid ", "forbid ", "forbidden "} {
		if strings.Contains(prefix, marker) {
			return true
		}
	}
	return false
}
