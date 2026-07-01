//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type recordingScheduledChildWakeExecutor struct {
	calls int
	err   error
}

func (e *recordingScheduledChildWakeExecutor) Supports(_ sandbox.Scope, _ core.DurableAgent) bool {
	return true
}

func (e *recordingScheduledChildWakeExecutor) Run(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
	e.calls++
	return e.err
}

func TestScheduledExternalChannelWakeRunsWhenLifecycleVerifiedAndRuntimeGrantPresent(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = provider
	executor := &recordingScheduledChildWakeExecutor{}
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = executor

	agent := scheduledExternalChannelWakeAgent("scheduled-ready-child", "gog_cli")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "gog_cli")

	if err := rt.pollDurableAgentWakeViaChild(context.Background(), agent, time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableAgentWakeViaChild() err = %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("child wake executor calls = %d, want 1", executor.calls)
	}
	events, err := store.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("pending review events = %#v, want none for runnable scheduled wake", events)
	}
}

func TestScheduledExternalChannelWakeInstalledFreshProofsAutoVerifyAndRun(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	tools := newScheduledExternalChannelWakeToolRegistry(t, cfg, store)
	executor := &recordingScheduledChildWakeExecutor{}
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = executor

	agent := scheduledExternalChannelWakeAgent("scheduled-installed-child", "gog_cli")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	manifest := scheduledExternalChannelWakeToolManifest(t, cfg.Agent.ExecRoot, "gog_cli")
	installAuditProbeExternalToolForWakeTest(t, tools, store, manifest)
	seedExternalChannelRuntimeGrant(t, store, agent.AgentID, "gog_cli")

	if err := rt.pollDurableAgentWakeViaChild(context.Background(), agent, time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableAgentWakeViaChild() err = %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("child wake executor calls = %d, want wake after deterministic lifecycle verification", executor.calls)
	}
	events, err := store.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("pending review events = %#v, want none after deterministic lifecycle verification", events)
	}
	install, ok, err := store.ToolInstallRecord("gog_cli")
	if err != nil {
		t.Fatalf("ToolInstallRecord() err = %v", err)
	}
	if !ok || install.Status != session.ToolInstallStatusVerified || install.AttestedAt.IsZero() {
		t.Fatalf("install record = %#v, want verified with attestation", install)
	}
	if strings.TrimSpace(install.StaleReason) != "" || strings.TrimSpace(string(install.DriftSource)) != "" {
		t.Fatalf("install stale fields = (%q, %q), want cleared by verification", install.StaleReason, install.DriftSource)
	}
}

func TestScheduledExternalChannelWakeActionableBackoffDoesNotSuppressDeterministicRepair(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	tools := newScheduledExternalChannelWakeToolRegistry(t, cfg, store)
	executor := &recordingScheduledChildWakeExecutor{}
	rt, err := New(cfg, store, &fakeProvider{}, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = executor

	now := time.Now().UTC()
	agent := scheduledExternalChannelWakeAgent("scheduled-backedoff-child", "gog_cli")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	manifest := scheduledExternalChannelWakeToolManifest(t, cfg.Agent.ExecRoot, "gog_cli")
	installAuditProbeExternalToolForWakeTest(t, tools, store, manifest)
	seedExternalChannelRuntimeGrant(t, store, agent.AgentID, "gog_cli")
	seedExternalChannelWakeBackoff(t, store, agent.AgentID, "gog_cli", now.Add(-5*time.Minute), now.Add(time.Hour), "child_runtime_blocked: preflight_failed adapter=gog_cli failure_code=lifecycle_unregistered")

	if err := rt.pollDurableAgentWakeViaChild(context.Background(), agent, now); err != nil {
		t.Fatalf("pollDurableAgentWakeViaChild() err = %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("child wake executor calls = %d, want deterministic repair to bypass stale actionable backoff", executor.calls)
	}
	_, continuity, err := loadDurableAgentContinuityFromStore(store, agent.AgentID)
	if err != nil {
		t.Fatalf("loadDurableAgentContinuityFromStore() err = %v", err)
	}
	if continuity.ExternalChannel == nil || !continuity.ExternalChannel.BackoffUntil.IsZero() {
		t.Fatalf("external channel state = %#v, want cleared backoff after deterministic repair", continuity.ExternalChannel)
	}
}

func TestScheduledExternalChannelWakeStaleProofsMaterializesAuditProbeRepair(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	executor := &recordingScheduledChildWakeExecutor{}
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = executor

	agent := scheduledExternalChannelWakeAgent("scheduled-stale-child", "gog_cli")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	seedExternalChannelToolLifecycle(t, store, agent.AgentID, "gog_cli", session.ToolInstallStatusInstalled, false, true, true)

	if err := rt.pollDurableAgentWakeViaChild(context.Background(), agent, time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableAgentWakeViaChild() err = %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("child wake executor calls = %d, want blocked before child starts", executor.calls)
	}
	events, err := store.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events = %#v, want one exact lifecycle repair card", events)
	}
	combined := events[0].Summary + "\n" + events[0].MetadataJSON
	for _, want := range []string{"audit", "probe", "verify", "gog_cli"} {
		if !strings.Contains(strings.ToLower(combined), want) {
			t.Fatalf("repair event = %s, want bounded audit/probe/verify term %q", combined, want)
		}
	}
	if strings.Contains(combined, "Only renew or create the required grant") {
		t.Fatalf("repair event = %s, want lifecycle repair, not generic grant renewal wording", combined)
	}
}

func TestScheduledExternalChannelWakeMissingRuntimeMaterialMaterializesGrantRepair(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	executor := &recordingScheduledChildWakeExecutor{}
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = executor

	agent := scheduledExternalChannelWakeAgent("scheduled-missing-runtime-child", "gog_cli")
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	seedExternalChannelToolLifecycle(t, store, agent.AgentID, "gog_cli", session.ToolInstallStatusVerified, true, true, false)

	if err := rt.pollDurableAgentWakeViaChild(context.Background(), agent, time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableAgentWakeViaChild() err = %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("child wake executor calls = %d, want blocked before child starts", executor.calls)
	}
	events, err := store.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events = %#v, want one exact child_runtime materialization card", events)
	}
	combined := events[0].Summary + "\n" + events[0].MetadataJSON
	for _, want := range []string{"child_runtime", "gog_cli", "tool grant"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("repair event = %s, want exact runtime materialization term %q", combined, want)
		}
	}
	if strings.Contains(combined, "Only renew or create the required grant") {
		t.Fatalf("repair event = %s, want materialization repair, not generic work-item wording", combined)
	}
}

