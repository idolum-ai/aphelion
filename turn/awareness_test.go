//go:build linux

package turn

import (
	"reflect"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func TestApplyHiddenInputAwareness(t *testing.T) {
	categories := []string{"x", "y"}
	base := prompt.RuntimeAwareness{
		HiddenInputCategories: []string{"old"},
		OperationFindings:     []string{"old"},
	}
	aw := ApplyHiddenInputAwareness(base, HiddenInputAwareness{
		Active:            true,
		Categories:        categories,
		ProvenanceSummary: "  provenance summary  ",
	})

	if !aw.HiddenInputsActive {
		t.Fatal("HiddenInputsActive = false, want true")
	}
	if got, want := aw.HiddenInputCategories, categories; !reflect.DeepEqual(got, want) {
		t.Fatalf("HiddenInputCategories = %#v, want %#v", got, want)
	}
	if got, want := aw.ProvenanceSummary, "provenance summary"; got != want {
		t.Fatalf("ProvenanceSummary = %q, want %q", got, want)
	}

	categories[0] = "mutated"
	if got, want := aw.HiddenInputCategories[0], "x"; got != want {
		t.Fatalf("HiddenInputCategories[0] = %q, want %q", got, want)
	}
}

func TestApplyPlanAwarenessCopiesPlanState(t *testing.T) {
	base := prompt.RuntimeAwareness{
		PlanSteps:   []string{"old"},
		PlanSummary: "old",
	}
	state := session.PlanState{
		Explanation: "  investigate this backlog  ",
		Steps: []session.PlanStep{
			{Step: " ", Status: "invalid"},
			{Step: "  draft design  ", Status: session.PlanStatusCompleted},
		},
	}
	aw := ApplyPlanAwarenessWithEvents(base, state, []session.PlanEvent{
		{Kind: session.PlanEventKindToolUpdated, PlanState: state},
		{Kind: session.PlanEventKindPhaseEntered, PlanState: state},
	})

	if !aw.PlanActive {
		t.Fatal("PlanActive = false, want true")
	}
	if got, want := aw.PlanSummary, "investigate this backlog"; got != want {
		t.Fatalf("PlanSummary = %q, want %q", got, want)
	}
	if got, want := aw.PlanSteps, []string{"[completed] draft design"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PlanSteps = %#v, want %#v", got, want)
	}
	if got := strings.Join(aw.PlanEvents, "\n"); !strings.Contains(got, "phase.entered") || strings.Contains(got, "tool_updated") {
		t.Fatalf("PlanEvents = %#v, want semantic events without tool_updated", aw.PlanEvents)
	}
}

func TestApplyOperationAwarenessBuildsRuntimeFindingsAndArtifacts(t *testing.T) {
	base := prompt.RuntimeAwareness{
		OperationFindings:  []string{"stale"},
		OperationArtifacts: []string{"stale"},
		OperationPhases:    []string{"stale"},
	}
	state := session.OperationState{
		Objective: "  evaluate options  ",
		Status:    session.OperationStatusActive,
		Stage:     "reviewing",
		Summary:   "  open investigation  ",
		Proposal: session.OperationProposal{
			ID:            "op-1",
			Kind:          "investigation",
			Status:        session.ProposalStatusDenied,
			WhyNow:        "  urgency  ",
			Summary:       "  review needed  ",
			BoundedEffect: "  concise action  ",
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-1",
			Goal:           "  evaluate options safely  ",
			CurrentPhaseID: "phase-2",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "  inspect first  ", Status: session.PlanStatusCompleted, AuthorityClass: "read_only_review"},
				{ID: "phase-2", Summary: "  patch next  ", Status: session.PlanStatusPending, AuthorityClass: "workspace_write", BoundedEffect: "  edit files and run tests  ", BlockedReasonCode: "waiting-for-opt-in", RequiresOptIn: true, SupersedesPhaseIDs: []string{"phase-old"}},
			},
		},
		Findings: []session.OperationFinding{
			{Claim: "  signal detected  ", Confidence: session.FindingConfidenceHigh, Basis: "  first pass  "},
		},
		Artifacts: []session.OperationArtifact{
			{Label: "spec", Ref: "  /tmp/spec.md  "},
			{Ref: " /tmp/raw.txt "},
			{Label: "blank", Ref: "   "},
		},
	}
	aw := ApplyOperationAwareness(base, state)

	if !aw.OperationActive {
		t.Fatal("OperationActive = false, want true")
	}
	if got, want := aw.OperationObjective, "evaluate options"; got != want {
		t.Fatalf("OperationObjective = %q, want %q", got, want)
	}
	if got, want := aw.OperationStatus, "active"; got != want {
		t.Fatalf("OperationStatus = %q, want %q", got, want)
	}
	if got, want := aw.OperationStage, "reviewing"; got != want {
		t.Fatalf("OperationStage = %q, want %q", got, want)
	}
	if got, want := aw.OperationSummary, "open investigation"; got != want {
		t.Fatalf("OperationSummary = %q, want %q", got, want)
	}
	if got, want := aw.ProposalStatus, "denied"; got != want {
		t.Fatalf("ProposalStatus = %q, want %q", got, want)
	}
	if got, want := aw.ProposalWhyNow, "urgency"; got != want {
		t.Fatalf("ProposalWhyNow = %q, want %q", got, want)
	}
	if got, want := aw.ProposalBoundedEffect, "concise action"; got != want {
		t.Fatalf("ProposalBoundedEffect = %q, want %q", got, want)
	}
	if !aw.PhasePlanActive {
		t.Fatal("PhasePlanActive = false, want true")
	}
	if got, want := aw.PhasePlanID, "plan-1"; got != want {
		t.Fatalf("PhasePlanID = %q, want %q", got, want)
	}
	if got, want := aw.PhasePlanCurrentPhaseID, "phase-2"; got != want {
		t.Fatalf("PhasePlanCurrentPhaseID = %q, want %q", got, want)
	}
	if len(aw.OperationDigest) != 1 || !strings.Contains(aw.OperationDigest[0], "op operation") || !strings.Contains(aw.OperationDigest[0], "current_phase=phase-2") {
		t.Fatalf("OperationDigest = %#v, want compact op/current phase", aw.OperationDigest)
	}
	if got, want := aw.OperationPhases, []string{
		"[completed] phase-1 · authority=read_only_review · summary=inspect first",
		"[pending] phase-2 · authority=workspace_write · blocked=waiting_for_opt_in · requires_opt_in · summary=patch next",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationPhases = %#v, want %#v", got, want)
	}
	for _, line := range aw.OperationPhases {
		if strings.Contains(line, "bounded_effect") || strings.Contains(line, "supersedes_phase_ids") {
			t.Fatalf("OperationPhases line is not compact: %q", line)
		}
	}
	if got, want := aw.OperationFindings, []string{"[high] signal detected (basis: first pass)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationFindings = %#v, want %#v", got, want)
	}
	if got, want := aw.OperationArtifacts, []string{"spec: /tmp/spec.md", "/tmp/raw.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationArtifacts = %#v, want %#v", got, want)
	}
}

