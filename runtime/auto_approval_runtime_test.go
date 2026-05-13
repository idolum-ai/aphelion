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
	if !strings.Contains(text, "Auto-approval") || !strings.Contains(text, "Status: enabled") || !strings.Contains(text, "Scope: all prompts") {
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

func TestRuntimeAutoApprovalOffRendersClearedGrantAndAuditsLeaseID(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.ConfigureAutoApproval(context.Background(), 99125, 1001, "15m all live test window"); err != nil {
		t.Fatalf("ConfigureAutoApproval(enable) err = %v", err)
	}
	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-auto-off",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99125,
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
	if result.Choice != "approve" {
		t.Fatalf("auto resolution = %#v, want approve", result)
	}

	text, err := rt.ConfigureAutoApproval(context.Background(), 99125, 1001, "off")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval(off) err = %v", err)
	}
	if !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared active grant: all prompts, used 1 time.") {
		t.Fatalf("off text = %q, want human grant summary", text)
	}
	if strings.Contains(strings.ToLower(text), "lease") || strings.Contains(text, "Revoked leases") {
		t.Fatalf("off text = %q, want no operator-facing lease wording", text)
	}

	key := session.SessionKey{ChatID: 99125, UserID: 0, Scope: telegramDMScopeRef(99125)}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var revoked session.ExecutionEvent
	for _, event := range events {
		if event.EventType == core.ExecutionEventAutoApprovalRevoked {
			revoked = event
		}
	}
	if revoked.ID == 0 {
		t.Fatalf("events = %#v, want auto-approval revoked event", events)
	}
	payload := executionEventPayload(revoked.PayloadJSON)
	leaseID := payloadString(payload, "lease_id")
	if leaseID == "" {
		t.Fatalf("revoked payload = %#v, want primary lease_id for audit", payload)
	}
	ids := payloadStringSlice(payload, "revoked_lease_ids")
	if len(ids) != 1 || ids[0] != leaseID {
		t.Fatalf("revoked_lease_ids = %#v lease_id=%q, want matching audit id", ids, leaseID)
	}
	if count, ok := payloadInt64(payload, "revoked_count"); !ok || count != 1 {
		t.Fatalf("revoked_count = %d ok=%v, want 1", count, ok)
	}
	if count, ok := payloadInt64(payload, "revoked_active_count"); !ok || count != 1 {
		t.Fatalf("revoked_active_count = %d ok=%v, want 1", count, ok)
	}
}

func TestRuntimeAutoApprovalOffExplainsExpiredOldGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "auto-expired-off",
		AdminUserID: 1001,
		ChatID:      99126,
		Scope:       session.OperatorAutoApprovalScopeWorkspace,
		UsedCount:   2,
		CreatedAt:   now.Add(-2 * time.Hour),
		ExpiresAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}

	text, err := rt.ConfigureAutoApproval(context.Background(), 99126, 1001, "off")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval(off) err = %v", err)
	}
	if !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared old expired grant: workspace prompts, used 2 times.") {
		t.Fatalf("off text = %q, want expired old-grant summary", text)
	}
	if strings.Contains(strings.ToLower(text), "lease") || strings.Contains(text, "Revoked leases") {
		t.Fatalf("off text = %q, want no operator-facing lease wording", text)
	}
}

