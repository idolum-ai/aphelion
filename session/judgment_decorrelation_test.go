//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDecorrelatedGroundForJudgmentRejectsMissingProvenance(t *testing.T) {
	for _, tc := range []struct {
		name       string
		challenged JudgmentGroundProfile
		support    JudgmentGroundProfile
	}{
		{name: "both empty"},
		{name: "support empty", challenged: JudgmentGroundProfile{SourceFaultDomains: []string{"shell_text"}}},
		{name: "challenged empty", support: JudgmentGroundProfile{SourceFaultDomains: []string{"operation_proposal"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := DecorrelatedGroundForJudgment(tc.challenged, tc.support)
			if decision.Decorrelated || decision.Reason != "insufficient tracked provenance" {
				t.Fatalf("decision = %#v, want insufficient tracked provenance rejection", decision)
			}
		})
	}
}

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

func TestJudgmentGroundProfileRejectsTransitiveSharedAncestor(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := SessionKey{ChatID: 1001, UserID: 2002}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	ancestor, err := store.RecordJudgment(JudgmentInput{
		Key:                key,
		Kind:               "operator_request",
		SubjectKey:         "request:shared",
		ClaimKey:           "current_operator_intent",
		InterpreterID:      "runtime.request_parser",
		InputRefs:          []string{"message:shared"},
		ResultJSON:         `{"intent":"shared"}`,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "operator_message", Ref: "msg-shared", Role: "source"}},
		SourceFaultDomains: []string{"operator_message"},
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordJudgment(ancestor) err = %v", err)
	}
	challenged, err := store.RecordJudgment(JudgmentInput{
		Key:                key,
		Kind:               "shell_effect_plan",
		SubjectKey:         "exec:push",
		ClaimKey:           "command_effect_plan",
		InterpreterID:      "commandeffect.plan_command",
		InputRefs:          []string{JudgmentRef(ancestor.ID)},
		ResultJSON:         `{"effect":"git_push"}`,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "judgment", Ref: ancestor.ID, Role: "support"}},
		SourceFaultDomains: []string{"shell_text"},
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordJudgment(challenged) err = %v", err)
	}
	support, err := store.RecordJudgment(JudgmentInput{
		Key:                key,
		Kind:               "approval_ground",
		SubjectKey:         "approval:push",
		ClaimKey:           "operator_approved_push",
		InterpreterID:      "runtime.approval_parser",
		InputRefs:          []string{JudgmentRef(ancestor.ID)},
		ResultJSON:         `{"approved":true}`,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "judgment", Ref: ancestor.ID, Role: "support"}},
		SourceFaultDomains: []string{"approval_event"},
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordJudgment(support) err = %v", err)
	}
	challengedProfile, err := store.JudgmentGroundProfile(challenged.ID, 4)
	if err != nil {
		t.Fatalf("JudgmentGroundProfile(challenged) err = %v", err)
	}
	supportProfile, err := store.JudgmentGroundProfile(support.ID, 4)
	if err != nil {
		t.Fatalf("JudgmentGroundProfile(support) err = %v", err)
	}
	decision := DecorrelatedGroundForJudgment(challengedProfile, supportProfile)
	if decision.Decorrelated {
		t.Fatalf("decision = %#v, want shared transitive ancestor to be correlated", decision)
	}
}

func TestJudgmentGroundProfileRejectsUnresolvedAncestor(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := SessionKey{ChatID: 1001, UserID: 2002}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	judgment, err := store.RecordJudgment(JudgmentInput{
		Key:                key,
		Kind:               "shell_effect_plan",
		SubjectKey:         "exec:push",
		ClaimKey:           "command_effect_plan",
		InterpreterID:      "commandeffect.plan_command",
		ResultJSON:         `{"effect":"git_push"}`,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "judgment", Ref: "missing-judgment", Role: "support"}},
		SourceFaultDomains: []string{"shell_text"},
		AsOf:               now,
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("RecordJudgment() err = %v", err)
	}
	profile, err := store.JudgmentGroundProfile(judgment.ID, 4)
	if err != nil {
		t.Fatalf("JudgmentGroundProfile() err = %v", err)
	}
	decision := DecorrelatedGroundForJudgment(profile, JudgmentGroundProfile{SourceFaultDomains: []string{"operation_proposal"}})
	if decision.Decorrelated || decision.Reason != "unresolved upstream provenance" {
		t.Fatalf("decision = %#v, want unresolved provenance rejection", decision)
	}
}
