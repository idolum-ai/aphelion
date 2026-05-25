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
			Goal: "Let the operator approve multiple bounded stages at once.",
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

func TestMaterializeCurrentFreshPhaseDoesNotFallbackToOlderPendingBundle(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "current-fresh-phase-no-stale-bundle-op",
		Objective: "Commit the already-staged final QoL patch.",
		Status:    session.OperationStatusBlocked,
		Stage:     "approval_request",
		PhasePlan: session.OperationPhasePlan{
			ID:             "current-fresh-phase-no-stale-bundle-plan",
			Goal:           "Do not resurrect older pending phases when reoffering a current commit approval.",
			CurrentPhaseID: "phase-final-commit",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-old-design",
					Summary:        "Old read-only design review",
					Status:         session.PlanStatusPending,
					AuthorityClass: "read_only_review",
					BoundedEffect:  "Inspect only; do not edit or commit.",
				},
				{
					ID:             "phase-old-implementation",
					Summary:        "Old implementation phase",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Edit local files and run tests only; do not commit.",
				},
				{
					ID:               "phase-final-commit",
					Summary:          "Create one local commit for the staged QoL patch",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "commit",
					BoundedEffect:    "Create exactly one local git commit from staged intended files, then report the hash.",
					AllowedActions:   []string{"git_commit", "report_commit_evidence"},
					ForbiddenActions: []string{"git_push", "deploy", "restart_service", "additional_file_edits"},
					ValidationPlan:   []string{"confirm staged files", "report commit hash"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want current commit phase approval")
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ApprovalBundle.Active() || len(cont.ApprovalBundle.Phases) != 0 {
		t.Fatalf("approval bundle = %#v, want standalone current commit phase", cont.ApprovalBundle)
	}
	if cont.RemainingTurns != 1 || continuationWorkMode(cont) != WorkModeCommit {
		t.Fatalf("continuation = %#v, want one-turn commit work mode", cont)
	}
	if actionListContains(cont.ActionProposal.ForbiddenActions, "commit") || actionListContains(cont.ContinuationLease.ForbiddenActions, "commit") {
		t.Fatalf("proposal/lease forbid commit: action=%#v lease=%#v", cont.ActionProposal.ForbiddenActions, cont.ContinuationLease.ForbiddenActions)
	}

	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.PhasePlan.CurrentPhaseID != "phase-final-commit" || got.PhasePlan.Phases[2].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("phase plan = %#v, want current final commit phase linked to standalone lease", got.PhasePlan)
	}
	if got.PhasePlan.Phases[0].LeaseID != "" || got.PhasePlan.Phases[1].LeaseID != "" {
		t.Fatalf("phase plan = %#v, want older pending phases untouched by commit approval", got.PhasePlan)
	}

	sender.mu.Lock()
	inlineText := ""
	if len(sender.inline) > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if !strings.Contains(inlineText, "Create one local commit") || strings.Contains(inlineText, "Old read-only design") || strings.Contains(inlineText, "Old implementation phase") {
		t.Fatalf("inline text = %q, want only current commit approval", inlineText)
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
		Objective: "Help a resource owner with a consent-first assistant.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:   "mada-intake-plan",
			Goal: "Consent-first resource-owner intake and profile scoring.",
			Phases: []session.OperationPhase{
				{
					ID:                "phase-33-mada-intake",
					Summary:           "Consent-first resource-owner intake and profile scoring rubric.",
					Status:            session.PlanStatusPending,
					AuthorityClass:    "private_data_intake",
					WhyNow:            "Blocked: the resource owner is not available today, and no explicit opt-in has been observed. Wait for opt-in on a later turn.",
					BoundedEffect:     "Ask approved preference questions and process resource-owner CV/preferences only after onboarding/opt-in.",
					ApprovalSubject:   "third_party",
					BlockedReasonCode: "requires_opt_in",
					RequiresOptIn:     true,
				},
				{
					ID:             "phase-34-email-ranking",
					Summary:        "Later mailbox read, private-material ranking, and bounded public opportunity scouting after profile approval.",
					Status:         session.PlanStatusPending,
					AuthorityClass: "external_account_email_read_public_web_read",
					BoundedEffect:  "Read only the approved mailbox after profile approval.",
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
	if sentCount != 1 || !strings.Contains(sentText, "Plan: Consent-first resource-owner intake") || !strings.Contains(sentText, "has not opted in") || !strings.Contains(sentText, "Use /status") {
		t.Fatalf("sent text = %q, want concise blocked status", sentText)
	}
	if strings.Contains(sentText, "Approval needed") || strings.Contains(sentText, "Use the buttons") || strings.Contains(sentText, "Details: /health trace") {
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
	if _, err := rt.ConfigureAutonomy(context.Background(), 9024, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
				Summary:           "Check whether existing mailbox adapter credentials/profile can authenticate without reading mailbox contents.",
				Status:            session.PlanStatusPending,
				AuthorityClass:    "read_only_auth_status_check",
				WhyNow:            "The governor is concerned about external account state and needs explicit operator approval before touching auth status.",
				BoundedEffect:     "Run one minimal status or identity check; report nonsecret exit code and auth validity only.",
				AllowedActions:    []string{"run_external_account_auth_status_or_identity_check", "inspect_nonsecret_exit_code_and_error", "report_auth_validity"},
				ForbiddenActions:  []string{"read_or_print_secret_values", "read_mailbox_contents", "run_mailbox_adapter_query", "start_oauth_flow", "copy_restore_delete_or_write_credentials", "mutate_external_account", "edit_config", "deploy", "restart"},
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
		t.Fatalf("inline text = %q, want escalated approval card, not blocked stale approval text", inlineText)
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
	if _, err := rt.ConfigureAutonomy(context.Background(), 9029, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
				Summary:             "Run one read-only mailbox smoke for channel@example.test.",
				Status:              session.PlanStatusPending,
				AuthorityClass:      "read_only_mailbox_smoke",
				WhyNow:              "The operator needs proof that configured mailbox adapter access can query the inbox label without exposing content.",
				BoundedEffect:       "Run one configured label:inbox query with max=1; suppress contents and report only exit code and parseability.",
				AllowedActions:      []string{"run_configured_mailbox_adapter_query_once", "suppress_mailbox_contents", "report_exit_code_and_parseability"},
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
		Summary:           "Check whether existing mailbox adapter credentials/profile can authenticate without reading mailbox contents.",
		Status:            session.PlanStatusPending,
		AuthorityClass:    "read_only_auth_status_check",
		AllowedActions:    []string{"run_external_account_auth_status_or_identity_check"},
		ForbiddenActions:  []string{"read_mailbox_contents", "run_mailbox_adapter_query", "start_oauth_flow"},
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
	thirdPartyConsentPhase.ID = "phase-resource-owner-private-intake"
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
