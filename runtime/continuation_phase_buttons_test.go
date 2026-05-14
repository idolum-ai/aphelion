//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestMaterializeVisibleButtonRequestBypassesAutoApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 9034, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
