//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func TestRuntimeAutoApprovalCommandAndDecisionResolution(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	text, err := rt.ConfigureAutoApproval(context.Background(), 99120, 1001, "15m all uses=2 test window")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	if !strings.Contains(text, "Auto-approval enabled") || !strings.Contains(text, "Scope: all") {
		t.Fatalf("ConfigureAutoApproval() text = %q, want enabled all scope", text)
	}

	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-auto",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99120,
			SenderID:      1002,
			Prompt:        "Approve this proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision() err = %v", err)
	}
	if result.Choice != "approve" || !strings.Contains(result.Reason, "auto_approved:") {
		t.Fatalf("auto resolution = %#v, want approve", result)
	}

	leases, err := store.ActiveOperatorAutoApprovalLeases(99120, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 1 {
		t.Fatalf("leases = %#v, want one active lease with one use", leases)
	}
	key := session.SessionKey{ChatID: 99120, UserID: 0, Scope: telegramDMScopeRef(99120)}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventAutoApprovalGranted)
	assertHasEventType(t, events, core.ExecutionEventAutoApprovalUsed)
}

func TestRuntimeAutoApprovalRejectsZeroUses(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutoApproval(context.Background(), 99122, 1001, "15m all uses=0")
	if err == nil || !strings.Contains(err.Error(), "invalid auto-approval max uses") {
		t.Fatalf("ConfigureAutoApproval() err = %v, want invalid max uses", err)
	}
}

func TestParseOperatorAutoApprovalDurationCapAllowsFortyEightHours(t *testing.T) {
	t.Parallel()

	action, spec, err := parseOperatorAutoApprovalCommand("48h all")
	if err != nil {
		t.Fatalf("parseOperatorAutoApprovalCommand(48h) err = %v", err)
	}
	if action != "enable" || spec.Duration != 48*time.Hour {
		t.Fatalf("action/spec = %q/%#v, want enable with 48h duration", action, spec)
	}

	_, _, err = parseOperatorAutoApprovalCommand("49h all")
	if err == nil || !strings.Contains(err.Error(), "48h0m0s") {
		t.Fatalf("parseOperatorAutoApprovalCommand(49h) err = %v, want 48h cap error", err)
	}
}

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
