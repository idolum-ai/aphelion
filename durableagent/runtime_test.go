//go:build linux

package durableagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestQueueReviewArtifactReusesReviewQueue(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	rt := NewRuntime(store)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Charter:            "help the family group and escalate durable drift",
		CapabilityEnvelope: []string{"read_channel", "synthesize_review"},
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	artifact := core.DurableReviewArtifact{
		AgentID:       agent.AgentID,
		Summary:       "Group pressure is recurring around a permanent tone change.",
		IntervalLabel: "messages 41-58",
		LocalActions:  []string{"declined to widen standing tone policy locally"},
		Questions:     []string{"Approve a broader family-group charter?"},
		RiskFlags:     []string{"durable drift pressure"},
		ArtifactRefs:  []string{"artifact://telegram/family-group/thread-12"},
	}
	if err := rt.QueueReviewArtifact(agent, artifact); err != nil {
		t.Fatalf("QueueReviewArtifact() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending len = %d, want 1", len(events))
	}
	event := events[0]
	if event.SourceRole != "durable_agent" {
		t.Fatalf("SourceRole = %q, want durable_agent", event.SourceRole)
	}
	if event.SourceScope.Kind != session.ScopeKindDurableAgent || event.SourceScope.ID != agent.AgentID {
		t.Fatalf("SourceScope = %#v, want durable_agent %q", event.SourceScope, agent.AgentID)
	}
	if event.TargetScope.Kind != session.ScopeKindTelegramDM || event.TargetScope.ID != "1001" {
		t.Fatalf("TargetScope = %#v, want telegram_dm 1001", event.TargetScope)
	}
	if !strings.Contains(event.Summary, "Group pressure is recurring") {
		t.Fatalf("Summary = %q, want durable review summary", event.Summary)
	}
	if !strings.Contains(event.MetadataJSON, "durable drift pressure") {
		t.Fatalf("MetadataJSON = %q, want risk flag", event.MetadataJSON)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastReviewAt.IsZero() {
		t.Fatal("LastReviewAt is zero, want queueing review artifact to update agent state")
	}
}

func newTestSQLiteStore(t *testing.T) *session.SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	return store
}
