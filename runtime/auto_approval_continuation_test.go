//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestAutoApprovedContinuationTriggerFailureIsRecordedAndReported(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	execErr := errors.New("work executor failed after auto approval")
	rt.workExecutor = newWorkExecutorSelector(cfg.Work, []WorkExecutor{&fakeWorkExecutor{name: "native", ready: true, err: execErr}})

	key := session.SessionKey{ChatID: 99123, UserID: 0, Scope: telegramDMScopeRef(99123)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision-auto-trigger-fail",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		StageSummary:   "Run the auto-approved workspace step.",
		ActionProposal: session.ActionProposal{
			ID:             "aprop-auto-trigger-fail",
			RiskClass:      "workspace_write",
			Summary:        "Run the auto-approved workspace step.",
			AllowedActions: []string{"workspace_write"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      now.Add(30 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-auto-trigger-fail",
			ProposalID:     "aprop-auto-trigger-fail",
			Status:         session.ContinuationLeaseStatusActive,
			AllowedActions: []string{"workspace_write"},
			ApprovedBy:     1001,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedAt:     now,
			ExpiresAt:      now.Add(30 * time.Minute),
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	lease := session.OperatorAutoApprovalLease{ID: "auto-trigger-fail", AdminUserID: 1001, ChatID: 99123}

	rt.triggerAutoApprovedContinuation(context.Background(), key, state, lease)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var found bool
	for _, event := range events {
		if event.EventType == core.ExecutionEventContinuationBlocked && event.Stage == "auto_approval" && event.Status == "trigger_failed" && strings.Contains(event.PayloadJSON, "auto_approval_trigger_failed") && strings.Contains(event.PayloadJSON, "auto-trigger-fail") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want auto-approval trigger failure event", events)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) == 0 || !strings.Contains(sender.sent[len(sender.sent)-1].Text, "Auto-approved continuation failed") {
		t.Fatalf("sent = %#v, want failure report message", sender.sent)
	}
}

func TestRuntimeAutoApprovesPendingPlanLeaseContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99121, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99121, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 99121, UserID: 0, Scope: telegramDMScopeRef(99121)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-plan",
		RemainingTurns: 1,
		StageSummary:   "Approve the bounded plan lease.",
		ActionProposal: session.ActionProposal{
			ID:             "aprop-plan",
			RiskClass:      "plan_lease",
			Summary:        "Approve the bounded plan lease.",
			AllowedActions: []string{"approve_operation_plan_lease"},
			Status:         session.ProposalStatusPending,
			ExpiresAt:      now.Add(30 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-plan",
			ProposalID:       "aprop-plan",
			Status:           session.ContinuationLeaseStatusPending,
			AllowedActions:   []string{"approve_operation_plan_lease"},
			MaxTurns:         1,
			RemainingTurns:   1,
			ExpiresAt:        now.Add(30 * time.Minute),
			LeaseClass:       session.ContinuationLeaseClassCapabilityGrant,
			ValidationPlan:   []string{"record approval"},
			ForbiddenActions: []string{"deploy"},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	approved, err := rt.maybeAutoApproveContinuationOffer(context.Background(), key, core.InboundMessage{ChatID: 99121}, state, "test_plan_lease")
	if err != nil {
		t.Fatalf("maybeAutoApproveContinuationOffer() err = %v", err)
	}
	if !approved {
		t.Fatal("maybeAutoApproveContinuationOffer() approved = false, want true")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.Status != session.ProposalStatusApproved || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation state = %#v, want approved consumed plan lease", got)
	}
}

func TestRuntimeAutoApprovalSkipsManualOnlyContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99127, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 99127, UserID: 0, Scope: telegramDMScopeRef(99127)}
	now := time.Now().UTC()
	manualOnly := false
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-manual-only",
		RemainingTurns: 1,
		StageSummary:   "Check auth status only.",
		ActionProposal: session.ActionProposal{
			ID:                  "aprop-manual-only",
			RiskClass:           "external_account_auth_status",
			Summary:             "Check auth status only.",
			BoundedEffect:       "Run one nonsecret auth status check.",
			AutoApproveEligible: &manualOnly,
			Status:              session.ProposalStatusPending,
			ExpiresAt:           now.Add(30 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-manual-only",
			ProposalID:     "aprop-manual-only",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(30 * time.Minute),
			LeaseClass:     session.ContinuationLeaseClassDataAccess,
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	approved, err := rt.maybeAutoApproveContinuationOffer(context.Background(), key, core.InboundMessage{ChatID: 99127}, state, "manual_only")
	if err != nil {
		t.Fatalf("maybeAutoApproveContinuationOffer() err = %v", err)
	}
	if approved {
		t.Fatal("maybeAutoApproveContinuationOffer() approved = true, want manual-only skip")
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99127, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want one unused lease", leases)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusPending || got.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("continuation state = %#v, want still pending", got)
	}
}

func TestRuntimeAutoApprovalSkipsRequiredCapabilityGrantContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99128, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 99128, UserID: 0, Scope: telegramDMScopeRef(99128)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-required-grant-auto-skip",
		RemainingTurns: 1,
		StageSummary:   "Update PR metadata.",
		ActionProposal: session.ActionProposal{
			ID:             "aprop-required-grant-auto-skip",
			RiskClass:      "external_account_action",
			Summary:        "Update PR metadata.",
			AllowedActions: []string{"update_pull_request_title"},
			Status:         session.ProposalStatusPending,
			ExpiresAt:      now.Add(30 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-required-grant-auto-skip",
			ProposalID:     "aprop-required-grant-auto-skip",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(30 * time.Minute),
			LeaseClass:     session.ContinuationLeaseClassCapabilityGrant,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-required-grant-auto-skip",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-auto-skip",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"write"},
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	approved, err := rt.maybeAutoApproveContinuationOffer(context.Background(), key, core.InboundMessage{ChatID: 99128}, state, "required_grant")
	if err != nil {
		t.Fatalf("maybeAutoApproveContinuationOffer() err = %v", err)
	}
	if approved {
		t.Fatal("maybeAutoApproveContinuationOffer() approved = true, want required capability grant to stay manual")
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99128, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want one unused lease", leases)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-auto-skip", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant() ok=%t err=%v, want no grant", ok, err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusPending || got.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("continuation state = %#v, want still pending", got)
	}
}
