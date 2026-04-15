//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestHeartbeatTargetNoneStoresMaintenanceWithoutOutbound(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "none"
	provider.replyText = "heartbeat canonical"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      222,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          1,
		TurnTo:            1,
		Summary:           "user is asking for help",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
	sender.mu.Unlock()

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()})
	if err != nil {
		t.Fatalf("Load(heartbeat session) err = %v", err)
	}
	if maintenance.LastFloorText != "heartbeat canonical" {
		t.Fatalf("maintenance floor = %q, want heartbeat canonical", maintenance.LastFloorText)
	}
	if len(maintenance.Messages) == 0 || maintenance.Messages[len(maintenance.Messages)-1].Content != "heartbeat canonical" {
		t.Fatalf("maintenance messages = %#v, want canonical heartbeat entry", maintenance.Messages)
	}
	if len(maintenance.Messages) != 2 || maintenance.Messages[0].Role != "user" || maintenance.Messages[1].Role != "assistant" {
		t.Fatalf("maintenance message roles = %#v, want synthetic user + assistant", maintenance.Messages)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
}

func TestHeartbeatDeliveryUsesFaceAndMarksReviewEventsDelivered(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "last"
	provider.replyText = "heartbeat canonical"
	provider.proposalReplyText = "A recurring deployment blocker keeps surfacing. Name it."
	provider.faceReplyText = "heartbeat rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "questions.md"), []byte("# questions.md\n\n- Should deployment rollback become a first-class workflow?"), 0o600); err != nil {
		t.Fatalf("write questions.md: %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      333,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          2,
		TurnTo:            2,
		Summary:           "deployment rollback needs review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}
	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      334,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          3,
		TurnTo:            3,
		Summary:           "deployment plan needs review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || sender.sent[0].Text != "heartbeat rendered" {
		t.Fatalf("sent = %#v, want rendered heartbeat to admin", sender.sent[0])
	}
	sender.mu.Unlock()

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.LastFloorText != "heartbeat canonical" {
		t.Fatalf("admin floor = %q, want heartbeat canonical", adminSession.LastFloorText)
	}
	if len(adminSession.Messages) == 0 || adminSession.Messages[len(adminSession.Messages)-1].Content != "heartbeat rendered" {
		t.Fatalf("admin messages = %#v, want rendered heartbeat entry", adminSession.Messages)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].FloorContent != "heartbeat canonical" {
		t.Fatalf("admin floor content = %q, want heartbeat canonical", adminSession.Messages[len(adminSession.Messages)-1].FloorContent)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("pending review events len = %d, want 0 after delivery", len(events))
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want hidden-input proposal for heartbeat outreach")
	}
	if !strings.Contains(provider.seenProposalSystem[len(provider.seenProposalSystem)-1], "- hidden_inputs_active: true") {
		t.Fatalf("heartbeat proposal prompt missing hidden-input awareness: %q", provider.seenProposalSystem[len(provider.seenProposalSystem)-1])
	}
	if !strings.Contains(provider.seenProposalSystem[len(provider.seenProposalSystem)-1], "semantic_recurrence") {
		t.Fatalf("heartbeat proposal prompt missing hidden-input categories: %q", provider.seenProposalSystem[len(provider.seenProposalSystem)-1])
	}
}

func TestHeartbeatDeliveryFaceFailureUsesSerializedFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "last"
	provider.replyText = strings.Join([]string{
		"FACTS:",
		"- Deployment readiness is still unresolved.",
		"COMMITMENTS:",
		"- Surface the blocker directly.",
		"SCENE_CONSTRAINTS:",
		"- Do not sound dramatic.",
	}, "\n")
	provider.proposalReplyText = "Name the unresolved blocker."
	provider.faceErr = errors.New("face unavailable")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      335,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          4,
		TurnTo:            4,
		Summary:           "deployment readiness needs review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}
	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      336,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          5,
		TurnTo:            5,
		Summary:           "deployment readiness still needs review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	want := strings.Join([]string{
		"What matters:",
		"- Deployment readiness is still unresolved.",
		"",
		"Committed:",
		"- Surface the blocker directly.",
	}, "\n")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != want {
		t.Fatalf("sent = %#v, want serialized heartbeat fallback %q", sender.sent[0], want)
	}
}

