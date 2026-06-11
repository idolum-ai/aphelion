//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
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

func TestNocturneRecordsLowWeightInteriorSignal(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	root := t.TempDir()
	cfg.Agent.SharedMemoryRoot = root
	cfg.Nocturne = config.NocturneConfig{Enabled: true, WindowStart: "23:00", WindowEnd: "07:00", ArtifactDir: "memory/nocturne"}
	provider := &queuedProvider{replies: []string{
		"# Quiet hinge\n\nRelease review keeps returning as a durable concern.",
		`{"record":true,"category":"semantic_recurrence","subject_key":"release-review","summary":"Nocturne noticed release review returning as a durable concern.","confidence":0.72}`,
	}}
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 4, 30, 23, 30, 0, 0, time.Local)
	if err := rt.runNocturneTick(context.Background(), now); err != nil {
		t.Fatalf("runNocturneTick() err = %v", err)
	}

	observations, err := store.RecentInteriorSignalObservations(heartbeatSignalKey(), []session.InteriorSignalRef{{Category: hiddenInputSemanticRecurrence, SubjectKey: "release-review"}}, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("RecentInteriorSignalObservations() err = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %#v, want one nocturne signal", observations)
	}
	observation := observations[0]
	if observation.Source != "nocturne" || observation.AppliedWeight != nocturneSignalWeight {
		t.Fatalf("observation = %#v, want low-weight nocturne source", observation)
	}
	if len(observation.Evidence) != 1 || observation.Evidence[0].Kind != "nocturne_artifact" {
		t.Fatalf("evidence = %#v, want nocturne artifact reference", observation.Evidence)
	}
}

func TestNocturneOnlyPressureDoesNotTriggerOutreach(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	states := []session.InteriorSignalState{{
		Category:         hiddenInputSemanticRecurrence,
		SubjectKey:       "release-review",
		Intensity:        nocturneSignalWeight,
		ObservationCount: 1,
	}}
	if got := evaluateHeartbeatInteriorSignals(states, now); got.Eligible {
		t.Fatalf("nocturne-only pressure eligible = %#v, want false", got)
	}
}

type queuedProvider struct {
	replies []string
	calls   int
}

func (p *queuedProvider) Complete(context.Context, []agent.Message, []agent.ToolDef) (*agent.Response, error) {
	if p.calls >= len(p.replies) {
		p.calls++
		return &agent.Response{}, nil
	}
	reply := p.replies[p.calls]
	p.calls++
	return &agent.Response{Content: reply}, nil
}

func (p *queuedProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}
