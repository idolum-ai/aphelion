//go:build linux

package session

import "strings"

const defaultJudgmentGroundProfileDepth = 4

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
	UnresolvedRefs      []string
}

type JudgmentDecorrelatedGroundDecision struct {
	Decorrelated bool
	Reason       string
	Shared       []string
}

func DecorrelatedGroundForJudgment(challenged JudgmentGroundProfile, support JudgmentGroundProfile) JudgmentDecorrelatedGroundDecision {
	challenged = normalizeJudgmentGroundProfile(challenged)
	support = normalizeJudgmentGroundProfile(support)
	if !judgmentGroundProfileHasTrackedGround(challenged) || !judgmentGroundProfileHasTrackedGround(support) {
		return JudgmentDecorrelatedGroundDecision{Decorrelated: false, Reason: "insufficient tracked provenance"}
	}
	if len(challenged.UnresolvedRefs) > 0 || len(support.UnresolvedRefs) > 0 {
		var unresolved []string
		unresolved = append(unresolved, challenged.UnresolvedRefs...)
		unresolved = append(unresolved, support.UnresolvedRefs...)
		return JudgmentDecorrelatedGroundDecision{Decorrelated: false, Reason: "unresolved upstream provenance", Shared: unresolved}
	}
	var shared []string
	appendShared := func(kind string, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			shared = append(shared, kind+":"+value)
		}
	}
	if challenged.JudgmentID != "" && challenged.JudgmentID == support.JudgmentID {
		appendShared("judgment", challenged.JudgmentID)
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
	profile.UnresolvedRefs = normalizeStringList(profile.UnresolvedRefs)
	return profile
}

func judgmentGroundProfileHasTrackedGround(profile JudgmentGroundProfile) bool {
	return len(profile.DependencyRefs) > 0 ||
		len(profile.SourceFaultDomains) > 0 ||
		profile.InterpreterID != "" ||
		profile.ModelCallID != "" ||
		profile.MaterialFloorRef != "" ||
		profile.MemorySummaryRef != "" ||
		profile.ExternalEvidenceRef != ""
}

func JudgmentGroundProfileForJudgment(judgment Judgment) JudgmentGroundProfile {
	judgment.ID = strings.TrimSpace(judgment.ID)
	return normalizeJudgmentGroundProfile(JudgmentGroundProfile{
		JudgmentID:         judgment.ID,
		DependencyRefs:     judgment.DependencyRefs,
		SourceFaultDomains: judgment.SourceFaultDomains,
		InterpreterID:      judgment.InterpreterID,
		InterpreterVersion: judgment.InterpreterVersion,
	})
}

func (s *SQLiteStore) JudgmentGroundProfile(judgmentID string, maxDepth int) (JudgmentGroundProfile, error) {
	judgmentID = strings.TrimSpace(judgmentID)
	if s == nil || s.db == nil || judgmentID == "" {
		return JudgmentGroundProfile{UnresolvedRefs: []string{JudgmentRef(judgmentID)}}, nil
	}
	if maxDepth <= 0 {
		maxDepth = defaultJudgmentGroundProfileDepth
	}
	visited := map[string]struct{}{}
	var out JudgmentGroundProfile
	var walk func(id string, depth int, root bool) error
	walk = func(id string, depth int, root bool) error {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil
		}
		if _, ok := visited[id]; ok {
			return nil
		}
		if depth < 0 {
			out.UnresolvedRefs = append(out.UnresolvedRefs, JudgmentRef(id))
			return nil
		}
		visited[id] = struct{}{}
		judgment, ok, err := s.Judgment(id)
		if err != nil {
			return err
		}
		if !ok {
			out.UnresolvedRefs = append(out.UnresolvedRefs, JudgmentRef(id))
			return nil
		}
		mergeJudgmentGroundProfile(&out, JudgmentGroundProfileForJudgment(judgment), root)
		nextIDs := judgmentDependencyJudgmentIDs(judgment)
		for _, nextID := range nextIDs {
			if err := walk(nextID, depth-1, false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(judgmentID, maxDepth, true); err != nil {
		return JudgmentGroundProfile{}, err
	}
	return normalizeJudgmentGroundProfile(out), nil
}

func mergeJudgmentGroundProfile(out *JudgmentGroundProfile, profile JudgmentGroundProfile, root bool) {
	if out == nil {
		return
	}
	profile = normalizeJudgmentGroundProfile(profile)
	if root {
		out.JudgmentID = profile.JudgmentID
		out.InterpreterID = profile.InterpreterID
		out.InterpreterVersion = profile.InterpreterVersion
		out.ModelCallID = profile.ModelCallID
		out.MaterialFloorRef = profile.MaterialFloorRef
		out.MemorySummaryRef = profile.MemorySummaryRef
		out.ExternalEvidenceRef = profile.ExternalEvidenceRef
	} else if profile.JudgmentID != "" {
		out.DependencyRefs = append(out.DependencyRefs, JudgmentDependencyRef{Kind: "judgment", Ref: profile.JudgmentID, Role: "ancestor"})
	}
	out.DependencyRefs = append(out.DependencyRefs, profile.DependencyRefs...)
	out.SourceFaultDomains = append(out.SourceFaultDomains, profile.SourceFaultDomains...)
	out.UnresolvedRefs = append(out.UnresolvedRefs, profile.UnresolvedRefs...)
}

func judgmentDependencyJudgmentIDs(judgment Judgment) []string {
	var out []string
	for _, dep := range normalizeJudgmentDependencyRefs(judgment.DependencyRefs) {
		if dep.Kind == "judgment" {
			out = append(out, dep.Ref)
		}
	}
	for _, ref := range normalizeStringList(judgment.InputRefs) {
		if kind, id, ok := strings.Cut(ref, ":"); ok && judgmentUseToken(kind) == "judgment" {
			out = append(out, strings.TrimSpace(id))
		}
	}
	return normalizeStringList(out)
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