func TestHeartbeatDeliveryCanTriggerFromLatentStateWithoutReviewEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "last"
	cfg.Agent.DailyNotes = true
	provider.replyText = "latent heartbeat floor"
	provider.proposalReplyText = "Something unresolved keeps surfacing around deployment readiness."
	provider.faceReplyText = "latent heartbeat scene"

	noteDir := filepath.Join(cfg.Agent.SharedMemoryRoot, cfg.Agent.DailyNotesDir)
	if err := os.MkdirAll(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.MkdirAll(noteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(noteDir) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "questions.md"), []byte("# questions.md\n\n- Should deployment readiness become a first-class heartbeat concern?"), 0o600); err != nil {
		t.Fatalf("write questions.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteDir, "2026-04-09.md"), []byte("Deployment readiness still feels unresolved."), 0o600); err != nil {
		t.Fatalf("write today note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteDir, "2026-04-08.md"), []byte("Need to revisit deployment readiness before the week closes."), 0o600); err != nil {
		t.Fatalf("write yesterday note: %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 || sender.sent[0].Text != "latent heartbeat scene" {
		t.Fatalf("sent = %#v, want latent heartbeat scene to admin", sender.sent[0])
	}
	sender.mu.Unlock()

	adminSession, err := store.Load(session.SessionKey{ChatID: 1001, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if adminSession.LastFloorText != "latent heartbeat floor" {
		t.Fatalf("admin floor = %q, want latent heartbeat floor", adminSession.LastFloorText)
	}
	if len(adminSession.Messages) == 0 || adminSession.Messages[len(adminSession.Messages)-1].Content != "latent heartbeat scene" {
		t.Fatalf("admin messages = %#v, want latent heartbeat scene entry", adminSession.Messages)
	}
	if adminSession.Messages[len(adminSession.Messages)-1].FloorContent != "latent heartbeat floor" {
		t.Fatalf("admin floor content = %q, want latent heartbeat floor", adminSession.Messages[len(adminSession.Messages)-1].FloorContent)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want hidden-input proposal for latent heartbeat outreach")
	}
	if !strings.Contains(provider.seenProposalSystem[len(provider.seenProposalSystem)-1], "- hidden_inputs_active: true") {
		t.Fatalf("heartbeat proposal prompt missing hidden-input awareness: %q", provider.seenProposalSystem[len(provider.seenProposalSystem)-1])
	}
	if !strings.Contains(provider.seenProposalSystem[len(provider.seenProposalSystem)-1], "semantic_recurrence") {
		t.Fatalf("heartbeat proposal prompt missing semantic recurrence category: %q", provider.seenProposalSystem[len(provider.seenProposalSystem)-1])
	}
	if !strings.Contains(provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1].Content, "There are no pending review events this turn.") {
		t.Fatalf("heartbeat request missing latent-state-only marker: %q", provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1].Content)
	}
}

func TestHeartbeatStaysSilentWithoutConvergingSignals(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "last"
	provider.replyText = "heartbeat canonical"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      333,
		SourceUserID:      1002,
		SourceRole:        "approved_user",
		TargetAdminChatID: 1001,
		TurnFrom:          2,
		TurnTo:            2,
		Summary:           "single isolated review item",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0 when signals do not converge", len(sender.sent))
	}
	sender.mu.Unlock()

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()})
	if err != nil {
		t.Fatalf("Load(heartbeat session) err = %v", err)
	}
	if maintenance.LastFloorText != "heartbeat canonical" {
		t.Fatalf("maintenance floor = %q, want heartbeat canonical", maintenance.LastFloorText)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1 when heartbeat stays silent", len(events))
	}
}

