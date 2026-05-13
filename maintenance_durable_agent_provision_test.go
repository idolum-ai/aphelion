//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

func TestRunDurableAgentProvisionDryRunShowsPlan(t *testing.T) {
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
	agent := testProvisionCLIStoredAgent(t, root)
	agent.ControlPlaneSecret = "enroll-token"
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	binary := filepath.Join(root, "bin", "aphelion")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary) err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentProvisionCommand([]string{
			"--config", cfgPath,
			"--agent", "family-child",
			"--binary", binary,
			"--ssh-user", "alice",
			"--parent-control-url", "http://aphelion.example.ts.net:8765/control",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentProvisionCommand(dry-run) err = %v", err)
	}
	for _, needle := range []string{
		"action: durable-agent provision",
		"status: dry_run",
		"ssh_target: alice@family-child",
		"service_name: aphelion-child-family-child",
		"parent_control_url: http://aphelion.example.ts.net:8765/control",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("provision dry-run output = %q, want substring %q", out, needle)
		}
	}
}

func TestRunDurableAgentProvisionApplyGeneratesSecretAndVerifiesEnrollment(t *testing.T) {
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
	agent := testProvisionCLIStoredAgent(t, root)
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	binary := filepath.Join(root, "bin", "aphelion")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatalf("WriteFile(binary) err = %v", err)
	}

	origRunner := newDurableAgentProvisionSSHRunner
	defer func() { newDurableAgentProvisionSSHRunner = origRunner }()
	fake := &fakeProvisionCommandRunner{}
	newDurableAgentProvisionSSHRunner = func(_ *config.Config, _ time.Duration) tailnet.SSHRunner {
		return fake
	}
	parentURL := "http://aphelion.example.ts.net:8765/control"
	fake.afterRun = func() {
		_ = store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
			AgentID:          "family-child",
			ParentControlURL: parentURL,
			ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
			Status:           "active",
			LastSequence:     5,
			LastSeenAt:       time.Now().UTC(),
		})
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentProvisionCommand([]string{
			"--config", cfgPath,
			"--agent", "family-child",
			"--binary", binary,
			"--ssh-user", "alice",
			"--parent-control-url", parentURL,
			"--apply",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentProvisionCommand(apply) err = %v", err)
	}
	if fake.target != "alice@family-child" {
		t.Fatalf("fake target = %q, want alice@family-child", fake.target)
	}
	for _, needle := range []string{
		"status: applied",
		"control_secret_source: generated",
		"remote_output: provisioned",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("provision apply output = %q, want substring %q", out, needle)
		}
	}
	updated, err := store.DurableAgent("family-child")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if strings.TrimSpace(updated.ControlPlaneSecret) == "" {
		t.Fatal("ControlPlaneSecret is empty, want generated secret")
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{
		ChatID: -1,
		Scope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "family-child", DurableAgentID: "family-child"},
	}, 0, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !testHasExecutionEventType(events, core.ExecutionEventDurableProvisionStarted) || !testHasExecutionEventType(events, core.ExecutionEventDurableProvisionCompleted) {
		t.Fatalf("events = %#v, want provision started/completed", events)
	}
}

func testProvisionCLIStoredAgent(t *testing.T, root string) core.DurableAgent {
	t.Helper()
	return core.DurableAgent{
		AgentID:            "family-child",
		ParentAgentID:      "house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:                   "Remote child",
			CapabilityEnvelope:        []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:              "read_only",
			DriftPolicy:               "admin_review",
			TailnetMode:               "tsnet",
			TailnetHostname:           "family-child",
			TailnetTags:               []string{"tag:aphelion-child"},
			TailnetSurfacePolicy:      "private_status",
			PublicSurfaceMode:         "none",
			SharedInferenceReuse:      "disabled",
			SharedInferenceReuseScope: "public_prefix_only",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:   "codex",
			CodexHome: filepath.Join(root, "codex"),
		},
		Status: "active",
	}
}

type fakeProvisionCommandRunner struct {
	target   string
	args     []string
	stdinLen int
	afterRun func()
}

func (r *fakeProvisionCommandRunner) Run(_ context.Context, target string, args []string, stdin []byte) (tailnet.SSHResult, error) {
	r.target = target
	r.args = append([]string(nil), args...)
	r.stdinLen = len(stdin)
	if r.afterRun != nil {
		r.afterRun()
	}
	return tailnet.SSHResult{Target: target, Args: args, Output: "provisioned"}, nil
}

func testHasExecutionEventType(events []session.ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
