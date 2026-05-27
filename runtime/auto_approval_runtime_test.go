//go:build linux

package runtime

import (
	"context"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestRuntimeAutoApprovalCommandAndDecisionResolution(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99120, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}

	text, err := rt.ConfigureAutoApproval(context.Background(), 99120, 1001, "15m all uses=2 test window")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	if !strings.Contains(text, "Auto approvals") || !strings.Contains(text, "Status: enabled") || !strings.Contains(text, "Scope: all prompts") {
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

func TestRuntimeAutoApprovalGrantAloneDoesNotResolve(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99133, 1001, "15m all uses=2 grant only"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}

	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-grant-only",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99133,
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
		t.Fatalf("auto resolution = %#v, want no approval without auto mode", result)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99133, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("leases = %#v, want unspent grant", leases)
	}
}

func TestRuntimeAutoModeAloneDoesNotResolve(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99134, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}

	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-mode-only",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99134,
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
		t.Fatalf("auto resolution = %#v, want no approval without grant", result)
	}
}

func TestRuntimeAutoModeScopeBlocksMismatchedGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99135, 1001, "leased 15m workspace"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99135, 1001, "15m deploy uses=1"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}

	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-scope-block",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99135,
			SenderID:      1002,
			Prompt:        "Approve this proposal?",
			Details:       "Restart the local service.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision() err = %v", err)
	}
	if result.Choice != "" {
		t.Fatalf("auto resolution = %#v, want no approval when mode scope blocks deploy", result)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99135, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("leases = %#v, want unspent deploy grant", leases)
	}
}

