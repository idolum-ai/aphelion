//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func TestRuntimeAutoApprovalDoubleExtendsWindowAndPreservesRemainingUses(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99201, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99201, 1001, "15m all uses=3 double window"); err != nil {
		t.Fatalf("ConfigureAutoApproval(enable) err = %v", err)
	}
	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-double-uses",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99201,
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

	text, err := rt.ConfigureAutoApproval(context.Background(), 99201, 1001, "double")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval(double) err = %v", err)
	}
	if !strings.Contains(text, "Auto-approval was extended") || !strings.Contains(text, "extended from 15m0s to 30m0s") || !strings.Contains(text, "used 0/2 approvals") {
		t.Fatalf("double text = %q, want compact doubled duration and budget", text)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99201, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].MaxUses != 2 || leases[0].UsedCount != 0 {
		t.Fatalf("leases = %#v, want fresh grant with 2 remaining uses and no used count", leases)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, 30*time.Minute)

	if _, err := rt.ConfigureAutoApproval(context.Background(), 99201, 1001, "double"); err != nil {
		t.Fatalf("ConfigureAutoApproval(double again) err = %v", err)
	}
	leases, err = store.ActiveOperatorAutoApprovalLeases(99201, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases(second) err = %v", err)
	}
	if len(leases) != 1 || leases[0].MaxUses != 2 || leases[0].UsedCount != 0 {
		t.Fatalf("leases second = %#v, want remaining use budget preserved", leases)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, time.Hour)
}

func TestRuntimeAutoApprovalDoubleCapsAtTwentyFourHours(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 99202, 1001, "20h all"); err != nil {
		t.Fatalf("ConfigureAutoApproval(enable) err = %v", err)
	}
	text, err := rt.ConfigureAutoApproval(context.Background(), 99202, 1001, "double")
	if err != nil {
		t.Fatalf("ConfigureAutoApproval(double) err = %v", err)
	}
	if !strings.Contains(text, "Auto-approval") || !strings.Contains(text, "tomorrow") {
		t.Fatalf("double text = %q, want compact capped 24h duration", text)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(99202, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %#v, want one active capped lease", leases)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, 24*time.Hour)
}

func TestRuntimeAutoApprovalDoubleRequiresActiveGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutoApproval(context.Background(), 99203, 1001, "double")
	if err == nil || !strings.Contains(err.Error(), "no active auto approval to double") {
		t.Fatalf("ConfigureAutoApproval(double) err = %v, want no-active grant error", err)
	}
}

func TestRuntimeAutoModeDoubleExtendsWindowAndRespectsCap(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.Ceiling = "leased"
	cfg.Autonomy.AllowLiveOverrides = true
	cfg.Autonomy.MaxOverrideDuration = "45m"
	cfg.Operator.DisplayTimezone = "America/New_York"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 99204, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy(enable) err = %v", err)
	}
	text, err := rt.ConfigureAutonomy(context.Background(), 99204, 1001, "double")
	if err != nil {
		t.Fatalf("ConfigureAutonomy(double) err = %v", err)
	}
	if !strings.Contains(text, "Auto mode was extended") || !strings.Contains(text, "extended from 15m0s to 30m0s") || strings.Contains(text, "UTC") {
		t.Fatalf("double text = %q, want compact 15m to 30m", text)
	}
	overrides, err := store.ActiveOperatorAutonomyOverrides(99204, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides() err = %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("overrides = %#v, want one active override", overrides)
	}
	assertOperatorWindowDuration(t, overrides[0].CreatedAt, overrides[0].ExpiresAt, 30*time.Minute)

	text, err = rt.ConfigureAutonomy(context.Background(), 99204, 1001, "double")
	if err != nil {
		t.Fatalf("ConfigureAutonomy(double capped) err = %v", err)
	}
	if !strings.Contains(text, "Auto mode was extended") || !strings.Contains(text, "extended from 30m0s to 45m0s") {
		t.Fatalf("double text = %q, want compact cap at 45m", text)
	}
	overrides, err = store.ActiveOperatorAutonomyOverrides(99204, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides(capped) err = %v", err)
	}
	if len(overrides) != 1 {
		t.Fatalf("overrides capped = %#v, want one active override", overrides)
	}
	assertOperatorWindowDuration(t, overrides[0].CreatedAt, overrides[0].ExpiresAt, 45*time.Minute)
}

