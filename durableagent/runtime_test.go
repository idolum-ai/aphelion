//go:build linux

package durableagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func testDurableAgentBootstrapLLM() core.NodeLLMBootstrap {
	return core.NodeLLMBootstrap{
		Backend:        "native",
		NativeProvider: "openrouter",
		APIKey:         "sk-or-group",
		Model:          "openrouter/test-model",
	}
}

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
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "help the family group and escalate durable drift",
			CapabilityEnvelope: []string{"read_channel", "synthesize_review"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
		BootstrapLLM: testDurableAgentBootstrapLLM(),
		Status:       "active",
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
	if _, err := rt.QueueReviewArtifact(agent, artifact); err != nil {
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
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.RecentInteractions) != 1 {
		t.Fatalf("RecentInteractions len = %d, want 1", len(continuity.RecentInteractions))
	}
	if !strings.Contains(continuity.RecentInteractions[0].Summary, "Group pressure is recurring") {
		t.Fatalf("RecentInteractions[0].Summary = %q, want durable review summary", continuity.RecentInteractions[0].Summary)
	}
	if len(continuity.PendingQuestions) != 1 {
		t.Fatalf("PendingQuestions len = %d, want 1", len(continuity.PendingQuestions))
	}
	if !strings.Contains(continuity.PendingQuestions[0].Question, "Approve a broader family-group charter") {
		t.Fatalf("PendingQuestions[0].Question = %q, want promoted review question", continuity.PendingQuestions[0].Question)
	}
	if len(continuity.ReviewRefs) != 1 {
		t.Fatalf("ReviewRefs len = %d, want 1", len(continuity.ReviewRefs))
	}
	if continuity.ReviewRefs[0].ReviewEventID == 0 {
		t.Fatalf("ReviewRefs[0].ReviewEventID = %d, want non-zero review event id", continuity.ReviewRefs[0].ReviewEventID)
	}
	if len(continuity.ReviewRefs[0].RiskFlags) != 1 || continuity.ReviewRefs[0].RiskFlags[0] != "durable drift pressure" {
		t.Fatalf("ReviewRefs[0].RiskFlags = %#v, want preserved durable drift pressure", continuity.ReviewRefs[0].RiskFlags)
	}
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) != 1 {
		t.Fatalf("Conversation = %#v, want 1 child message", continuity.Conversation)
	}
	if continuity.Conversation.Messages[0].Role != "child" {
		t.Fatalf("Conversation.Messages[0].Role = %q, want child", continuity.Conversation.Messages[0].Role)
	}
	if !strings.Contains(continuity.Conversation.Messages[0].Text, "Group pressure is recurring") {
		t.Fatalf("Conversation.Messages[0].Text = %q, want artifact summary", continuity.Conversation.Messages[0].Text)
	}
}