func TestRuntimeStatusSurfacesActiveAutoApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99124, 1001, "30m workspace uses=3 live test window"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(99124, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.AutoApproval == nil || !snapshot.AutoApproval.Active {
		t.Fatalf("AutoApproval = %#v, want active snapshot", snapshot.AutoApproval)
	}
	if snapshot.AutoApproval.Scope != session.OperatorAutoApprovalScopeWorkspace || snapshot.AutoApproval.MaxUses != 3 || snapshot.AutoApproval.UsedCount != 0 {
		t.Fatalf("AutoApproval = %#v, want workspace scope with 3-use budget", snapshot.AutoApproval)
	}

	diagnostics, err := rt.StatusDiagnostics(99124)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	if !strings.Contains(strings.Join(diagnostics, "\n"), "Auto-approval: active (workspace)") {
		t.Fatalf("diagnostics = %#v, want auto-approval visibility", diagnostics)
	}
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

func TestRuntimeAutoApprovalRespectsAutonomyDurationCap(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "20m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutoApproval(context.Background(), 99128, 1001, "30m all")
	if err == nil || !strings.Contains(err.Error(), "autonomy live override duration is capped at 20m0s") {
		t.Fatalf("ConfigureAutoApproval() err = %v, want autonomy duration cap", err)
	}
}

func TestRuntimeAutoApprovalRespectsAutonomyCeiling(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "ask_first"
	cfg.Autonomy.AllowLiveOverrides = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutoApproval(context.Background(), 99129, 1001, "15m all")
	if err == nil || !strings.Contains(err.Error(), "exceeds configured ceiling ask_first") {
		t.Fatalf("ConfigureAutoApproval() err = %v, want autonomy ceiling rejection", err)
	}
}

func TestRuntimeAutoApprovalExistingLeaseIsInertWhenAutonomyCeilingTightens(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "ask_first"
	cfg.Autonomy.AllowLiveOverrides = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "existing-lease-blocked-by-ceiling",
		AdminUserID: 1001,
		ChatID:      99132,
		Scope:       session.OperatorAutoApprovalScopeAll,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(30 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}

	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-blocked-by-ceiling",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99132,
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
	if result.Choice != "" {
		t.Fatalf("AutoResolveDecision() = %#v, want no auto-resolution when ceiling blocks existing lease", result)
	}
	snapshot, err := rt.ChatAutonomyStatusSnapshot(99132, 1001)
	if err != nil {
		t.Fatalf("ChatAutonomyStatusSnapshot() err = %v", err)
	}
	if snapshot.ActiveOverrideMode != "" {
		t.Fatalf("Autonomy snapshot = %#v, want existing lease hidden by ceiling", snapshot)
	}
	chatStatus, err := rt.ChatStatusSnapshot(99132, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if chatStatus.AutoApproval != nil {
		t.Fatalf("ChatStatusSnapshot.AutoApproval = %#v, want hidden inert lease", chatStatus.AutoApproval)
	}
	lease, ok, err := store.OperatorAutoApprovalLease("existing-lease-blocked-by-ceiling")
	if err != nil {
		t.Fatalf("OperatorAutoApprovalLease() err = %v", err)
	}
	if !ok || lease.UsedCount != 0 {
		t.Fatalf("existing lease = %#v ok=%v, want unused lease", lease, ok)
	}
}

func TestRuntimeAutonomyLeasedCommandCreatesBoundedOverride(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "2h"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	text, err := rt.ConfigureAutonomy(context.Background(), 99130, 1001, "leased 30m workspace uses=2 focused plan")
	if err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if !strings.Contains(text, "Autonomy override enabled") || !strings.Contains(text, "Mode: Leased") || !strings.Contains(text, "Scope: workspace prompts") {
		t.Fatalf("ConfigureAutonomy() text = %q, want leased workspace override", text)
	}
	snapshot, err := rt.ChatAutonomyStatusSnapshot(99130, 1001)
	if err != nil {
		t.Fatalf("ChatAutonomyStatusSnapshot() err = %v", err)
	}
	if snapshot.ActiveOverrideMode != "leased" || snapshot.ActiveOverrideScope != session.OperatorAutoApprovalScopeWorkspace || snapshot.ActiveOverrideMax != 2 {
		t.Fatalf("Autonomy snapshot = %#v, want active leased workspace override", snapshot)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99130, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].Scope != session.OperatorAutoApprovalScopeWorkspace || leases[0].MaxUses != 2 {
		t.Fatalf("leases = %#v, want one workspace auto-approval lease", leases)
	}
}

func TestRuntimeAutonomyOffRevokesBoundedOverride(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99131, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy(enable) err = %v", err)
	}
	text, err := rt.ConfigureAutonomy(context.Background(), 99131, 1001, "off")
	if err != nil {
		t.Fatalf("ConfigureAutonomy(off) err = %v", err)
	}
	if !strings.Contains(text, "Autonomy") || !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared") {
		t.Fatalf("ConfigureAutonomy(off) text = %q, want cleared override", text)
	}
	snapshot, err := rt.ChatAutonomyStatusSnapshot(99131, 1001)
	if err != nil {
		t.Fatalf("ChatAutonomyStatusSnapshot() err = %v", err)
	}
	if snapshot.ActiveOverrideMode != "" {
		t.Fatalf("Autonomy snapshot = %#v, want no active override", snapshot)
	}
}

func TestParseOperatorAutoApprovalDurationCapAllowsTwentyFourHours(t *testing.T) {
	t.Parallel()

	action, spec, err := parseOperatorAutoApprovalCommand("24h all")
	if err != nil {
		t.Fatalf("parseOperatorAutoApprovalCommand(24h) err = %v", err)
	}
	if action != "enable" || spec.Duration != 24*time.Hour {
		t.Fatalf("action/spec = %q/%#v, want enable with 24h duration", action, spec)
	}

	_, _, err = parseOperatorAutoApprovalCommand("25h all")
	if err == nil || !strings.Contains(err.Error(), "24h0m0s") {
		t.Fatalf("parseOperatorAutoApprovalCommand(25h) err = %v, want 24h cap error", err)
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