func TestRuntimeAutoModeDoubleRequiresActiveOverride(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_, err = rt.ConfigureAutonomy(context.Background(), 99205, 1001, "double")
	if err == nil || !strings.Contains(err.Error(), "no active auto mode to double") {
		t.Fatalf("ConfigureAutonomy(double) err = %v, want no-active mode error", err)
	}
}

func TestApprovalWindowOfferRendersIntentSubjectAndLocalExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Operator.DisplayTimezone = "America/New_York"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 6, 1, 10, 50, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 99213, UserID: 0, Scope: telegramDMScopeRef(99213)}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-compact-window",
		StageSummary:   "Commit validated rendering updates",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-compact-window",
			Summary:       "Commit the validated compact patch",
			BoundedEffect: "Create one commit and open the PR.",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	offer, err := store.CreateApprovalWindowOffer(session.ApprovalWindowOffer{
		ID:                 "offer-compact-window",
		ChatID:             99213,
		AdminUserID:        1001,
		SessionID:          session.SessionIDForKey(key),
		ScopeKind:          string(key.Scope.Kind),
		ScopeID:            key.Scope.ID,
		SourceKind:         session.ApprovalWindowOfferSourceContinuation,
		SourceID:           "decision-compact-window",
		SourceDecisionKind: string(session.TurnAuthorizationKindContinuation),
		OpenedLeaseID:      "lease-offer",
		OpenedOverrideID:   "override-offer",
		CreatedAt:          now,
		ExpiresAt:          now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	lease := session.OperatorAutoApprovalLease{ID: "lease-offer", AdminUserID: 1001, ChatID: 99213, ScopeKind: string(key.Scope.Kind), ScopeID: key.Scope.ID, Scope: session.OperatorAutoApprovalScopeAll, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	override := session.OperatorAutonomyOverride{ID: "override-offer", ChatID: 99213, ScopeKind: string(key.Scope.Kind), ScopeID: key.Scope.ID, Mode: "leased", Scope: session.OperatorAutoApprovalScopeAll, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}

	text := rt.renderApprovalWindowEnabledForOffer(offer, lease, override, now)
	for _, want := range []string{"Approval window is active for “Commit the validated compact patch”", "new approval requests in this chat or thread until 7:05 AM."} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval window text = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Status:", "Details:", "Expires:", "UTC", "aprop-compact-window", "decision-compact-window"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("approval window text = %q, did not want scaffold/internal fragment %q", text, notWant)
		}
	}
}

func TestRuntimeApprovalWindowCreatesModeGateAndApprovalGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 99206, Scope: session.TelegramThreadScopeRef(99206, 42)}
	text, err := rt.EnableApprovalWindowForKey(context.Background(), key, 1001, 15*time.Minute)
	if err != nil {
		t.Fatalf("EnableApprovalWindowForKey() err = %v", err)
	}
	if !strings.Contains(text, "Approval window is active for new approval requests") || strings.Contains(text, "Status:") || strings.Contains(text, "Details:") {
		t.Fatalf("EnableApprovalWindowForKey() text = %q, want compact active approval window", text)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(99206, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	overrides, err := store.ActiveOperatorAutonomyOverridesForScope(99206, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(leases) != 1 || leases[0].Scope != session.OperatorAutoApprovalScopeAll || leases[0].MaxUses != 0 {
		t.Fatalf("leases = %#v, want one unlimited all-scope grant", leases)
	}
	if len(overrides) != 1 || overrides[0].Mode != "leased" || overrides[0].Scope != session.OperatorAutoApprovalScopeAll {
		t.Fatalf("overrides = %#v, want one leased all-scope gate", overrides)
	}
	result, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-approval-window-thread",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99206,
			SenderID:      1002,
			ScopeKind:     scopeKind,
			ScopeID:       scopeID,
			Prompt:        "Approve this thread proposal?",
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

	defaultResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-approval-window-default",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99206,
			SenderID:      1002,
			Prompt:        "Approve this default chat proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(default) err = %v", err)
	}
	if defaultResult.Choice != "" {
		t.Fatalf("default auto resolution = %#v, want no approval from thread window", defaultResult)
	}

	otherKind, otherID := operatorAutoThreadScope(99206, 43)
	otherThreadResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-approval-window-other-thread",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        99206,
			SenderID:      1002,
			ScopeKind:     otherKind,
			ScopeID:       otherID,
			Prompt:        "Approve this other thread proposal?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(other thread) err = %v", err)
	}
	if otherThreadResult.Choice != "" {
		t.Fatalf("other thread auto resolution = %#v, want no approval from thread 42 window", otherThreadResult)
	}
}

