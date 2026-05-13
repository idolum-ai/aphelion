//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
)

func TestDurableAgentArtifactPutWritesChildSpecificArtifactHome(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "artifact-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	input, err := json.Marshal(durableAgentInput{
		Action:  "artifact_put",
		AgentID: "artifact-child",
		Artifact: &durableAgentArtifactInput{
			Path:    "schemas/console_status.schema.json",
			Kind:    "schema",
			Reason:  "child-owned status contract",
			Content: "{\n  \"type\": \"object\"\n}\n",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		input,
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_put) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent artifact put") ||
		!strings.Contains(out, "written: artifacts/schemas/console_status.schema.json") ||
		!strings.Contains(out, "sha256:") {
		t.Fatalf("artifact_put output = %q, want written artifact summary", out)
	}
	raw, err := os.ReadFile(filepath.Join(childMemory, "artifacts", "schemas", "console_status.schema.json"))
	if err != nil {
		t.Fatalf("ReadFile(artifact) err = %v", err)
	}
	if string(raw) != "{\n  \"type\": \"object\"\n}\n" {
		t.Fatalf("artifact content = %q, want exact child-specific content", raw)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(childMemory, "artifacts", "ARTIFACTS.json"))
	if err != nil {
		t.Fatalf("ReadFile(manifest) err = %v", err)
	}
	for _, needle := range []string{
		`"agent_id": "artifact-child"`,
		`"path": "schemas/console_status.schema.json"`,
		`"kind": "schema"`,
		`"source": "parent_governed_artifact"`,
		`"reason": "child-owned status contract"`,
	} {
		if !strings.Contains(string(manifestRaw), needle) {
			t.Fatalf("manifest = %s, want %s", manifestRaw, needle)
		}
	}

	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"artifact_list","agent_id":"artifact-child"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_list) err = %v", err)
	}
	if !strings.Contains(listOut, "count: 1") || !strings.Contains(listOut, "path=artifacts/schemas/console_status.schema.json") {
		t.Fatalf("artifact_list output = %q, want artifact entry", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"artifact_show","agent_id":"artifact-child","artifact":{"path":"schemas/console_status.schema.json"}}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(artifact_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent artifact show") ||
		!strings.Contains(showOut, "content:\n{\n  \"type\": \"object\"\n}") {
		t.Fatalf("artifact_show output = %q, want artifact content", showOut)
	}
}

func TestDurableAgentArtifactPutRejectsEscapingPath(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "artifact-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	input, err := json.Marshal(durableAgentInput{
		Action:  "artifact_put",
		AgentID: "artifact-child",
		Artifact: &durableAgentArtifactInput{
			Path:    "../core/console_status.go",
			Content: "package core\n",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		input,
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(artifact_put) err = nil, want escaping path error")
	}
	if !strings.Contains(err.Error(), "artifact path") {
		t.Fatalf("err = %v, want artifact path context", err)
	}
}

func TestDurableAgentArtifactPutRejectsSymlinkTarget(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "artifact-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	artifactDir := filepath.Join(childMemory, "artifacts", "schemas")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(artifactDir) err = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.schema.json")
	if err := os.Symlink(outside, filepath.Join(artifactDir, "console_status.schema.json")); err != nil {
		t.Fatalf("Symlink() err = %v", err)
	}
	input, err := json.Marshal(durableAgentInput{
		Action:  "artifact_put",
		AgentID: "artifact-child",
		Artifact: &durableAgentArtifactInput{
			Path:    "schemas/console_status.schema.json",
			Content: "{\n  \"type\": \"object\"\n}\n",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		adminSessionKey(),
		"durable_agent",
		input,
	)
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(artifact_put) err = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v, want symlink context", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside target stat err = %v, want not created", err)
	}
}

func TestDurableAgentDefinitionIncludesArtifactActions(t *testing.T) {
	registry, _ := newDurableAgentToolRegistry(t)
	var durableDef string
	for _, def := range registry.Definitions() {
		if def.Name == "durable_agent" {
			durableDef = string(def.Parameters)
			break
		}
	}
	if durableDef == "" {
		t.Fatal("durable_agent definition not found")
	}
	for _, needle := range []string{`"artifact_put"`, `"artifact_list"`, `"artifact_show"`, `"artifact"`, `"archetype_list"`, `"archetype_show"`, `"create_from_archetype"`, `"archetype"`} {
		if !strings.Contains(durableDef, needle) {
			t.Fatalf("durable_agent definition missing %s: %s", needle, durableDef)
		}
	}
}

