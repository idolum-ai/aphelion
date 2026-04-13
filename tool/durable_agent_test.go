//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDefinitionsIncludeDurableAgentToolWhenStoreConfigured(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(t.TempDir(), time.Second)
	names := make([]string, 0, len(registry.Definitions()))
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if containsString(names, "durable_agent") {
		t.Fatalf("definitions without store = %#v, do not want durable_agent", names)
	}

	store := newToolTestStore(t)
	registry = NewRegistry(t.TempDir(), time.Second).WithSessionStore(store)
	names = names[:0]
	for _, def := range registry.Definitions() {
		names = append(names, def.Name)
	}
	if !containsString(names, "durable_agent") {
		t.Fatalf("definitions with store = %#v, want durable_agent", names)
	}
}

func TestDurableAgentToolListAndPolicyShow(t *testing.T) {
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

	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"list"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(list) err = %v", err)
	}
	if !strings.Contains(listOut, "[DURABLE_AGENTS]") || !strings.Contains(listOut, "family-group") {
		t.Fatalf("list output = %q, want durable agent summary", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"policy_show","agent_id":"family-group"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent policy show") || !strings.Contains(showOut, "outbound_mode: reply_with_policy_authorization") {
		t.Fatalf("policy show output = %q, want policy details", showOut)
	}
}

func TestDurableAgentToolPolicyApplyUsesReviewEventProvenance(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := core.DurableAgent{
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
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	reviewID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole: "durable_agent",
		SourceScope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             "family-group",
			DurableAgentID: "family-group",
		},
		TargetAdminChatID: 1001,
		TargetScope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   "1001",
		},
		Summary: "family-group requested tighter reply control",
		Status:  "pending",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(fmt.Sprintf(`{"action":"policy_apply","agent_id":"family-group","review_event_id":%d,"outbound_mode":"read_only"}`, reviewID)),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(policy_apply) err = %v", err)
	}
	if !strings.Contains(out, "changed: true") || !strings.Contains(out, fmt.Sprintf("source_review_event_id: %d", reviewID)) {
		t.Fatalf("policy apply output = %q, want changed policy with review provenance", out)
	}

	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound_mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated policy_version = %d, want 2", updated.PolicyVersion)
	}
	updates, err := store.DurableAgentPolicyUpdates(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("DurableAgentPolicyUpdates() err = %v", err)
	}
	if len(updates) != 1 || updates[0].SourceReviewEventID != reviewID {
		t.Fatalf("policy updates = %#v, want one update linked to review event %d", updates, reviewID)
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
