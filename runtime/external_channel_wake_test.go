//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/runtime/codex"
)

func TestGenericExternalChannelWakeAdapterRecordsBlockedWhenChildReportsMissingMaterial(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = strings.Join([]string{
		"Adapter runtime material is missing; I need a child_runtime grant before reading the external channel.",
		`EXTERNAL_CHANNEL_OUTCOME: {"schema_version":"aphelion.external_channel_wake.v1","status":"blocked","reason_code":"grant_missing","adapter":"child_adapter","agent_id":"child-alpha","error":"child_runtime grant missing","evidence_refs":["grant://child-alpha"]}`,
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := genericExternalChannelTestAgent("child-alpha")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	rt.durableWakeChild = nil

	now := time.Date(2026, 4, 29, 15, 0, 0, 0, time.UTC)
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now); err != nil {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v", err)
	}

	cont := loadExternalChannelContinuity(t, store, "child-alpha")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want generic runtime state")
	}
	if cont.ExternalChannel.Adapter != "child_adapter" || cont.ExternalChannel.LastCommand != genericExternalChannelPollCommandName || cont.ExternalChannel.LastStatus != "wake_blocked" {
		t.Fatalf("external channel state = %#v, want generic wake_blocked state", cont.ExternalChannel)
	}
	if cont.ExternalChannel.LastSuccessAt.IsZero() == false {
		t.Fatalf("last_success_at = %v, want zero for blocked wake", cont.ExternalChannel.LastSuccessAt)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29 * time.Minute)) {
		t.Fatalf("backoff_until = %v, want failure backoff", cont.ExternalChannel.BackoffUntil)
	}
	if !strings.Contains(cont.ExternalChannel.LastError, "child_runtime grant missing") {
		t.Fatalf("last_error = %q, want child-authored blocker", cont.ExternalChannel.LastError)
	}
	if len(cont.ReviewRefs) == 0 || len(cont.RecentInteractions) == 0 {
		t.Fatalf("continuity review refs=%d recent=%d, want queued review artifact", len(cont.ReviewRefs), len(cont.RecentInteractions))
	}
	if !strings.Contains(cont.RecentInteractions[0].Summary, "child_runtime grant") {
		t.Fatalf("recent summary = %q, want child-authored blocker preserved", cont.RecentInteractions[0].Summary)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("PendingReviewEvents() empty, want blocked review artifact")
	}
	if !strings.Contains(events[0].Summary, "External-channel wake blocked") || !strings.Contains(events[0].Summary, "recorded explicit failure/backoff") {
		t.Fatalf("review summary = %q, want blocked local action", events[0].Summary)
	}
	if strings.Contains(events[0].Summary, "Dispatched a due external-channel wake") {
		t.Fatalf("review summary = %q, should not use ambiguous dispatched action", events[0].Summary)
	}

	provider.mu.Lock()
	governorSystems := append([]string(nil), provider.seenGovernorSystem...)
	provider.mu.Unlock()
	joined := strings.Join(governorSystems, "\n")
	if !strings.Contains(joined, "generic external_channel adapter dispatcher") || !strings.Contains(joined, "did not execute channel-specific work") {
		t.Fatalf("governor context = %q, want generic dispatcher boundary", joined)
	}
	if !strings.Contains(joined, "EXTERNAL_CHANNEL_OUTCOME") || !strings.Contains(joined, genericExternalChannelWakeOutcomeSchema) {
		t.Fatalf("governor context = %q, want typed completion/blocker outcome contract", joined)
	}
	for _, forbidden := range []string{"gmail", "gog", "recruiter", "job"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("governor context = %q, should not contain child-specialized term %q", joined, forbidden)
		}
	}
}

func TestGenericExternalChannelReviewArtifactGrantExpiredIsOperatorReadable(t *testing.T) {
	t.Parallel()

	agent := genericExternalChannelTestAgent("console")
	agent.ChannelConfig.External.Adapter = "codex_app_server"
	artifact := genericExternalChannelReviewArtifact(agent, "codex_app_server", "", time.Date(2026, 5, 6, 3, 14, 45, 0, time.UTC), "wake_blocked", "child_runtime_blocked: grant_expired grant_id=capg-console-codex-app-server-readonly-heartbeat-20260505T0040Z")

	if artifact.Summary != "Console wake paused: required Codex app-server heartbeat grant expired." {
		t.Fatalf("summary = %q, want operator-readable pause", artifact.Summary)
	}
	for key, want := range map[string]string{
		"operator_title":             "Console wake paused",
		"operator_status":            "paused",
		"operator_summary":           "The required Codex app-server heartbeat grant expired, so Console did not wake.",
		"operator_action":            "request_admin_grant_review",
		"operator_next_action":       "Approve renewal or replacement of the required Codex app-server heartbeat grant if Console should wake; otherwise leave it expired.",
		"admin_approval_required":    "true",
		"wake_hold_reason":           "required_grant_expired",
		"grant_scope":                "required_child_runtime",
		"child_runtime_block_reason": "grant_expired",
		"grant_id":                   "capg-console-codex-app-server-readonly-heartbeat-20260505T0040Z",
		"grant_label":                "Codex app-server heartbeat grant",
	} {
		if got := artifact.Metadata[key]; got != want {
			t.Fatalf("metadata[%s] = %q, want %q", key, got, want)
		}
	}
	if strings.Contains(artifact.Summary, "wake wake_blocked") || strings.Contains(artifact.Summary, "child_runtime_blocked") || strings.Contains(artifact.Summary, "capg-console") {
		t.Fatalf("summary = %q, want no raw runtime detail", artifact.Summary)
	}
}

