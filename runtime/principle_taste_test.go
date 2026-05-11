//go:build linux

package runtime

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestContinuationPlanTitlePrefersExplicitTitleFields(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:       session.ContinuationStatusPending,
		DecisionID:   "phase-resource-owner-assistant",
		Objective:    "Create a consented Telegram resource-owner assistant.",
		StageSummary: "Approve stages 33-36: Consent-first resource-owner intake.",
		ActionProposal: session.ActionProposal{
			ID:            "aprop-phase-resource-owner-assistant",
			OperatorTitle: "Resource-owner consented assistant",
			PlanTitle:     "Resource-owner assistant plan",
			Summary:       "Approve stages 33-36: Consent-first resource-owner intake.",
		},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-phase-resource-owner-assistant",
			ProposalID: "aprop-phase-resource-owner-assistant",
			Status:     session.ContinuationLeaseStatusPending,
		},
	}
	if got := continuationUserFacingPlanLabel(state); got != "Plan: Resource-owner consented assistant" {
		t.Fatalf("continuation label = %q, want explicit operator title", got)
	}
}

func TestActionProposalHashIgnoresPresentationTitles(t *testing.T) {
	t.Parallel()

	base := session.ActionProposal{
		ID:               "aprop-title-hash",
		Summary:          "Patch one file.",
		WhyNow:           "The fix is bounded.",
		BoundedEffect:    "Edit one local file and run focused tests.",
		RiskClass:        "workspace_write",
		AllowedActions:   []string{"workspace_write"},
		ForbiddenActions: []string{"deploy"},
		ValidationPlan:   []string{"go test ./runtime"},
	}
	withTitle := base
	withTitle.OperatorTitle = "Human title"
	withTitle.PlanTitle = "Canonical display title"
	if actionProposalHash(base) != actionProposalHash(withTitle) {
		t.Fatal("actionProposalHash changed when only presentation titles changed")
	}
}

func TestFloorPendingAudioIntentRequiresTypedClaim(t *testing.T) {
	t.Parallel()

	summaryOnly := encodeFloorMetadata(core.FloorMetadata{
		HiddenInputs: []core.HiddenInput{{
			Category: hiddenInputPendingMediaIntent,
			Summary:  "next audio should be transcribed and answered in text",
		}},
	})
	if floorHasPendingAudioTranscriptionIntent(summaryOnly) {
		t.Fatalf("summary-only floor metadata was treated as authority: %s", summaryOnly)
	}

	claim := core.NormalizeInterpretationClaim(core.InterpretationClaim{
		Intent:             hiddenInputPendingMediaIntent,
		Scope:              "next_audio",
		ProposedNextAction: "transcribe_and_reply_text",
		Confidence:         "high",
		Source:             "test",
	})
	typed := encodeFloorMetadata(core.FloorMetadata{
		HiddenInputs: []core.HiddenInput{{
			Category: hiddenInputPendingMediaIntent,
			Summary:  "typed claim controls pending media behavior",
			Claim:    &claim,
		}},
	})
	if !floorHasPendingAudioTranscriptionIntent(typed) {
		t.Fatalf("typed floor metadata was not honored: %s", typed)
	}
}

func TestExecutionClaimAdjudicationCarriesTypedInterpretationClaims(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9315, UserID: 0, Scope: telegramDMScopeRef(9315)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{EventType: core.ExecutionEventTurnStarted, Stage: "turn", Status: "running", PayloadJSON: `{}`, CreatedAt: now.Add(-20 * time.Second)},
		{EventType: core.ExecutionEventTurnCompleted, Stage: "turn", Status: "completed", PayloadJSON: `{}`, CreatedAt: now.Add(-10 * time.Second)},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	adjudication := rt.adjudicateFinalReplyExecutionClaims(key, "I ran go test and all tests passed.")
	if !adjudication.HasFindings() {
		t.Fatal("adjudication.HasFindings() = false, want missing test evidence finding")
	}
	if len(adjudication.Interpretation) == 0 {
		t.Fatal("Interpretation claims empty, want typed claim candidates")
	}
	claim := core.NormalizeInterpretationClaim(adjudication.Interpretation[0])
	if claim.Intent != "reply_execution_claim" || claim.Scope != "final_reply" || claim.Source != "test_interpretation_role" {
		t.Fatalf("interpretation claim = %#v, want typed final-reply claim from interpretation role", claim)
	}
}
