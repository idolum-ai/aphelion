//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurableAgentToolConversationShowIncludesRetryStateOnInferenceFailure(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-ant-test",
			Model:          "claude-sonnet-4-6",
			MaxTokens:      4096,
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent notes when inference is available.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Please summarize the latest intake.", time.Now().UTC().Add(-2*time.Minute))
	continuity = continuity.WithConversationMessage("child", "provider_failure: codex: server_is_overloaded", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   "child-alpha",
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_show","agent_id":"child-alpha","history":6}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_show) err = %v", err)
	}
	if !strings.Contains(showOut, "thread_state: retrying_after_inference_failure") {
		t.Fatalf("conversation_show output = %q, want retrying thread state", showOut)
	}
	if !strings.Contains(showOut, "last_child_error: provider_failure: codex: server_is_overloaded") {
		t.Fatalf("conversation_show output = %q, want surfaced child inference error", showOut)
	}
}

func TestDurableAgentToolEnrollmentShowAndUpdate(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "remote-child",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "remote_host",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Watch the host and escalate anomalies.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		BootstrapCeiling: core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:   []string{"bounded_review_artifact"},
			AllowedOutboundModes: []string{"read_only"},
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "child-key",
			Model:          "claude-test",
		},
		ControlPlaneSecret: "secret-v1",
		PolicyVersion:      1,
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "manual",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://parent.example.test/control",
		KeyFingerprint:   "keyfp-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
		EnrolledAt:       time.Unix(1710000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_show","agent_id":"remote-child"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(enrollment_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent enrollment") || !strings.Contains(showOut, "status: active") {
		t.Fatalf("enrollment show output = %q, want enrollment details", showOut)
	}

	updateOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_update","agent_id":"remote-child","operation":"rotate_secret","secret":"secret-v2"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(enrollment_update) err = %v", err)
	}
	if !strings.Contains(updateOut, "action: durable-agent enrollment") || !strings.Contains(updateOut, "status: active") {
		t.Fatalf("enrollment update output = %q, want enrollment update summary", updateOut)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.ControlPlaneSecret != "secret-v2" {
		t.Fatalf("updated control plane secret = %q, want secret-v2", updated.ControlPlaneSecret)
	}
}

func TestDurableAgentToolEnrollmentShowMissingIsExplicit(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "child-key",
			Model:          "openrouter/group-model",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "default",
		WakeupMode:        "telegram_update",
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"enrollment_show","agent_id":"family-group"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(enrollment_show missing enrollment) err = nil, want explicit missing-enrollment error")
	}
	if !strings.Contains(err.Error(), "has no remote enrollment") {
		t.Fatalf("err = %v, want explicit no-remote-enrollment guidance", err)
	}
}

func TestDurableAgentToolApprovedUserIsDenied(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{TelegramUserID: 42, Role: principal.RoleApprovedUser},
		session.SessionKey{ChatID: 42, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "42"}},
		"durable_agent",
		json.RawMessage(`{"action":"list"}`),
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(durable_agent) err = nil, want admin-only denial")
	}
	if !strings.Contains(err.Error(), "admin-only") {
		t.Fatalf("err = %v, want admin-only denial", err)
	}
}

func newToolTestStore(t *testing.T) *session.SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func TestDurableAgentConnectionTestDoesNotPromoteAdapterGrantsToLiveProbe(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "host@idolum.ai",
			Account: "host@idolum.ai",
			Adapter: "child_adapter",
			Query:   "topic:important",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Read channel metadata and surface bounded review artifacts.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		WakeupMode: "poll",
		Status:     "draft",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	principalID := core.DurableAgentPrincipal("child-alpha")
	for _, grant := range []session.CapabilityGrant{
		{
			GrantID:        "grant-channel-account",
			GrantedBy:      "telegram:1001",
			GrantedTo:      principalID,
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "child_adapter:host@idolum.ai",
			AllowedActions: []string{"read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
		},
		{
			GrantID:        "grant-channel-tool",
			GrantedBy:      "telegram:1001",
			GrantedTo:      principalID,
			Kind:           session.CapabilityKindTool,
			TargetResource: "child_adapter",
			AllowedActions: []string{"invoke", "read", "search", "metadata", "connection_test"},
			Status:         session.CapabilityGrantStatusActive,
		},
	} {
		if _, err := store.UpsertCapabilityGrant(grant); err != nil {
			t.Fatalf("UpsertCapabilityGrant(%s) err = %v", grant.GrantID, err)
		}
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"connection_test","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(connection_test) err = %v", err)
	}
	if !strings.Contains(out, "status: configuration_only") {
		t.Fatalf("connection_test output = %q, want configuration_only", out)
	}
	for _, forbidden := range []string{
		"status: ok",
		"external_account_grant: grant-channel-account",
		"tool_grant: grant-channel-tool",
		"live_probe:",
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("connection_test output = %q, should not expose adapter-specific marker %q", out, forbidden)
		}
	}
}

func newDurableAgentToolRegistry(t *testing.T) (*Registry, *session.SQLiteStore) {
	t.Helper()

	tmp := t.TempDir()
	globalRoot := filepath.Join(tmp, "global")
	store := newToolTestStore(t)
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        globalRoot,
			SharedMemoryRoot:  filepath.Join(tmp, "shared-memory"),
			UserWorkspaceRoot: filepath.Join(tmp, "users-workspace"),
			UserMemoryRoot:    filepath.Join(tmp, "users-memory"),
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	registry := NewRegistryWithSandbox(globalRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunner(t, registry)
	return registry, store
}

func adminSessionKey() session.SessionKey {
	return session.SessionKey{
		ChatID: 1001,
		UserID: 0,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"},
	}
}

func writeToolTestArchetype(t *testing.T, workspace, name string) {
	t.Helper()
	files := map[string]string{
		"AGENT.md":                  "# Aphelion Maintainer\n\nDiagnose Aphelion and propose fixes. Implementation work must happen in a /tmp clone and return as a GitHub PR.\n",
		"profile/charter.md":        "Review Aphelion sessions, memory, prompts, and code health; propose fixes with evidence.\n",
		"profile/policy.md":         "- outbound_mode: read_only\n- public_surface_mode: explicit_parent_relay_only\n- shared_inference_reuse: disabled\n- shared_inference_reuse_scope: public_prefix_only\n",
		"profile/capabilities.md":   "- session_log_read\n- repo_read\n- bounded_review_artifact\n- patch_proposal\n",
		"profile/runtime.md":        "Never mutate the local Aphelion clone. If implementation is approved, use a /tmp clone and propose the result via GitHub PR with an approved GitHub App credential.\n",
		"examples/doctor-report.md": "## State\n\nConcise diagnosis.\n",
	}
	for rel, content := range files {
		target := filepath.Join(workspace, "agents", "archetypes", name, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) err = %v", target, err)
		}
	}
}
