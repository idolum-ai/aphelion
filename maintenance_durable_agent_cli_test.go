//go:build linux

package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func TestRunDurableAgentWakeCommandRunsOneNamedAgent(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	wakeTime := time.Date(2026, time.May, 4, 18, 30, 0, 123, time.UTC)
	fake := &fakeDurableAgentWakeRuntime{}
	cleanupCalled := false

	out, err := captureStdout(t, func() error {
		return runDurableAgentWakeCommandWithFactory([]string{
			"--config", cfgPath,
			"--agent", " image2 ",
			"--now", wakeTime.Format(time.RFC3339Nano),
		}, func(cfg *config.Config) (durableAgentWakeRuntime, func(), error) {
			if cfg == nil || !strings.HasSuffix(cfg.Sessions.DBPath, "sessions.db") {
				t.Fatalf("factory cfg = %#v, want loaded config", cfg)
			}
			return fake, func() { cleanupCalled = true }, nil
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentWakeCommandWithFactory() err = %v", err)
	}
	if fake.agentID != "image2" || !fake.now.Equal(wakeTime) {
		t.Fatalf("wake call = agent %q now %s, want image2 %s", fake.agentID, fake.now.Format(time.RFC3339Nano), wakeTime.Format(time.RFC3339Nano))
	}
	if !cleanupCalled {
		t.Fatal("cleanup was not called")
	}
	for _, needle := range []string{"action: durable-agent wake", "agent_id: image2", "status: completed"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("wake output = %q, want substring %q", out, needle)
		}
	}
}

func TestRunDurableAgentCommandAcceptsWakeSubcommand(t *testing.T) {
	err := runDurableAgentCommand([]string{"wake"})
	if err == nil || !strings.Contains(err.Error(), "durable-agent wake requires --agent") {
		t.Fatalf("runDurableAgentCommand(wake) err = %v, want --agent requirement", err)
	}
}

func TestRunDurableAgentListShowsRegisteredAgents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	for _, agent := range []core.DurableAgent{
		{
			AgentID:            "family-group",
			ReviewTargetChatID: 1001,
			ChannelKind:        "telegram_group",
			Status:             "active",
			BootstrapLLM: core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "openrouter",
				APIKey:         "sk-or-group",
				Model:          "openrouter/test-model",
			},
			LivePolicy: core.DurableAgentLivePolicy{
				OutboundMode: "reply_with_parent_review",
			},
			PolicyVersion: 2,
		},
		{
			AgentID:            "mail-digest",
			ReviewTargetChatID: 1001,
			ChannelKind:        "external_channel",
			Status:             "draft",
			BootstrapLLM: core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "openrouter",
				APIKey:         "sk-or-mail",
				Model:          "openrouter/test-model",
			},
			LivePolicy: core.DurableAgentLivePolicy{
				OutboundMode: "read_only",
			},
			PolicyVersion: 1,
		},
	} {
		if err := store.UpsertDurableAgent(agent); err != nil {
			t.Fatalf("UpsertDurableAgent(%s) err = %v", agent.AgentID, err)
		}
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentListCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runDurableAgentListCommand() err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent list") || !strings.Contains(out, "count: 2") {
		t.Fatalf("durable-agent list output = %q, want action/count", out)
	}
	if !strings.Contains(out, "agent_id=family-group") || !strings.Contains(out, "agent_id=mail-digest") {
		t.Fatalf("durable-agent list output = %q, want both agents", out)
	}
}

func TestRunDurableAgentHealthShowsStateAndEnrollment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 7,
		PolicyHash:    "abcdef1234567890",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         agent.AgentID,
		LastApplyStatus: "failed",
		LastApplyError:  "child runtime unavailable",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
		LastSequence:     12,
		LastSeenAt:       time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentHealthCommand([]string{"--config", cfgPath, "--agent", "family-group"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentHealthCommand() err = %v", err)
	}
	for _, needle := range []string{
		"action: durable-agent health",
		"agent_id: family-group",
		"health: degraded",
		"last_apply_status: failed",
		"last_apply_error: child runtime unavailable",
		"enrollment: present",
		"enrollment_status: active",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("durable-agent health output = %q, want substring %q", out, needle)
		}
	}
}

