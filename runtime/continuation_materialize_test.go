//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	_ "github.com/mattn/go-sqlite3"
)

func TestHandleInboundMaterializesPendingOperationProposalAsButtonBackedLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I need approval before I cross this boundary."
	provider.faceReplyText = "Approve this lease with the buttons."
	provider.proposalReplyText = testPersonaContinuationProposal(session.ContinuationIntentDecisionHold, "")
	provider.planningReplyText = testGovernorContinuationRatification(session.ContinuationIntentDecisionHold, "Hold until explicit approval.", false)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9011, UserID: 0, Scope: telegramDMScopeRef(9011)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "button-backed-lease-test",
		Objective: "Implement a local button-backed approval path.",
		Status:    session.OperationStatusBlocked,
		Stage:     "lease_proposal",
		Proposal: session.OperationProposal{
			ID:            "button-backed-lease-local-v1",
			Kind:          "system_change",
			Summary:       "Materialize assistant-authored leases as buttons",
			WhyNow:        "Typed approvals are causing boop tax.",
			BoundedEffect: "Inspect and patch locally; stop before commit/deploy/restart.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{ChatID: 9011, SenderID: 1001, SenderName: "admin", Text: "go get it", MessageID: 1})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline count = %d, want 1 button-backed lease prompt", len(sender.inline))
	}
	text := sender.inline[0].text
	if !strings.Contains(text, "Approval:") || !strings.Contains(text, "Materialize assistant-authored leases as buttons") || !strings.Contains(text, "Inspect and patch locally") {
		t.Fatalf("inline text = %q, want materialized operation proposal details", text)
	}
	labels := []string{
		sender.inline[0].rows[0][0].Text, sender.inline[0].rows[0][1].Text,
		sender.inline[0].rows[1][0].Text, sender.inline[0].rows[1][1].Text,
	}
	wantLabels := []string{"Start", "Details", "Change", "Pause"}
	for i, want := range wantLabels {
		if labels[i] != want {
			t.Fatalf("labels = %#v, want prefix %#v", labels, wantLabels)
		}
	}
	if got := sender.inline[0].rows[0][0].CallbackData; got == "" || len(got) > core.TelegramCallbackDataMaxBytes {
		t.Fatalf("approve callback = %q len=%d, want non-empty <= %d", got, len(got), core.TelegramCallbackDataMaxBytes)
	}

	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending || state.ActionProposal.OperationID != "button-backed-lease-local-v1" {
		t.Fatalf("state = %#v, want pending continuation tied to operation proposal", state)
	}
	if state.ActionProposal.BoundedEffect != "Inspect and patch locally; stop before commit/deploy/restart." {
		t.Fatalf("bounded effect = %q", state.ActionProposal.BoundedEffect)
	}
}

