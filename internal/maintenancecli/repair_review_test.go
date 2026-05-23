//go:build linux

package maintenancecli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func TestRepairReviewRedactionsRestoresConceptOnlySummary(t *testing.T) {
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "mail-child")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1",
		ReviewTargetChatID: 1,
		ChannelKind:        "external_channel",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rawSummary := "External-channel wake blocked because mailbox adapter credential backend requires an interactive passphrase prompt; no TTY is available."
	ref, err := durableagent.WriteForensicRecord(agent, durableagent.ForensicRecord{
		AgentID:        agent.AgentID,
		Reason:         "secret_like_material",
		CreatedAt:      time.Now().UTC(),
		RedactedFields: []string{"summary"},
		Payload: map[string]string{
			"summary": rawSummary,
		},
	})
	if err != nil {
		t.Fatalf("WriteForensicRecord() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: agent.AgentID, DurableAgentID: agent.AgentID},
		TargetAdminChatID: 1,
		Summary:           "durable_agent=mail-child channel=external_channel\nsummary: [REDACTED: summary]\nrisks: external_channel",
		MetadataJSON:      `{"agent_id":"mail-child","summary":"[REDACTED: summary]","interval_label":"2026-05-08T02:50:01Z","risk_flags":["external_channel"],"artifact_refs":["` + ref + `"],"metadata":{"channel_kind":"external_channel","external_channel_status":"wake_blocked","forensic_ref":"` + ref + `","redacted_fields":"summary","redaction_action":"quarantined_fields","redaction_source":"deterministic","redaction_reason":"concrete_secret_value"}}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	result, err := repairReviewRedactions(context.Background(), store, 10, false, time.Date(2026, 5, 8, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("repairReviewRedactions() err = %v", err)
	}
	if result.Inspected != 1 || result.Repaired != 1 || result.StillRedacted != 0 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one repaired row", result)
	}
	updated, err := store.ReviewEventByID(eventID)
	if err != nil {
		t.Fatalf("ReviewEventByID() err = %v", err)
	}
	if strings.Contains(updated.Summary, "[REDACTED: summary]") || !strings.Contains(updated.Summary, "passphrase prompt") {
		t.Fatalf("updated summary = %q, want restored concept-only summary", updated.Summary)
	}
	if strings.Contains(updated.MetadataJSON, `"summary":"[REDACTED: summary]"`) {
		t.Fatalf("metadata still has redacted summary: %q", updated.MetadataJSON)
	}
	if !strings.Contains(updated.MetadataJSON, `"redaction_action":"none"`) || !strings.Contains(updated.MetadataJSON, `"redaction_source":"maintenance_repair"`) {
		t.Fatalf("metadata = %q, want maintenance repair decision", updated.MetadataJSON)
	}
}

func TestRepairReviewRedactionsLeavesConcreteSecretSummaryRedacted(t *testing.T) {
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "mail-child")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1",
		ReviewTargetChatID: 1,
		ChannelKind:        "external_channel",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	ref, err := durableagent.WriteForensicRecord(agent, durableagent.ForensicRecord{
		AgentID:        agent.AgentID,
		Reason:         "secret_like_material",
		CreatedAt:      time.Now().UTC(),
		RedactedFields: []string{"summary"},
		Payload: map[string]string{
			"summary": "External adapter printed token: sk-testSECRETabcdef123456",
		},
	})
	if err != nil {
		t.Fatalf("WriteForensicRecord() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: agent.AgentID, DurableAgentID: agent.AgentID},
		TargetAdminChatID: 1,
		Summary:           "durable_agent=mail-child channel=external_channel\nsummary: [REDACTED: summary]",
		MetadataJSON:      `{"agent_id":"mail-child","summary":"[REDACTED: summary]","metadata":{"channel_kind":"external_channel","forensic_ref":"` + ref + `","redacted_fields":"summary"}}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	result, err := repairReviewRedactions(context.Background(), store, 10, false, time.Date(2026, 5, 8, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("repairReviewRedactions() err = %v", err)
	}
	if result.Inspected != 1 || result.Repaired != 0 || result.StillRedacted != 1 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one still-redacted row", result)
	}
	updated, err := store.ReviewEventByID(eventID)
	if err != nil {
		t.Fatalf("ReviewEventByID() err = %v", err)
	}
	if !strings.Contains(updated.Summary, "[REDACTED: summary]") || strings.Contains(updated.MetadataJSON, "sk-testSECRET") {
		t.Fatalf("updated event = %#v, want concrete secret to remain redacted", updated)
	}
}