func TestRuntimeApprovalWindowDoubleAndCancelKeepGateAndGrantTogether(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 99207}
	if _, err := rt.EnableApprovalWindowForKey(context.Background(), key, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowForKey() err = %v", err)
	}
	text, err := rt.DoubleApprovalWindowForKey(context.Background(), key, 1001)
	if err != nil {
		t.Fatalf("DoubleApprovalWindowForKey() err = %v", err)
	}
	if !strings.Contains(text, "Approval window was extended") || !strings.Contains(text, "extended from 15m0s to 30m0s") {
		t.Fatalf("DoubleApprovalWindowForKey() text = %q, want compact doubled duration", text)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(99207, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	overrides, err := store.ActiveOperatorAutonomyOverridesForScope(99207, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(leases) != 1 || len(overrides) != 1 {
		t.Fatalf("active window records = leases:%#v overrides:%#v, want one of each", leases, overrides)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, 30*time.Minute)
	assertOperatorWindowDuration(t, overrides[0].CreatedAt, overrides[0].ExpiresAt, 30*time.Minute)

	text, err = rt.CancelApprovalWindowForKey(context.Background(), key, 1001)
	if err != nil {
		t.Fatalf("CancelApprovalWindowForKey() err = %v", err)
	}
	if !strings.Contains(text, "Approval window is off") {
		t.Fatalf("CancelApprovalWindowForKey() text = %q, want compact off status", text)
	}
	leases, err = store.ActiveOperatorAutoApprovalLeasesForScope(99207, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope(after cancel) err = %v", err)
	}
	overrides, err = store.ActiveOperatorAutonomyOverridesForScope(99207, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope(after cancel) err = %v", err)
	}
	if len(leases) != 0 || len(overrides) != 0 {
		t.Fatalf("active window records after cancel = leases:%#v overrides:%#v, want none", leases, overrides)
	}
}

func assertOperatorWindowDuration(t *testing.T, created time.Time, expires time.Time, want time.Duration) {
	t.Helper()
	got := expires.Sub(created)
	if got < want-time.Second || got > want+time.Second {
		t.Fatalf("window duration = %s, want approximately %s (created=%s expires=%s)", got, want, created, expires)
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

func TestRuntimeApprovalWindowOfferNonAdminDoesNotConsumeOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99311)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99311"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-nonadmin", string(decision.KindProposalApproval))
	if err != nil {
		t.Fatalf("CreateApprovalWindowOfferForKey() err = %v", err)
	}
	if !created || offer.ID == "" {
		t.Fatalf("offer = %#v created=%v, want persisted offer", offer, created)
	}

	text, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1002, 15*time.Minute)
	if err != nil {
		t.Fatalf("EnableApprovalWindowOffer(non-admin) err = %v", err)
	}
	if !strings.Contains(text, "admin only") {
		t.Fatalf("EnableApprovalWindowOffer(non-admin) text = %q, want admin-only response", text)
	}

	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() = %#v, %t, %v; want stored offer", stored, ok, err)
	}
	if !stored.UsedAt.IsZero() {
		t.Fatalf("offer UsedAt = %s, want zero for non-active enable response", stored.UsedAt)
	}
	if !stored.ClosedAt.IsZero() {
		t.Fatalf("offer ClosedAt = %s, want zero", stored.ClosedAt)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %#v, want no active approval leases for non-admin tap", leases)
	}
}

func TestRuntimeDefaultApprovalWindowSuppressesInitialApprovalWindowOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.DefaultApprovalWindow = "15m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99324)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99324"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-default-hidden", string(decision.KindProposalApproval))
	if err != nil {
		t.Fatalf("CreateApprovalWindowOfferForKey() err = %v", err)
	}
	if created || offer.ID != "" {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v created=%v, want no visible approval-window offer", offer, created)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	now := time.Now().UTC()
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 || leases[0].Reason != defaultApprovalWindowReason || leases[0].UsedCount != 0 {
		t.Fatalf("leases = %#v, want one unspent default baseline lease", leases)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, 15*time.Minute)
	overrides, err := store.ActiveOperatorAutonomyOverridesForScope(chatID, scopeKind, scopeID, now)
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(overrides) != 1 || overrides[0].Reason != defaultApprovalWindowReason || overrides[0].Mode != "leased" {
		t.Fatalf("overrides = %#v, want one default baseline override", overrides)
	}
	if existing, ok, err := store.ActiveApprovalWindowOfferForSource(chatID, session.ApprovalWindowOfferSourceDecision, "decision-default-hidden", now); err != nil || ok {
		t.Fatalf("ActiveApprovalWindowOfferForSource() = %#v ok=%v err=%v, want no card offer", existing, ok, err)
	}
}