func TestGenericExternalChannelWakeAdapterRecordsSuccessOnlyWhenChildReportsCompleted(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = strings.Join([]string{
		"Adapter-local read-only poll completed; no relevant items found.",
		`EXTERNAL_CHANNEL_OUTCOME: {"schema_version":"aphelion.external_channel_wake.v1","status":"completed","reason_code":"poll_completed","adapter":"child_adapter","agent_id":"child-success","evidence_refs":["conversation://durable-agent/child-success"]}`,
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := genericExternalChannelTestAgent("child-success")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	rt.durableWakeChild = nil

	now := time.Date(2026, 4, 29, 15, 5, 0, 0, time.UTC)
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now); err != nil {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v", err)
	}

	cont := loadExternalChannelContinuity(t, store, "child-success")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want generic runtime state")
	}
	if cont.ExternalChannel.LastStatus != "wake_completed" {
		t.Fatalf("last_status = %q, want wake_completed", cont.ExternalChannel.LastStatus)
	}
	if cont.ExternalChannel.LastSuccessAt.IsZero() || cont.ExternalChannel.LastAttemptAt.IsZero() {
		t.Fatalf("external channel timestamps = attempt %v success %v, want both set", cont.ExternalChannel.LastAttemptAt, cont.ExternalChannel.LastSuccessAt)
	}
	if !cont.ExternalChannel.BackoffUntil.IsZero() || cont.ExternalChannel.FailureCount != 0 || strings.TrimSpace(cont.ExternalChannel.LastError) != "" {
		t.Fatalf("external channel failure fields = backoff %v failures %d err %q, want cleared", cont.ExternalChannel.BackoffUntil, cont.ExternalChannel.FailureCount, cont.ExternalChannel.LastError)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("PendingReviewEvents() empty, want success review artifact")
	}
	if !strings.Contains(events[0].Summary, "External-channel wake completed") || !strings.Contains(events[0].Summary, "reported authorized adapter-local work completed") {
		t.Fatalf("review summary = %q, want completion local action", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, `"wake_outcome_source":"typed_outcome"`) {
		t.Fatalf("metadata = %q, want typed wake outcome source", events[0].MetadataJSON)
	}
}

func TestGenericExternalChannelWakeOutcomePrefersTypedContract(t *testing.T) {
	t.Parallel()

	outcome := genericExternalChannelWakeOutcomeFromSummary(strings.Join([]string{
		"Adapter blocked before touching the external channel.",
		`EXTERNAL_CHANNEL_OUTCOME: {"schema_version":"aphelion.external_channel_wake.v1","status":"blocked","reason_code":"grant_missing","adapter":"child_adapter","agent_id":"child-alpha","grant_id":"capg-child-alpha","error":"child runtime grant missing","evidence_refs":["grant://capg-child-alpha"]}`,
		"ordinary prose says completed but has no authority",
	}, "\n"))

	if outcome.Completed || outcome.Status != "wake_blocked" || outcome.Source != "typed_outcome" {
		t.Fatalf("outcome = %#v, want typed blocked outcome to override prose line", outcome)
	}
	if outcome.ReasonCode != "grant_missing" || outcome.GrantID != "capg-child-alpha" || len(outcome.EvidenceRefs) != 1 {
		t.Fatalf("outcome = %#v, want typed reason, grant, and evidence refs", outcome)
	}
}

func TestGenericExternalChannelWakeAdapterInferenceUnavailableRecordsBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = durableWakeInferenceUnavailableFallback
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := genericExternalChannelTestAgent("child-inference")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	rt.durableWakeChild = nil

	now := time.Date(2026, 4, 29, 15, 6, 0, 0, time.UTC)
	err = rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now)
	if err == nil || !strings.Contains(err.Error(), "durable wake inference unavailable") {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v, want inference unavailable", err)
	}

	cont := loadExternalChannelContinuity(t, store, "child-inference")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want generic runtime state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" {
		t.Fatalf("last_status = %q, want wake_blocked", cont.ExternalChannel.LastStatus)
	}
	if !cont.ExternalChannel.LastSuccessAt.IsZero() {
		t.Fatalf("last_success_at = %v, want zero when inference unavailable", cont.ExternalChannel.LastSuccessAt)
	}
	if !strings.Contains(cont.ExternalChannel.LastError, "durable wake inference unavailable") {
		t.Fatalf("last_error = %q, want inference unavailable preserved", cont.ExternalChannel.LastError)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29*time.Minute)) || cont.ExternalChannel.FailureCount != 1 {
		t.Fatalf("backoff/failures = %v/%d, want first failure backoff", cont.ExternalChannel.BackoffUntil, cont.ExternalChannel.FailureCount)
	}
}