func TestQueueReviewArtifactRedactsSecretLikeMetadataIntoForensicSidecar(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	workspaceRoot, memoryRoot := DefaultLocalRoots(filepath.Join(t.TempDir(), "sessions.db"), "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}

	rt := NewRuntime(store)
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "help the family group and escalate durable drift",
			CapabilityEnvelope: []string{"read_channel", "synthesize_review"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
		BootstrapLLM:      testDurableAgentBootstrapLLM(),
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	artifact := core.DurableReviewArtifact{
		AgentID:       agent.AgentID,
		Summary:       "Group pressure is recurring around credential exposure.",
		IntervalLabel: "messages 80-81",
		LocalActions:  []string{"Refused to accept the token as standing authority."},
		Questions:     []string{"Approve a broader secret scope?"},
		RiskFlags:     []string{"secret_request_pressure"},
		Metadata: map[string]string{
			"source_excerpt": "Use this password: super-secret-123 and keep it forever.",
			"local_response": "I will not store that password or use it as standing authority.",
		},
	}
	if _, err := rt.QueueReviewArtifact(agent, artifact); err != nil {
		t.Fatalf("QueueReviewArtifact() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending len = %d, want 1", len(events))
	}
	if strings.Contains(events[0].MetadataJSON, "super-secret-123") {
		t.Fatalf("MetadataJSON leaked secret-like content: %q", events[0].MetadataJSON)
	}
	if !strings.Contains(events[0].MetadataJSON, "forensic://durable-agent/family-group/") {
		t.Fatalf("MetadataJSON = %q, want forensic ref", events[0].MetadataJSON)
	}

	var payload struct {
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(events[0].MetadataJSON), &payload); err != nil {
		t.Fatalf("json.Unmarshal(metadata) err = %v", err)
	}
	ref := payload.Metadata["forensic_ref"]
	record, err := ReadForensicRecord(agent, ref)
	if err != nil {
		t.Fatalf("ReadForensicRecord() err = %v", err)
	}
	if record.Payload["source_excerpt"] != "Use this password: super-secret-123 and keep it forever." {
		t.Fatalf("forensic source_excerpt = %q, want preserved raw secret-bearing excerpt", record.Payload["source_excerpt"])
	}
	if _, ok := record.Payload["summary"]; ok {
		t.Fatalf("forensic payload unexpectedly redacted summary: %#v", record.Payload)
	}
	if !strings.Contains(payload.Metadata["source_excerpt"], "[REDACTED") {
		t.Fatalf("source_excerpt metadata = %q, want redacted marker", payload.Metadata["source_excerpt"])
	}
	if strings.Contains(events[0].MetadataJSON, "[REDACTED: summary]") {
		t.Fatalf("MetadataJSON = %q, summary should not redact credential concept mention", events[0].MetadataJSON)
	}
	if payload.Metadata["redacted_fields"] != "source_excerpt" {
		t.Fatalf("redacted_fields = %q, want source_excerpt only", payload.Metadata["redacted_fields"])
	}
}

func TestQueueReviewArtifactDoesNotRedactCredentialConceptSummary(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	rt := NewRuntime(store)
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		BootstrapLLM:       testDurableAgentBootstrapLLM(),
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	artifact := core.DurableReviewArtifact{
		AgentID:       agent.AgentID,
		Summary:       "What matters: mailbox adapter credential backend requires an interactive passphrase prompt; no TTY is available, so no mailbox credential was read.",
		IntervalLabel: "2026-05-08T02:50:01Z",
		LocalActions:  []string{"External-channel wake blocked; recorded explicit failure/backoff instead of success."},
		RiskFlags:     []string{"external_channel", "adapter_dispatch"},
		Metadata: map[string]string{
			"channel_kind":            "external_channel",
			"external_channel_status": "wake_blocked",
			"external_channel_error":  "mailbox adapter credential backend requires an interactive passphrase prompt; no TTY is available.",
		},
	}
	if _, err := rt.QueueReviewArtifact(agent, artifact); err != nil {
		t.Fatalf("QueueReviewArtifact() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending len = %d, want 1", len(events))
	}
	if strings.Contains(events[0].MetadataJSON, "[REDACTED: summary]") || strings.Contains(events[0].Summary, "[REDACTED: summary]") {
		t.Fatalf("event redacted safe credential concept summary: summary=%q metadata=%q", events[0].Summary, events[0].MetadataJSON)
	}
	if strings.Contains(events[0].MetadataJSON, "forensic://") {
		t.Fatalf("MetadataJSON = %q, did not want forensic sidecar for concept-only summary", events[0].MetadataJSON)
	}
	if !strings.Contains(events[0].MetadataJSON, `"redaction_action":"none"`) || !strings.Contains(events[0].MetadataJSON, `"redaction_reason":"secret_concept_without_value"`) {
		t.Fatalf("MetadataJSON = %q, want typed no-redaction decision for credential concept mention", events[0].MetadataJSON)
	}
}

func TestQueueReviewArtifactRedactsConcreteSecretValueInSummary(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	workspaceRoot, memoryRoot := DefaultLocalRoots(filepath.Join(t.TempDir(), "sessions.db"), "mail-child")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}

	rt := NewRuntime(store)
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		BootstrapLLM:       testDurableAgentBootstrapLLM(),
		LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	rawSummary := "External-channel wake blocked after a child reported token: sk-testSECRETabcdef123456 in adapter output."
	artifact := core.DurableReviewArtifact{
		AgentID:       agent.AgentID,
		Summary:       rawSummary,
		IntervalLabel: "2026-05-08T02:50:01Z",
		Metadata: map[string]string{
			"channel_kind":            "external_channel",
			"external_channel_status": "wake_blocked",
			"external_channel_error":  "adapter output contained a concrete token value",
		},
	}
	if _, err := rt.QueueReviewArtifact(agent, artifact); err != nil {
		t.Fatalf("QueueReviewArtifact() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending len = %d, want 1", len(events))
	}
	if strings.Contains(events[0].MetadataJSON, "sk-testSECRET") || strings.Contains(events[0].Summary, "sk-testSECRET") {
		t.Fatalf("event leaked concrete secret summary: summary=%q metadata=%q", events[0].Summary, events[0].MetadataJSON)
	}

	var payload struct {
		Summary  string            `json:"summary"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(events[0].MetadataJSON), &payload); err != nil {
		t.Fatalf("json.Unmarshal(metadata) err = %v", err)
	}
	if payload.Summary != "[REDACTED: summary]" {
		t.Fatalf("metadata summary = %q, want redacted marker", payload.Summary)
	}
	if payload.Metadata["operator_summary"] == "" || strings.Contains(payload.Metadata["operator_summary"], "sk-testSECRET") {
		t.Fatalf("operator_summary = %q, want safe synthetic summary", payload.Metadata["operator_summary"])
	}
	if payload.Metadata["safe_operator_summary"] != payload.Metadata["operator_summary"] {
		t.Fatalf("safe_operator_summary = %q, want %q", payload.Metadata["safe_operator_summary"], payload.Metadata["operator_summary"])
	}
	ref := payload.Metadata["forensic_ref"]
	record, err := ReadForensicRecord(agent, ref)
	if err != nil {
		t.Fatalf("ReadForensicRecord() err = %v", err)
	}
	if record.Payload["summary"] != rawSummary {
		t.Fatalf("forensic summary = %q, want raw summary", record.Payload["summary"])
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

func TestQueueReviewArtifactReturnsErrorForInvalidContinuityJSON(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		BootstrapLLM:       testDurableAgentBootstrapLLM(),
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: `{not json`}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	_, err := NewRuntime(store).QueueReviewArtifact(agent, core.DurableReviewArtifact{Summary: "Child report."})
	if err == nil {
		t.Fatal("QueueReviewArtifact() err = nil, want invalid continuity JSON error")
	}
	if !strings.Contains(err.Error(), "parse durable agent continuity state") {
		t.Fatalf("QueueReviewArtifact() err = %v, want parse continuity context", err)
	}
}

func TestReadForensicRecordRejectsCrossAgentAndInvalidRefs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	agent := core.DurableAgent{AgentID: "child-a", LocalStorageRoots: []string{filepath.Join(root, "work-a"), filepath.Join(root, "mem-a")}}
	other := core.DurableAgent{AgentID: "child-b", LocalStorageRoots: []string{filepath.Join(root, "work-b"), filepath.Join(root, "mem-b")}}
	ref, err := WriteForensicRecord(agent, ForensicRecord{AgentID: agent.AgentID, Reason: "test", CreatedAt: time.Now().UTC(), Payload: map[string]string{"summary": "raw"}})
	if err != nil {
		t.Fatalf("WriteForensicRecord() err = %v", err)
	}
	if _, err := ReadForensicRecord(other, ref); err == nil {
		t.Fatal("ReadForensicRecord(cross-agent) err = nil, want rejection")
	}
	for _, badRef := range []string{"", "forensic://durable-agent/child-a/../secret.json", "file://durable-agent/child-a/x.json"} {
		if _, err := ReadForensicRecord(agent, badRef); err == nil {
			t.Fatalf("ReadForensicRecord(%q) err = nil, want invalid ref", badRef)
		}
	}
}