func TestRuntimeDefaultApprovalWindowSuppressesExistingUnusedApprovalWindowOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.DefaultApprovalWindow = "15m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99325)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99325"}}
	now := time.Now().UTC()
	created, err := store.CreateApprovalWindowOffer(session.ApprovalWindowOffer{
		ID:                 "offer-default-hidden-existing",
		ChatID:             chatID,
		AdminUserID:        1001,
		SessionID:          session.SessionIDForKey(key),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "99325",
		SourceKind:         session.ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-default-existing",
		SourceDecisionKind: string(decision.KindProposalApproval),
		CreatedAt:          now.Add(-time.Minute),
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	offer, ok, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, created.SourceID, string(decision.KindProposalApproval))
	if err != nil {
		t.Fatalf("CreateApprovalWindowOfferForKey() err = %v", err)
	}
	if ok || offer.ID != "" {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v ok=%v, want suppressed existing unused offer", offer, ok)
	}
	stored, ok, err := store.ApprovalWindowOffer(created.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%v err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero; want unused offer closed when default baseline suppresses it")
	}
}

func TestRuntimeDefaultApprovalWindowExpiredBaselineAllowsApprovalWindowOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.DefaultApprovalWindow = "15m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99326)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99326"}}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "lease-default-expired-baseline",
		AdminUserID: 1001,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		CreatedAt:   now.Add(-30 * time.Minute),
		ExpiresAt:   now.Add(-15 * time.Minute),
		UpdatedAt:   now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease(expired) err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(session.OperatorAutonomyOverride{
		ID:          "override-default-expired-baseline",
		AdminUserID: 1001,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		CreatedAt:   now.Add(-30 * time.Minute),
		ExpiresAt:   now.Add(-15 * time.Minute),
		UpdatedAt:   now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride(expired) err = %v", err)
	}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-after-default-expired", string(decision.KindProposalApproval))
	if err != nil {
		t.Fatalf("CreateApprovalWindowOfferForKey() err = %v", err)
	}
	if !created || offer.ID == "" {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v created=%v, want visible approval-window offer after baseline expiry", offer, created)
	}
}

func TestRuntimeDefaultApprovalWindowSuppressesPostApprovalOfferAfterBaselineExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Autonomy.DefaultApprovalWindow = "15m"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99327)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99327"}}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	now := time.Now().UTC()
	if _, err := store.CreateOperatorAutoApprovalLease(session.OperatorAutoApprovalLease{
		ID:          "lease-default-expired-post-approval",
		AdminUserID: 1001,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		CreatedAt:   now.Add(-30 * time.Minute),
		ExpiresAt:   now.Add(-15 * time.Minute),
		UpdatedAt:   now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease(expired) err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(session.OperatorAutonomyOverride{
		ID:          "override-default-expired-post-approval",
		AdminUserID: 1001,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		CreatedAt:   now.Add(-30 * time.Minute),
		ExpiresAt:   now.Add(-15 * time.Minute),
		UpdatedAt:   now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride(expired) err = %v", err)
	}

	suppress, err := rt.SuppressPostApprovalDefaultWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceContinuation, "decision-post-approval", "continuation")
	if err != nil {
		t.Fatalf("SuppressPostApprovalDefaultWindowOfferForKey() err = %v", err)
	}
	if !suppress {
		t.Fatal("suppress = false, want post-approval approval-window row hidden")
	}
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 || leases[0].Reason != defaultApprovalWindowReason || leases[0].UsedCount != 0 {
		t.Fatalf("leases = %#v, want fresh unspent default approval window", leases)
	}
	assertOperatorWindowDuration(t, leases[0].CreatedAt, leases[0].ExpiresAt, 15*time.Minute)
	overrides, err := store.ActiveOperatorAutonomyOverridesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(overrides) != 1 || overrides[0].Reason != defaultApprovalWindowReason || overrides[0].Mode != "leased" {
		t.Fatalf("overrides = %#v, want fresh default autonomy override", overrides)
	}
	if offer, ok, err := store.ActiveApprovalWindowOfferForSource(chatID, session.ApprovalWindowOfferSourceContinuation, "decision-post-approval", time.Now().UTC()); err != nil || ok {
		t.Fatalf("ActiveApprovalWindowOfferForSource() = %#v ok=%v err=%v, want no visible post-approval offer", offer, ok, err)
	}
}

func TestRuntimeApprovalWindowOfferDuplicateTapDoesNotOpenSecondWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99312)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99312"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-duplicate", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("first EnableApprovalWindowOffer() err = %v", err)
	}
	if result, err := rt.EnableApprovalWindowOfferResult(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil || !result.Active {
		t.Fatalf("second EnableApprovalWindowOfferResult() = %#v, %v; want replay to return existing live window", result, err)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %#v, want exactly one live approval lease", leases)
	}
}

