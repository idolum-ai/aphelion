//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestRunNocturneTickWritesPrivateArtifactAndConfirmsAfterWindow(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	root := t.TempDir()
	cfg.Agent.SharedMemoryRoot = root
	cfg.Nocturne = config.NocturneConfig{Enabled: true, WindowStart: "23:00", WindowEnd: "07:00", ArtifactDir: "memory/nocturne", Confirmation: "Nocturne happened"}
	provider.replyText = "# Quiet hinge\n\nA private note."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	loc := time.Local
	if err := rt.runNocturneTick(context.Background(), time.Date(2026, 4, 30, 23, 30, 0, 0, loc)); err != nil {
		t.Fatalf("night tick err = %v", err)
	}
	artifact := filepath.Join(root, "memory", "nocturne", "2026-04-30.md")
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("ReadFile artifact err = %v", err)
	}
	if !strings.Contains(string(raw), "private: true") || !strings.Contains(string(raw), "Quiet hinge") {
		t.Fatalf("artifact = %q", raw)
	}

	if err := rt.runNocturneTick(context.Background(), time.Date(2026, 5, 1, 7, 10, 0, 0, loc)); err != nil {
		t.Fatalf("morning tick err = %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "Nocturne happened: Quiet hinge") {
		t.Fatalf("sent = %#v", sender.sent)
	}
	if _, err := os.Stat(artifact + ".confirmed"); err != nil {
		t.Fatalf("confirmed marker err = %v", err)
	}
}

func TestNocturneSkipsWhenOutsideWindowAndNoArtifact(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Agent.SharedMemoryRoot = t.TempDir()
	cfg.Nocturne = config.NocturneConfig{Enabled: true, WindowStart: "23:00", WindowEnd: "07:00", ArtifactDir: "memory/nocturne"}
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if err := rt.runNocturneTick(context.Background(), time.Date(2026, 4, 30, 12, 0, 0, 0, time.Local)); err != nil {
		t.Fatalf("tick err = %v", err)
	}
	if provider.callCount != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.callCount)
	}
}
