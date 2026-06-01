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
					BoundedEffect:  "Process only resource-owner preferences after approval.",
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

func TestStartupRepairRevokesStaleContinuationDerivedOrganicProposal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9039, UserID: 0, Scope: telegramDMScopeRef(9039)}
	now := time.Now().UTC()
	opState := session.OperationState{
		ID:        "organic-proposal-stale-readme",
		Objective: "review the readme of Aphelion. Do you consider it speaks in your voice?",
		Status:    session.OperationStatusBlocked,
		Stage:     "organic_proposal",
		Summary:   "Organic proposal inferred one bounded next-step proposal from ordinary conversation.",
		Proposal: session.OperationProposal{
			ID:            "organic-proposal-stale-readme",
			Kind:          "read_only_review",
			Summary:       "review the readme of Aphelion. Do you consider it speaks in your voice?",
			BoundedEffect: "Work only on the stale README review.",
			Status:        session.ProposalStatusPending,
			UpdatedAt:     now,
		},
		Findings: []session.OperationFinding{{
			Claim:      "Organic proposal inferred exactly one high-confidence bounded next lease from ordinary conversation.",
			Confidence: session.FindingConfidenceHigh,
			Basis:      "Persisted continuation state carried a concrete next step; no explicit face contract was required.",
		}},
		UpdatedAt: now,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := continuationStateFromOperationProposal(opState, "draft readme", now)
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairInvalidPendingContinuationApprovals(context.Background(), now)
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
	if cont.Status != session.ContinuationStatusRevoked || cont.ActionProposal.Status != session.ProposalStatusSuperseded {
		t.Fatalf("continuation = %#v, want revoked stale approval", cont)
	}
	opState, err = store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Proposal.Status != session.ProposalStatusSuperseded || opState.Status != session.OperationStatusIdle {
		t.Fatalf("operation state = %#v, want superseded idle stale proposal", opState)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var adjudicated session.ExecutionEvent
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationAdjudicated {
			adjudicated = event
		}
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered {
			t.Fatalf("events = %#v, want no reoffered stale continuation", events)
		}
	}
	if adjudicated.ID == 0 || !strings.Contains(adjudicated.PayloadJSON, "stale_continuation_projection") {
		t.Fatalf("adjudicated event = %#v payload=%q, want stale projection repair", adjudicated, adjudicated.PayloadJSON)
	}
	sender.mu.Lock()
	sentCount := len(sender.sent)
	sentText := ""
	if sentCount > 0 {
		sentText = sender.sent[0].Text
	}
	sender.mu.Unlock()
	if sentCount != 1 || !strings.Contains(sentText, "Stopped stale approval") {
		t.Fatalf("sent count/text = %d/%q, want concise stale repair notice", sentCount, sentText)
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
	for _, want := range []string{"Approve “Inspect approval rendering” for 2 turns", "Review only continuation prompt rendering", "Covers phase 1: Inspect approval rendering", "phase 2: Patch approval rendering"} {
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
					BoundedEffect:  "Commit the intended repo changes, build, install, restart the user service, and run verify-deploy.",
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
	if !strings.Contains(inlineText, "Approve plan for “Check nonsecret adapter auth status”") || !strings.Contains(inlineText, "Step 1: Check nonsecret adapter auth status") || !strings.Contains(inlineText, "Step 2: Patch local status reporting") {
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