func TestRunDurableAgentBootstrapWriteExportsRemoteBootstrap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	bootstrapPath := filepath.Join(root, "family-group-bootstrap.json")
	out, err := captureStdout(t, func() error {
		return runDurableAgentBootstrapCommand([]string{
			"--config", cfgPath,
			"--agent", "family-group",
			"--path", bootstrapPath,
			"--parent-control-url", "https://house.example/control",
			"--enrollment-token", "enroll-token-1",
			"write",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentBootstrapCommand(write) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent bootstrap write") || !strings.Contains(out, "agent_id: family-group") {
		t.Fatalf("bootstrap write output = %q, want action and agent id", out)
	}

	bootstrap, err := durableagent.ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		t.Fatalf("ReadRemoteBootstrap() err = %v", err)
	}
	if bootstrap.AgentID != "family-group" {
		t.Fatalf("bootstrap.AgentID = %q, want family-group", bootstrap.AgentID)
	}
	if bootstrap.ParentControlURL != "https://house.example/control" {
		t.Fatalf("bootstrap.ParentControlURL = %q, want https://house.example/control", bootstrap.ParentControlURL)
	}
	if bootstrap.EnrollmentToken != "enroll-token-1" {
		t.Fatalf("bootstrap.EnrollmentToken = %q, want enroll-token-1", bootstrap.EnrollmentToken)
	}
	if bootstrap.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("bootstrap.BootstrapLLM.NativeProvider = %q, want openrouter", bootstrap.BootstrapLLM.NativeProvider)
	}
	if bootstrap.NetworkPolicy != "restricted" {
		t.Fatalf("bootstrap.NetworkPolicy = %q, want restricted", bootstrap.NetworkPolicy)
	}
	persisted, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if persisted.ControlPlaneSecret != "enroll-token-1" {
		t.Fatalf("persisted ControlPlaneSecret = %q, want enroll-token-1", persisted.ControlPlaneSecret)
	}
}

func TestRunDurableAgentEnrollmentShowRevokeAndReactivate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		ControlPlaneSecret: "enroll-token-1",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	showOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "show"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(show) err = %v", err)
	}
	if !strings.Contains(showOut, "status: active") {
		t.Fatalf("show output = %q, want active status", showOut)
	}

	revokeOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "revoke"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(revoke) err = %v", err)
	}
	if !strings.Contains(revokeOut, "status: revoked") {
		t.Fatalf("revoke output = %q, want revoked status", revokeOut)
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(revoked) err = %v", err)
	}
	if enrollment.Status != "revoked" || enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after revoke = %#v, want revoked with timestamp", enrollment)
	}

	reactivateOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "reactivate"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(reactivate) err = %v", err)
	}
	if !strings.Contains(reactivateOut, "status: active") {
		t.Fatalf("reactivate output = %q, want active status", reactivateOut)
	}
	enrollment, err = store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(reactivated) err = %v", err)
	}
	if enrollment.Status != "active" || !enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after reactivate = %#v, want active without revocation", enrollment)
	}
}