func TestRuntimeApprovalWindowOfferClaimWithoutLiveWindowStaysInflight(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99313)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99313"}}
	offer, err := store.CreateApprovalWindowOffer(session.ApprovalWindowOffer{
		ID:                 "offer-fresh-claimed-unopened",
		ChatID:             chatID,
		AdminUserID:        1001,
		SessionID:          session.SessionIDForKey(key),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "99313",
		SourceKind:         session.ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-stranded",
		SourceDecisionKind: string(decision.KindProposalApproval),
		CreatedAt:          time.Now().UTC().Add(-time.Second),
		ExpiresAt:          time.Now().UTC().Add(time.Hour),
		UsedAt:             time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer(claimed unopened) err = %v", err)
	}
	result, err := rt.EnableApprovalWindowOfferResult(context.Background(), offer.ID, 1001, 15*time.Minute)
	if err == nil {
		t.Fatalf("EnableApprovalWindowOfferResult() = %#v, nil; want repair error for claimed offer without live window", result)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if !stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt = %s, want claimed-unopened offer left in-flight", stored.ClosedAt)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %#v, want none", leases)
	}
}

func TestRuntimeStaleClaimedUnopenedOfferRepairsClosed(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	chatID := int64(99322)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99322"}}
	usedAt := now.Add(-approvalWindowOfferOpeningGrace - time.Minute)
	offer, err := store.CreateApprovalWindowOffer(session.ApprovalWindowOffer{
		ID:                 "offer-stale-claimed-repair",
		ChatID:             chatID,
		AdminUserID:        1001,
		SessionID:          session.SessionIDForKey(key),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "99322",
		SourceKind:         session.ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-stale-claimed-repair",
		SourceDecisionKind: string(decision.KindProposalApproval),
		CreatedAt:          usedAt.Add(-time.Second),
		ExpiresAt:          now.Add(time.Hour),
		UsedAt:             usedAt,
		UpdatedAt:          usedAt,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	if result, err := rt.EnableApprovalWindowOfferResult(context.Background(), offer.ID, 1001, 15*time.Minute); err == nil {
		t.Fatalf("EnableApprovalWindowOfferResult() = %#v, nil; want stale repair error", result)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero, want stale claimed-unopened offer closed")
	}
}

func TestRuntimeCloseStaleClaimedUnopenedOfferCloses(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	chatID := int64(99323)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99323"}}
	usedAt := now.Add(-approvalWindowOfferOpeningGrace - time.Minute)
	offer, err := store.CreateApprovalWindowOffer(session.ApprovalWindowOffer{
		ID:                 "offer-stale-claimed-close",
		ChatID:             chatID,
		AdminUserID:        1001,
		SessionID:          session.SessionIDForKey(key),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "99323",
		SourceKind:         session.ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-stale-claimed-close",
		SourceDecisionKind: string(decision.KindProposalApproval),
		CreatedAt:          usedAt.Add(-time.Second),
		ExpiresAt:          now.Add(time.Hour),
		UsedAt:             usedAt,
		UpdatedAt:          usedAt,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	if err := rt.CloseApprovalWindowOffer(context.Background(), offer.ID, 0); err != nil {
		t.Fatalf("CloseApprovalWindowOffer(internal stale claimed unopened) err = %v", err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero, want stale claimed-unopened offer closed")
	}
}

func TestRuntimeApprovalWindowOfferSourceReplayReturnsOpenedOfferForRedraw(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99314)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99314"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-redraw", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	redrawOffer, ok, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-redraw", string(decision.KindProposalApproval))
	if err != nil || !ok {
		t.Fatalf("CreateApprovalWindowOfferForKey(redraw) = %#v, %t, %v; want existing opened offer", redrawOffer, ok, err)
	}
	if redrawOffer.ID != offer.ID {
		t.Fatalf("redraw offer ID = %q, want original %q", redrawOffer.ID, offer.ID)
	}
	if redrawOffer.OpenedLeaseID == "" || redrawOffer.OpenedOverrideID == "" {
		t.Fatalf("redraw offer = %#v, want opened binding for active controls", redrawOffer)
	}
}

func TestRuntimeApprovalWindowOfferStaleUsedHandleCannotCancelLaterWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99315)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99315"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-stale-handle", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	if _, err := rt.EnableApprovalWindowForKey(context.Background(), key, 1001, 20*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowForKey(later) err = %v", err)
	}
	if result, err := rt.CancelApprovalWindowOfferResult(context.Background(), offer.ID, 1001); err == nil || result.Canceled {
		t.Fatalf("CancelApprovalWindowOfferResult(stale) = %#v, %v; want stale handle rejection", result, err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero; want stale handle closed")
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %#v, want later live window preserved", leases)
	}
}

func TestRuntimeApprovalWindowOfferStaleUsedHandleCannotDoubleLaterWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99316)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99316"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-stale-double", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	if _, err := rt.EnableApprovalWindowForKey(context.Background(), key, 1001, 20*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowForKey(later) err = %v", err)
	}
	if text, err := rt.DoubleApprovalWindowOffer(context.Background(), offer.ID, 1001); err == nil || strings.Contains(text, "extended") {
		t.Fatalf("DoubleApprovalWindowOffer(stale) = %q, %v; want stale handle rejection", text, err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero; want stale handle closed")
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %#v, want later live window preserved", leases)
	}
	remaining := leases[0].ExpiresAt.Sub(time.Now().UTC())
	if remaining > 21*time.Minute {
		t.Fatalf("remaining = %s, want later window not doubled", remaining)
	}
}

func TestRuntimeApprovalWindowOfferDoubleUpdatesExactBinding(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99317)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99317"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-double-exact", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	before, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(before) ok=%t err=%v", ok, err)
	}
	if _, err := rt.DoubleApprovalWindowOffer(context.Background(), offer.ID, 1001); err != nil {
		t.Fatalf("DoubleApprovalWindowOffer() err = %v", err)
	}
	after, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(after) ok=%t err=%v", ok, err)
	}
	if after.OpenedLeaseID == "" || after.OpenedOverrideID == "" {
		t.Fatalf("after = %#v, want rebound opened ids", after)
	}
	if after.OpenedLeaseID == before.OpenedLeaseID || after.OpenedOverrideID == before.OpenedOverrideID {
		t.Fatalf("after = %#v before = %#v, want new binding after exact double", after, before)
	}
	oldLease, ok, err := store.OperatorAutoApprovalLease(before.OpenedLeaseID)
	if err != nil || !ok {
		t.Fatalf("OperatorAutoApprovalLease(old) ok=%t err=%v", ok, err)
	}
	if oldLease.RevokedAt.IsZero() {
		t.Fatalf("old lease RevokedAt is zero; want exact old lease revoked")
	}
	newLease, ok, err := store.OperatorAutoApprovalLease(after.OpenedLeaseID)
	if err != nil || !ok {
		t.Fatalf("OperatorAutoApprovalLease(new) ok=%t err=%v", ok, err)
	}
	if !newLease.RevokedAt.IsZero() || !newLease.ActiveAt(time.Now().UTC()) {
		t.Fatalf("new lease = %#v, want active replacement", newLease)
	}
}

