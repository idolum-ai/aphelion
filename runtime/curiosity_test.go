//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
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
	if tools.calls[0].role != principal.RoleApprovedUser {
		t.Fatalf("tool principal role = %q, want approved_user curiosity lane", tools.calls[0].role)
	}
	if observations[0].SubjectKey != "release-v023" {
		t.Fatalf("observation subject key = %q, want runtime candidate key", observations[0].SubjectKey)
	}
	if observations[0].ContentHash == "sha256:model-2" || !strings.HasPrefix(observations[0].ContentHash, "sha256:") {
		t.Fatalf("observation content hash = %q, want runtime tool-output hash", observations[0].ContentHash)
	}
	sender.mu.Lock()
	sent := len(sender.sent)
	sender.mu.Unlock()
	if sent != 0 {
		t.Fatalf("outbound messages = %d, want silent curiosity observation", sent)
	}
}

func TestRunCuriosityOnceRuntimeHashDedupeIgnoresModelHashDrift(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	cfg.Curiosity.Enabled = true
	cfg.Curiosity.Every = "1h"
	cfg.Curiosity.LeaseTTL = "24h"
	cfg.Curiosity.DailyTurnBudget = 2
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
		t.Fatalf("runCuriosityOnce(first) err = %v", err)
	}
	if err := rt.runCuriosityOnce(context.Background(), now.Add(time.Hour)); err != nil {
		t.Fatalf("runCuriosityOnce(second) err = %v", err)
	}
	observations, err := store.CuriosityObservations(10)
	if err != nil {
		t.Fatalf("CuriosityObservations() err = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want one deduped runtime-hash row", len(observations))
	}
}

func TestCuriosityCandidatesRequireNonCuriositySupport(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Curiosity.Enabled = true
	cfg.Curiosity.MinSignalIntensity = 0.5
	cfg.Curiosity.SourceClasses = []string{session.CuriositySourceWorkspace}
	cfg.Curiosity.WorkspacePaths = []string{"README.md"}

	rt, err := New(cfg, store, provider, &curiosityRecordingTools{}, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	_, err = store.RecordInteriorSignalObservations(session.SessionKey{ChatID: heartbeatSessionChatID, Scope: heartbeatScopeRef()}, []session.InteriorSignalObservationInput{{
		Category:   hiddenInputSemanticRecurrence,
		SubjectKey: "self-fed",
		Summary:    "curiosity observed itself",
		Source:     "curiosity",
		Weight:     0.8,
		Confidence: 0.9,
		ObservedAt: now,
	}}, now)
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations() err = %v", err)
	}
	candidates, err := rt.curiosityCandidates(&curiosityRecordingTools{}, "", now)
	if err != nil {
		t.Fatalf("curiosityCandidates() err = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want curiosity-only pressure excluded", candidates)
	}
}

func TestCuriosityCandidatesRequireCurrentIndependentSupport(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Curiosity.Enabled = true
	cfg.Curiosity.MinSignalIntensity = 0.5
	cfg.Curiosity.SourceClasses = []string{session.CuriositySourceWorkspace}
	cfg.Curiosity.WorkspacePaths = []string{"README.md"}

	rt, err := New(cfg, store, provider, &curiosityRecordingTools{}, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: heartbeatSessionChatID, Scope: heartbeatScopeRef()}
	_, err = store.RecordInteriorSignalObservations(key, []session.InteriorSignalObservationInput{
		{
			Category:          hiddenInputSemanticRecurrence,
			SubjectKey:        "stale-seed",
			Summary:           "old non-curiosity support",
			Source:            "heartbeat_reflection",
			SourceFingerprint: "old-support",
			Weight:            0.8,
			Confidence:        0.9,
			ObservedAt:        now.Add(-10 * 24 * time.Hour),
		},
		{
			Category:          hiddenInputSemanticRecurrence,
			SubjectKey:        "stale-seed",
			Summary:           "fresh curiosity pressure",
			Source:            "curiosity",
			SourceFingerprint: "fresh-curiosity",
			Weight:            0.8,
			Confidence:        0.9,
			ObservedAt:        now,
		},
	}, now)
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations() err = %v", err)
	}
	candidates, err := rt.curiosityCandidates(&curiosityRecordingTools{}, "", now)
	if err != nil {
		t.Fatalf("curiosityCandidates() err = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want stale non-curiosity support decayed out", candidates)
	}
}

func TestCuriosityURLEvidenceTagsThirdPartyText(t *testing.T) {
	refs := curiosityEvidence(curiosityCandidate{
		ID:         "candidate-url",
		SourceKind: session.CuriositySourceURL,
		SourceRef:  "https://example.com/feed",
		ToolName:   "fetch_url",
	}, "sha256:tool", nil)
	if !recordRefsContain(refs, "untrusted_external_source", "https://example.com/feed") {
		t.Fatalf("evidence = %#v, want untrusted external provenance", refs)
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
	if p.calls%2 == 1 {
		if len(tools) != 1 || tools[0].Name != "read_file" {
			return &agent.Response{Content: `{"summary":"no read tool available","confidence":0.1}`}, nil
		}
		return &agent.Response{ToolCalls: []agent.ToolCall{{
			ID:    "curiosity-read",
			Name:  "read_file",
			Input: json.RawMessage(`{"path":"README.md","max_bytes":32768,"full":true}`),
		}}}, nil
	}
	return &agent.Response{Content: fmt.Sprintf(`{"summary":"README still ties release work to safer continuation.","subject_key":"model-drift-%d","confidence":0.83,"content_hash":"sha256:model-%d","evidence":[{"kind":"source","ref":"README.md"}]}`, p.calls, p.calls)}, nil
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
	role  principal.Role
}

func (t *curiosityRecordingTools) Definitions() []agent.ToolDef {
	return []agent.ToolDef{{Name: "read_file"}, {Name: "exec"}}
}

func (t *curiosityRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	return "", nil
}

func (t *curiosityRecordingTools) ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error) {
	return t.ExecuteForSessionPrincipal(ctx, p, session.SessionKey{}, name, input)
}

func (t *curiosityRecordingTools) ExecuteForSessionPrincipal(_ context.Context, p principal.Principal, _ session.SessionKey, name string, input json.RawMessage) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, curiosityToolCall{name: strings.TrimSpace(name), input: strings.TrimSpace(string(input)), role: p.Role})
	return "Release v0.2.3 notes mention safer continuation.", nil
}

func (t *curiosityRecordingTools) SupportsPrincipal(principal.Principal) bool {
	return true
}

func (t *curiosityRecordingTools) SupportsParallelToolCall(name string, input json.RawMessage) bool {
	return strings.TrimSpace(name) == "read_file"
}

func recordRefsContain(refs []session.RecordReference, kind string, ref string) bool {
	for _, item := range refs {
		if item.Kind == kind && item.Ref == ref {
			return true
		}
	}
	return false
}
