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
	if !strings.Contains(text, "Doubled: 15m0s -> 30m0s") || !strings.Contains(text, "Use budget remaining: 2 approval(s).") {
		t.Fatalf("double text = %q, want doubled duration and remaining-use budget", text)
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
	if !strings.Contains(text, "Doubled: 20h0m0s -> 24h0m0s") {
		t.Fatalf("double text = %q, want capped 24h duration", text)
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
	if !strings.Contains(text, "Doubled: 15m0s -> 30m0s") {
		t.Fatalf("double text = %q, want 15m to 30m", text)
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
	if !strings.Contains(text, "Doubled: 30m0s -> 45m0s") {
		t.Fatalf("double text = %q, want cap at 45m", text)
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
	if !strings.Contains(text, "Approval window") || !strings.Contains(text, "Status: active") {
		t.Fatalf("EnableApprovalWindowForKey() text = %q, want active approval window", text)
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
	if !strings.Contains(text, "Doubled: 15m0s -> 30m0s") {
		t.Fatalf("DoubleApprovalWindowForKey() text = %q, want doubled duration", text)
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
	if !strings.Contains(text, "Status: off") {
		t.Fatalf("CancelApprovalWindowForKey() text = %q, want off status", text)
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
