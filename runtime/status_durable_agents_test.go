//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestDurableAgentsStatusSnapshotIncludesHealthSignals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agentDormant := core.DurableAgent{
		AgentID:            "agent-dormant",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-dormant",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 3,
		PolicyHash:    "hash-dormant",
	}
	if err := store.UpsertDurableAgent(agentDormant); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-dormant) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:                  "agent-dormant",
		DormantAt:                time.Now().UTC().Add(-15 * time.Minute),
		LastWakeAt:               time.Now().UTC().Add(-30 * time.Minute),
		LastReviewAt:             time.Now().UTC().Add(-25 * time.Minute),
		LastAppliedPolicyVersion: 3,
		LastAppliedPolicyAt:      time.Now().UTC().Add(-40 * time.Minute),
		LastApplyStatus:          "ok",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(agent-dormant) err = %v", err)
	}

	agentDegraded := core.DurableAgent{
		AgentID:            "agent-degraded",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-degraded",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_parent_review",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 5,
		PolicyHash:    "hash-degraded",
	}
	if err := store.UpsertDurableAgent(agentDegraded); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-degraded) err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         "agent-degraded",
		LastApplyStatus: "failed",
		LastApplyError:  "child runtime unavailable",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState(agent-degraded) err = %v", err)
	}

	agentInactive := core.DurableAgent{
		AgentID:            "agent-inactive",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		Status:             "draft",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-inactive",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"read_channel"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-inactive",
	}
	if err := store.UpsertDurableAgent(agentInactive); err != nil {
		t.Fatalf("UpsertDurableAgent(agent-inactive) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if snapshot.TotalAgents != 3 {
		t.Fatalf("TotalAgents = %d, want 3", snapshot.TotalAgents)
	}
	if snapshot.ActiveAgents != 2 {
		t.Fatalf("ActiveAgents = %d, want 2", snapshot.ActiveAgents)
	}
	if snapshot.DormantAgents != 1 {
		t.Fatalf("DormantAgents = %d, want 1", snapshot.DormantAgents)
	}
	if snapshot.DegradedAgents != 1 {
		t.Fatalf("DegradedAgents = %d, want 1", snapshot.DegradedAgents)
	}
	if snapshot.InactiveAgents != 1 {
		t.Fatalf("InactiveAgents = %d, want 1", snapshot.InactiveAgents)
	}
	if len(snapshot.Agents) != 3 {
		t.Fatalf("Agents len = %d, want 3", len(snapshot.Agents))
	}

	healthByID := map[string]string{}
	runtimeSourceByID := map[string]string{}
	identitySourceByID := map[string]string{}
	for _, agent := range snapshot.Agents {
		healthByID[agent.AgentID] = agent.Health
		runtimeSourceByID[agent.AgentID] = strings.TrimSpace(agent.RuntimePostureSource)
		identitySourceByID[agent.AgentID] = strings.TrimSpace(agent.IdentitySource)
	}
	if healthByID["agent-dormant"] != "dormant" {
		t.Fatalf("agent-dormant health = %q, want dormant", healthByID["agent-dormant"])
	}
	if healthByID["agent-degraded"] != "degraded" {
		t.Fatalf("agent-degraded health = %q, want degraded", healthByID["agent-degraded"])
	}
	if healthByID["agent-inactive"] != "inactive" {
		t.Fatalf("agent-inactive health = %q, want inactive", healthByID["agent-inactive"])
	}
	for id, source := range identitySourceByID {
		want := "canonical:session.durable_agents"
		if id == "agent-dormant" || id == "agent-degraded" {
			want = "canonical:session.durable_agents+canonical:session.durable_agent_identity_state"
		}
		if source != want {
			t.Fatalf("identity source for %s = %q, want %s", id, source, want)
		}
	}
	for id, source := range runtimeSourceByID {
		if source != "operational_current_state_store:session.durable_agent_state" {
			t.Fatalf("runtime posture source for %s = %q, want operational_current_state_store:session.durable_agent_state", id, source)
		}
	}
}

func TestDurableAgentsStatusSnapshotOverlaysPolicyFailureFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-events-failure",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-events-failure",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-events-failure",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:             agent.AgentID,
		LastApplyStatus:     "applied",
		LastAppliedPolicyAt: time.Now().UTC().Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agent.AgentID,
			DurableAgentID: agent.AgentID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurablePolicyApplyFailed,
		Stage:       "durable",
		Status:      "failed",
		PayloadJSON: `{"agent_id":"agent-events-failure","error":"child runtime unavailable"}`,
		CreatedAt:   time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(durable policy failed) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.LastApplyStatus != "failed" {
		t.Fatalf("LastApplyStatus = %q, want failed from TES overlay", row.LastApplyStatus)
	}
	if !strings.Contains(strings.ToLower(row.LastApplyError), "child runtime unavailable") {
		t.Fatalf("LastApplyError = %q, want TES error propagation", row.LastApplyError)
	}
	if row.Health != "degraded" {
		t.Fatalf("Health = %q, want degraded after TES policy failure", row.Health)
	}
	if strings.TrimSpace(row.IdentitySource) != "canonical:session.durable_agents+canonical:session.durable_agent_identity_state" {
		t.Fatalf("IdentitySource = %q, want canonical durable agent identity+registry source", row.IdentitySource)
	}
	if strings.TrimSpace(row.RuntimePostureSource) != "operational_current_state_store:session.durable_agent_state+projection:tes_execution_events" {
		t.Fatalf("RuntimePostureSource = %q, want combined operational+projection source", row.RuntimePostureSource)
	}
}