func scheduledExternalChannelWakeAgent(agentID string, adapter string) core.DurableAgent {
	return core.DurableAgent{
		AgentID:            agentID,
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter:      adapter,
			PollInterval: "12h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Run scheduled external-channel work when due.",
			CapabilityEnvelope: []string{"external_channel_poll", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
}

func seedExternalChannelToolLifecycle(t *testing.T, store *session.SQLiteStore, agentID string, adapterName string, installStatus session.ToolInstallStatus, auditPassed bool, probePassed bool, runtimeMaterial bool) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: adapterName, ImplementationRef: "external:" + adapterName, Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{
		ToolName:          adapterName,
		Status:            installStatus,
		ProbeStatus:       probeStatus(probePassed),
		InstalledAt:       now.Add(-3 * time.Minute),
		LastProbedAt:      timeOrZero(probePassed, now.Add(-1*time.Minute)),
		AttestedAt:        timeOrZero(installStatus == session.ToolInstallStatusVerified, now),
		StaleReason:       staleReason(!auditPassed || !probePassed),
		DriftSource:       driftSource(!auditPassed || !probePassed),
		Installer:         "test",
		InstallRef:        "workspace:test-fixture",
		CurrentInstallRef: "workspace:test-fixture",
	}); err != nil {
		t.Fatalf("UpsertToolInstallRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{
		ToolName:  adapterName,
		Status:    auditStatus(auditPassed),
		AuditedAt: timeOrZero(auditPassed, now.Add(-2*time.Minute)),
		Rationale: "test audit",
	}); err != nil {
		t.Fatalf("UpsertToolAuditRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{
		ToolName:  adapterName,
		Status:    probeStatus(probePassed),
		ProbedAt:  timeOrZero(probePassed, now.Add(-1*time.Minute)),
		Rationale: "test probe",
	}); err != nil {
		t.Fatalf("UpsertToolProbeRecord(%s) err = %v", adapterName, err)
	}
	contract := `{}`
	if runtimeMaterial {
		materialRoot := t.TempDir()
		contract = `{"child_runtime":{"readonly_paths":["` + materialRoot + `"]}}`
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-" + agentID + "-" + adapterName,
		Kind:           session.CapabilityKindTool,
		TargetResource: adapterName,
		GrantedTo:      core.DurableAgentPrincipal(agentID),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       contract,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s) err = %v", adapterName, err)
	}
}

func newScheduledExternalChannelWakeToolRegistry(t *testing.T, cfg *config.Config, store *session.SQLiteStore) *toolpkg.Registry {
	t.Helper()
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	registry := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, registry)
	return registry
}