func TestRuntimeCloseApprovalWindowOfferRevokesBoundLiveWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99318)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99318"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-close-live", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	if err := rt.CloseApprovalWindowOffer(context.Background(), offer.ID, 1001); err != nil {
		t.Fatalf("CloseApprovalWindowOffer(opened) err = %v", err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt is zero; want closed offer")
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %#v, want bound live window revoked", leases)
	}
}

func TestRuntimeCASMissCleanupDoesNotCloseReboundOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99319)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99319"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-rebound-close", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	opened, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(opened) ok=%t err=%v", ok, err)
	}
	if _, err := rt.DoubleApprovalWindowOffer(context.Background(), offer.ID, 1001); err != nil {
		t.Fatalf("DoubleApprovalWindowOffer() err = %v", err)
	}
	if _, _, err := rt.closeOfferIfStillBound(opened, time.Now().UTC()); err != nil {
		t.Fatalf("closeOfferIfStillBound(stale opened) err = %v", err)
	}
	rebound, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(rebound) ok=%t err=%v", ok, err)
	}
	if !rebound.ClosedAt.IsZero() {
		t.Fatalf("rebound.ClosedAt = %s, want stale cleanup not to close rebound offer", rebound.ClosedAt)
	}
	if rebound.OpenedLeaseID == opened.OpenedLeaseID || rebound.OpenedOverrideID == opened.OpenedOverrideID {
		t.Fatalf("rebound = %#v opened = %#v, want new binding after double", rebound, opened)
	}
}