func TestRunDurableAgentEnrollmentRotateSecretAndDecommission(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		ControlPlaneSecret: "enroll-token-1",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	rotateOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "--secret", "enroll-token-2", "rotate-secret"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(rotate-secret) err = %v", err)
	}
	if !strings.Contains(rotateOut, "status: active") {
		t.Fatalf("rotate-secret output = %q, want active status", rotateOut)
	}
	updatedAgent, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(rotated) err = %v", err)
	}
	if updatedAgent.ControlPlaneSecret != "enroll-token-2" {
		t.Fatalf("ControlPlaneSecret = %q, want enroll-token-2", updatedAgent.ControlPlaneSecret)
	}

	decommissionOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "decommission"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(decommission) err = %v", err)
	}
	if !strings.Contains(decommissionOut, "status: decommissioned") {
		t.Fatalf("decommission output = %q, want decommissioned status", decommissionOut)
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(decommissioned) err = %v", err)
	}
	if enrollment.Status != "decommissioned" || enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after decommission = %#v, want decommissioned with timestamp", enrollment)
	}

	if err := runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "reactivate"}); err == nil {
		t.Fatal("runDurableAgentEnrollmentCommand(reactivate) err = nil, want decommissioned refusal")
	} else if !strings.Contains(err.Error(), "decommissioned") {
		t.Fatalf("runDurableAgentEnrollmentCommand(reactivate) err = %v, want decommissioned refusal", err)
	}
}

