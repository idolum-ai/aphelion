//go:build linux

package session

import "testing"

func TestDecorrelatedGroundForJudgmentRejectsSharedUpstream(t *testing.T) {
	challenged := JudgmentGroundProfile{
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "evidence", Ref: "summary-1", Scope: "session"}},
		SourceFaultDomains: []string{"model_call:governor-1", "memory_summary:recent"},
		InterpreterID:      "runtime.material_floor",
		MaterialFloorRef:   "floor-1",
	}
	support := JudgmentGroundProfile{
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "evidence", Ref: "summary-1", Scope: "session"}},
		SourceFaultDomains: []string{"tool_observation"},
		InterpreterID:      "runtime.evidence_hydration",
		MaterialFloorRef:   "floor-2",
	}
	decision := DecorrelatedGroundForJudgment(challenged, support)
	if decision.Decorrelated {
		t.Fatalf("decision = %#v, want shared dependency to be correlated", decision)
	}
	if len(decision.Shared) == 0 {
		t.Fatalf("decision = %#v, want shared upstream details", decision)
	}
}

func TestDecorrelatedGroundForJudgmentAcceptsIndependentGround(t *testing.T) {
	challenged := JudgmentGroundProfile{
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "material_floor", Ref: "floor-1"}},
		SourceFaultDomains: []string{"model_call:governor-1", "pipeline_material_parser_v1"},
		InterpreterID:      "runtime.material_floor",
	}
	support := JudgmentGroundProfile{
		DependencyRefs:      []JudgmentDependencyRef{{Kind: "effect_attempt", Ref: "eff-1"}},
		SourceFaultDomains:  []string{"effect_attempt_ledger", "tool_observation"},
		InterpreterID:       "session.effect_attempt",
		ExternalEvidenceRef: "exec:eff-1",
		InterpreterVersion:  "v1",
		MaterialFloorRef:    "floor-2",
		MemorySummaryRef:    "summary-2",
		ModelCallID:         "provider-call-2",
	}
	decision := DecorrelatedGroundForJudgment(challenged, support)
	if !decision.Decorrelated {
		t.Fatalf("decision = %#v, want independent effect-attempt ground", decision)
	}
}