func scheduledExternalChannelWakeToolManifest(t *testing.T, execRoot string, toolName string) toolpkg.ExternalToolManifest {
	t.Helper()
	workdir := filepath.Join(execRoot, "external-tool-"+toolName)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) err = %v", workdir, err)
	}
	return toolpkg.ExternalToolManifest{
		Name:      toolName,
		Owner:     "durable-agent",
		Execution: toolpkg.ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh", Workdir: workdir},
		IO: toolpkg.ExternalToolManifestIO{
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}},"required":["summary"]}`),
		},
		Probe: toolpkg.ExternalToolManifestProbe{Command: []string{"./probe.sh"}, ExpectedOutputContains: "probe ok"},
	}
}

func installAuditProbeExternalToolForWakeTest(t *testing.T, registry *toolpkg.Registry, store *session.SQLiteStore, manifest toolpkg.ExternalToolManifest) {
	t.Helper()
	manifest = toolpkg.NormalizeExternalToolManifest(manifest)
	workdir := strings.TrimSpace(manifest.Execution.Workdir)
	runScript := filepath.Join(workdir, "run.sh")
	if err := os.WriteFile(runScript, []byte("#!/usr/bin/env bash\ncat >/dev/null\necho '{\"summary\":\"child-owned tool ran\"}'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", runScript, err)
	}
	probeScript := filepath.Join(workdir, "probe.sh")
	if err := os.WriteFile(probeScript, []byte("#!/usr/bin/env bash\necho 'probe ok'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", probeScript, err)
	}
	if _, err := registry.WithExternalToolManifests([]toolpkg.ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests(%s) err = %v", manifest.Name, err)
	}
	key := session.SessionKey{
		ChatID: 1001,
		UserID: 1001,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"},
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	steps := []json.RawMessage{
		json.RawMessage(fmt.Sprintf(`{"action":"install_set","tool_name":%q,"status":"installed","installer":"aphelion","install_ref":"workspace:test-fixture"}`, manifest.Name)),
		json.RawMessage(fmt.Sprintf(`{"action":"audit_run","tool_name":%q}`, manifest.Name)),
		json.RawMessage(fmt.Sprintf(`{"action":"probe_run","tool_name":%q}`, manifest.Name)),
	}
	for _, input := range steps {
		if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "tool_authority", input); err != nil {
			t.Fatalf("tool_authority(%s) err = %v", input, err)
		}
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: manifest.Name, ImplementationRef: "external:" + manifest.Name, Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool(%s) err = %v", manifest.Name, err)
	}
}

func seedExternalChannelRuntimeGrant(t *testing.T, store *session.SQLiteStore, agentID string, adapterName string) {
	t.Helper()
	materialRoot := t.TempDir()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-" + agentID + "-" + adapterName,
		Kind:           session.CapabilityKindTool,
		TargetResource: adapterName,
		GrantedTo:      core.DurableAgentPrincipal(agentID),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + materialRoot + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s) err = %v", adapterName, err)
	}
}

func seedExternalChannelWakeBackoff(t *testing.T, store *session.SQLiteStore, agentID string, adapterName string, lastAttempt time.Time, backoffUntil time.Time, lastErr string) {
	t.Helper()
	state := core.DurableAgentExternalChannelRuntimeState{
		Adapter:       adapterName,
		LastCommand:   genericExternalChannelPollCommandName,
		LastAttemptAt: lastAttempt.UTC(),
		LastStatus:    "wake_blocked",
		LastError:     lastErr,
		LastErrorAt:   lastAttempt.UTC(),
		BackoffUntil:  backoffUntil.UTC(),
		FailureCount:  1,
	}
	continuity := core.NormalizeDurableAgentContinuityState(core.DurableAgentContinuityState{
		ExternalChannel: encodeGenericExternalChannelState(state, adapterName),
	})
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("Marshal continuity err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
}

func timeOrZero(ok bool, value time.Time) time.Time {
	if !ok {
		return time.Time{}
	}
	return value
}

func auditStatus(passed bool) session.ToolAuditStatus {
	if passed {
		return session.ToolAuditStatusPassed
	}
	return session.ToolAuditStatusFailed
}

func probeStatus(passed bool) session.ToolProbeStatus {
	if passed {
		return session.ToolProbeStatusPassed
	}
	return session.ToolProbeStatusFailed
}

func staleReason(stale bool) string {
	if stale {
		return "workspace_drift: test fixture"
	}
	return ""
}

func driftSource(stale bool) session.ToolDriftSource {
	if stale {
		return session.ToolDriftSourceWorkspaceDrift
	}
	return ""
}