func TestDurableAgentArchetypeListShowAndCreate(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	writeToolTestArchetype(t, registry.workspace, "aphelion-maintainer")
	registry.WithDurableAgentBootstrapLLM(core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"})

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	listOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"archetype_list"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(archetype_list) err = %v", err)
	}
	if !strings.Contains(listOut, "action: durable-agent archetype list") || !strings.Contains(listOut, "aphelion-maintainer") {
		t.Fatalf("archetype_list output = %q, want maintainer archetype", listOut)
	}

	showOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"archetype_show","archetype":"aphelion-maintainer"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(archetype_show) err = %v", err)
	}
	if !strings.Contains(showOut, "action: durable-agent archetype show") ||
		!strings.Contains(showOut, "required_files:") ||
		!strings.Contains(showOut, "examples/doctor-report.md") {
		t.Fatalf("archetype_show output = %q, want archetype summary", showOut)
	}
	if !strings.Contains(showOut, "/tmp clone") || !strings.Contains(showOut, "GitHub PR") {
		t.Fatalf("archetype_show output = %q, want clone/PR boundary", showOut)
	}

	createOut, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"create_from_archetype","agent_id":"aphelion-maintainer-live","archetype":"aphelion-maintainer"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(create_from_archetype) err = %v", err)
	}
	if !strings.Contains(createOut, "action: durable-agent archetype create") ||
		!strings.Contains(createOut, "status: draft") ||
		!strings.Contains(createOut, "archetype: aphelion-maintainer") {
		t.Fatalf("create_from_archetype output = %q, want archetype create summary", createOut)
	}

	agent, err := store.DurableAgent("aphelion-maintainer-live")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if agent.Status != "draft" {
		t.Fatalf("agent status = %q, want draft", agent.Status)
	}
	if agent.LivePolicy.OutboundMode != "read_only" || !containsString(agent.LivePolicy.CapabilityEnvelope, "session_log_read") {
		t.Fatalf("agent policy = %+v, want read-only session-log posture", agent.LivePolicy)
	}
	memoryRoot, err := durableAgentMemoryRoot(*agent, store)
	if err != nil {
		t.Fatalf("durableAgentMemoryRoot() err = %v", err)
	}
	provenanceRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "ARCHETYPE.json"))
	if err != nil {
		t.Fatalf("ReadFile(ARCHETYPE.json) err = %v", err)
	}
	if !strings.Contains(string(provenanceRaw), `"name": "aphelion-maintainer"`) {
		t.Fatalf("ARCHETYPE.json = %s, want archetype provenance", provenanceRaw)
	}
	copiedAgentRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "archetype", "AGENT.md"))
	if err != nil {
		t.Fatalf("ReadFile(archetype AGENT.md) err = %v", err)
	}
	if !strings.Contains(string(copiedAgentRaw), "Aphelion Maintainer") {
		t.Fatalf("copied AGENT.md = %q, want archetype template copy", copiedAgentRaw)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "archetype", "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(archetype runtime.md) err = %v", err)
	}
	if !strings.Contains(string(runtimeRaw), "/tmp clone") || !strings.Contains(string(runtimeRaw), "GitHub PR") {
		t.Fatalf("copied runtime.md = %q, want clone/PR boundary", runtimeRaw)
	}
	if _, err := os.Stat(filepath.Join(registry.workspace, "core", "aphelion_maintainer.go")); !os.IsNotExist(err) {
		t.Fatalf("unexpected repo-specific child file err = %v", err)
	}
}

func TestDurableAgentPolicyApplySyncsProfileFiles(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	childWorkspace := filepath.Join(t.TempDir(), "child", "workspace")
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "profile-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{childWorkspace, childMemory},
		Status:            "active",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "codex", CodexAuthSource: "codex_cli", CodexHome: "/tmp/codex-home"},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"bounded_review_artifact", "session_recall"},
			AllowedOutboundModes:         []string{"read_only", "reply_with_policy_authorization"},
			AllowedPublicSurfaceModes:    []string{"none", "explicit_parent_relay_only"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Initial charter.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	_, err := registry.applyDurableAgentPolicy(durableAgentInput{
		AgentID: "profile-child",
		PolicyPatch: &durableAgentPolicyPatchInput{
			Charter:      "Updated child charter.",
			Capabilities: []string{"bounded_review_artifact", "session_recall"},
		},
		Reason: "ratify profile files",
	})
	if err != nil {
		t.Fatalf("applyDurableAgentPolicy() err = %v", err)
	}
	charterRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "charter.md"))
	if err != nil {
		t.Fatalf("ReadFile(charter) err = %v", err)
	}
	capRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "capabilities.md"))
	if err != nil {
		t.Fatalf("ReadFile(capabilities) err = %v", err)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(runtime) err = %v", err)
	}
	growthRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "growth.md"))
	if err != nil {
		t.Fatalf("ReadFile(growth) err = %v", err)
	}
	ledgerRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "capability-ledger.md"))
	if err != nil {
		t.Fatalf("ReadFile(capability-ledger) err = %v", err)
	}
	scorecardRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "scorecard.md"))
	if err != nil {
		t.Fatalf("ReadFile(scorecard) err = %v", err)
	}
	if !strings.Contains(string(charterRaw), "Updated child charter.") ||
		!strings.Contains(string(capRaw), "session_recall") ||
		!strings.Contains(string(runtimeRaw), "child_runtime") ||
		!strings.Contains(string(growthRaw), "delegation_request") ||
		!strings.Contains(string(growthRaw), "json.loads") ||
		!strings.Contains(string(ledgerRaw), "Active grants:") ||
		!strings.Contains(string(scorecardRaw), "Accurate statements") {
		t.Fatalf("profile files missing ratified content: charter=%q capabilities=%q runtime=%q", charterRaw, capRaw, runtimeRaw)
	}
}