func TestApplyBrokerageAwarenessSummarizesContracts(t *testing.T) {
	base := prompt.RuntimeAwareness{
		SuggestedExecutionContract: "old",
	}
	aw := ApplyBrokerageAwareness(base, BrokerageAwareness{
		Active:                     true,
		Phase:                      "brokerage",
		Ratification:               "accept",
		SignalJudgment:             "confirmed",
		SuggestedExecutionContract: &pipeline.ExecutionContract{NeedsInspection: true, NeedsQuestion: false, MayAnswerNow: true},
		RatifiedExecutionContract:  &pipeline.ExecutionContract{NeedsInspection: false, NeedsQuestion: true, MayAnswerNow: false},
	})

	if !aw.BrokerageActive {
		t.Fatal("BrokerageActive = false, want true")
	}
	if got, want := aw.BrokeragePhase, "brokerage"; got != want {
		t.Fatalf("BrokeragePhase = %q, want %q", got, want)
	}
	if got, want := aw.BrokerageRatification, "accept"; got != want {
		t.Fatalf("BrokerageRatification = %q, want %q", got, want)
	}
	if got, want := aw.SignalJudgment, "confirmed"; got != want {
		t.Fatalf("SignalJudgment = %q, want %q", got, want)
	}
	if got, want := aw.SuggestedExecutionContract, "inspect=yes, question=no, answer=yes"; got != want {
		t.Fatalf("SuggestedExecutionContract = %q, want %q", got, want)
	}
	if got, want := aw.RatifiedExecutionContract, "inspect=no, question=yes, answer=no"; got != want {
		t.Fatalf("RatifiedExecutionContract = %q, want %q", got, want)
	}

	aw = ApplyBrokerageAwareness(base, BrokerageAwareness{
		Active:         true,
		Phase:          "proposal",
		Ratification:   "adapt",
		SignalJudgment: "not_material",
	})
	if got := aw.SuggestedExecutionContract; got != "" {
		t.Fatalf("SuggestedExecutionContract = %q, want empty", got)
	}
	if got := aw.RatifiedExecutionContract; got != "" {
		t.Fatalf("RatifiedExecutionContract = %q, want empty", got)
	}
}

func TestApplyContinuationAwarenessCopiesHandshakeSignals(t *testing.T) {
	base := prompt.RuntimeAwareness{
		ContinuationPersonaIntent: "old",
		ContinuationGovernorWhy:   "old",
	}
	state := session.ContinuationState{
		Status: session.ContinuationStatusPending,
		PersonaIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "persona sees a concrete next step",
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:  session.ContinuationIntentDecisionContinue,
			Rationale: "governor ratified the active plan",
			Ratified:  true,
		},
		HandshakeBlockedReason: " ",
	}

	aw := ApplyContinuationAwareness(base, state)
	if got, want := aw.ContinuationStatus, "pending"; got != want {
		t.Fatalf("ContinuationStatus = %q, want %q", got, want)
	}
	if !aw.ContinuationActive {
		t.Fatal("ContinuationActive = false, want true")
	}
	if got, want := aw.ContinuationPersonaIntent, "continue"; got != want {
		t.Fatalf("ContinuationPersonaIntent = %q, want %q", got, want)
	}
	if got, want := aw.ContinuationPersonaWhy, "persona sees a concrete next step"; got != want {
		t.Fatalf("ContinuationPersonaWhy = %q, want %q", got, want)
	}
	if got, want := aw.ContinuationGovernorIntent, "continue"; got != want {
		t.Fatalf("ContinuationGovernorIntent = %q, want %q", got, want)
	}
	if got, want := aw.ContinuationGovernorWhy, "governor ratified the active plan"; got != want {
		t.Fatalf("ContinuationGovernorWhy = %q, want %q", got, want)
	}
	if !aw.ContinuationRatified {
		t.Fatal("ContinuationRatified = false, want true")
	}
	if got := aw.ContinuationBlockedReason; got != "" {
		t.Fatalf("ContinuationBlockedReason = %q, want empty", got)
	}
}
