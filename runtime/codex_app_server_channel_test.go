//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type fakeCodexAppServerDoer struct{ result codexAppServerResult }

func (f fakeCodexAppServerDoer) Do(_ context.Context, req codexAppServerRequest) (codexAppServerResult, error) {
	out := f.result
	out.ThreadID = firstNonEmpty(out.ThreadID, "thread-1")
	out.TurnID = firstNonEmpty(out.TurnID, "turn-1")
	if len(out.EnvelopeRaw) == 0 {
		payload := json.RawMessage(`{"display_name":"Lighthouse","mode":"read_only"}`)
		hash, _ := core.DurableChildStatusPayloadHash(payload)
		out.EnvelopeRaw = []byte(`{"kind":"durable_child_status","agent_id":"` + req.Agent.AgentID + `","schema_version":"lighthouse.status.v1","generated_at":"` + req.Now.UTC().Format(time.RFC3339) + `","capability_posture":"read_only","payload":` + string(payload) + `,"payload_hash":"` + hash + `"}`)
	}
	env, err := core.ParseDurableChildStatusEnvelope(out.EnvelopeRaw)
	if err != nil {
		return codexAppServerResult{}, err
	}
	out.Envelope = env
	out.PayloadHash = env.PayloadHash
	return out, nil
}

func TestCodexAppServerWakeAdapterStoresHeartbeatAndThreadState(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "ok"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{AgentID: "console", ParentScopeKind: "telegram_dm", ParentScopeID: "1001", ReviewTargetChatID: 1001, ChannelKind: "external_channel", ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{Address: "ws://lighthouse:8390", Adapter: "codex_app_server", PollInterval: "1m"}}, LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{Charter: "Read-only status.", CapabilityEnvelope: []string{"read_only_status_surface"}, OutboundMode: "read_only", DriftPolicy: "admin_review"}), WakeupMode: "poll", Status: "active"}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	adapter := &codexAppServerWakeAdapter{doer: fakeCodexAppServerDoer{}}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	now := time.Date(2026, 4, 29, 4, 30, 0, 0, time.UTC)
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now); err != nil {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v", err)
	}
	state, err := store.DurableAgentState("console")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	cont, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cont.ExternalChannel == nil || cont.ExternalChannel.Adapter != "codex_app_server" || cont.ExternalChannel.SessionRef != "thread-1" {
		t.Fatalf("external channel state = %#v", cont.ExternalChannel)
	}
	adapterState := decodeCodexAdapterState(cont.ExternalChannel.AdapterState)
	if adapterState.ThreadID != "thread-1" || adapterState.LastTurnID != "turn-1" {
		t.Fatalf("adapter state = %#v", adapterState)
	}
	if !strings.Contains(cont.ExternalChannel.LastArtifact, "artifacts/heartbeats/codex-app-server-") {
		t.Fatalf("artifact = %q", cont.ExternalChannel.LastArtifact)
	}
}

func TestCodexAppServerCommandAllowedIsNarrow(t *testing.T) {
	if !codexAppServerCommandAllowed("hostname") || !codexAppServerCommandAllowed("ps -A -o comm= -r | head -5") {
		t.Fatal("expected read-only status command allowed")
	}
	for _, cmd := range []string{"ps aux", "cat ~/.ssh/id_rsa", "screencapture x.png", "kill 1", "open -a Mail"} {
		if codexAppServerCommandAllowed(cmd) {
			t.Fatalf("%q should be denied", cmd)
		}
	}
}

type failingCodexAppServerDoer struct {
	calls int
	err   error
}

func (f *failingCodexAppServerDoer) Do(_ context.Context, req codexAppServerRequest) (codexAppServerResult, error) {
	f.calls++
	return codexAppServerResult{ThreadID: req.ThreadID, Text: `{"not":"a valid status"}`}, f.err
}

func TestCodexAppServerWakeAdapterQuarantinesFailureAndBacksOff(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "ok"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{AgentID: "console", ParentScopeKind: "telegram_dm", ParentScopeID: "1001", ReviewTargetChatID: 1001, ChannelKind: "external_channel", ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{Address: "ws://lighthouse:8390", Adapter: "codex_app_server", PollInterval: "1m"}}, LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{Charter: "Read-only status.", CapabilityEnvelope: []string{"read_only_status_surface"}, OutboundMode: "read_only", DriftPolicy: "admin_review"}), WakeupMode: "poll", Status: "active"}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	doer := &failingCodexAppServerDoer{err: errCodexAppServerNoStatusEnvelope}
	adapter := &codexAppServerWakeAdapter{doer: doer}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	now := time.Date(2026, 4, 29, 7, 10, 0, 0, time.UTC)
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now); err != nil {
		t.Fatalf("runDurableAgentChildWakeLoaded() err = %v, want failure quarantined", err)
	}
	if doer.calls != 1 {
		t.Fatalf("doer calls = %d, want 1", doer.calls)
	}
	state, err := store.DurableAgentState("console")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	cont, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cont.ExternalChannel == nil || cont.ExternalChannel.Adapter != "codex_app_server" || cont.ExternalChannel.LastStatus != "blocked" {
		t.Fatalf("external channel state = %#v, want blocked", cont.ExternalChannel)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29 * time.Minute)) {
		t.Fatalf("backoff_until = %v, want about 30m later", cont.ExternalChannel.BackoffUntil)
	}
	if !strings.Contains(cont.ExternalChannel.LastArtifact, "failure") {
		t.Fatalf("last artifact = %q, want failure quarantine", cont.ExternalChannel.LastArtifact)
	}
	if err := rt.runDurableAgentChildWakeLoaded(context.Background(), agent, now.Add(time.Minute)); err != nil {
		t.Fatalf("second run err = %v, want backed off", err)
	}
	if doer.calls != 1 {
		t.Fatalf("doer calls after backoff = %d, want still 1", doer.calls)
	}
}
