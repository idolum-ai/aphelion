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
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

type liveRuntimeHarness struct {
	cfg     *config.Config
	store   *session.SQLiteStore
	rt      *runtimepkg.Runtime
	sender  *deployVerificationSender
	cleanup func()
}

func newLiveRuntimeHarness(t *testing.T) *liveRuntimeHarness {
	t.Helper()
	if strings.TrimSpace(os.Getenv("APHELION_LIVE_TESTS")) != "1" {
		t.Skip("set APHELION_LIVE_TESTS=1 to run live constitutional tests")
	}

	configPath, err := config.ResolveConfigPath("")
	if err != nil {
		t.Skipf("config resolution failed: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Skipf("config unavailable at %s: %v", configPath, err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load(%q) err = %v", configPath, err)
	}
	cfg.Governor.Backend = "native"

	tempRoot := t.TempDir()
	cfg.Sessions.DBPath = filepath.Join(tempRoot, "sessions.db")
	if err := prepareFilesystem(cfg); err != nil {
		t.Fatalf("prepareFilesystem() err = %v", err)
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	built, err := defaultDeployVerificationRuntimeBuilder(cfg, store)
	if err != nil {
		store.Close()
		t.Fatalf("defaultDeployVerificationRuntimeBuilder() err = %v", err)
	}
	rt, ok := built.Runner.(*runtimepkg.Runtime)
	if !ok {
		if built.Cleanup != nil {
			built.Cleanup()
		}
		store.Close()
		t.Fatalf("built runner type = %T, want *runtime.Runtime", built.Runner)
	}
	return &liveRuntimeHarness{
		cfg:    cfg,
		store:  store,
		rt:     rt,
		sender: built.Sender,
		cleanup: func() {
			if built.Cleanup != nil {
				built.Cleanup()
			}
			_ = store.Close()
		},
	}
}

func liveTurnContext(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), timeout)
}

func TestLiveConstitution_MermaidCodebaseRequestProducesUnifiedPersonaDelivery(t *testing.T) {
	harness := newLiveRuntimeHarness(t)
	defer harness.cleanup()

	var audit runtimepkg.TurnAudit
	harness.rt.SetTurnAuditSink(func(got runtimepkg.TurnAudit) {
		audit = got
	})

	ctx, cancel := liveTurnContext(t, 3*time.Minute)
	defer cancel()

	if _, err := harness.rt.HandleInbound(ctx, core.InboundMessage{
		ChatID:     990001,
		SenderID:   harness.cfg.Principals.Telegram.AdminUserIDs[0],
		SenderName: "live-admin",
		MessageID:  1,
		Text: strings.Join([]string{
			"Please review this codebase and generate a couple of Mermaid-based architecture diagrams.",
			"Render and attach the diagrams you make, narrate them as Idolum in one unified voice, and do not mention internal layers or handoff.",
			"Write the generated PNG files under the repo-local docs/architecture/diagrams/generated/ directory, then attach them with MEDIA: absolute/path.png lines in the reply floor; do not use /tmp or OpenAI file uploads for the user-visible attachment step.",
		}, " "),
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	last, ok := harness.sender.Last()
	if !ok {
		t.Fatal("expected at least one outbound message")
	}
	if len(last.Media) == 0 {
		t.Fatalf("outbound media len = 0; audit = %#v", audit)
	}
	if strings.TrimSpace(last.Text) == "" {
		t.Fatalf("final narration empty; audit = %#v", audit)
	}
	lower := strings.ToLower(last.Text)
	for _, marker := range []string{"governor", "deferred to aphelion", "handed this to aphelion", "idolum and aphelion", "idolum (system)", "idolum and idolum (system)"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("final narration leaked internal relationship via %q: %q", marker, last.Text)
		}
	}
	if strings.Contains(lower, "i can't") || strings.Contains(lower, "i cannot") {
		t.Fatalf("final narration contradicted delivered media: %q", last.Text)
	}
	if len(audit.ToolCalls) == 0 {
		t.Fatalf("expected tool activity in live constitutional run; audit = %#v", audit)
	}
	for _, progress := range audit.ProgressMessages {
		lowerProgress := strings.ToLower(progress)
		for _, marker := range []string{"inspecting files", "running command", "using exec", "searching semantic memory"} {
			if strings.Contains(lowerProgress, marker) {
				t.Fatalf("progress leaked old command taxonomy via %q: %q", marker, progress)
			}
		}
	}
}

func TestLiveConstitution_ConversationDerivedPostureAndProgress(t *testing.T) {
	harness := newLiveRuntimeHarness(t)
	defer harness.cleanup()

	var audit runtimepkg.TurnAudit
	harness.rt.SetTurnAuditSink(func(got runtimepkg.TurnAudit) {
		audit = got
	})

	ctx, cancel := liveTurnContext(t, 90*time.Second)
	defer cancel()

	if _, err := harness.rt.HandleInbound(ctx, core.InboundMessage{
		ChatID:     990002,
		SenderID:   harness.cfg.Principals.Telegram.AdminUserIDs[0],
		SenderName: "live-admin",
		MessageID:  1,
		Text:       "Inspect the repo directly and tell me where turn posture is decided. Use the codebase, keep the answer short, and do not mention internal layers.",
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	last, ok := harness.sender.Last()
	if !ok {
		t.Fatal("expected outbound message")
	}
	if strings.TrimSpace(last.Text) == "" {
		t.Fatalf("final reply empty; audit = %#v", audit)
	}
	if len(audit.ToolCalls) == 0 {
		t.Fatalf("expected tool activity; audit = %#v", audit)
	}
	for _, progress := range audit.ProgressMessages {
		lowerProgress := strings.ToLower(progress)
		for _, marker := range []string{"inspecting files", "running command", "using exec", "searching semantic memory"} {
			if strings.Contains(lowerProgress, marker) {
				t.Fatalf("progress leaked old command taxonomy via %q: %q", marker, progress)
			}
		}
	}
}

func TestLiveConstitution_ConversationDerivedPolicyExpression(t *testing.T) {
	harness := newLiveRuntimeHarness(t)
	defer harness.cleanup()

	if err := harness.store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: harness.cfg.Principals.Telegram.AdminUserIDs[0],
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
	if err := harness.store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          "family-group",
		Status:           "active",
		ParentControlURL: "https://example.invalid/control",
		ProtocolVersion:  "v1",
		EnrolledAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	ctx, cancel := liveTurnContext(t, 90*time.Second)
	defer cancel()

	if _, err := harness.rt.HandleInbound(ctx, core.InboundMessage{
		ChatID:     990003,
		SenderID:   harness.cfg.Principals.Telegram.AdminUserIDs[0],
		SenderName: "live-admin",
		MessageID:  1,
		Text:       "Adjust the family-group durable agent so it reviews before replying, stays private, and keeps shared context isolated. This is an ordinary policy change, not a remote enrollment change. Apply the change.",
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	updated, err := harness.store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "reply_with_parent_review" {
		t.Fatalf("updated outbound_mode = %q, want reply_with_parent_review", updated.LivePolicy.OutboundMode)
	}
	if updated.LivePolicy.PublicSurfaceMode != "none" {
		t.Fatalf("updated public_surface_mode = %q, want none", updated.LivePolicy.PublicSurfaceMode)
	}
	if updated.LivePolicy.SharedInferenceReuse != "disabled" {
		t.Fatalf("updated shared_inference_reuse = %q, want disabled", updated.LivePolicy.SharedInferenceReuse)
	}
}