func TestDurableAgentsStatusSnapshotMarksDormantFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-events-dormant",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-events-dormant",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 1,
		PolicyHash:    "hash-events-dormant",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agent.AgentID,
			DurableAgentID: agent.AgentID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurableStateDormant,
		Stage:       "durable",
		Status:      "dormant",
		PayloadJSON: `{"agent_id":"agent-events-dormant"}`,
		CreatedAt:   time.Now().UTC().Add(-30 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(durable dormant) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.Health != "dormant" {
		t.Fatalf("Health = %q, want dormant from TES event", row.Health)
	}
	if row.DormantAt.IsZero() {
		t.Fatalf("DormantAt = %s, want non-zero from TES event", row.DormantAt.Format(time.RFC3339Nano))
	}
	if strings.TrimSpace(row.RuntimePostureSource) != "projection:tes_execution_events" {
		t.Fatalf("RuntimePostureSource = %q, want projection:tes_execution_events", row.RuntimePostureSource)
	}
}

func TestDurableAgentsStatusSnapshotKeepsCanonicalIdentityWhenOperationalStateConflicts(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "agent-identity-boundary",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-identity-boundary",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 11,
		PolicyHash:    "hash-identity-boundary",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	// This operational state status intentionally conflicts with canonical durable identity.
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         agent.AgentID,
		Status:          "inactive",
		LastApplyStatus: "failed",
		LastApplyError:  "simulated runtime fault",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("Agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.AgentID != agent.AgentID {
		t.Fatalf("AgentID = %q, want %q", row.AgentID, agent.AgentID)
	}
	if row.Status != "active" {
		t.Fatalf("Status = %q, want canonical durable_agents status active", row.Status)
	}
	if row.ChannelKind != "telegram_group" {
		t.Fatalf("ChannelKind = %q, want canonical durable_agents channel telegram_group", row.ChannelKind)
	}
	if strings.TrimSpace(row.IdentitySource) != "canonical:session.durable_agents+canonical:session.durable_agent_identity_state" {
		t.Fatalf("IdentitySource = %q, want canonical durable agent identity+registry source", row.IdentitySource)
	}
}

func TestDurableAgentsStatusSnapshotDoesNotFabricateIdentityFromTESOnlyEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	ghostID := "ghost-agent"
	key := session.SessionKey{
		ChatID: 1001,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             ghostID,
			DurableAgentID: ghostID,
		},
	}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventDurableWakeStarted,
		Stage:       "durable",
		Status:      "running",
		PayloadJSON: `{"agent_id":"ghost-agent"}`,
		CreatedAt:   time.Now().UTC().Add(-10 * time.Second),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(ghost durable wake) err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 0 {
		t.Fatalf("Agents = %#v, want no fabricated durable identity rows from TES-only events", snapshot.Agents)
	}
}

func TestDurableAgentsStatusSnapshotProjectsChildRuntimeAndProfileRepairState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	root := t.TempDir()
	memoryRoot := filepath.Join(root, "memory")
	agent := core.DurableAgent{
		AgentID:           "child-alpha",
		ChannelKind:       "external_channel",
		Status:            "active",
		PolicyHash:        "policy-hash-current",
		LocalStorageRoots: []string{filepath.Join(root, "workspace"), memoryRoot},
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexHome: filepath.Join(root, "codex")},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			TailnetMode:          "tsnet",
			TailnetHostname:      "child-alpha",
			TailnetTags:          []string{"tag:aphelion-child", "tag:alpha"},
			TailnetSurfacePolicy: "private_status",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(memoryRoot, "profile"), 0o755); err != nil {
		t.Fatalf("MkdirAll(profile) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "profile", "PROFILE.json"), []byte(`{"policy_hash":"old-policy-hash","files":[{"path":"profile/persona.md"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile(PROFILE.json) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["/srv/mail"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	snapshot, err := rt.DurableAgentsStatusSnapshot()
	if err != nil {
		t.Fatalf("DurableAgentsStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Agents) != 1 {
		t.Fatalf("agents len = %d, want 1", len(snapshot.Agents))
	}
	row := snapshot.Agents[0]
	if row.CanonicalPrincipal != core.DurableAgentPrincipal("child-alpha") {
		t.Fatalf("CanonicalPrincipal = %q, want durable agent principal", row.CanonicalPrincipal)
	}
	if row.ChildRuntimeGrantCount != 1 || row.ChildRuntimeBlockedReason != "" {
		t.Fatalf("child runtime status = count %d blocked %q, want one fresh grant", row.ChildRuntimeGrantCount, row.ChildRuntimeBlockedReason)
	}
	if row.ProfileManifestStatus != "policy_hash_mismatch" || row.ProfileManifestFileCount != 1 {
		t.Fatalf("profile manifest status = %q files=%d, want policy_hash_mismatch files=1", row.ProfileManifestStatus, row.ProfileManifestFileCount)
	}
	if !containsString(row.SubstrateLabels, "codex_home") {
		t.Fatalf("SubstrateLabels = %#v, want codex_home", row.SubstrateLabels)
	}
	if row.TailnetMode != "tsnet" || row.TailnetHostname != "child-alpha" || row.TailnetSurfacePolicy != "private_status" {
		t.Fatalf("tailnet status = %#v, want declared child tailnet identity", row)
	}
	if !containsString(row.SubstrateLabels, "tailnet:tsnet") {
		t.Fatalf("SubstrateLabels = %#v, want tailnet substrate label", row.SubstrateLabels)
	}
}
