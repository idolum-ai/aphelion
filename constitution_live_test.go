//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

func TestLiveConstitution_MermaidCodebaseRequestProducesUnifiedPersonaDelivery(t *testing.T) {
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

	tempRoot := t.TempDir()
	cfg.Sessions.DBPath = filepath.Join(tempRoot, "sessions.db")
	if err := prepareFilesystem(cfg); err != nil {
		t.Fatalf("prepareFilesystem() err = %v", err)
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	built, err := defaultDeployVerificationRuntimeBuilder(cfg, store)
	if err != nil {
		t.Fatalf("defaultDeployVerificationRuntimeBuilder() err = %v", err)
	}
	defer func() {
		if built.Cleanup != nil {
			built.Cleanup()
		}
	}()

	rt, ok := built.Runner.(*runtimepkg.Runtime)
	if !ok {
		t.Fatalf("built runner type = %T, want *runtime.Runtime", built.Runner)
	}

	var audit runtimepkg.TurnAudit
	rt.SetTurnAuditSink(func(got runtimepkg.TurnAudit) {
		audit = got
	})

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     990001,
		SenderID:   cfg.Principals.Telegram.AdminUserIDs[0],
		SenderName: "live-admin",
		MessageID:  1,
		Text: strings.Join([]string{
			"Please review this codebase and generate a couple of Mermaid-based architecture diagrams.",
			"Render and attach the diagrams you make, narrate them as Idolum in one unified voice, and do not mention internal layers or handoff.",
		}, " "),
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	last, ok := built.Sender.Last()
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
	for _, marker := range []string{"governor", "deferred to aphelion", "handed this to aphelion", "idolum and aphelion"} {
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
}
