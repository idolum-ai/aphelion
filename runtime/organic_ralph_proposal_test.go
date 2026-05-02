//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestHandleInboundInfersOrganicRalphProposalAndMaterializesButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Yes — this wants one bounded next step."
	provider.faceReplyText = "I think this wants a button-backed lease."
	provider.proposalReplyText = strings.Join([]string{
		"This wants to become a bounded continuation proposal.",
		"ORGANIC_RALPH_SCHEMA_VERSION: 1",
		"ORGANIC_RALPH_PROPOSAL: yes",
		"ORGANIC_RALPH_KIND: read_only_review",
		"ORGANIC_RALPH_SUMMARY: Inspect Ralph insertion points",
		"ORGANIC_RALPH_WHY_NOW: Daniel asked to finish the Ralph loop organically.",
		"ORGANIC_RALPH_BOUNDED_EFFECT: Inspect local runtime paths and report the design; no code or deploy; stop after evidence.",
		"ORGANIC_RALPH_CONFIDENCE: high",
		"CONTINUATION_SCHEMA_VERSION: 1",
		"CONTINUATION_INTENT: hold",
		"CONTINUATION_RATIONALE: Ask for button confirmation first.",
		"CONTINUATION_NEXT_STEP: Inspect Ralph insertion points.",
		"CONTINUATION_CONFIDENCE: high",
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9021, UserID: 0, Scope: telegramDMScopeRef(9021)}
	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 9021, SenderID: 1001, SenderName: "admin", Text: "let's finish the Ralph loop organically", MessageID: 77})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1 organic proposal approval", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Approval needed") || !strings.Contains(sender.inline[0].text, "Inspect Ralph insertion points") {
		t.Fatalf("inline text = %q, want materialized organic proposal", sender.inline[0].text)
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got == "" || len(got) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("approve callback = %q len=%d, want non-empty <= %d", got, len(got), core.TelegramCallbackDataMaxBytes)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("operation state = %#v, want blocked with pending proposal", opState)
	}
	if opState.Proposal.Kind != "read_only_review" || opState.Proposal.Summary != "Inspect Ralph insertion points" {
		t.Fatalf("proposal = %#v, want read_only_review Inspect Ralph insertion points", opState.Proposal)
	}
	if !strings.Contains(opState.Proposal.BoundedEffect, "stop after evidence") {
		t.Fatalf("bounded effect = %q, want stop/report condition", opState.Proposal.BoundedEffect)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != opState.Proposal.ID {
		t.Fatalf("continuation = %#v, want pending linked to operation proposal %q", cont, opState.Proposal.ID)
	}
}

func TestOrganicRalphInferenceSkipsCommandsTurnAuthorizationAndLowConfidence(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9022, UserID: 0, Scope: telegramDMScopeRef(9022)}
	result := &turn.Result{ProposalNote: strings.Join([]string{
		"ORGANIC_RALPH_SCHEMA_VERSION: 1",
		"ORGANIC_RALPH_PROPOSAL: yes",
		"ORGANIC_RALPH_KIND: read_only_review",
		"ORGANIC_RALPH_SUMMARY: Inspect something",
		"ORGANIC_RALPH_WHY_NOW: It may matter.",
		"ORGANIC_RALPH_BOUNDED_EFFECT: Inspect only and report; stop after evidence.",
		"ORGANIC_RALPH_CONFIDENCE: medium",
	}, "\n")}
	for _, msg := range []core.InboundMessage{
		{ChatID: 9022, SenderID: 1001, Text: "/mission list", MessageID: 1},
		{ChatID: 9022, SenderID: 1001, Text: "continue", Origin: core.InboundOriginTurnAuthorization, MessageID: 2},
	} {
		inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, msg, msg.Text, result)
		if err != nil {
			t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
		}
		if inferred {
			t.Fatalf("maybeInferOrganicOperationProposal(%#v) inferred=true, want false", msg)
		}
	}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, core.InboundMessage{ChatID: 9022, SenderID: 1001, Text: "maybe", MessageID: 3}, "maybe", result)
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal(low confidence) err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferOrganicOperationProposal(low confidence) = true, want false")
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Active() {
		t.Fatalf("operation state = %#v, want no inferred proposal", opState)
	}
}

func TestOrganicRalphInferenceSkipsWhenContinuationOrPendingProposalExists(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9023, UserID: 0, Scope: telegramDMScopeRef(9023)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "already", RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	result := &turn.Result{ProposalNote: organicRalphHighConfidenceTestContract()}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, core.InboundMessage{ChatID: 9023, SenderID: 1001, Text: "go", MessageID: 1}, "go", result)
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("inferred with active continuation = true, want false")
	}

	key2 := session.SessionKey{ChatID: 9024, UserID: 0, Scope: telegramDMScopeRef(9024)}
	if err := store.UpdateOperationState(key2, session.OperationState{Proposal: session.OperationProposal{ID: "pending", Summary: "Existing", Status: session.ProposalStatusPending}}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	inferred, err = rt.maybeInferOrganicOperationProposal(context.Background(), key2, core.InboundMessage{ChatID: 9024, SenderID: 1001, Text: "go", MessageID: 2}, "go", result)
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal(existing proposal) err = %v", err)
	}
	if inferred {
		t.Fatal("inferred with pending operation proposal = true, want false")
	}
}

func organicRalphHighConfidenceTestContract() string {
	return strings.Join([]string{
		"ORGANIC_RALPH_SCHEMA_VERSION: 1",
		"ORGANIC_RALPH_PROPOSAL: yes",
		"ORGANIC_RALPH_KIND: read_only_review",
		"ORGANIC_RALPH_SUMMARY: Inspect one path",
		"ORGANIC_RALPH_WHY_NOW: The conversation implies one bounded next step.",
		"ORGANIC_RALPH_BOUNDED_EFFECT: Inspect only and report evidence; stop after report.",
		"ORGANIC_RALPH_CONFIDENCE: high",
	}, "\n")
}