func TestDurableAgentProfileSyncIncludesTailnetDeclaration(t *testing.T) {
	t.Parallel()

	_, store := newDurableAgentToolRegistry(t)
	childMemory := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "tailnet-child",
		ChannelKind:       "headless",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "child", "workspace"), childMemory},
		Status:            "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:              "Tailnet-aware helper.",
			OutboundMode:         "read_only",
			DriftPolicy:          "admin_review",
			TailnetMode:          "tsnet",
			TailnetHostname:      "tailnet-helper",
			TailnetTags:          []string{"tag:aphelion-child"},
			TailnetSurfacePolicy: "private_status",
		}),
	}

	if _, err := syncDurableAgentProfileFiles(agent, store); err != nil {
		t.Fatalf("syncDurableAgentProfileFiles() err = %v", err)
	}
	policyRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "policy.md"))
	if err != nil {
		t.Fatalf("ReadFile(policy) err = %v", err)
	}
	runtimeRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "runtime.md"))
	if err != nil {
		t.Fatalf("ReadFile(runtime) err = %v", err)
	}
	surfaceRaw, err := os.ReadFile(filepath.Join(childMemory, "profile", "surface-rules.md"))
	if err != nil {
		t.Fatalf("ReadFile(surface-rules) err = %v", err)
	}
	for name, raw := range map[string]string{
		"policy.md":        string(policyRaw),
		"runtime.md":       string(runtimeRaw),
		"surface-rules.md": string(surfaceRaw),
	} {
		if !strings.Contains(raw, "tailnet_mode: tsnet") || !strings.Contains(raw, "tailnet_hostname: tailnet-helper") {
			t.Fatalf("%s = %q, want child tailnet declaration", name, raw)
		}
	}
	if !strings.Contains(string(surfaceRaw), "declared only") || !strings.Contains(string(surfaceRaw), "verify actual materialization") {
		t.Fatalf("surface-rules.md = %q, want declared-only materialization warning", string(surfaceRaw))
	}
}

func TestDurableAgentProfileApplyWritesChildAuthoredManifestEntry(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	memoryRoot := filepath.Join(t.TempDir(), "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "script-scout",
		ChannelKind:       "external_channel",
		Status:            "active",
		PolicyHash:        "policy-hash-1",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "native", NativeProvider: "openrouter", APIKey: "sk-test", Model: "test-model"},
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), memoryRoot},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	out, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminSessionKey(), "durable_agent", json.RawMessage(`{
		"action":"profile_apply",
		"agent_id":"script-scout",
		"profile_edit":{"target_file":"persona.md","content":"Curious scout. Asks before synthesizing.","reason":"seed scout voice"}
	}`))
	if err != nil {
		t.Fatalf("profile_apply err = %v", err)
	}
	if !strings.Contains(out, "profile/PROFILE.json") || !strings.Contains(out, "ownership=child_authored") {
		t.Fatalf("profile_apply output = %q, want child-authored manifest entry", out)
	}
	raw, err := os.ReadFile(filepath.Join(memoryRoot, "profile", "persona.md"))
	if err != nil {
		t.Fatalf("ReadFile(persona.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "profile_ownership: child_authored") || !strings.Contains(string(raw), "Curious scout") {
		t.Fatalf("persona.md = %q, want child-authored profile content", string(raw))
	}
}

func TestDurableAgentProfileApplyRejectsSymlinkTarget(t *testing.T) {
	registry, store := newDurableAgentToolRegistry(t)
	memoryRoot := filepath.Join(t.TempDir(), "memory")
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:           "script-scout",
		ChannelKind:       "external_channel",
		Status:            "active",
		PolicyHash:        "policy-hash-1",
		BootstrapLLM:      core.NodeLLMBootstrap{Backend: "native", NativeProvider: "openrouter", APIKey: "sk-test", Model: "test-model"},
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), memoryRoot},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	profileRoot := filepath.Join(memoryRoot, "profile")
	if err := os.MkdirAll(profileRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(profileRoot) err = %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside-persona.md")
	if err := os.Symlink(outside, filepath.Join(profileRoot, "persona.md")); err != nil {
		t.Fatalf("Symlink() err = %v", err)
	}

	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), admin, adminSessionKey(), "durable_agent", json.RawMessage(`{
		"action":"profile_apply",
		"agent_id":"script-scout",
		"profile_edit":{"target_file":"persona.md","content":"Curious scout.","reason":"seed scout voice"}
	}`))
	if err == nil {
		t.Fatal("profile_apply err = nil, want symlink rejection")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("profile_apply err = %v, want symlink context", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside target stat err = %v, want not created", err)
	}
}
