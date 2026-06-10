//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestCuriosityToolRegistryRequiresSelectedCandidate(t *testing.T) {
	base := &stubToolRegistry{defs: []agent.ToolDef{{Name: "read_file"}, {Name: "exec"}}}
	registry := &curiosityToolRegistry{
		base: base,
		candidate: curiosityCandidate{
			ToolName:  "read_file",
			ToolInput: json.RawMessage(`{"path":"README.md","full":true}`),
		},
	}
	if defs := registry.Definitions(); len(defs) != 1 || defs[0].Name != "read_file" {
		t.Fatalf("Definitions() = %#v, want only selected read_file", defs)
	}
	if _, err := registry.Execute(context.Background(), "read_file", json.RawMessage(`{"full":true,"path":"README.md"}`)); err != nil {
		t.Fatalf("Execute(selected) err = %v", err)
	}
	if _, err := registry.Execute(context.Background(), "read_file", json.RawMessage(`{"full":true,"path":"OTHER.md"}`)); err == nil || !strings.Contains(err.Error(), "input does not match") {
		t.Fatalf("Execute(drifted input) err = %v, want candidate input rejection", err)
	}
	if _, err := registry.Execute(context.Background(), "exec", json.RawMessage(`{"command":"pwd"}`)); err == nil || !strings.Contains(err.Error(), "not the selected") {
		t.Fatalf("Execute(exec) err = %v, want selected tool rejection", err)
	}
}

func TestRunCuriosityOnceRecordsSilentObservation(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Curiosity.Enabled = true
	cfg.Curiosity.Every = "1h"
	cfg.Curiosity.LeaseTTL = "24h"
	cfg.Curiosity.DailyTurnBudget = 1
	cfg.Curiosity.MaxLooksPerTurn = 1
	cfg.Curiosity.MinSignalIntensity = 0.5
	cfg.Curiosity.SourceClasses = []string{session.CuriositySourceWorkspace}
	cfg.Curiosity.WorkspacePaths = []string{"README.md"}

	provider := &curiosityRunProvider{}
	tools := &curiosityRecordingTools{}
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	_, err = store.RecordInteriorSignalObservations(session.SessionKey{ChatID: heartbeatSessionChatID, Scope: heartbeatScopeRef()}, []session.InteriorSignalObservationInput{{
		Category:   hiddenInputSemanticRecurrence,
		SubjectKey: "release-v023",
		Summary:    "Release v0.2.3 keeps recurring in recent work.",
		Source:     "test",
		Weight:     0.8,
		Confidence: 0.9,
		ObservedAt: now,
	}}, now)
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations() err = %v", err)
	}

	if err := rt.runCuriosityOnce(context.Background(), now); err != nil {
		t.Fatalf("runCuriosityOnce() err = %v", err)
	}
	observations, err := store.CuriosityObservations(10)
	if err != nil {
		t.Fatalf("CuriosityObservations() err = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(observations))
	}
	if observations[0].SourceKind != session.CuriositySourceWorkspace || observations[0].SourceRef != "README.md" {
		t.Fatalf("observation source = %s/%s, want workspace README.md", observations[0].SourceKind, observations[0].SourceRef)
	}
	if !strings.Contains(observations[0].Summary, "release") {
		t.Fatalf("observation summary = %q, want provider observation", observations[0].Summary)
	}
	if len(tools.calls) != 1 || tools.calls[0].name != "read_file" {
		t.Fatalf("tool calls = %#v, want one read_file", tools.calls)
	}
	sender.mu.Lock()
	sent := len(sender.sent)
	sender.mu.Unlock()
	if sent != 0 {
		t.Fatalf("outbound messages = %d, want silent curiosity observation", sent)
	}
}

func TestCuriosityMemoryToolPathUsesSharedRoot(t *testing.T) {
	got := curiosityMemoryToolPath("/srv/aphelion/agent", "memory/questions.md")
	if got != "/srv/aphelion/agent/memory/questions.md" {
		t.Fatalf("curiosityMemoryToolPath() = %q, want shared-root absolute path", got)
	}
	absolute := curiosityMemoryToolPath("/srv/aphelion/agent", "/tmp/explicit.md")
	if absolute != "/tmp/explicit.md" {
		t.Fatalf("curiosityMemoryToolPath(abs) = %q, want unchanged absolute path", absolute)
	}
}

type curiosityRunProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *curiosityRunProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		if len(tools) != 1 || tools[0].Name != "read_file" {
			return &agent.Response{Content: `{"summary":"no read tool available","confidence":0.1}`}, nil
		}
		return &agent.Response{ToolCalls: []agent.ToolCall{{
			ID:    "curiosity-read",
			Name:  "read_file",
			Input: json.RawMessage(`{"path":"README.md","max_bytes":32768,"full":true}`),
		}}}, nil
	}
	return &agent.Response{Content: `{"summary":"README still ties release work to safer continuation.","subject_key":"release-v023","confidence":0.83,"evidence":[{"kind":"source","ref":"README.md"}]}`}, nil
}

func (p *curiosityRunProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

type curiosityRecordingTools struct {
	mu    sync.Mutex
	calls []curiosityToolCall
}

type curiosityToolCall struct {
	name  string
	input string
}

func (t *curiosityRecordingTools) Definitions() []agent.ToolDef {
	return []agent.ToolDef{{Name: "read_file"}, {Name: "exec"}}
}

func (t *curiosityRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}

func (t *curiosityRecordingTools) ExecuteForPrincipal(_ context.Context, _ principal.Principal, name string, input json.RawMessage) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, curiosityToolCall{name: strings.TrimSpace(name), input: strings.TrimSpace(string(input))})
	return "Release v0.2.3 notes mention safer continuation.", nil
}

func (t *curiosityRecordingTools) SupportsPrincipal(principal.Principal) bool {
	return true
}

func (t *curiosityRecordingTools) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	return strings.TrimSpace(name) == "read_file"
}