func TestMaterializePendingOperationProposalFailsClosedOnUnreadableContinuationState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I need approval before I cross this boundary."
	provider.faceReplyText = "Approve this lease with the buttons."
	provider.proposalReplyText = testPersonaContinuationProposal(session.ContinuationIntentDecisionHold, "")
	provider.planningReplyText = testGovernorContinuationRatification(session.ContinuationIntentDecisionHold, "Hold until explicit approval.", false)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9012, UserID: 0, Scope: telegramDMScopeRef(9012)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "fail-closed-continuation-read",
		Objective: "Do not materialize approval prompts when current approval state is unreadable.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "proposal-fail-closed",
			Kind:          "system_change",
			Summary:       "Fail closed on unreadable approval state",
			WhyNow:        "Duplicate approval prompts are unsafe when the existing state cannot be read.",
			BoundedEffect: "Inspect local state and stop before commit/deploy/restart.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "broken-current-state", RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	db, err := sql.Open("sqlite3", store.DBPath())
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE sessions SET continuation_state_json = ? WHERE session_id = ?`, "{", session.SessionIDForKey(key)); err != nil {
		t.Fatalf("corrupt continuation state: %v", err)
	}

	if _, exists, err := store.ContinuationStateIfExists(key); err == nil || !exists || !strings.Contains(err.Error(), "decode continuation state") {
		t.Fatalf("ContinuationStateIfExists() exists=%v err=%v, want decode error before materialization", exists, err)
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9012, SenderID: 1001, SenderName: "admin", Text: "go get it", MessageID: 1}, "go get it", nil)
	if err == nil || !strings.Contains(err.Error(), "read prior continuation state") {
		t.Fatalf("materializePendingOperationProposalApproval() materialized=%v err=%v, want fail-closed continuation read error", materialized, err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline count = %d, want no approval prompt when continuation state is unreadable", len(sender.inline))
	}
}

func TestMaterializeOperationProposalShowsDataAccessLeaseClassCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9031, UserID: 0, Scope: telegramDMScopeRef(9031)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "data-access-card",
		Objective: "Inspect generated image artifact through governed data access.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "data-access-image-read",
			Kind:          "data_access",
			Summary:       "Read one generated image artifact",
			WhyNow:        "The model can analyze the image only if the artifact is routed as data.",
			BoundedEffect: "Read artifact://image2/field-of-attention.png once; no retention or broad filesystem scan.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9031, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want data-access lease card")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess {
		t.Fatalf("lease class = %q, want data_access", cont.ContinuationLease.LeaseClass)
	}
	if !actionListContains(cont.ActionProposal.AllowedActions, "read_approved_resource") || !actionListContains(cont.ActionProposal.ForbiddenActions, "silent_data_ingestion") {
		t.Fatalf("proposal actions allowed=%#v forbidden=%#v, want data-access boundaries", cont.ActionProposal.AllowedActions, cont.ActionProposal.ForbiddenActions)
	}
	if cont.ContinuationLease.Constraints["resource"] == "" || cont.ContinuationLease.Constraints["retention"] == "" {
		t.Fatalf("lease constraints = %#v, want data-access constraints", cont.ContinuationLease.Constraints)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	for _, want := range []string{"Approval: Read one generated image artifact", "Scope:", "Read artifact://image2/field-of-attention.png once"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	for _, notWant := range []string{"Operator card:", "Constraint: resource=", "Use the buttons"} {
		if strings.Contains(inlineText, notWant) {
			t.Fatalf("inline text = %q, did not want verbose contract fragment %q", inlineText, notWant)
		}
	}
}

func TestApproveMaterializedOperationProposalUpdatesOperationProposalStatus(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9012, UserID: 0, Scope: telegramDMScopeRef(9012)}
	opState := session.OperationState{Proposal: session.OperationProposal{ID: "lease-approve-sync", Summary: "Approve sync", Status: session.ProposalStatusPending}}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationProposal(opState, "", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if _, err := rt.ApproveContinuation(9012, 1001); err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusApproved || got.Status != session.OperationStatusActive {
		t.Fatalf("operation state = %#v, want approved/active", got)
	}
}

func TestMaterializePendingOperationProposalAfterTurnAuthorization(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9014, UserID: 0, Scope: telegramDMScopeRef(9014)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "post-continuation-next-lease",
		Objective: "Continue a broader goal after one approved turn.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "post-continuation-next-lease-v1",
			Kind:          "read_only_review",
			Summary:       "Plan the next safe phase",
			WhyNow:        "The approved turn completed only phase one.",
			BoundedEffect: "Review only and report one next proposal.",
			Status:        session.ProposalStatusPending,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{
		ChatID:       9014,
		SenderID:     1001,
		Text:         approvedContinuationEventText,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	}, approvedContinuationEventText, nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want post-authorization proposal buttons")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want 1", inlineCount)
	}
}

func TestMaterializeDurablePhasePlanUsesNextPendingPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9015, UserID: 0, Scope: telegramDMScopeRef(9015)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-plan-op",
		Objective: "Deliver Lighthouse inbox workflow.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_plan",
		Proposal: session.OperationProposal{
			ID:      "stale-single-step",
			Summary: "Do the whole thing in one step",
			Status:  session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:   "phase-plan",
			Goal: "Deliver Lighthouse inbox workflow.",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-1-contract",
					Summary:          "Write the read-only contract",
					Status:           session.PlanStatusCompleted,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Inspect only and write the contract.",
					RequiresApproval: true,
				},
				{
					ID:               "phase-2-implementation",
					Summary:          "Implement the local inbox bridge",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					WhyNow:           "The contract phase is complete.",
					BoundedEffect:    "Edit local files and run tests; stop before deploy.",
					AllowedActions:   []string{"edit_files", "run_tests"},
					ForbiddenActions: []string{"deploy", "restart_service"},
					RequiresApproval: true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9015, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase-plan approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending lease", cont)
	}
	if cont.ActionProposal.RiskClass != "plan_lease" || !strings.Contains(cont.ActionProposal.Summary, "Approve plan budget") {
		t.Fatalf("action proposal = %#v, want next pending phase plan budget", cont.ActionProposal)
	}
	if len(cont.ApprovalBundle.Phases) != 1 || cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-2-implementation" {
		t.Fatalf("approval bundle = %#v, want next pending phase budget lane", cont.ApprovalBundle)
	}
	if cont.ContinuationLease.MaxTurns != 1 || cont.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("lease = %#v, want one-turn plan budget lease", cont.ContinuationLease)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusPending || !strings.Contains(opState.Proposal.Summary, "Approve plan budget") {
		t.Fatalf("operation proposal = %#v, want synthetic pending plan budget proposal", opState.Proposal)
	}
	if opState.PhasePlan.CurrentPhaseID != "phase-2-implementation" || opState.PhasePlan.Phases[1].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("phase plan = %#v, want current phase linked to lease", opState.PhasePlan)
	}

	sender.mu.Lock()
	inlineText := ""
	var labels []string
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Implement the local inbox bridge") || strings.Contains(inlineText, "Do the whole thing in one step") {
		t.Fatalf("inline text = %q, want next phase without stale proposal", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializePhasePlanIgnoresStaleInProgressWhenCurrentPhaseIsPending(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9028, UserID: 0, Scope: telegramDMScopeRef(9028)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "synth-remainder-op",
		Objective: "Finish the repo-only Synth Telegram runner work.",
		Status:    session.OperationStatusBlocked,
		Stage:     "review_complete_plan_draft_ready_not_armed_due_autoapproval",
		Proposal: session.OperationProposal{
			ID:      "draft-synth-remainder",
			Summary: "Draft repo-only Synth continuation",
			Status:  session.ProposalStatusSuperseded,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "synth-remainder-plan",
			Goal:           "Finish the repo-only Synth custom Telegram runner.",
			CurrentPhaseID: "phase-r1-repo-finish",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-stale-live-route",
					Summary:        "Old live route config phase",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "config_change_restart",
					LeaseID:        "lease-old-live-route",
				},
				{
					ID:               "phase-r1-repo-finish",
					Summary:          "Commit current dirty safety/status slice and continue repo-only hardening",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Edit local repo files, run tests, and create local commits; stop before deploy.",
					AllowedActions:   []string{"edit_files", "run_tests", "git_commit"},
					ForbiddenActions: []string{"deploy", "restart_service", "read_token"},
				},
				{
					ID:             "phase-r2-status-polish",
					Summary:        "Polish doctor and status projections",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Patch local status/doctor code and tests only.",
					AllowedActions: []string{"edit_files", "run_tests"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9028, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want approval prompt despite stale non-current in-progress phase")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.RiskClass != "plan_lease" {
		t.Fatalf("continuation = %#v, want pending multi-step plan lease", cont)
	}
	if len(cont.ApprovalBundle.Phases) != 2 ||
		cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-r1-repo-finish" ||
		cont.ApprovalBundle.Phases[1].OperationPhaseID != "phase-r2-status-polish" {
		t.Fatalf("continuation = %#v, want current and next repo phases bundled with stale phase excluded", cont)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.PhasePlan.Phases[0].Status != session.PlanStatusPending || opState.PhasePlan.Phases[0].LeaseID != "" {
		t.Fatalf("stale phase = %#v, want cleared back to pending without old lease", opState.PhasePlan.Phases[0])
	}
	if opState.PhasePlan.CurrentPhaseID != "phase-r1-repo-finish" {
		t.Fatalf("CurrentPhaseID = %q, want phase-r1-repo-finish", opState.PhasePlan.CurrentPhaseID)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Commit current dirty safety/status slice") || strings.Contains(inlineText, "Old live route config phase") {
		t.Fatalf("inline text = %q, want current commit phase without stale phase", inlineText)
	}
}

func TestMaterializePhasePlanRecoversCurrentPhaseAfterRevokedLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9032, UserID: 0, Scope: telegramDMScopeRef(9032)}
	leaseID := "lease-phase-recover-current"
	opState := session.OperationState{
		ID:        "recover-current-phase-op",
		Objective: "Reoffer the current phase after a bad lease revocation.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "recover-current-phase-plan",
			CurrentPhaseID: "phase-r1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-r1",
					Summary:        "Commit validated local repo slices",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "Run tests, commit coherent local slices, and report evidence.",
					AllowedActions: []string{"run_go_tests", "git_commit_validated_slices"},
					LeaseID:        leaseID,
				},
			},
		},
	}
	opState.Proposal = session.OperationProposal{
		ID:            operationPhaseProposalID(opState, opState.PhasePlan.Phases[0]),
		Kind:          "workspace_commit_then_repo_write_bounded",
		Summary:       "Commit validated local repo slices",
		BoundedEffect: "Run tests, commit coherent local slices, and report evidence.",
		Status:        session.ProposalStatusApproved,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	now := time.Now().UTC()
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusRevoked,
		StageSummary:   "Commit validated local repo slices",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-" + opState.Proposal.ID,
			OperationID: opState.Proposal.ID,
			Summary:     "Commit validated local repo slices",
			RiskClass:   "workspace_commit_then_repo_write_bounded",
			Status:      session.ProposalStatusApproved,
			ExpiresAt:   now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             leaseID,
			ProposalID:     "aprop-" + opState.Proposal.ID,
			Status:         session.ContinuationLeaseStatusRevoked,
			MaxTurns:       1,
			RemainingTurns: 0,
			RevokedAt:      now,
			ExpiresAt:      now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9032, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want fresh prompt after revoked current-phase lease")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want fresh pending lease", cont)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusPending || got.PhasePlan.Phases[0].LeaseID == "" {
		t.Fatalf("phase = %#v, want re-materialized pending phase lease", got.PhasePlan.Phases[0])
	}
	if got.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("proposal status = %q, want fresh pending proposal", got.Proposal.Status)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want fresh approval buttons", inlineCount)
	}
}

func TestMaterializeMetadataPreflightPhaseUsesReadOnlyContract(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9033, UserID: 0, Scope: telegramDMScopeRef(9033)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "metadata-preflight-op",
		Objective: "Run a metadata-only preflight.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "metadata-preflight-plan",
			CurrentPhaseID: "phase-metadata",
			Phases: []session.OperationPhase{{
				ID:             "phase-metadata",
				Summary:        "Live-adjacent metadata preflight. Prior diagnostic mentioned workspace_write mismatch.",
				Status:         session.PlanStatusPending,
				AuthorityClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
				BoundedEffect:  "Inspect config route and token-file metadata only; no token contents and no Telegram network.",
				AllowedActions: []string{"report_button_diagnosis"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9033, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want metadata phase prompt")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if mode := continuationWorkMode(cont); mode != WorkModeReadOnly {
		t.Fatalf("continuationWorkMode() = %q, want read_only", mode)
	}
	if !actionListContains(cont.ContinuationLease.AllowedActions, session.AuthorityWorkActionReadOnly) {
		t.Fatalf("lease allowed actions = %#v, want read_only", cont.ContinuationLease.AllowedActions)
	}
	if actionListContains(cont.ContinuationLease.AllowedActions, string(WorkModeWorkspaceWrite)) {
		t.Fatalf("lease allowed actions = %#v, should not allow workspace_write", cont.ContinuationLease.AllowedActions)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want real approval buttons", inlineCount)
	}
}

func TestMaterializePlanningOnlyPhaseOffersPlanBudget(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9031, UserID: 0, Scope: telegramDMScopeRef(9031)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "children-diagnostic-20260504",
		Objective: "Repair child diagnostic failures.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "phase-children-diagnostic-20260504",
			CurrentPhaseID: "phase-2-repair-planning",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-2-repair-planning",
					Summary:          "Turn child diagnostic failures into explicit repair phases.",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Draft repair phases only; do not execute repairs.",
					AllowedActions:   []string{"draft_repair_phases", "update_operation_phase_plan"},
					RequiresApproval: true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9031, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want planning phase offered as a plan budget")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.RiskClass != "plan_lease" || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want one-turn plan budget", cont)
	}
	if len(cont.ApprovalBundle.Phases) != 1 || cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-2-repair-planning" {
		t.Fatalf("approval bundle = %#v, want planning phase as budget lane", cont.ApprovalBundle)
	}
	sender.mu.Lock()
	inlineText := ""
	labels := []string(nil)
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Plan:") || !strings.Contains(inlineText, "I'll do:") || strings.Contains(inlineText, "Allowed actions:") {
		t.Fatalf("inline text = %q, want compact plan budget prompt", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializeCompletedPhasePlanWithoutProposalAllowsContinuationFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9029, UserID: 0, Scope: telegramDMScopeRef(9029)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-plan-no-proposal",
		Objective: "Allow organic continuation when the phase plan has no actionable approval.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "completed-plan",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Review", Status: session.PlanStatusCompleted, CompletedAt: time.Now().UTC()},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9029, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want false so organic continuation fallback can run")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no materialized prompt", inlineCount)
	}
}

func TestMaterializePendingOperationProposalWhenPhasePlanHasNoPendingPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9017, UserID: 0, Scope: telegramDMScopeRef(9017)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-phase-plan-op",
		Objective: "Ship the remaining operator cleanup.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "ordinary-proposal-after-phases",
			Kind:          "read_only_review",
			Summary:       "Review the completed phase evidence and propose cleanup",
			WhyNow:        "The durable phases are complete, but the operator asked for one more ordinary proposal.",
			BoundedEffect: "Inspect only and report the next bounded proposal.",
			Status:        session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID: "completed-phase-plan",
			Phases: []session.OperationPhase{
				{
					ID:          "phase-1",
					Summary:     "Write contract",
					Status:      session.PlanStatusCompleted,
					LeaseID:     "lease-phase-1",
					CompletedAt: time.Now().UTC(),
				},
				{
					ID:          "phase-2",
					Summary:     "Implement contract",
					Status:      session.PlanStatusCompleted,
					LeaseID:     "lease-phase-2",
					CompletedAt: time.Now().UTC(),
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9017, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want ordinary proposal approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != "ordinary-proposal-after-phases" {
		t.Fatalf("continuation = %#v, want pending ordinary proposal lease", cont)
	}
	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Review the completed phase evidence and propose cleanup") {
		t.Fatalf("inline text = %q, want ordinary proposal prompt", inlineText)
	}
}

func TestMaterializePlanLeaseUsesAutoApprovalInsteadOfSuppressingPrompt(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9030, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9030, UserID: 0, Scope: telegramDMScopeRef(9030)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "autoapprove-plan-lease-op",
		Objective: "Approve a bounded plan envelope without manual buttons.",
		Status:    session.OperationStatusBlocked,
		Stage:     "plan_lease_proposal",
		PlanLease: session.OperationPlanLease{
			ID:         "autoapprove-plan-lease",
			Summary:    "Approve bounded local review budget",
			Status:     session.PlanLeaseStatusProposed,
			TurnBudget: 2,
			Lanes: []session.OperationPlanLeaseLane{
				{ID: "review", Summary: "Review state", AuthorityClass: "read_only_review", ExpectedTurns: 2},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9030, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want auto-approved plan lease")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want autoapproval to consume without manual buttons", inlineCount)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.Status != session.ProposalStatusApproved || cont.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want auto-approved consumed plan lease", cont)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9030, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 1 {
		t.Fatalf("autoapproval leases = %#v, want one consumed use", leases)
	}
}

func TestMaterializeVisibleButtonRequestBypassesAutoApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9034, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9034, UserID: 0, Scope: telegramDMScopeRef(9034)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "visible-buttons-op",
		Objective: "Ask for real visible approval buttons.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "visible-buttons-plan",
			CurrentPhaseID: "phase-visible",
			Phases: []session.OperationPhase{{
				ID:               "phase-visible",
				Summary:          "Read status only",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "read_only_review",
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9034, SenderID: 1001, Text: "send me request for approval with buttons", MessageID: 1}, "send me request for approval with buttons", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want visible approval prompt")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want real buttons despite active autoapproval", inlineCount)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending visible button prompt", cont.Status)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9034, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want no consumed use", leases)
	}
}

func TestMaterializePendingOperationProposalWhilePhasePlanIsInProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9018, UserID: 0, Scope: telegramDMScopeRef(9018)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "in-progress-phase-plan-op",
		Objective: "Keep operator work moving without suppressing explicit proposals.",
		Status:    session.OperationStatusBlocked,
		Proposal: session.OperationProposal{
			ID:            "ordinary-proposal-during-phase",
			Kind:          "status_check",
			Summary:       "Report whether the active phase has enough evidence",
			WhyNow:        "The operator asked for a separate status proposal while a phase is marked in progress.",
			BoundedEffect: "Inspect state only and report status; do not advance the active phase.",
			Status:        session.ProposalStatusPending,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "in-progress-phase-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1",
					Summary:        "Patch the implementation",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_write",
					LeaseID:        "lease-phase-1",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9018, SenderID: 1001, Text: "status", MessageID: 1}, "status", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want ordinary proposal approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != "ordinary-proposal-during-phase" {
		t.Fatalf("continuation = %#v, want pending ordinary proposal lease", cont)
	}
	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Report whether the active phase has enough evidence") {
		t.Fatalf("inline text = %q, want ordinary proposal prompt", inlineText)
	}
}

func TestMaterializeDoesNotReofferSyntheticPhaseProposalAsOrdinaryProposal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9019, UserID: 0, Scope: telegramDMScopeRef(9019)}
	opState := session.OperationState{
		ID:        "synthetic-phase-proposal-op",
		Objective: "Avoid duplicate phase approvals.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "synthetic-phase-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1",
					Summary:        "Patch the implementation",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "workspace_write",
					LeaseID:        "lease-phase-1",
				},
			},
		},
	}
	opState.Proposal = session.OperationProposal{
		ID:            operationPhaseProposalID(opState, opState.PhasePlan.Phases[0]),
		Kind:          "workspace_write",
		Summary:       "Patch the implementation",
		BoundedEffect: "Edit files and run tests.",
		Status:        session.ProposalStatusPending,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9019, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase-plan ownership to suppress generic continuation")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no duplicate ordinary proposal prompt", inlineCount)
	}
}

func TestApproveDurablePhasePlanLeaseMarksPhaseInProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9016, UserID: 0, Scope: telegramDMScopeRef(9016)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-plan-approve-op",
		Objective: "Deliver durable phase plan.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "phase-plan-approve",
			Phases: []session.OperationPhase{{
				ID:               "phase-1",
				Summary:          "Patch the operation planner",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "workspace_write",
				BoundedEffect:    "Edit files and run tests.",
				RequiresApproval: true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9016, SenderID: 1001, Text: "go", MessageID: 1}, "go", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want phase lease")
	}

	if _, err := rt.ApproveContinuation(9016, 1001); err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusApproved || got.Status != session.OperationStatusActive {
		t.Fatalf("operation = %#v, want approved active synthetic proposal", got)
	}
	if len(got.PhasePlan.Phases) != 1 || got.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase plan = %#v, want approved phase in_progress", got.PhasePlan)
	}
	if got.PhasePlan.CurrentPhaseID != "phase-1" {
		t.Fatalf("CurrentPhaseID = %q, want phase-1", got.PhasePlan.CurrentPhaseID)
	}
}

func TestMaterializeSingleLocalDesignPhaseDoesNotRaiseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9035, UserID: 0, Scope: telegramDMScopeRef(9035)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "single-local-design-op",
		Objective: "Draft one local design note without external effects.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "single-local-design-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-design",
				Summary:        "Draft local child-agent design artifact",
				Status:         session.PlanStatusPending,
				AuthorityClass: "read_only_review",
				BoundedEffect:  "Inspect local notes and write a local design artifact only.",
				AllowedActions: []string{"inspect_local_notes", "draft_contract"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9035, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want no approval prompt for one local design lane")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no approval/status ritual", inlineCount, sentCount)
	}
}

func TestMaterializeSingleLocalReportPhaseDoesNotRaiseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9036, UserID: 0, Scope: telegramDMScopeRef(9036)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "single-local-report-op",
		Objective: "Write a local lifecycle report.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "single-local-report-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-report",
				Summary:        "Map local lifecycle evidence and write report",
				Status:         session.PlanStatusPending,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Read local state and write a local report artifact; no external account or restart.",
				AllowedActions: []string{"inspect_local_state", "write_report_artifact"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9036, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want no approval prompt for one local report lane")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no approval/status ritual", inlineCount, sentCount)
	}
}

func TestMaterializePublicReadPhaseStillRaisesFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9037, UserID: 0, Scope: telegramDMScopeRef(9037)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "public-read-op",
		Objective: "Run one public account metadata read.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "public-read-plan",
			Phases: []session.OperationPhase{{
				ID:             "phase-public-read",
				Summary:        "Read public profile metadata once",
				Status:         session.PlanStatusPending,
				AuthorityClass: "public_account_content_read",
				BoundedEffect:  "Invoke exactly one public profile metadata read for example_handle.",
				AllowedActions: []string{"public_profile_metadata_read"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9037, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want public read approval prompt")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.RiskClass != "public_account_content_read" {
		t.Fatalf("continuation = %#v, want pending public read approval", cont)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want approval buttons", inlineCount)
	}
}

func TestMaterializeSupersededPhaseIsStructurallySuppressed(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9038, UserID: 0, Scope: telegramDMScopeRef(9038)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "superseded-phase-op",
		Objective: "Avoid stale duplicate approvals.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "superseded-phase-plan",
			CurrentPhaseID: "phase-old",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-old",
					Summary:        "Verify old app-only bearer readiness",
					Status:         session.PlanStatusPending,
					AuthorityClass: "external_account_auth_status",
					BoundedEffect:  "Old readiness check that should no longer be used.",
				},
				{
					ID:                 "phase-new",
					Summary:            "Use newer completed bearer readiness evidence",
					Status:             session.PlanStatusCompleted,
					AuthorityClass:     "external_account_auth_status",
					SupersedesPhaseIDs: []string{"phase-old"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9038, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want superseded phase suppressed")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 0 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d, want no stale duplicate prompt", inlineCount, sentCount)
	}
}

func TestRevokeMaterializedOperationProposalDeniesPendingOperationProposal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9013, UserID: 0, Scope: telegramDMScopeRef(9013)}
	opState := session.OperationState{Proposal: session.OperationProposal{ID: "lease-revoke-sync", Summary: "Revoke sync", Status: session.ProposalStatusPending}}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationProposal(opState, "", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if _, err := rt.RevokeContinuation(9013); err != nil {
		t.Fatalf("RevokeContinuation() err = %v", err)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Proposal.Status != session.ProposalStatusDenied || got.Status != session.OperationStatusBlocked {
		t.Fatalf("operation state = %#v, want denied/blocked", got)
	}
}

func TestMaterializeDurablePhasePlanBundlesConsecutiveSafePhases(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9020, UserID: 0, Scope: telegramDMScopeRef(9020)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-bundle-op",
		Objective: "Ship approval bundles safely.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "phase-bundle-plan",
			Goal: "Let Daniel approve multiple bounded stages at once.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1-design",
					Summary:        "Design the bundle contract",
					Status:         session.PlanStatusPending,
					AuthorityClass: "read_only_review",
					BoundedEffect:  "Inspect only and write the contract.",
					AllowedActions: []string{"inspect_code", "draft_contract"},
				},
				{
					ID:               "phase-2-implementation",
					Summary:          "Implement bundled approvals",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "workspace_write",
					BoundedEffect:    "Edit continuation code and focused tests; stop before deploy.",
					AllowedActions:   []string{"edit_files", "run_tests"},
					ForbiddenActions: []string{"deploy", "mailbox_access"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9020, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want bundled phase approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending bundled lease", cont)
	}
	if cont.RemainingTurns != 2 || cont.ContinuationLease.MaxTurns != 2 || cont.ContinuationLease.RemainingTurns != 2 {
		t.Fatalf("turns = state %d lease %d/%d, want bundled 2", cont.RemainingTurns, cont.ContinuationLease.MaxTurns, cont.ContinuationLease.RemainingTurns)
	}
	bundle := session.NormalizeContinuationApprovalBundle(cont.ApprovalBundle)
	if bundle.ID == "" || len(bundle.Phases) != 2 || bundle.CurrentPhaseID != bundle.Phases[0].ID {
		t.Fatalf("bundle = %#v, want two phases with first current", bundle)
	}
	if bundle.Phases[0].OperationPhaseID != "phase-1-design" || bundle.Phases[0].AuthorityClass != "read_only_review" {
		t.Fatalf("bundle first phase = %#v", bundle.Phases[0])
	}
	if bundle.Phases[1].OperationPhaseID != "phase-2-implementation" || bundle.Phases[1].AuthorityClass != "workspace_write" {
		t.Fatalf("bundle second phase = %#v", bundle.Phases[1])
	}
	if got := cont.ActionProposal.RiskClass; got != "plan_lease" {
		t.Fatalf("risk class = %q, want plan_lease budget envelope", got)
	}
	if !strings.Contains(cont.ActionProposal.BoundedEffect, "Work inside this approved plan budget only") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "turn_budget=2") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "lane phase-1-design read_only_review") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "lane phase-2-implementation workspace_write") {
		t.Fatalf("bounded effect = %q, want compact plan-budget lane boundaries", cont.ActionProposal.BoundedEffect)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "plan_lease_approval" || opState.Proposal.ID != cont.ActionProposal.OperationID || opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("operation = %#v, want synthetic plan budget proposal", opState)
	}
	if opState.PhasePlan.CurrentPhaseID != "phase-1-design" || opState.PhasePlan.Phases[0].LeaseID != cont.ContinuationLease.ID || opState.PhasePlan.Phases[1].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("phase plan = %#v, want both bundled phases linked to same lease", opState.PhasePlan)
	}

	sender.mu.Lock()
	inlineText := ""
	var labels []string
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Plan:") || !strings.Contains(inlineText, "I'll do:") || !strings.Contains(inlineText, "Design the bundle contract") || !strings.Contains(inlineText, "Implement bundled approvals") {
		t.Fatalf("inline text = %q, want compact plan budget details", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializeBlockedConsentPhaseSendsStatusWithoutApprovalButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9023, UserID: 0, Scope: telegramDMScopeRef(9023)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "mada-intake-op",
		Objective: "Help Mada with a consent-first job agent.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "mada-intake-plan",
			Goal: "Consent-first Mada intake and profile scoring.",
			Phases: []session.OperationPhase{
				{
					ID:                "phase-33-mada-intake",
					Summary:           "Consent-first Mada intake and wife-owned profile/scoring rubric.",
					Status:            session.PlanStatusPending,
					AuthorityClass:    "private_data_intake",
					WhyNow:            "Blocked: Daniel reported Mada is not available today, and no Mada opt-in has been observed. Wait for her explicit opt-in on a later turn.",
					BoundedEffect:     "Ask approved preference questions and process wife-provided CV/preferences only after onboarding/opt-in.",
					ApprovalSubject:   "third_party",
					BlockedReasonCode: "requires_opt_in",
					RequiresOptIn:     true,
				},
				{
					ID:             "phase-34-email-ranking",
					Summary:        "Later email-forward/job-ranking and bounded public job scouting after profile approval.",
					Status:         session.PlanStatusPending,
					AuthorityClass: "external_account_email_read_public_web_read",
					BoundedEffect:  "Read only synth@idolum.ai job-forwarding mailbox after profile approval.",
				},
				{
					ID:             "phase-36-stale-repo-finish",
					Summary:        "Superseded prior R1 repo-only finish phase after commit-denial failure.",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "No authority from this stale phase should be used.",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9023, SenderID: 1001, Text: "continue", MessageID: 55}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want blocked status handled")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sentText := ""
	if sentCount > 0 {
		sentText = sender.sent[sentCount-1].Text
	}
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval buttons for blocked opt-in phase", inlineCount)
	}
	if sentCount != 1 || !strings.Contains(sentText, "Blocked: Consent-first Mada intake") || !strings.Contains(sentText, "explicit opt-in") || !strings.Contains(sentText, "Details: /debug") {
		t.Fatalf("sent text = %q, want concise blocked status", sentText)
	}
	if strings.Contains(sentText, "Approval needed") || strings.Contains(sentText, "Use the buttons") {
		t.Fatalf("sent text = %q, want no approval ritual", sentText)
	}

	events, err := store.ExecutionEventsByChat(9023, time.Now().Add(-time.Hour), 20)
	if err != nil {
		t.Fatalf("ExecutionEventsByChat() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationAdjudicated) {
		t.Fatalf("events = %#v, want continuation approval adjudication", events)
	}
}

func TestMaterializeEscalatedOperatorPhaseShowsManualApprovalDespiteAutoApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9024, 1001, "15m all live auth-status check"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9024, UserID: 0, Scope: telegramDMScopeRef(9024)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "email-child-credential-recovery-20260507",
		Objective: "Recover whether the child email credentials are usable without reading mailbox contents.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "email-child-credential-recovery-plan",
			Goal: "Check bounded auth status before any private email work.",
			Phases: []session.OperationPhase{{
				ID:                "phase-e1b-readonly-auth-status-check",
				Summary:           "Check whether existing gog_cli credentials/profile can authenticate without reading mailbox contents.",
				Status:            session.PlanStatusPending,
				AuthorityClass:    "read_only_auth_status_check",
				WhyNow:            "The governor is concerned about external account state and needs explicit operator approval before touching auth status.",
				BoundedEffect:     "Run one minimal status or identity check; report nonsecret exit code and auth validity only.",
				AllowedActions:    []string{"run_gog_cli_auth_status_or_identity_check", "inspect_nonsecret_exit_code_and_error", "report_auth_validity"},
				ForbiddenActions:  []string{"read_or_print_secret_values", "read_mailbox_contents", "run_gog_cli_mail_query", "start_oauth_flow", "copy_restore_delete_or_write_credentials", "mutate_google_account", "edit_config", "deploy", "restart"},
				BlockedReasonCode: "waiting_for_explicit_approval",
				RequiresConsent:   true,
				RequiresOptIn:     true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9024, SenderID: 1001, Text: "continue", MessageID: 56}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want escalated approval prompt")
	}

	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending", state.Status)
	}
	if state.ActionProposal.AutoApproveEligible == nil || *state.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want explicit false", state.ActionProposal.AutoApproveEligible)
	}
	if state.ActionProposal.RiskClass != "external_account_auth_status" {
		t.Fatalf("risk class = %q, want external_account_auth_status", state.ActionProposal.RiskClass)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	inlineText := ""
	var labels []string
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
		labels = continuationButtonLabels(sender.inline[inlineCount-1].rows)
	}
	sender.mu.Unlock()
	if inlineCount != 1 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d text=%q, want one manual approval prompt and no blocked notice", inlineCount, sentCount, inlineText)
	}
	for _, want := range []string{"Approval:", "Why I'm asking:", "I'll do:", "Approve this step?"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	if strings.Contains(inlineText, "Blocked:") || strings.Contains(inlineText, "Approval needed.") {
		t.Fatalf("inline text = %q, want escalated approval card, not blocked/legacy approval text", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9024, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want one unused lease", leases)
	}
}

func TestMaterializeResourceOwnerMailboxConsentShowsManualApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9029, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9029, UserID: 0, Scope: telegramDMScopeRef(9029)}
	manualOnly := false
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "email-child-mailbox-smoke-20260507",
		Objective: "Check one configured mailbox query without surfacing message contents.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "email-child-mailbox-smoke-plan",
			Goal: "Run a bounded resource-owner mailbox smoke after explicit approval.",
			Phases: []session.OperationPhase{{
				ID:                  "phase-e2b-readonly-mailbox-smoke",
				Summary:             "Run one read-only mailbox smoke for host@idolum.ai.",
				Status:              session.PlanStatusPending,
				AuthorityClass:      "read_only_mailbox_smoke",
				WhyNow:              "The operator needs proof that configured gog_cli access can query the inbox label without exposing content.",
				BoundedEffect:       "Run one configured label:inbox query with max=1; suppress contents and report only exit code and parseability.",
				AllowedActions:      []string{"run_configured_gog_cli_mail_query_once", "suppress_mailbox_contents", "report_exit_code_and_parseability"},
				ForbiddenActions:    []string{"print_mailbox_contents", "read_or_print_secret_values", "start_oauth_flow", "mutate_google_account", "deploy", "restart"},
				GateLevel:           "escalated_operator_approval",
				GateReasonCode:      "mailbox_content",
				ApprovalSubject:     "resource_owner",
				AutoApproveEligible: &manualOnly,
				BlockedReasonCode:   "waiting_for_explicit_approval",
				RequiresConsent:     true,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9029, SenderID: 1001, Text: "continue", MessageID: 57}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want resource-owner mailbox approval prompt")
	}

	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending", state.Status)
	}
	if state.ActionProposal.AutoApproveEligible == nil || *state.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want explicit false", state.ActionProposal.AutoApproveEligible)
	}
	if state.ActionProposal.RiskClass != "mailbox_content" {
		t.Fatalf("risk class = %q, want mailbox_content", state.ActionProposal.RiskClass)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	inlineText := ""
	var labels []string
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
		labels = continuationButtonLabels(sender.inline[inlineCount-1].rows)
	}
	sender.mu.Unlock()
	if inlineCount != 1 || sentCount != 0 {
		t.Fatalf("inline=%d sent=%d text=%q, want one manual approval prompt and no blocked notice", inlineCount, sentCount, inlineText)
	}
	for _, want := range []string{"Approval:", "Why I'm asking:", "I'll do:", "Approve this step?"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	if strings.Contains(inlineText, "Blocked:") || strings.Contains(inlineText, "explicit consent") {
		t.Fatalf("inline text = %q, want approval prompt, not consent block", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9029, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want one unused lease", leases)
	}
}

func TestOperationPhaseApprovalUsesTypedGovernanceMetadata(t *testing.T) {
	t.Parallel()

	optInPhase := session.OperationPhase{
		ID:             "phase-opt-in",
		Summary:        "Consent-first intake",
		Status:         session.PlanStatusPending,
		AuthorityClass: "private_data_intake",
		RequiresOptIn:  true,
	}
	if got := operationPhaseApprovalBlockedReason(optInPhase); got != "waiting for explicit opt-in" {
		t.Fatalf("operationPhaseApprovalBlockedReason(opt-in) = %q, want explicit opt-in", got)
	}

	consentPhase := session.OperationPhase{
		ID:                "phase-consent",
		Summary:           "Consent-first intake",
		Status:            session.PlanStatusPending,
		AuthorityClass:    "private_data_intake",
		BlockedReasonCode: "consent-required",
	}
	if got := operationPhaseApprovalBlockedReason(consentPhase); got != "waiting for explicit consent" {
		t.Fatalf("operationPhaseApprovalBlockedReason(consent) = %q, want explicit consent", got)
	}

	escalatedPhase := session.OperationPhase{
		ID:                "phase-e1b-readonly-auth-status-check",
		Summary:           "Check whether existing gog_cli credentials/profile can authenticate without reading mailbox contents.",
		Status:            session.PlanStatusPending,
		AuthorityClass:    "read_only_auth_status_check",
		AllowedActions:    []string{"run_gog_cli_auth_status_or_identity_check"},
		ForbiddenActions:  []string{"read_mailbox_contents", "run_gog_cli_mail_query", "start_oauth_flow"},
		BlockedReasonCode: "waiting_for_explicit_approval",
		RequiresOptIn:     true,
		RequiresConsent:   true,
	}
	if got := operationPhaseApprovalBlockedReason(escalatedPhase); got != "" {
		t.Fatalf("operationPhaseApprovalBlockedReason(escalated auth status) = %q, want materializable approval", got)
	}
	gate := operationPhaseApprovalGate(escalatedPhase)
	if gate.Level != operationGateLevelEscalatedOperatorApproval || gate.AutoApproveEligible {
		t.Fatalf("operationPhaseApprovalGate(escalated auth status) = %#v, want escalated/manual gate", gate)
	}

	resourceOwnerConsentPhase := session.OperationPhase{
		ID:              "phase-e2b-readonly-mailbox-smoke",
		Summary:         "Run one read-only mailbox smoke.",
		Status:          session.PlanStatusPending,
		AuthorityClass:  "read_only_mailbox_smoke",
		GateLevel:       "escalated_operator_approval",
		GateReasonCode:  "mailbox_content",
		ApprovalSubject: "resource_owner",
		RequiresConsent: true,
	}
	if got := operationPhaseApprovalBlockedReason(resourceOwnerConsentPhase); got != "" {
		t.Fatalf("operationPhaseApprovalBlockedReason(resource-owner consent) = %q, want materializable approval", got)
	}
	gate = operationPhaseApprovalGate(resourceOwnerConsentPhase)
	if gate.Level != operationGateLevelEscalatedOperatorApproval || gate.AutoApproveEligible || gate.ApprovalSubject != "resource_owner" {
		t.Fatalf("operationPhaseApprovalGate(resource-owner consent) = %#v, want manual resource-owner gate", gate)
	}

	thirdPartyConsentPhase := resourceOwnerConsentPhase
	thirdPartyConsentPhase.ID = "phase-wife-private-intake"
	thirdPartyConsentPhase.AuthorityClass = "private_data_intake"
	thirdPartyConsentPhase.ApprovalSubject = "third_party"
	if got := operationPhaseApprovalBlockedReason(thirdPartyConsentPhase); got != "waiting for explicit consent" {
		t.Fatalf("operationPhaseApprovalBlockedReason(third-party consent) = %q, want hard consent block", got)
	}

	privateDataPhase := session.OperationPhase{
		ID:             "phase-private-intake",
		Summary:        "Consent-first private intake",
		Status:         session.PlanStatusPending,
		AuthorityClass: "private_data_intake",
		GateLevel:      "escalated_operator_approval",
		RequiresOptIn:  true,
	}
	if got := operationPhaseApprovalBlockedReason(privateDataPhase); got != "waiting for explicit opt-in" {
		t.Fatalf("operationPhaseApprovalBlockedReason(private explicit escalated) = %q, want hard opt-in block", got)
	}

	stalePhase := session.OperationPhase{
		ID:             "phase-old",
		Summary:        "Prior repo finish phase",
		Status:         session.PlanStatusPending,
		AuthorityClass: "workspace_write",
		StaleAuthority: true,
	}
	if got := operationPhaseApprovalExcludedReason(session.OperationPhasePlan{}, stalePhase); got != "superseded or stale phase" {
		t.Fatalf("operationPhaseApprovalExcludedReason(stale) = %q, want stale exclusion", got)
	}
}

func TestMaterializeMixedAuthorityPhasePlanSplitsToSingleDataApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9024, UserID: 0, Scope: telegramDMScopeRef(9024)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "mixed-authority-op",
		Objective: "Handle data intake before repo work.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "mixed-authority-plan",
			Goal: "Keep data and repo authority separate.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-private-profile",
					Summary:        "Collect approved profile preferences",
					Status:         session.PlanStatusPending,
					AuthorityClass: "private_data_intake",
					BoundedEffect:  "Process only wife-provided preferences after approval.",
				},
				{
					ID:             "phase-repo-fix",
					Summary:        "Patch the local runner",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "Edit, test, and commit the validated local slice.",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9024, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want single data approval")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.RemainingTurns != 1 || len(cont.ApprovalBundle.Phases) != 0 {
		t.Fatalf("continuation = %#v, want single phase approval without mixed bundle", cont)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess {
		t.Fatalf("lease class = %q, want data_access", cont.ContinuationLease.LeaseClass)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Approval: Collect approved profile preferences") || strings.Contains(inlineText, "Patch the local runner") {
		t.Fatalf("inline text = %q, want only first data phase surfaced", inlineText)
	}
	if strings.Contains(inlineText, "phase-private-profile") || strings.Contains(inlineText, "Use the buttons") || strings.Contains(inlineText, "Operator card:") {
		t.Fatalf("inline text = %q, want no raw ids or verbose operator card", inlineText)
	}
}

func TestMaterializeRepairsInvalidPendingMixedAuthorityBundle(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9025, UserID: 0, Scope: telegramDMScopeRef(9025)}
	opState := session.OperationState{
		ID:        "repair-invalid-bundle-op",
		Objective: "Repair invalid live continuation bundle.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "repair-invalid-bundle-plan",
			Goal: "Repair invalid approvals.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-private-profile",
					Summary:        "Collect approved profile preferences",
					Status:         session.PlanStatusPending,
					AuthorityClass: "private_data_intake",
					BoundedEffect:  "Process only wife-provided preferences after approval.",
				},
				{
					ID:             "phase-repo-fix",
					Summary:        "Patch the local runner",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "Edit, test, and commit the validated local slice.",
				},
			},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationPhaseBundle(opState, opState.PhasePlan.Phases, "continue", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9025, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want repaired invalid bundle and fresh proposal")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want fresh single data approval after repair", cont)
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 1 || sentCount != 1 {
		t.Fatalf("sender inline=%d sent=%d, want one repair notice and one fresh approval", inlineCount, sentCount)
	}
}

func TestStartupRepairRevokesInvalidPendingApprovalBundles(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9026, UserID: 0, Scope: telegramDMScopeRef(9026)}
	opState := session.OperationState{
		ID:        "startup-repair-invalid-bundle-op",
		Objective: "Repair invalid live continuation bundle during startup.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "startup-repair-invalid-bundle-plan",
			Goal: "Repair invalid approvals.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-private-profile",
					Summary:        "Collect approved profile preferences",
					Status:         session.PlanStatusPending,
					AuthorityClass: "private_data_intake",
					BoundedEffect:  "Process only wife-provided preferences after approval.",
				},
				{
					ID:             "phase-repo-fix",
					Summary:        "Patch the local runner",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_commit_then_repo_write_bounded",
					BoundedEffect:  "Edit, test, and commit the validated local slice.",
				},
			},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationPhaseBundle(opState, opState.PhasePlan.Phases, "continue", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairInvalidPendingContinuationApprovals(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("repairInvalidPendingContinuationApprovals() err = %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want 1", repaired)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusRevoked || cont.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation = %#v, want revoked invalid pending approval", cont)
	}
	opState, err = store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	for _, phase := range opState.PhasePlan.Phases {
		if strings.TrimSpace(phase.LeaseID) != "" {
			t.Fatalf("phase = %#v, want invalid lease ids cleared", phase)
		}
	}

	sender.mu.Lock()
	sentCount := len(sender.sent)
	sentText := ""
	if sentCount > 0 {
		sentText = sender.sent[0].Text
	}
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if sentCount != 1 || inlineCount != 0 || !strings.Contains(sentText, "Stopped stale approval") {
		t.Fatalf("sender sent=%d inline=%d text=%q, want concise repair notice without buttons", sentCount, inlineCount, sentText)
	}
}

func TestRenderOperationPhaseBundlePromptIsConciseAndHidesRawLeaseDetails(t *testing.T) {
	t.Parallel()

	opState := session.OperationState{
		ID:        "bundle-render-op",
		Objective: "Improve continuation cards.",
		PhasePlan: session.OperationPhasePlan{
			ID: "bundle-render-plan",
			Phases: []session.OperationPhase{
				{
					ID:               "phase-raw-internal-id-a",
					Summary:          "Inspect approval rendering",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "read_only_review",
					BoundedEffect:    "Review only continuation prompt rendering.",
					ForbiddenActions: []string{"deploy", "restart_service"},
				},
				{
					ID:             "phase-raw-internal-id-b",
					Summary:        "Patch approval rendering",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Edit local renderer and focused tests.",
				},
			},
		},
	}
	state := continuationStateFromOperationPhaseBundle(opState, opState.PhasePlan.Phases, "continue", time.Now().UTC())
	text := renderOperationProposalMaterializedPromptFallback(state)
	for _, want := range []string{"Approval: Inspect approval rendering", "Scope:", "This covers:", "Approve 2 bounded turns?"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Bundle phases:", "Operator card:", "Use the buttons", "phase-raw-internal-id", "lease-", "aprop-"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("text = %q, did not want raw/verbose fragment %q", text, notWant)
		}
	}
}

func TestMaterializeDurablePhasePlanBundleStopsBeforeHardEscalationGate(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9021, UserID: 0, Scope: telegramDMScopeRef(9021)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-bundle-stop-op",
		Objective: "Stop bundles before deploy.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "phase-bundle-stop-plan",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-1-readonly",
					Summary:        "Inspect state",
					Status:         session.PlanStatusPending,
					AuthorityClass: "read_only_review",
					BoundedEffect:  "Read only and report.",
				},
				{
					ID:             "phase-2-deploy",
					Summary:        "Deploy the runtime",
					Status:         session.PlanStatusPending,
					AuthorityClass: "deploy",
					BoundedEffect:  "Commit, restart, and smoke test.",
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9021, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want single phase approval before hard gate")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if len(cont.ApprovalBundle.Phases) != 1 || cont.ApprovalBundle.Phases[0].OperationPhaseID != "phase-1-readonly" {
		t.Fatalf("approval bundle = %#v, want only safe phase before deploy gate", cont.ApprovalBundle)
	}
	if cont.ActionProposal.RiskClass != "plan_lease" || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want one-turn plan budget before deploy gate", cont)
	}
	sender.mu.Lock()
	labels := []string(nil)
	if len(sender.inline) > 0 {
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializeDeployPhaseUsesStandaloneCommitBuildInstallRestartLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9043, UserID: 0, Scope: telegramDMScopeRef(9043)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "deploy-phase-op",
		Objective: "Ship approved approval-flow changes to the live service.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "deploy-phase-plan",
			CurrentPhaseID: "phase-deploy",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-implement",
					Summary:        "Patch and validate approval UX",
					Status:         session.PlanStatusCompleted,
					AuthorityClass: "workspace_write",
				},
				{
					ID:             "phase-deploy",
					Summary:        "Deploy the validated runtime",
					Status:         session.PlanStatusPending,
					AuthorityClass: "deploy",
					BoundedEffect:  "Commit the intended repo changes, build, install, restart aphelion, and run verify-deploy.",
					AllowedActions: []string{"git_commit_intended_changes", "make_build", "install_user_service", "restart_aphelion_service", "run_verify_deploy"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9043, SenderID: 1001, Text: "deploy it", MessageID: 1}, "deploy it", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want deploy phase approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ApprovalBundle.Active() || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want standalone one-turn deploy lease", cont)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDeployRestart {
		t.Fatalf("lease class = %q, want deploy_restart", cont.ContinuationLease.LeaseClass)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want explicit false for deploy", cont.ActionProposal.AutoApproveEligible)
	}
	for _, want := range []string{"git_commit_intended_changes", "make_build", "install_user_service", "restart_aphelion_service", "run_verify_deploy"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	for _, want := range []string{"commit_unrelated_changes", "skip_build_or_tests_before_restart", "skip_post_deploy_verification"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}
	if !strings.Contains(strings.Join(cont.ActionProposal.ValidationPlan, "\n"), "verify-deploy") {
		t.Fatalf("validation plan = %#v, want verify-deploy evidence", cont.ActionProposal.ValidationPlan)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "deploy_approval" || opState.PhasePlan.CurrentPhaseID != "phase-deploy" || opState.PhasePlan.Phases[1].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("operation state = %#v, want deploy approval stage and linked phase", opState)
	}

	sender.mu.Lock()
	labels := []string(nil)
	if len(sender.inline) > 0 {
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestMaterializePlanBudgetCanDiscloseEscalatedReadOnlyLane(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9042, UserID: 0, Scope: telegramDMScopeRef(9042)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "escalated-read-plan-op",
		Objective: "Diagnose external adapter state, then patch local reporting.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "escalated-read-plan",
			Goal: "Diagnose external adapter readiness.",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-auth-status",
					Summary:        "Check nonsecret adapter auth status",
					Status:         session.PlanStatusPending,
					AuthorityClass: "external_account_auth_status",
					GateLevel:      operationGateLevelEscalatedOperatorApproval,
					GateReasonCode: "external_account_auth_status",
					BoundedEffect:  "Inspect nonsecret adapter status only; do not read mailbox content.",
					AllowedActions: []string{"inspect_nonsecret_environment_metadata", "report_auth_validity"},
				},
				{
					ID:             "phase-local-reporting",
					Summary:        "Patch local status reporting",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Edit local status rendering and tests only.",
					AllowedActions: []string{"edit_files", "run_tests"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9042, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want disclosed plan-budget approval")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.RiskClass != "plan_lease" || len(cont.ApprovalBundle.Phases) != 2 || cont.RemainingTurns != 2 {
		t.Fatalf("continuation = %#v, want two-lane plan lease", cont)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want explicit manual approval for escalated lane", cont.ActionProposal.AutoApproveEligible)
	}
	sender.mu.Lock()
	inlineText := ""
	labels := []string(nil)
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Plan: Check nonsecret adapter auth status") || !strings.Contains(inlineText, "Step 1: Check nonsecret adapter auth status") || !strings.Contains(inlineText, "Step 2: Patch local status reporting") {
		t.Fatalf("inline text = %q, want disclosed multi-step plan", inlineText)
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestApproveBundledPhasePlanLeaseMarksOnlyCurrentPhaseInProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9022, UserID: 0, Scope: telegramDMScopeRef(9022)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "phase-bundle-approve-op",
		Objective: "Approve bundle sequentially.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID: "phase-bundle-approve-plan",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Read", Status: session.PlanStatusPending, AuthorityClass: "read_only_review", BoundedEffect: "Read only."},
				{ID: "phase-2", Summary: "Patch", Status: session.PlanStatusPending, AuthorityClass: "workspace_write", BoundedEffect: "Patch only."},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9022, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil); err != nil || !materialized {
		t.Fatalf("materialize = %v err=%v, want bundled continuation", materialized, err)
	}

	approved, err := rt.ApproveContinuation(9022, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	bundle := session.NormalizeContinuationApprovalBundle(approved.ApprovalBundle)
	if bundle.Status != session.ContinuationLeaseStatusActive || len(bundle.Phases) != 2 || bundle.Phases[0].Status != session.ContinuationLeaseStatusActive || bundle.Phases[1].Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("approved bundle = %#v, want active first phase and pending second", bundle)
	}
	if approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive || approved.RemainingTurns != 2 {
		t.Fatalf("approved continuation = %#v, want active runnable budget lease", approved)
	}
	if got := continuationWorkMode(approved); got != WorkModeReadOnly {
		t.Fatalf("continuationWorkMode() = %q, want first budget lane authority %q", got, WorkModeReadOnly)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Status != session.OperationStatusActive || got.Proposal.Status != session.ProposalStatusApproved {
		t.Fatalf("operation = %#v, want active approved bundle proposal", got)
	}
	if got.PlanLease.Status != session.PlanLeaseStatusActive {
		t.Fatalf("plan lease = %#v, want active budget while first lane runs", got.PlanLease)
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusInProgress || got.PhasePlan.Phases[1].Status != session.PlanStatusPending {
		t.Fatalf("phase plan = %#v, want only first bundled phase in_progress", got.PhasePlan)
	}
}

func TestMaterializePlanLeaseApprovalDoesNotGrantCapabilities(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9026, UserID: 0, Scope: telegramDMScopeRef(9026)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "plan-lease-op",
		Objective: "Execute a broad recovery plan without approval churn.",
		Status:    session.OperationStatusBlocked,
		Stage:     "plan_lease_proposal",
		PlanLease: session.OperationPlanLease{
			ID:             "plan-lease-broad-recovery",
			Summary:        "Approve a bounded multi-turn recovery envelope",
			Status:         session.PlanLeaseStatusProposed,
			TurnBudget:     4,
			AllowedActions: []string{"read_runtime_state", "patch_local_files"},
			Lanes: []session.OperationPlanLeaseLane{
				{ID: "review", Summary: "Review state", AuthorityClass: "read_only_review", ExpectedTurns: 1, AllowedActions: []string{"inspect_status"}},
				{ID: "patch", Summary: "Patch local code", AuthorityClass: "workspace_write", ExpectedTurns: 3, AllowedActions: []string{"edit_files"}, ForbiddenActions: []string{"deploy"}},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9026, SenderID: 1001, Text: "approve the broad plan", MessageID: 1}, "approve the broad plan", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want plan lease approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.RiskClass != "plan_lease" {
		t.Fatalf("continuation = %#v, want pending plan_lease", cont)
	}
	if cont.ActionProposal.OperationID != "plan-lease-broad-recovery" || cont.RemainingTurns != 4 {
		t.Fatalf("continuation operation/turns = %#v, want plan lease id and turn budget", cont)
	}
	for _, forbidden := range []string{"treat_plan_lease_as_capability_grant", "activate_unapproved_autonomous_work", "grant_or_revoke_capability", "deploy"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, forbidden) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, forbidden)
		}
	}
	if !strings.Contains(cont.ActionProposal.BoundedEffect, "Work inside this approved plan budget only") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "turn_budget=4") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "lane review read_only_review 1 turn") ||
		!strings.Contains(cont.ActionProposal.BoundedEffect, "lane patch workspace_write 3 turn") {
		t.Fatalf("bounded effect = %q, want compact bounded plan-budget authority", cont.ActionProposal.BoundedEffect)
	}
	sender.mu.Lock()
	labels := []string(nil)
	if len(sender.inline) > 0 {
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}
}

func TestApprovePlanLeaseMarksEnvelopeApprovedNotActive(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9027, UserID: 0, Scope: telegramDMScopeRef(9027)}
	opState := session.OperationState{
		ID:        "plan-lease-approve-op",
		Objective: "Approve a broad plan lease only.",
		Status:    session.OperationStatusBlocked,
		PlanLease: session.OperationPlanLease{
			ID:         "plan-lease-approval-only",
			Summary:    "Approve the envelope",
			Status:     session.PlanLeaseStatusProposed,
			TurnBudget: 2,
			Lanes: []session.OperationPlanLeaseLane{
				{ID: "inspect", Summary: "Inspect", AuthorityClass: "read_only_review", ExpectedTurns: 2},
			},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationPlanLease(opState, opState.PlanLease, "", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	approved, err := rt.ApproveContinuation(9027, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuation() err = %v", err)
	}
	if approved.Status != session.ContinuationStatusIdle || approved.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || approved.RemainingTurns != 0 {
		t.Fatalf("approved continuation = %#v, want consumed approval edge without runnable continuation", approved)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.PlanLease.Status != session.PlanLeaseStatusApproved || got.PlanLease.ApprovedBy != 1001 || got.PlanLease.ApprovedAt.IsZero() {
		t.Fatalf("plan lease = %#v, want approved with approver metadata", got.PlanLease)
	}
	if got.PlanLease.Status == session.PlanLeaseStatusActive || got.Status == session.OperationStatusActive {
		t.Fatalf("operation = %#v, want approved envelope but no active work", got)
	}
	if err := rt.TriggerContinuation(context.Background(), 9027); err != nil {
		t.Fatalf("TriggerContinuation() err = %v, want no-op for consumed plan lease approval", err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no extra prompt from approval-only trigger", inlineCount)
	}
}
