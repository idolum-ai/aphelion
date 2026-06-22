//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

func TestRuntimeInterpretationAdaptersRecordJudgmentsAndUses(t *testing.T) {
	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 8701, UserID: 1001, Scope: telegramDMScopeRef(8701)}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	if err := rt.recordPerceptionBudgetJudgmentUse(key, 11, memstore.PerceptionBudgetContract{
		Posture:           memstore.PerceptionPostureImplementation,
		TotalBudgetTokens: 1000,
		Admitted: []memstore.PerceptionLayerAccounting{{
			Name:            memstore.PerceptionLayerCurrentInput,
			Source:          "turn.user_text",
			EpistemicStatus: memstore.PerceptionStatusCurrent,
			EstimatedTokens: 20,
		}},
	}, now); err != nil {
		t.Fatalf("recordPerceptionBudgetJudgmentUse() err = %v", err)
	}
	if err := rt.recordMaterialFloorJudgmentUse(key, 11, "plain reply", core.TextMaterialPacket("plain reply"), "plain reply", false, now); err != nil {
		t.Fatalf("recordMaterialFloorJudgmentUse() err = %v", err)
	}
	if err := rt.recordConstitutionJudgmentUse(key, 11, "final_reply", []pipeline.ConstitutionViolation{{
		Rule:    pipeline.RuleMediaReplyContradiction,
		Surface: "final_reply",
		Detail:  "reply contradicts delivered media",
	}}, now); err != nil {
		t.Fatalf("recordConstitutionJudgmentUse() err = %v", err)
	}
	if err := rt.recordBudgetRecoveryScopeJudgmentUse(key, core.InboundMessage{Text: "stay on the PDF"}, session.OperationState{ID: "op-pdf", Objective: "deliver PDF"}, "request:abc", map[string]any{"reason": "use_current_request"}, now); err != nil {
		t.Fatalf("recordBudgetRecoveryScopeJudgmentUse() err = %v", err)
	}

	for _, tc := range []struct {
		kind        string
		consequence session.JudgmentUseConsequence
	}{
		{"perception_budget_contract", session.JudgmentUseConsequenceModelContextAdmission},
		{"material_floor_interpretation", session.JudgmentUseConsequencePresentation},
		{"constitution_violation_check", session.JudgmentUseConsequencePresentation},
		{"budget_recovery_scope_selection", session.JudgmentUseConsequenceRecoverySelection},
	} {
		judgments, err := store.JudgmentsByKind(key, tc.kind, 10)
		if err != nil {
			t.Fatalf("JudgmentsByKind(%s) err = %v", tc.kind, err)
		}
		if len(judgments) != 1 {
			t.Fatalf("JudgmentsByKind(%s) len = %d, want 1: %#v", tc.kind, len(judgments), judgments)
		}
		uses, err := store.JudgmentUsesByJudgmentRef(judgments[0].ID, 10)
		if err != nil {
			t.Fatalf("JudgmentUsesByJudgmentRef(%s) err = %v", tc.kind, err)
		}
		if len(uses) != 1 || uses[0].Consequence != tc.consequence {
			t.Fatalf("uses for %s = %#v, want one %s use", tc.kind, uses, tc.consequence)
		}
	}
}

func TestRuntimeCuriositySelectionRecordsJudgmentUse(t *testing.T) {
	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	key := curiositySessionKey()
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	candidate := curiosityCandidate{
		ID:              "curiosity-test",
		SignalCategory:  hiddenInputSemanticRecurrence,
		SubjectKey:      "judgment-plane",
		SignalIntensity: 0.9,
		SourceKind:      session.CuriositySourceWorkspace,
		SourceRef:       "docs/architecture/interpretation-surfaces.md",
		ToolName:        "read_file",
	}
	if err := rt.recordCuriositySelectionJudgmentUse(key, session.CuriosityLease{ID: "curiosity-lease"}, candidate, now); err != nil {
		t.Fatalf("recordCuriositySelectionJudgmentUse() err = %v", err)
	}
	judgments, err := store.JudgmentsByKind(key, "curiosity_candidate_selection", 10)
	if err != nil {
		t.Fatalf("JudgmentsByKind(curiosity_candidate_selection) err = %v", err)
	}
	if len(judgments) != 1 {
		t.Fatalf("judgments = %#v, want one curiosity selection judgment", judgments)
	}
	uses, err := store.JudgmentUsesByJudgmentRef(judgments[0].ID, 10)
	if err != nil {
		t.Fatalf("JudgmentUsesByJudgmentRef() err = %v", err)
	}
	if len(uses) != 1 || uses[0].Consequence != session.JudgmentUseConsequenceModelContextAdmission {
		t.Fatalf("uses = %#v, want curiosity model-context admission use", uses)
	}
}
