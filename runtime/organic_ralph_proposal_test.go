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
	if !actionListContains(cont.ActionProposal.AllowedActions, organicRalphSandboxAction) {
		t.Fatalf("allowed actions = %#v, want Organic Ralph sandbox action", cont.ActionProposal.AllowedActions)
	}
	if !actionListContains(cont.ActionProposal.ForbiddenActions, "edit_files") || !actionListContains(cont.ActionProposal.ForbiddenActions, "network_access_without_separate_grant") {
		t.Fatalf("forbidden actions = %#v, want read-only sandbox boundaries", cont.ActionProposal.ForbiddenActions)
	}
}

func TestOrganicRalphInfersProposalFromPersistedStateWithoutContract(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9025, UserID: 0, Scope: telegramDMScopeRef(9025)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Finish the callback recovery work.",
		Steps: []session.PlanStep{{
			Step:   "Patch Organic Ralph fallback inference and prove it with tests",
			Status: session.PlanStatusInProgress,
		}},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "organic-state-fallback",
		Objective: "Deliver the intended Organic Ralph loop.",
		Status:    session.OperationStatusBlocked,
		Stage:     "awaiting_button_backed_lease",
		Summary:   "The plan has one bounded next implementation step but no explicit face contract.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	msg := core.InboundMessage{ChatID: 9025, SenderID: 1001, Text: "keep going with that", MessageID: 91}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, msg, msg.Text, &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
	}
	if !inferred {
		t.Fatal("maybeInferOrganicOperationProposal() = false, want state-inferred proposal")
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", opState.Proposal.Status)
	}
	if opState.Proposal.Kind != "system_change" || !strings.Contains(opState.Proposal.Summary, "Patch Organic Ralph fallback") {
		t.Fatalf("proposal = %#v, want system_change patch proposal from plan state", opState.Proposal)
	}
	if len(opState.Findings) != 1 {
		t.Fatalf("findings = %#v, want one inference finding", opState.Findings)
	}
	if !strings.Contains(opState.Findings[0].Basis, "plan state") || strings.Contains(opState.Findings[0].Basis, "ORGANIC_RALPH") {
		t.Fatalf("finding basis = %q, want persisted plan-state inference basis", opState.Findings[0].Basis)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, msg, msg.Text, &turn.Result{})
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want inferred proposal buttons")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one inferred proposal prompt", inlineCount)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if !actionListContains(cont.ActionProposal.AllowedActions, organicRalphSandboxAction) || !actionListContains(cont.ActionProposal.AllowedActions, organicRalphSandboxWriteBoundary) {
		t.Fatalf("allowed actions = %#v, want approved_user sandbox write boundary", cont.ActionProposal.AllowedActions)
	}
	for _, want := range []string{"commit_without_separate_approval", "deploy", "restart_service", "push_remote", "network_access_without_separate_grant"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}
	if !actionListContains(cont.ContinuationLease.AllowedActions, organicRalphSandboxAction) || !actionListContains(cont.ContinuationLease.ForbiddenActions, "deploy") {
		t.Fatalf("lease sandbox actions = allowed %#v forbidden %#v, want sandbox copied into lease", cont.ContinuationLease.AllowedActions, cont.ContinuationLease.ForbiddenActions)
	}
	if !strings.Contains(cont.ActionProposal.BoundedEffect, "Sandbox boundary") || !strings.Contains(cont.ActionProposal.BoundedEffect, "approved_user isolated") {
		t.Fatalf("bounded effect = %q, want explicit approved_user sandbox boundary", cont.ActionProposal.BoundedEffect)
	}
	validationMentionsSandbox := false
	for _, step := range cont.ActionProposal.ValidationPlan {
		if strings.Contains(step, "approved_user isolated sandbox") {
			validationMentionsSandbox = true
			break
		}
	}
	if !validationMentionsSandbox {
		t.Fatalf("validation plan = %#v, want approved_user sandbox verification", cont.ActionProposal.ValidationPlan)
	}
	if _, err := rt.ApproveContinuation(9025, 1001); err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	approved, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(approved) err = %v", err)
	}
	if approved.Proposal.Status != session.ProposalStatusApproved || approved.Status != session.OperationStatusActive {
		t.Fatalf("approved operation = %#v, want normalized approved/active proposal", approved)
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

func TestOrganicRalphDoesNotInferWithoutContractOrState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9026, UserID: 0, Scope: telegramDMScopeRef(9026)}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, core.InboundMessage{ChatID: 9026, SenderID: 1001, Text: "sounds good", MessageID: 1}, "sounds good", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferOrganicOperationProposal() = true, want false without contract or persisted state")
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

func TestOrganicRalphExpiredOperationProposalIsStaleEvidenceNotBlocker(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9027, UserID: 0, Scope: telegramDMScopeRef(9027)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "clean-organic-ralph-regression",
		Objective: "Clean live-test Organic Ralph v1 inference from ordinary conversation without a manually pre-written OperationProposal.",
		Status:    session.OperationStatusBlocked,
		Stage:     "awaiting_ordinary_prompt",
		Summary:   "Inspect whether Organic Ralph can infer a harmless status-check proposal from this conversation and stop after evidence.",
		Proposal: session.OperationProposal{
			ID:      "stale-organic-ralph-live-test",
			Summary: "Expired prior live-test proposal",
			Status:  session.ProposalStatusExpired,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	msg := core.InboundMessage{ChatID: 9027, SenderID: 1001, Text: "approved", MessageID: 11}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, msg, msg.Text, &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
	}
	if !inferred {
		t.Fatal("maybeInferOrganicOperationProposal() = false, want inference despite expired stale proposal")
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", opState.Proposal.Status)
	}
	if strings.Contains(opState.Proposal.Summary, "awaiting_ordinary_prompt") {
		t.Fatalf("proposal summary = %q, want semantic summary not internal stage", opState.Proposal.Summary)
	}
	if !strings.Contains(opState.Proposal.Summary, "Inspect whether Organic Ralph") {
		t.Fatalf("proposal summary = %q, want operation summary semantic fallback", opState.Proposal.Summary)
	}
}

func TestOrganicRalphDoesNotInferOnlyInternalStageLabel(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9028, UserID: 0, Scope: telegramDMScopeRef(9028)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:     "internal-stage-only",
		Status: session.OperationStatusBlocked,
		Stage:  "awaiting_ordinary_prompt",
		Proposal: session.OperationProposal{
			ID:     "expired-no-semantic-evidence",
			Status: session.ProposalStatusExpired,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	msg := core.InboundMessage{ChatID: 9028, SenderID: 1001, Text: "approved", MessageID: 12}
	inferred, err := rt.maybeInferOrganicOperationProposal(context.Background(), key, msg, msg.Text, &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferOrganicOperationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferOrganicOperationProposal() = true, want false when only internal stage label remains")
	}
}
