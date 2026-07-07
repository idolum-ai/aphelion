//go:build linux

package core

import (
	"fmt"
	"strings"
)

type WorkAgreementOperatorSummary struct {
	AgreementID        string   `json:"agreement_id,omitempty"`
	Version            int      `json:"version,omitempty"`
	Title              string   `json:"title,omitempty"`
	Status             string   `json:"status,omitempty"`
	AuthorityPrincipal string   `json:"authority_principal,omitempty"`
	ReviewPrincipal    string   `json:"review_principal,omitempty"`
	Runtime            string   `json:"runtime,omitempty"`
	Schedule           string   `json:"schedule,omitempty"`
	AuthorizedWork     []string `json:"authorized_work,omitempty"`
	ExplicitExclusions []string `json:"explicit_exclusions,omitempty"`
	Surfaces           []string `json:"surfaces,omitempty"`
	ExpectedEvidence   []string `json:"expected_evidence,omitempty"`
}

type WorkAgreementAmendmentOperatorSummary struct {
	AmendmentID      string   `json:"amendment_id,omitempty"`
	AgreementID      string   `json:"agreement_id,omitempty"`
	FromVersion      int      `json:"from_version,omitempty"`
	ProposedVersion  int      `json:"proposed_version,omitempty"`
	Status           string   `json:"status,omitempty"`
	ProposedBy       string   `json:"proposed_by,omitempty"`
	ChangeClasses    []string `json:"change_classes,omitempty"`
	ReviewFocus      []string `json:"review_focus,omitempty"`
	ExplicitNoChange []string `json:"explicit_no_change,omitempty"`
}

func BuildWorkAgreementOperatorSummary(agreement WorkAgreement, grants []ConditionalGrant, spec DurableExternalRuntimeSpec) (WorkAgreementOperatorSummary, error) {
	agreement = NormalizeWorkAgreement(agreement)
	spec = NormalizeDurableExternalRuntimeSpec(spec)
	if err := ValidateDurableExternalRuntimeSpec(spec); err != nil {
		return WorkAgreementOperatorSummary{}, err
	}
	agreementForValidation := agreement
	agreementForValidation.Status = ""
	if err := ValidateWorkAgreement(agreementForValidation); err != nil {
		return WorkAgreementOperatorSummary{}, err
	}
	summary := WorkAgreementOperatorSummary{
		AgreementID:        agreement.ID,
		Version:            agreement.Version,
		Title:              firstNonEmptyExternal(agreement.Title, agreement.ID),
		Status:             agreement.Status,
		AuthorityPrincipal: agreement.Principals.AuthorityPrincipal,
		ReviewPrincipal:    agreement.Principals.ReviewPrincipal,
		Runtime:            spec.Kind + "/" + spec.Mode,
		Schedule:           summarizeSchedule(agreement.Schedule),
		ExplicitExclusions: []string{
			"no ambient runtime authority",
			"no unleased tool, secret, network, or channel access",
			"no parent memory write without governed admission",
			"no work agreement widening without review",
		},
		ExpectedEvidence: []string{
			"matched work agreement version",
			"materialized lease ids",
			"bounded result or review artifact",
		},
	}
	for _, grant := range grants {
		grant = NormalizeConditionalGrant(grant)
		if grant.WorkAgreementID != agreement.ID || grant.WorkAgreementVersion != agreement.Version {
			continue
		}
		summary.AuthorizedWork = append(summary.AuthorizedWork, summarizeConditionalGrant(grant))
		if grant.CredentialScope != "" {
			summary.Surfaces = appendUniqueSummary(summary.Surfaces, "credential:"+grant.CredentialScope)
		}
		if grant.Tool != "" {
			summary.Surfaces = appendUniqueSummary(summary.Surfaces, "tool:"+grant.Tool)
		}
	}
	summary.Surfaces = appendUniqueSummary(summary.Surfaces, "runtime:"+summary.Runtime)
	for _, networkClass := range spec.NetworkClasses {
		summary.Surfaces = appendUniqueSummary(summary.Surfaces, "network:"+networkClass)
	}
	for _, root := range spec.DependencyRoots {
		if root.Kind != "" {
			summary.Surfaces = appendUniqueSummary(summary.Surfaces, "dependency:"+root.Kind)
		}
	}
	if len(summary.AuthorizedWork) == 0 {
		summary.AuthorizedWork = []string{"no conditional grants materialize from this agreement version"}
	}
	return summary, nil
}

func BuildWorkAgreementAmendmentOperatorSummary(amendment WorkAgreementAmendment) (WorkAgreementAmendmentOperatorSummary, error) {
	amendment = NormalizeWorkAgreementAmendment(amendment)
	if err := ValidateWorkAgreementAmendment(amendment); err != nil {
		return WorkAgreementAmendmentOperatorSummary{}, err
	}
	summary := WorkAgreementAmendmentOperatorSummary{
		AmendmentID:     amendment.ID,
		AgreementID:     amendment.WorkAgreementID,
		FromVersion:     amendment.FromVersion,
		ProposedVersion: amendment.ProposedVersion,
		Status:          amendment.Status,
		ProposedBy:      amendment.ProposedBy,
		ChangeClasses:   append([]string(nil), amendment.ChangeClass...),
		ExplicitNoChange: []string{
			"current work agreement remains active until approval",
			"existing leases stay fenced to their original version",
			"approval does not bypass runtime preflight or lease materialization",
		},
	}
	for _, change := range amendment.ChangeClass {
		summary.ReviewFocus = append(summary.ReviewFocus, "review "+strings.ReplaceAll(change, "_", " "))
	}
	if len(summary.ReviewFocus) == 0 {
		summary.ReviewFocus = []string{"review proposed work agreement diff"}
	}
	return summary, nil
}

func summarizeSchedule(schedule ScheduleSpec) string {
	schedule.Kind = normalizeExternalRuntimeToken(schedule.Kind)
	schedule.Expression = strings.TrimSpace(schedule.Expression)
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	switch {
	case schedule.Kind == "" && schedule.Expression == "":
		return "manual trigger only"
	case schedule.Expression == "":
		return schedule.Kind
	case schedule.Timezone == "":
		return schedule.Kind + ":" + schedule.Expression
	default:
		return schedule.Kind + ":" + schedule.Expression + " " + schedule.Timezone
	}
}

func summarizeConditionalGrant(grant ConditionalGrant) string {
	parts := []string{grant.Capability}
	if grant.Tool != "" {
		parts = append(parts, "via "+grant.Tool)
	}
	if len(grant.Actions) > 0 {
		parts = append(parts, "actions "+strings.Join(grant.Actions, ","))
	}
	if len(grant.Conditions.Triggers) > 0 {
		parts = append(parts, "when "+strings.Join(grant.Conditions.Triggers, ","))
	}
	if grant.Materializes.LeaseKind != "" {
		parts = append(parts, "materializes "+grant.Materializes.LeaseKind)
	}
	return fmt.Sprintf("%s", strings.Join(parts, " "))
}

func appendUniqueSummary(values []string, next string) []string {
	next = strings.TrimSpace(next)
	if next == "" {
		return values
	}
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}