func TestHeartbeatReflectionWritesCuratedMemoryFromDailyNotes(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "none"
	cfg.Agent.DailyNotes = true
	noteDir := filepath.Join(cfg.Agent.SharedMemoryRoot, cfg.Agent.DailyNotesDir)
	if err := os.MkdirAll(noteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(noteDir) err = %v", err)
	}
	notePath := filepath.Join(noteDir, "2026-04-09.md")
	if err := os.WriteFile(notePath, []byte("Daniel prefers concise updates and wants durable memory."), 0o600); err != nil {
		t.Fatalf("write daily note: %v", err)
	}
	provider.reflectionReplyText = strings.Join([]string{
		"[MEMORY]",
		"Keep concise progress updates near the top of long tasks.",
		"[/MEMORY]",
		"[KNOWLEDGE]",
		"- Prefers concise progress updates [observed, confidence: 0.90]",
		"[/KNOWLEDGE]",
		"[DECISIONS]",
		"- Use heartbeat reflection for durable note distillation.",
		"[/DECISIONS]",
		"[QUESTIONS]",
		"- Should session search surface recalled snippets by default?",
		"[/QUESTIONS]",
		"[RHIZOME]",
		"- heartbeat <-> memory distillation <-> continuity",
		"[/RHIZOME]",
	}, "\n")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	for _, check := range []struct {
		path string
		want string
	}{
		{filepath.Join(cfg.Agent.SharedMemoryRoot, "MEMORY.md"), "Keep concise progress updates near the top of long tasks."},
		{filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "knowledge.md"), "Prefers concise progress updates"},
		{filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "decisions.md"), "Use heartbeat reflection"},
		{filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "questions.md"), "Should session search"},
		{filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "rhizome.md"), "heartbeat <-> memory distillation"},
	} {
		raw, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("ReadFile(%s) err = %v", check.path, err)
		}
		if !strings.Contains(string(raw), check.want) {
			t.Fatalf("%s = %q, want substring %q", check.path, string(raw), check.want)
		}
	}
	rhizomeRaw, err := os.ReadFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "rhizome.md"))
	if err != nil {
		t.Fatalf("ReadFile(rhizome.md) err = %v", err)
	}
	rhizomeText := string(rhizomeRaw)
	if !strings.Contains(rhizomeText, "memory distillation") {
		t.Fatalf("rhizome.md = %q, want projected association text", rhizomeText)
	}
	if !strings.Contains(rhizomeText, "strength:") {
		t.Fatalf("rhizome.md = %q, want graph projection metadata", rhizomeText)
	}

	sender.mu.Lock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0 for reflection-only heartbeat", len(sender.sent))
	}
	sender.mu.Unlock()

	maintenance, err := store.Load(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()})
	if err != nil {
		t.Fatalf("Load(heartbeat session) err = %v", err)
	}
	if len(maintenance.Messages) != 2 {
		t.Fatalf("maintenance messages len = %d, want 2", len(maintenance.Messages))
	}
	if maintenance.Messages[0].Content != "[heartbeat reflection]" {
		t.Fatalf("maintenance user content = %q, want reflection marker", maintenance.Messages[0].Content)
	}
	if !strings.Contains(maintenance.Messages[1].Content, "Reflected curated memory updates for:") {
		t.Fatalf("maintenance reply = %q, want reflection summary", maintenance.Messages[1].Content)
	}
}

func TestHeartbeatReflectionAddsSemanticContext(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Target = "none"
	cfg.Agent.DailyNotes = true
	cfg.Memory.Semantic.Enabled = true
	cfg.Memory.Semantic.Sources = []string{"memory/knowledge.md"}
	cfg.Memory.Semantic.IncludeDailyNotes = true
	cfg.Memory.Semantic.HeartbeatTopK = 12
	cfg.Memory.Semantic.HeartbeatMaxChars = 12000

	noteDir := filepath.Join(cfg.Agent.SharedMemoryRoot, cfg.Agent.DailyNotesDir)
	if err := os.MkdirAll(noteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(noteDir) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteDir, "2026-04-09.md"), []byte("Need to preserve the user's preference for concise progress updates."), 0o600); err != nil {
		t.Fatalf("write daily note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Agent.SharedMemoryRoot, "memory", "knowledge.md"), []byte("# knowledge.md\n\n- Prefers concise progress updates [observed, confidence: 0.90]"), 0o600); err != nil {
		t.Fatalf("write knowledge: %v", err)
	}
	provider.reflectionReplyText = strings.Join([]string{
		"[MEMORY]", "[/MEMORY]",
		"[KNOWLEDGE]", "[/KNOWLEDGE]",
		"[DECISIONS]", "[/DECISIONS]",
		"[QUESTIONS]", "[/QUESTIONS]",
		"[RHIZOME]", "[/RHIZOME]",
	}, "\n")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := rt.runHeartbeatOnce(context.Background(), time.Date(2026, time.April, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("runHeartbeatOnce() err = %v", err)
	}

	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("lastGovernorMsgs empty, want reflection request")
	}
	request := provider.lastGovernorMsgs[len(provider.lastGovernorMsgs)-1].Content
	if !strings.Contains(request, "## Semantic Context") {
		t.Fatalf("reflection request = %q, want semantic context section", request)
	}
	if !strings.Contains(request, "Prefers concise progress updates") {
		t.Fatalf("reflection request = %q, want semantic knowledge hit", request)
	}
}
