//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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

func TestCodexAppServerStatusPromptUsesGenericChildEnvelope(t *testing.T) {
	t.Parallel()

	prompt := codexAppServerStatusPrompt(core.DurableAgent{AgentID: "console"}, time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"## Role",
		"## Goal",
		"## Success Criteria",
		"## Stop Rules",
		"## Output",
		`"kind": "durable_child_status"`,
		`"agent_id": "console"`,
		`"schema_version": "durable_child_status.v1"`,
		`"capability_posture": "read_only"`,
		`"display_name": "console"`,
		`"machine": {`,
		`"top_processes": {`,
		`"capability_limits": {`,
		"Process entries include process names only",
		"Unsafe or unavailable fields are empty arrays or null",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
	for _, notWant := range []string{"Lighthouse", "lighthouse.status.v1"} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("prompt = %q, did not want child-specific fixture %q", prompt, notWant)
		}
	}
}

func TestCodexAppServerInstructionsUseReadOnlyContract(t *testing.T) {
	t.Parallel()

	agent := core.DurableAgent{
		AgentID:    "console",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{Charter: "Observe status only."}),
	}
	base := codexAppServerBaseInstructions(agent)
	for _, want := range []string{"## Role", "## Goal", "## Success Criteria", "## Stop Rules", "console", "return only the requested durable_child_status JSON object"} {
		if !strings.Contains(base, want) {
			t.Fatalf("base instructions missing %q:\n%s", want, base)
		}
	}
	developer := codexAppServerDeveloperInstructions(agent)
	for _, want := range []string{"## Charter", "Observe status only.", "## Boundary", "read-only status/heartbeat tasks only", "process names only"} {
		if !strings.Contains(developer, want) {
			t.Fatalf("developer instructions missing %q:\n%s", want, developer)
		}
	}
}

func TestCodexAppServerClientRaisesWebsocketReadLimit(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("x", 40*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		raw, err := json.Marshal(map[string]any{
			"id":     "aphelion-large-message-test",
			"result": map[string]any{"blob": large},
		})
		if err != nil {
			return
		}
		_ = conn.Write(context.Background(), websocket.MessageText, raw)
	}))
	defer server.Close()

	client := newCodexAppServerClient("ws://" + strings.TrimPrefix(server.URL, "http://"))
	defer client.Close(websocket.StatusNormalClosure, "done")
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() err = %v", err)
	}
	msg, err := client.readMessage(context.Background())
	if err != nil {
		t.Fatalf("readMessage() err = %v, want large websocket message accepted", err)
	}
	result := asObject(msg["result"])
	if got := stringField(result, "blob"); len(got) != len(large) {
		t.Fatalf("blob length = %d, want %d", len(got), len(large))
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