func TestRunDurableAgentRemoteRunOnceSyncsAndUploadsArtifacts(t *testing.T) {
	root := t.TempDir()
	parentCfgPath := writeMaintenanceConfig(t, root)

	parentCfg, _, err := loadConfigForCommand(parentCfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	parentStore, err := session.NewSQLiteStore(parentCfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("parent NewSQLiteStore() err = %v", err)
	}
	defer parentStore.Close()

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(parentCfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
		Status:            "active",
	}
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childDBPath := filepath.Join(root, "remote-child.db")
	bootstrapPath := filepath.Join(root, "family-group-bootstrap.json")
	if err := runDurableAgentBootstrapCommand([]string{
		"--config", parentCfgPath,
		"--agent", "family-group",
		"--path", bootstrapPath,
		"--parent-control-url", "https://house.example",
		"--enrollment-token", "enroll-token-1",
		"write",
	}); err != nil {
		t.Fatalf("runDurableAgentBootstrapCommand(write) err = %v", err)
	}

	messagePath := filepath.Join(root, "message.json")
	msgRaw := []byte(`{
  "ChatID": -100123,
  "ChatType": "group",
  "SenderID": 77,
  "SenderName": "Aunt May",
  "Text": "Can you remind everyone again?",
  "MessageID": 22,
  "DurableAgentID": "family-group",
  "Timestamp": "2026-04-13T00:00:00Z"
}`)
	if err := os.WriteFile(messagePath, msgRaw, 0o600); err != nil {
		t.Fatalf("WriteFile(message) err = %v", err)
	}

	origClientFactory := durableAgentRemoteClientFactory
	origExecutorFactory := durableAgentRemoteExecutorFactory
	defer func() {
		durableAgentRemoteClientFactory = origClientFactory
		durableAgentRemoteExecutorFactory = origExecutorFactory
	}()

	durableAgentRemoteClientFactory = func(b core.DurableAgentRemoteBootstrap) (durableagent.RemoteControlClient, error) {
		client, err := durableagent.NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = &http.Client{Transport: maintenanceHandlerRoundTripper{handler: durableagent.NewHTTPHandler(parentStore).Handler()}}
		return client, nil
	}
	durableAgentRemoteExecutorFactory = func(store *session.SQLiteStore, dbPath string) durableagent.RemoteChildExecutor {
		return durableagent.RemoteChildExecutorFunc(func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
			_, err := durableagent.NewRuntime(store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				Summary:       "Family schedule drift keeps resurfacing around the dinner plan.",
				IntervalLabel: "messages 20-25",
				LocalActions:  []string{"Held reply pending parent visibility."},
				Questions:     []string{"Should this become a standing family reminder?"},
				RiskFlags:     []string{"family_relevant_update"},
			})
			return err
		})
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentRemoteCommand([]string{
			"--bootstrap", bootstrapPath,
			"--db", childDBPath,
			"--message", messagePath,
			"run-once",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentRemoteCommand(run-once) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent remote run-once") {
		t.Fatalf("remote run-once output = %q, want action line", out)
	}
	if !strings.Contains(out, "uploaded_review_artifacts: 1") {
		t.Fatalf("remote run-once output = %q, want uploaded review count", out)
	}

	parentEvents, err := parentStore.PendingReviewEvents(1, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
}

func TestRunDurableAgentRemoteLoopProcessesQueuedMessages(t *testing.T) {
	root := t.TempDir()
	parentCfgPath := writeMaintenanceConfig(t, root)

	parentCfg, _, err := loadConfigForCommand(parentCfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	parentStore, err := session.NewSQLiteStore(parentCfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("parent NewSQLiteStore() err = %v", err)
	}
	defer parentStore.Close()

	workspaceRoot := filepath.Join(root, "durable-work")
	memoryRoot := filepath.Join(root, "durable-memory")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
		Status:            "active",
	}
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childDBPath := filepath.Join(root, "remote-child.db")
	bootstrapPath := filepath.Join(root, "family-group-bootstrap.json")
	if err := runDurableAgentBootstrapCommand([]string{
		"--config", parentCfgPath,
		"--agent", "family-group",
		"--path", bootstrapPath,
		"--parent-control-url", "https://house.example",
		"--enrollment-token", "enroll-token-1",
		"write",
	}); err != nil {
		t.Fatalf("runDurableAgentBootstrapCommand(write) err = %v", err)
	}

	inboxDir := filepath.Join(root, "remote-inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox) err = %v", err)
	}
	msgRaw := []byte(`{
  "ChatID": -100123,
  "ChatType": "group",
  "SenderID": 77,
  "SenderName": "Aunt May",
  "Text": "Can you remind everyone again?",
  "MessageID": 22,
  "DurableAgentID": "family-group",
  "Timestamp": "2026-04-13T00:00:00Z"
}`)
	if err := os.WriteFile(filepath.Join(inboxDir, "0001.json"), msgRaw, 0o600); err != nil {
		t.Fatalf("WriteFile(message) err = %v", err)
	}

	origClientFactory := durableAgentRemoteClientFactory
	origExecutorFactory := durableAgentRemoteExecutorFactory
	defer func() {
		durableAgentRemoteClientFactory = origClientFactory
		durableAgentRemoteExecutorFactory = origExecutorFactory
	}()

	durableAgentRemoteClientFactory = func(b core.DurableAgentRemoteBootstrap) (durableagent.RemoteControlClient, error) {
		client, err := durableagent.NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = &http.Client{Transport: maintenanceHandlerRoundTripper{handler: durableagent.NewHTTPHandler(parentStore).Handler()}}
		return client, nil
	}
	durableAgentRemoteExecutorFactory = func(store *session.SQLiteStore, dbPath string) durableagent.RemoteChildExecutor {
		return durableagent.RemoteChildExecutorFunc(func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
			_, err := durableagent.NewRuntime(store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				Summary:       "Family schedule drift keeps resurfacing around the dinner plan.",
				IntervalLabel: "messages 20-25",
				LocalActions:  []string{"Held reply pending parent visibility."},
				Questions:     []string{"Should this become a standing family reminder?"},
				RiskFlags:     []string{"family_relevant_update"},
			})
			return err
		})
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentRemoteCommand([]string{
			"--bootstrap", bootstrapPath,
			"--db", childDBPath,
			"--inbox-dir", inboxDir,
			"--iterations", "1",
			"loop",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentRemoteCommand(loop) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent remote loop") {
		t.Fatalf("remote loop output = %q, want action line", out)
	}
	if !strings.Contains(out, "messages_processed: 1") {
		t.Fatalf("remote loop output = %q, want processed count", out)
	}

	parentEvents, err := parentStore.PendingReviewEvents(1, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
	if _, err := os.Stat(filepath.Join(inboxDir, "0001.json")); !os.IsNotExist(err) {
		t.Fatalf("message file still exists, err=%v", err)
	}
}
