//go:build linux

package session

import "strings"

type JudgmentGroundProfile struct {
	JudgmentID          string
	DependencyRefs      []JudgmentDependencyRef
	SourceFaultDomains  []string
	InterpreterID       string
	InterpreterVersion  string
	ModelCallID         string
	MaterialFloorRef    string
	MemorySummaryRef    string
	ExternalEvidenceRef string
}

type JudgmentDecorrelatedGroundDecision struct {
	Decorrelated bool
	Reason       string
	Shared       []string
}

func DecorrelatedGroundForJudgment(challenged JudgmentGroundProfile, support JudgmentGroundProfile) JudgmentDecorrelatedGroundDecision {
	challenged = normalizeJudgmentGroundProfile(challenged)
	support = normalizeJudgmentGroundProfile(support)
	var shared []string
	appendShared := func(kind string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			shared = append(shared, kind+":"+value)
		}
	}
	for domain := range stringSet(challenged.SourceFaultDomains) {
		if _, ok := stringSet(support.SourceFaultDomains)[domain]; ok {
			appendShared("fault_domain", domain)
		}
	}
	for dep := range dependencyRefSet(challenged.DependencyRefs) {
		if _, ok := dependencyRefSet(support.DependencyRefs)[dep]; ok {
			appendShared("dependency", dep)
		}
	}
	if challenged.InterpreterID != "" && challenged.InterpreterID == support.InterpreterID {
		appendShared("interpreter", challenged.InterpreterID)
	}
	if challenged.ModelCallID != "" && challenged.ModelCallID == support.ModelCallID {
		appendShared("model_call", challenged.ModelCallID)
	}
	if challenged.MaterialFloorRef != "" && challenged.MaterialFloorRef == support.MaterialFloorRef {
		appendShared("material_floor", challenged.MaterialFloorRef)
	}
	if challenged.MemorySummaryRef != "" && challenged.MemorySummaryRef == support.MemorySummaryRef {
		appendShared("memory_summary", challenged.MemorySummaryRef)
	}
	if challenged.ExternalEvidenceRef != "" && challenged.ExternalEvidenceRef == support.ExternalEvidenceRef {
		appendShared("external_evidence", challenged.ExternalEvidenceRef)
	}
	if len(shared) > 0 {
		return JudgmentDecorrelatedGroundDecision{
			Decorrelated: false,
			Reason:       "shared upstream interpretation source",
			Shared:       shared,
		}
	}
	return JudgmentDecorrelatedGroundDecision{Decorrelated: true, Reason: "no shared tracked upstream source"}
}

func normalizeJudgmentGroundProfile(profile JudgmentGroundProfile) JudgmentGroundProfile {
	profile.JudgmentID = strings.TrimSpace(profile.JudgmentID)
	profile.DependencyRefs = normalizeJudgmentDependencyRefs(profile.DependencyRefs)
	profile.SourceFaultDomains = normalizeStringList(profile.SourceFaultDomains)
	profile.InterpreterID = judgmentUseToken(profile.InterpreterID)
	profile.InterpreterVersion = strings.TrimSpace(profile.InterpreterVersion)
	profile.ModelCallID = strings.TrimSpace(profile.ModelCallID)
	profile.MaterialFloorRef = strings.TrimSpace(profile.MaterialFloorRef)
	profile.MemorySummaryRef = strings.TrimSpace(profile.MemorySummaryRef)
	profile.ExternalEvidenceRef = strings.TrimSpace(profile.ExternalEvidenceRef)
	return profile
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range normalizeStringList(values) {
		out[value] = struct{}{}
	}
	return out
}

func dependencyRefSet(refs []JudgmentDependencyRef) map[string]struct{} {
	out := make(map[string]struct{}, len(refs))
	for _, ref := range normalizeJudgmentDependencyRefs(refs) {
		out[ref.Kind+"|"+ref.Ref+"|"+ref.Scope] = struct{}{}
	}
	return out
}
