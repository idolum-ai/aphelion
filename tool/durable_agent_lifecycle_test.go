//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestDurableAgentLifecycleParkAndResumePreserveState(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := testLifecycleDurableAgent(t, "ops-child")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentRuntimeState(core.DurableAgentRuntimeState{
		AgentID:   "ops-child",
		Status:    "awake",
		StateJSON: "{}",
	}); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState() err = %v", err)
	}

	parkOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"park","agent_id":"ops-child","reason":"quiet window"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(park) err = %v", err)
	}
	if !strings.Contains(parkOut, "action: durable-agent park") ||
		!strings.Contains(parkOut, "status: parked") ||
		!strings.Contains(parkOut, "scheduled and poll wakes stop") {
		t.Fatalf("park output = %q, want lifecycle evidence", parkOut)
	}
	stored, err := store.DurableAgent("ops-child")
	if err != nil {
		t.Fatalf("DurableAgent(after park) err = %v", err)
	}
	if stored.Status != "parked" {
		t.Fatalf("agent status after park = %q, want parked", stored.Status)
	}
	runtimeState, err := store.DurableAgentRuntimeState("ops-child")
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState(after park) err = %v", err)
	}
	if runtimeState.Status != "dormant" || runtimeState.DormantAt.IsZero() || runtimeState.LastApplyStatus != "parked" {
		t.Fatalf("runtime state after park = %#v, want dormant parked state", runtimeState)
	}

	resumeOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"resume","agent_id":"ops-child"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(resume) err = %v", err)
	}
	if !strings.Contains(resumeOut, "action: durable-agent resume") ||
		!strings.Contains(resumeOut, "status: active") ||
		!strings.Contains(resumeOut, "profile files synced") {
		t.Fatalf("resume output = %q, want activation evidence", resumeOut)
	}
	stored, err = store.DurableAgent("ops-child")
	if err != nil {
		t.Fatalf("DurableAgent(after resume) err = %v", err)
	}
	if stored.Status != "active" {
		t.Fatalf("agent status after resume = %q, want active", stored.Status)
	}
}

func TestDurableAgentLifecycleRetireRevokesActiveSurfaces(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	agent := testLifecycleDurableAgent(t, "ops-child")
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
		Charter:              "Operate from the tailnet child.",
		CapabilityEnvelope:   []string{"bounded_review_artifact"},
		OutboundMode:         "read_only",
		DriftPolicy:          "admin_review",
		TailnetMode:          "tsnet",
		TailnetSurfacePolicy: "declare",
	})
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-ops-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      core.DurableAgentPrincipal("ops-child"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex-app-server",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          "ops-child",
		ParentControlURL: "https://ops-child.example.test/control",
		ProtocolVersion:  "v1",
		Status:           "active",
		LastSequence:     7,
		EnrolledAt:       time.Now().UTC().Add(-time.Hour),
		LastSeenAt:       time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "durable_agent:ops-child:tsnet_http:status",
		OwnerKind:   "durable_agent",
		OwnerID:     "ops-child",
		SurfaceKind: "tsnet_http",
		Name:        "ops-child",
		Hostname:    "ops-child",
		Status:      session.TailnetSurfaceStatusActive,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"retire","agent_id":"ops-child","reason":"completed mission"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(retire) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent retire") ||
		!strings.Contains(out, "status: retired") ||
		!strings.Contains(out, "revoked capability grants: 1") ||
		!strings.Contains(out, "remote enrollment: decommissioned") ||
		!strings.Contains(out, "tailnet surface: revoked") {
		t.Fatalf("retire output = %q, want authority revocation evidence", out)
	}
	stored, err := store.DurableAgent("ops-child")
	if err != nil {
		t.Fatalf("DurableAgent(after retire) err = %v", err)
	}
	if stored.Status != "retired" {
		t.Fatalf("agent status after retire = %q, want retired", stored.Status)
	}
	runtimeState, err := store.DurableAgentRuntimeState("ops-child")
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState(after retire) err = %v", err)
	}
	if runtimeState.Status != "dormant" || runtimeState.DormantAt.IsZero() || runtimeState.LastApplyStatus != "retired" {
		t.Fatalf("runtime state after retire = %#v, want dormant retired state", runtimeState)
	}
	grant, ok, err := store.CapabilityGrant("grant-ops-tool")
	if err != nil || !ok {
		t.Fatalf("CapabilityGrant(after retire) = %#v ok=%t err=%v", grant, ok, err)
	}
	if grant.Status != session.CapabilityGrantStatusRevoked || grant.RevokedAt.IsZero() ||
		!strings.Contains(grant.StaleReason, "completed mission") {
		t.Fatalf("grant after retire = %#v, want revoked with reason", grant)
	}
	enrollment, err := store.DurableAgentRemoteEnrollment("ops-child")
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(after retire) err = %v", err)
	}
	if enrollment.Status != "decommissioned" || enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after retire = %#v, want decommissioned", enrollment)
	}
	surface, ok, err := store.TailnetSurface("durable_agent:ops-child:tsnet_http:status")
	if err != nil || !ok {
		t.Fatalf("TailnetSurface(after retire) = %#v ok=%t err=%v", surface, ok, err)
	}
	if surface.Status != session.TailnetSurfaceStatusRevoked || surface.RevokedAt.IsZero() {
		t.Fatalf("surface after retire = %#v, want revoked", surface)
	}
}

func testLifecycleDurableAgent(t *testing.T, agentID string) core.DurableAgent {
	t.Helper()

	policy := core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
		Charter:            "Read a bounded child channel and surface review artifacts.",
		CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
		OutboundMode:       "read_only",
		DriftPolicy:        "admin_review",
	})
	return core.DurableAgent{
		AgentID:            agentID,
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address: "ops-child@example.test",
			Adapter: "mailbox_adapter",
		}},
		LivePolicy:       policy,
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("external_channel", policy),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-test",
			Model:          "claude-test",
		},
		PolicyVersion:     1,
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "restricted",
		WakeupMode:        "poll",
		Status:            "active",
	}
}