func TestRuntimeAutoApprovalOffRendersClearedGrantAndAuditsLeaseID(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.ConfigureAutonomy(context.Background(), 99125, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
	if !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared active approval window: all prompts, used 1 time.") {
		t.Fatalf("off text = %q, want human approval-window summary", text)
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
	if !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared old expired approval window: workspace prompts, used 2 times.") {
		t.Fatalf("off text = %q, want expired old approval-window summary", text)
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
	if _, err := rt.ConfigureAutonomy(context.Background(), 99124, 1001, "leased 30m workspace"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
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
	if !strings.Contains(strings.Join(diagnostics, "\n"), "Auto approvals: active (workspace)") {
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

func TestRuntimeAutoModeRespectsAutonomyDurationCap(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "20m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutonomy(context.Background(), 99128, 1001, "leased 30m all")
	if err == nil || !strings.Contains(err.Error(), "autonomy live override duration is capped at 20m0s") {
		t.Fatalf("ConfigureAutonomy() err = %v, want autonomy duration cap", err)
	}
}

func TestRuntimeAutoModeRespectsAutonomyCeiling(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "ask_first"
	cfg.Autonomy.AllowLiveOverrides = true
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutonomy(context.Background(), 99129, 1001, "leased 15m all")
	if err == nil || !strings.Contains(err.Error(), "exceeds configured ceiling ask_first") {
		t.Fatalf("ConfigureAutonomy() err = %v, want autonomy ceiling rejection", err)
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
	if _, err := store.CreateOperatorAutonomyOverride(session.OperatorAutonomyOverride{
		ID:          "existing-mode-blocked-by-ceiling",
		AdminUserID: 1001,
		ChatID:      99132,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		CreatedAt:   now.Add(-time.Minute),
		ExpiresAt:   now.Add(30 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride() err = %v", err)
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
		if chatStatus.AutoApproval.Usable || !strings.Contains(chatStatus.AutoApproval.BlockedReason, "open an approval window") {
			t.Fatalf("ChatStatusSnapshot.AutoApproval = %#v, want blocked visible grant", chatStatus.AutoApproval)
		}
	} else {
		t.Fatalf("ChatStatusSnapshot.AutoApproval = nil, want blocked visible grant")
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

	text, err := rt.ConfigureAutonomy(context.Background(), 99130, 1001, "leased 30m workspace focused plan")
	if err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if !strings.Contains(text, "Auto mode") || !strings.Contains(text, "Mode: Leased") || !strings.Contains(text, "Scope: workspace prompts") {
		t.Fatalf("ConfigureAutonomy() text = %q, want leased workspace override", text)
	}
	snapshot, err := rt.ChatAutonomyStatusSnapshot(99130, 1001)
	if err != nil {
		t.Fatalf("ChatAutonomyStatusSnapshot() err = %v", err)
	}
	if snapshot.ActiveOverrideMode != "leased" || snapshot.ActiveOverrideScope != session.OperatorAutoApprovalScopeWorkspace {
		t.Fatalf("Autonomy snapshot = %#v, want active leased workspace override", snapshot)
	}
	overrides, err := store.ActiveOperatorAutonomyOverrides(99130, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides() err = %v", err)
	}
	if len(overrides) != 1 || overrides[0].Scope != session.OperatorAutoApprovalScopeWorkspace {
		t.Fatalf("overrides = %#v, want one workspace auto mode override", overrides)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99130, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %#v, want no auto-approval grants from mode command", leases)
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
	if !strings.Contains(text, "Auto mode") || !strings.Contains(text, "Status: off") || !strings.Contains(text, "Cleared") {
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

func TestRuntimeAutoModeAndApprovalsOffAreIndependent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99136, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99136, 1001, "15m all uses=2"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}

	if _, err := rt.ConfigureAutonomy(context.Background(), 99136, 1001, "off"); err != nil {
		t.Fatalf("ConfigureAutonomy(off) err = %v", err)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99136, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("auto approval leases = %#v, want grant left active after mode off", leases)
	}

	if _, err := rt.ConfigureAutonomy(context.Background(), 99136, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy(reopen) err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99136, 1001, "off"); err != nil {
		t.Fatalf("ConfigureAutoApproval(off) err = %v", err)
	}
	overrides, err := store.ActiveOperatorAutonomyOverrides(99136, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides() err = %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("auto mode overrides = %#v, want mode left active after approvals off", overrides)
	}
}

func TestRuntimeAutoApprovalThreadScopeDoesNotLeakAcrossScopes(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99160)
	threadKey := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramThreadScopeRef(chatID, 4)}
	threadKind, threadID := operatorAutoThreadScope(chatID, 4)
	otherThreadKind, otherThreadID := operatorAutoThreadScope(chatID, 5)

	if _, err := rt.ConfigureAutonomyForKey(context.Background(), threadKey, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomyForKey(thread) err = %v", err)
	}
	if _, err := rt.ConfigureAutoApprovalForKey(context.Background(), threadKey, 1001, "15m all uses=2 thread window"); err != nil {
		t.Fatalf("ConfigureAutoApprovalForKey(thread) err = %v", err)
	}

	defaultResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-default-scope",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			Prompt:        "Approve default chat proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(default) err = %v", err)
	}
	if defaultResult.Choice != "" {
		t.Fatalf("default result = %#v, want no approval from thread-scoped grant", defaultResult)
	}

	otherThreadResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-other-thread",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			ScopeKind:     otherThreadKind,
			ScopeID:       otherThreadID,
			Prompt:        "Approve other thread proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(other thread) err = %v", err)
	}
	if otherThreadResult.Choice != "" {
		t.Fatalf("other thread result = %#v, want no approval from thread 4 grant", otherThreadResult)
	}

	threadResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-thread-scope",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			ScopeKind:     threadKind,
			ScopeID:       threadID,
			Prompt:        "Approve thread proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(thread) err = %v", err)
	}
	if threadResult.Choice != "approve" || !strings.Contains(threadResult.Reason, "auto_approved:") {
		t.Fatalf("thread result = %#v, want scoped approval", threadResult)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, threadKind, threadID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope(thread) err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 1 {
		t.Fatalf("thread leases = %#v, want one spent lease", leases)
	}
}