func TestGenericExternalChannelWakeAdapterHardTurnErrorRecordsBlocked(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = "this should not complete"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := genericExternalChannelTestAgent("child-hard-error")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	rt.durableWakeChild = nil

	now := time.Date(2026, 4, 29, 15, 7, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = rt.runDurableAgentChildWakeLoaded(ctx, agent, now)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v, want hard context cancellation", err)
	}

	cont := loadExternalChannelContinuity(t, store, "child-hard-error")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want generic runtime state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" {
		t.Fatalf("last_status = %q, want wake_blocked", cont.ExternalChannel.LastStatus)
	}
	if !strings.Contains(cont.ExternalChannel.LastError, "context canceled") {
		t.Fatalf("last_error = %q, want hard turn error preserved", cont.ExternalChannel.LastError)
	}
	if !cont.ExternalChannel.LastSuccessAt.IsZero() {
		t.Fatalf("last_success_at = %v, want zero on hard turn error", cont.ExternalChannel.LastSuccessAt)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29*time.Minute)) || cont.ExternalChannel.FailureCount != 1 {
		t.Fatalf("backoff/failures = %v/%d, want first failure backoff", cont.ExternalChannel.BackoffUntil, cont.ExternalChannel.FailureCount)
	}
}

func TestGenericExternalChannelWakeAdapterPollCadenceAndSupport(t *testing.T) {
	t.Parallel()

	adapter := newGenericExternalChannelWakeAdapter()
	if !adapter.Supports(core.DurableAgent{Status: "active", WakeupMode: "poll", ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{Adapter: "child_adapter"}}}) {
		t.Fatal("Supports(generic external adapter) = false, want true")
	}
	if adapter.Supports(core.DurableAgent{Status: "active", WakeupMode: "poll", ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{Adapter: codex.AdapterName}}}) {
		t.Fatal("Supports(codex_app_server) = true, want specialized codex adapter to own it")
	}
	if adapter.Supports(core.DurableAgent{Status: "active", WakeupMode: "push", ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{Adapter: "child_adapter"}}}) {
		t.Fatal("Supports(push) = true, want poll-only generic adapter for now")
	}

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = strings.Join([]string{
		"No channel work performed; runtime material missing.",
		`EXTERNAL_CHANNEL_OUTCOME: {"schema_version":"aphelion.external_channel_wake.v1","status":"blocked","reason_code":"missing_grant","adapter":"child_adapter","agent_id":"child-beta","error":"runtime material missing","evidence_refs":[]}`,
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := genericExternalChannelTestAgent("child-beta")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = nil
	now := time.Date(2026, 4, 29, 15, 10, 0, 0, time.UTC)
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now); err != nil {
		t.Fatalf("first run err = %v", err)
	}
	provider.mu.Lock()
	callsAfterFirst := provider.callCount
	provider.mu.Unlock()
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("second run err = %v, want skipped before interval/backoff", err)
	}
	provider.mu.Lock()
	callsAfterSecond := provider.callCount
	provider.mu.Unlock()
	if callsAfterSecond != callsAfterFirst {
		t.Fatalf("provider calls after backed-off cadence = %d, want %d", callsAfterSecond, callsAfterFirst)
	}
}

func genericExternalChannelTestAgent(agentID string) core.DurableAgent {
	return core.DurableAgent{
		AgentID:            agentID,
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "child-endpoint",
			Adapter:      "child_adapter",
			Query:        "inbox selector",
			PollInterval: "30m",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle the external channel without parent-specific semantics.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		WakeupMode: "poll",
		Status:     "active",
	}
}

func loadExternalChannelContinuity(t *testing.T, store durableWakeTestStore, agentID string) core.DurableAgentContinuityState {
	t.Helper()
	state, err := store.DurableAgentState(agentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	cont, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	return cont
}

type durableWakeTestStore interface {
	DurableAgentState(agentID string) (*core.DurableAgentState, error)
}