func TestRuntimeCloseApprovalWindowOfferNonAdminDoesNotCloseUnusedOffer(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99322)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramGroup, ID: "99322"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-close-unused-non-admin", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if err := rt.CloseApprovalWindowOffer(context.Background(), offer.ID, 2002); err == nil {
		t.Fatal("CloseApprovalWindowOffer(non-admin unused) err = nil, want admin-only")
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if !stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt = %s, want unused offer still open", stored.ClosedAt)
	}
}

func TestRuntimeCloseApprovalWindowOfferNonAdminDoesNotRevokeLiveWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99321)
	key := session.SessionKey{ChatID: chatID, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "99321"}}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), key, 1001, session.ApprovalWindowOfferSourceDecision, "decision-close-non-admin", string(decision.KindProposalApproval))
	if err != nil || !created {
		t.Fatalf("CreateApprovalWindowOfferForKey() = %#v, %t, %v; want offer", offer, created, err)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}
	if err := rt.CloseApprovalWindowOffer(context.Background(), offer.ID, 2002); err == nil {
		t.Fatal("CloseApprovalWindowOffer(non-admin) err = nil, want admin-only")
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if !stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt = %s, want offer still open", stored.ClosedAt)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(chatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("leases = %#v, want live window preserved", leases)
	}
}

func TestRuntimeApprovalWindowOfferUsesPersistedThreadScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	chatID := int64(99301)
	threadKey := session.SessionKey{ChatID: chatID, Scope: session.TelegramThreadScopeRef(chatID, 44)}
	offer, created, err := rt.CreateApprovalWindowOfferForKey(context.Background(), threadKey, 1001, session.ApprovalWindowOfferSourceDecision, "decision-44", string(decision.KindProposalApproval))
	if err != nil {
		t.Fatalf("CreateApprovalWindowOfferForKey() err = %v", err)
	}
	if !created || offer.ID == "" {
		t.Fatalf("offer = %#v created=%v, want persisted offer", offer, created)
	}
	if _, err := rt.EnableApprovalWindowOffer(context.Background(), offer.ID, 1001, 15*time.Minute); err != nil {
		t.Fatalf("EnableApprovalWindowOffer() err = %v", err)
	}

	defaultResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "offer-default-scope",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			Prompt:        "Approve default?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(default) err = %v", err)
	}
	if defaultResult.Choice != "" {
		t.Fatalf("default result = %#v, want no approval from thread offer", defaultResult)
	}

	otherKind, otherID := operatorAutoThreadScope(chatID, 45)
	otherThreadResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "offer-other-thread",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			ScopeKind:     otherKind,
			ScopeID:       otherID,
			Prompt:        "Approve other?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(other thread) err = %v", err)
	}
	if otherThreadResult.Choice != "" {
		t.Fatalf("other result = %#v, want no approval from thread 44 offer", otherThreadResult)
	}

	threadKind, threadID := operatorAutoThreadScope(chatID, 44)
	threadResult, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "offer-thread-scope",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        chatID,
			SenderID:      1002,
			ScopeKind:     threadKind,
			ScopeID:       threadID,
			Prompt:        "Approve thread?",
			Details:       "Run a bounded workspace check.",
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision(thread) err = %v", err)
	}
	if threadResult.Choice != "approve" {
		t.Fatalf("thread result = %#v, want approval from persisted thread offer scope", threadResult)
	}
}
