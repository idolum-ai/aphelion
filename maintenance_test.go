//go:build linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	_ "github.com/mattn/go-sqlite3"
)

var captureStdoutMu sync.Mutex

func TestSeedAgentPromptFilesSeedsStructuredMemoryFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			PromptRoot:       filepath.Join(root, "agent"),
			SharedMemoryRoot: filepath.Join(root, "agent"),
			DailyNotes:       true,
			DailyNotesDir:    "memory/daily",
		},
	}

	created, err := seedAgentPromptFiles(cfg)
	if err != nil {
		t.Fatalf("seedAgentPromptFiles() err = %v", err)
	}
	if len(created) == 0 {
		t.Fatal("seedAgentPromptFiles() created no files, want defaults")
	}

	for _, rel := range []string{
		"SOUL.md",
		"IDENTITY.md",
		"USER.md",
		"AGENTS.md",
		"TOOLS.md",
		"IDOLUM.md",
		"QUESTIONS-TO-IDOLUM.md",
		"MEMORY.md",
		"HEARTBEAT.md",
		filepath.Join("memory", "knowledge.md"),
		filepath.Join("memory", "decisions.md"),
		filepath.Join("memory", "questions.md"),
		filepath.Join("memory", "rhizome.md"),
	} {
		path := filepath.Join(cfg.Agent.PromptRoot, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Stat(%s) err = %v", path, err)
		}
	}
}

func TestClearSharedDynamicMemoryPreservesIdentitySectionInMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			DynamicFiles:     []string{"MEMORY.md", "HEARTBEAT.md"},
			DailyNotes:       true,
			DailyNotesDir:    "memory",
		},
	}

	memoryRaw := `# MEMORY.md — Shared Curated Memory

<!-- APHELION:IDENTITY-BEGIN -->
## Identity-Bearing Continuity

- This should survive resets.
<!-- APHELION:IDENTITY-END -->

## Operational Notes

- This should be cleared.
`
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(memoryRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "HEARTBEAT.md"), []byte("temporary"), 0o600); err != nil {
		t.Fatalf("WriteFile(HEARTBEAT.md) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "knowledge.md"), []byte("clear me"), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "2026-04-10.md"), []byte("daily note"), 0o600); err != nil {
		t.Fatalf("WriteFile(daily note) err = %v", err)
	}

	removed, err := clearSharedDynamicMemory(cfg)
	if err != nil {
		t.Fatalf("clearSharedDynamicMemory() err = %v", err)
	}
	if removed == 0 {
		t.Fatal("clearSharedDynamicMemory() removed 0 paths, want preserved/cleared entries")
	}

	raw, err := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "This should survive resets.") {
		t.Fatalf("MEMORY.md = %q, want preserved identity block", text)
	}
	if strings.Contains(text, "This should be cleared.") {
		t.Fatalf("MEMORY.md = %q, want non-identity content removed", text)
	}
	if _, err := os.Stat(filepath.Join(root, "HEARTBEAT.md")); !os.IsNotExist(err) {
		t.Fatalf("HEARTBEAT.md still exists, want removed; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "knowledge.md")); !os.IsNotExist(err) {
		t.Fatalf("knowledge.md still exists, want removed; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "2026-04-10.md")); !os.IsNotExist(err) {
		t.Fatalf("daily note still exists, want removed; err=%v", err)
	}
}

func TestArchiveColdDailyNotesMovesOldNotesIntoArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			UserMemoryRoot:   filepath.Join(root, "users"),
			DailyNotesDir:    "memory/daily",
		},
		Memory: config.MemoryConfig{
			Decay: config.MemoryDecayConfig{
				Enabled:  true,
				HotDays:  3,
				WarmDays: 7,
				ColdDays: 30,
			},
		},
	}

	noteRoot := filepath.Join(root, "memory", "daily")
	if err := os.MkdirAll(noteRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(noteRoot) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteRoot, "2026-01-01.md"), []byte("old note"), 0o600); err != nil {
		t.Fatalf("WriteFile(old note) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteRoot, "2026-04-09.md"), []byte("recent note"), 0o600); err != nil {
		t.Fatalf("WriteFile(recent note) err = %v", err)
	}

	archived, err := archiveColdDailyNotes(cfg, time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("archiveColdDailyNotes() err = %v", err)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}

	if _, err := os.Stat(filepath.Join(root, "memory", "daily", "2026-01-01.md")); !os.IsNotExist(err) {
		t.Fatalf("old note still active, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "archive", "daily", "2026-01-01.md")); err != nil {
		t.Fatalf("archived note missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "daily", "2026-04-09.md")); err != nil {
		t.Fatalf("recent note missing: %v", err)
	}
}

func TestArchiveOversizedCuratedMemoryArchivesAndCompacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			UserMemoryRoot:   filepath.Join(root, "users"),
		},
		Memory: config.MemoryConfig{
			Decay: config.MemoryDecayConfig{Enabled: true},
		},
	}

	path := filepath.Join(root, "memory", "knowledge.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(path, []byte("# knowledge.md\n\n"+strings.Repeat("- fact worth keeping around for a long time\n\n", 500)), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge.md) err = %v", err)
	}

	archived, err := archiveOversizedCuratedMemory(cfg, time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("archiveOversizedCuratedMemory() err = %v", err)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(knowledge.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "Excerpted for prompt efficiency") {
		t.Fatalf("knowledge.md = %q, want compacted excerpt", string(raw))
	}

	archiveEntries, err := os.ReadDir(filepath.Join(root, "memory", "archive"))
	if err != nil {
		t.Fatalf("ReadDir(archive) err = %v", err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(archiveEntries))
	}
}

func TestRunImportAuditCommandListsAndApprovesImportedDocs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, "aphelion.toml")
	configRaw := `
[telegram]
bot_token = "token"

[principals.telegram]
admin_user_ids = [1]

[providers.anthropic]
api_key = "anthropic-key"

[sessions]
db_path = "` + filepath.ToSlash(filepath.Join(root, "state", "sessions.db")) + `"

[agent]
prompt_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
exec_root = "` + filepath.ToSlash(filepath.Join(root, "workspace")) + `"
shared_memory_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
user_workspace_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "workspaces")) + `"
user_memory_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "memory")) + `"
`
	if err := os.WriteFile(cfgPath, []byte(configRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(config) err = %v", err)
	}

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		t.Fatalf("newSemanticEngineForConfig() err = %v", err)
	}
	defer engine.Close()

	docID, err := engine.ImportDocument(context.Background(), memstore.SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "imports/openclaw/notes.md",
		SourceKind:       "knowledge",
		SourceClass:      "imported_archive",
		ProvenanceSource: "openclaw_import",
		ImportState:      memstore.SemanticImportStateQuarantine,
		Content:          "- Imported durable preference",
		MTime:            time.Date(2026, time.April, 11, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}

	listOut, err := captureStdout(t, func() error {
		return runImportAuditCommand([]string{"--config", cfgPath, "list"})
	})
	if err != nil {
		t.Fatalf("runImportAuditCommand(list) err = %v", err)
	}
	if !strings.Contains(listOut, "id="+strconv.FormatInt(docID, 10)) {
		t.Fatalf("list output = %q, want imported doc id %d", listOut, docID)
	}

	if _, err := captureStdout(t, func() error {
		return runImportAuditCommand([]string{"--config", cfgPath, "--id", strconv.FormatInt(docID, 10), "approve"})
	}); err != nil {
		t.Fatalf("runImportAuditCommand(approve) err = %v", err)
	}

	approvedOut, err := captureStdout(t, func() error {
		return runImportAuditCommand([]string{"--config", cfgPath, "--state", "approved", "list"})
	})
	if err != nil {
		t.Fatalf("runImportAuditCommand(list approved) err = %v", err)
	}
	if !strings.Contains(approvedOut, "state=approved") {
		t.Fatalf("approved list output = %q, want approved state", approvedOut)
	}
}

func TestRunImportSemanticCommandImportsOpenClawCorpus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := filepath.Join(root, "aphelion.toml")
	configRaw := `
[telegram]
bot_token = "token"

[principals.telegram]
admin_user_ids = [1]

[providers.anthropic]
api_key = "anthropic-key"

[sessions]
db_path = "` + filepath.ToSlash(filepath.Join(root, "state", "sessions.db")) + `"

[agent]
prompt_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
exec_root = "` + filepath.ToSlash(filepath.Join(root, "workspace")) + `"
shared_memory_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
user_workspace_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "workspaces")) + `"
user_memory_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "memory")) + `"
`
	if err := os.WriteFile(cfgPath, []byte(configRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(config) err = %v", err)
	}

	foreignDBPath := filepath.Join(root, "openclaw.db")
	createOpenClawImportFixture(t, foreignDBPath)

	out, err := captureStdout(t, func() error {
		return runImportSemanticCommand([]string{
			"--config", cfgPath,
			"--db", foreignDBPath,
			"--scope", "principal",
			"--principal", "telegram:7",
			"openclaw",
		})
	})
	if err != nil {
		t.Fatalf("runImportSemanticCommand() err = %v", err)
	}
	if !strings.Contains(out, "documents: 1") || !strings.Contains(out, "chunks: 2") {
		t.Fatalf("import output = %q, want document/chunk summary", out)
	}
	if !strings.Contains(out, "contract: openclaw_observed_v1") || !strings.Contains(out, "embedding_use: preserved_only") || !strings.Contains(out, "embedding_chunks: 2") {
		t.Fatalf("import output = %q, want contract and embedding summary", out)
	}

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		t.Fatalf("newSemanticEngineForConfig() err = %v", err)
	}
	defer engine.Close()

	docs, err := engine.ListImportAudit(context.Background(), memstore.SemanticAuditFilter{
		State:       memstore.SemanticImportStateQuarantine,
		Scope:       "principal",
		PrincipalID: "telegram:7",
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("ListImportAudit() len = %d, want 1", len(docs))
	}
	if docs[0].ProvenanceSource != "openclaw_import" || docs[0].SourceKind != "knowledge" {
		t.Fatalf("doc = %#v, want openclaw knowledge import", docs[0])
	}
}

func TestRunDurableAgentPolicyShowAndApply(t *testing.T) {
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		Status:            "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceRole: "durable_agent",
		SourceScope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             "family-group",
			DurableAgentID: "family-group",
		},
		TargetAdminChatID: 1,
		TargetScope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   "1",
		},
		Summary: "Family group pressure suggests a narrower reply mode.",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}
	events, err := store.PendingReviewEvents(1, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	reviewID := events[0].ID

	showOut, err := captureStdout(t, func() error {
		return runDurableAgentPolicyCommand([]string{"--config", cfgPath, "--agent", "family-group", "show"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentPolicyCommand(show) err = %v", err)
	}
	if !strings.Contains(showOut, "charter: Initial charter") || !strings.Contains(showOut, "outbound_mode: reply_with_policy_authorization") {
		t.Fatalf("policy show output = %q, want initial policy", showOut)
	}
	if !strings.Contains(showOut, "bootstrap_allowed_outbound_modes: read_only,draft_only,reply_with_parent_review,reply_with_policy_authorization") {
		t.Fatalf("policy show output = %q, want bootstrap ceiling lines", showOut)
	}

	applyOut, err := captureStdout(t, func() error {
		return runDurableAgentPolicyCommand([]string{
			"--config", cfgPath,
			"--agent", "family-group",
			"--review-event", strconv.FormatInt(reviewID, 10),
			"--outbound-mode", "read_only",
			"--reason", "ratified quieter family group mode",
			"apply",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentPolicyCommand(apply) err = %v", err)
	}
	if !strings.Contains(applyOut, "changed: true") || !strings.Contains(applyOut, "policy_version: 2") {
		t.Fatalf("policy apply output = %q, want changed version 2", applyOut)
	}

	updated, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent(updated) err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	updates, err := store.DurableAgentPolicyUpdates("family-group", 10)
	if err != nil {
		t.Fatalf("DurableAgentPolicyUpdates() err = %v", err)
	}
	if len(updates) != 1 || updates[0].SourceReviewEventID != reviewID {
		t.Fatalf("policy updates = %#v, want one update with review id %d", updates, reviewID)
	}
}

func TestRunDurableAgentForensicShowReadsRestrictedSidecar(t *testing.T) {
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
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
		AgentID:        "family-group",
		Reason:         "secret_like_material",
		CreatedAt:      time.Now().UTC(),
		RedactedFields: []string{"source_excerpt"},
		Payload: map[string]string{
			"source_excerpt": "Use this password: super-secret-123",
		},
	})
	if err != nil {
		t.Fatalf("WriteForensicRecord() err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentForensicCommand([]string{
			"--config", cfgPath,
			"--agent", "family-group",
			"--ref", ref,
			"show",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentForensicCommand(show) err = %v", err)
	}
	if !strings.Contains(out, "payload.source_excerpt: Use this password: super-secret-123") {
		t.Fatalf("forensic show output = %q, want preserved forensic payload", out)
	}
}

func TestRunDurableAgentBootstrapWriteExportsRemoteBootstrap(t *testing.T) {
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "reply_with_policy_authorization",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
		Status:            "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	bootstrapPath := filepath.Join(root, "family-group-bootstrap.json")
	out, err := captureStdout(t, func() error {
		return runDurableAgentBootstrapCommand([]string{
			"--config", cfgPath,
			"--agent", "family-group",
			"--path", bootstrapPath,
			"--parent-control-url", "https://house.example/control",
			"--enrollment-token", "enroll-token-1",
			"--key-fingerprint", "child-key-fp",
			"write",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentBootstrapCommand(write) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent bootstrap write") || !strings.Contains(out, "agent_id: family-group") {
		t.Fatalf("bootstrap write output = %q, want action and agent id", out)
	}

	bootstrap, err := durableagent.ReadRemoteBootstrap(bootstrapPath)
	if err != nil {
		t.Fatalf("ReadRemoteBootstrap() err = %v", err)
	}
	if bootstrap.AgentID != "family-group" {
		t.Fatalf("bootstrap.AgentID = %q, want family-group", bootstrap.AgentID)
	}
	if bootstrap.ParentControlURL != "https://house.example/control" {
		t.Fatalf("bootstrap.ParentControlURL = %q, want https://house.example/control", bootstrap.ParentControlURL)
	}
	if bootstrap.EnrollmentToken != "enroll-token-1" {
		t.Fatalf("bootstrap.EnrollmentToken = %q, want enroll-token-1", bootstrap.EnrollmentToken)
	}
	if bootstrap.KeyFingerprint != "child-key-fp" {
		t.Fatalf("bootstrap.KeyFingerprint = %q, want child-key-fp", bootstrap.KeyFingerprint)
	}
	if bootstrap.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("bootstrap.BootstrapLLM.NativeProvider = %q, want openrouter", bootstrap.BootstrapLLM.NativeProvider)
	}
	if bootstrap.NetworkPolicy != "restricted" {
		t.Fatalf("bootstrap.NetworkPolicy = %q, want restricted", bootstrap.NetworkPolicy)
	}
	persisted, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if persisted.ControlPlaneSecret != "enroll-token-1" {
		t.Fatalf("persisted ControlPlaneSecret = %q, want enroll-token-1", persisted.ControlPlaneSecret)
	}
}

func TestRunDurableAgentRemoteRunOnceSyncsAndUploadsArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parentCfgPath := writeMaintenanceConfig(t, root)

	parentCfg, _, err := loadConfigForCommand(parentCfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	parentStore, err := session.NewSQLiteStore(parentCfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("parent NewSQLiteStore() err = %v", err)
	}
	defer parentStore.Close()

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(parentCfg.Sessions.DBPath, "family-group")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Initial charter",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		SecretScopes:      []string{"telegram_bot"},
		NetworkPolicy:     "restricted",
		Status:            "active",
	}
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childDBPath := filepath.Join(root, "remote-child.db")
	bootstrapPath := filepath.Join(root, "family-group-bootstrap.json")
	if err := runDurableAgentBootstrapCommand([]string{
		"--config", parentCfgPath,
		"--agent", "family-group",
		"--path", bootstrapPath,
		"--parent-control-url", "https://house.example",
		"--enrollment-token", "enroll-token-1",
		"--key-fingerprint", "child-key-fp",
		"write",
	}); err != nil {
		t.Fatalf("runDurableAgentBootstrapCommand(write) err = %v", err)
	}

	messagePath := filepath.Join(root, "message.json")
	msgRaw := []byte(`{
  "ChatID": -100123,
  "ChatType": "group",
  "SenderID": 77,
  "SenderName": "Aunt May",
  "Text": "Can you remind everyone again?",
  "MessageID": 22,
  "DurableAgentID": "family-group",
  "Timestamp": "2026-04-13T00:00:00Z"
}`)
	if err := os.WriteFile(messagePath, msgRaw, 0o600); err != nil {
		t.Fatalf("WriteFile(message) err = %v", err)
	}

	origClientFactory := durableAgentRemoteClientFactory
	origExecutorFactory := durableAgentRemoteExecutorFactory
	defer func() {
		durableAgentRemoteClientFactory = origClientFactory
		durableAgentRemoteExecutorFactory = origExecutorFactory
	}()

	durableAgentRemoteClientFactory = func(b core.DurableAgentRemoteBootstrap) (durableagent.RemoteControlClient, error) {
		client, err := durableagent.NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = &http.Client{Transport: maintenanceHandlerRoundTripper{handler: durableagent.NewHTTPHandler(parentStore).Handler()}}
		return client, nil
	}
	durableAgentRemoteExecutorFactory = func(store *session.SQLiteStore, dbPath string) durableagent.RemoteChildExecutor {
		return durableagent.RemoteChildExecutorFunc(func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
			_, err := durableagent.NewRuntime(store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				Summary:       "Family schedule drift keeps resurfacing around the dinner plan.",
				IntervalLabel: "messages 20-25",
				LocalActions:  []string{"Held reply pending parent visibility."},
				Questions:     []string{"Should this become a standing family reminder?"},
				RiskFlags:     []string{"family_relevant_update"},
			})
			return err
		})
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentRemoteCommand([]string{
			"--bootstrap", bootstrapPath,
			"--db", childDBPath,
			"--message", messagePath,
			"run-once",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentRemoteCommand(run-once) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent remote run-once") {
		t.Fatalf("remote run-once output = %q, want action line", out)
	}
	if !strings.Contains(out, "uploaded_review_artifacts: 1") {
		t.Fatalf("remote run-once output = %q, want uploaded review count", out)
	}

	parentEvents, err := parentStore.PendingReviewEvents(1, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
}

type maintenanceHandlerRoundTripper struct {
	handler http.Handler
}

func (rt maintenanceHandlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := captureResponseRecorder{header: make(http.Header)}
	rt.handler.ServeHTTP(&rec, req)
	return &http.Response{
		StatusCode: rec.code,
		Header:     rec.header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(rec.body.Bytes())),
		Request:    req,
	}, nil
}

type captureResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *captureResponseRecorder) Header() http.Header {
	return r.header
}

func (r *captureResponseRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *captureResponseRecorder) WriteHeader(statusCode int) {
	r.code = statusCode
}

func writeMaintenanceConfig(t *testing.T, root string) string {
	t.Helper()

	cfgPath := filepath.Join(root, "aphelion.toml")
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatalf("MkdirAll(state) err = %v", err)
	}
	configRaw := `
[telegram]
bot_token = "token"

[principals.telegram]
admin_user_ids = [1]

[providers.anthropic]
api_key = "anthropic-key"

[sessions]
db_path = "` + filepath.ToSlash(filepath.Join(root, "state", "sessions.db")) + `"

[agent]
prompt_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
exec_root = "` + filepath.ToSlash(filepath.Join(root, "workspace")) + `"
shared_memory_root = "` + filepath.ToSlash(filepath.Join(root, "agent")) + `"
user_workspace_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "workspaces")) + `"
user_memory_root = "` + filepath.ToSlash(filepath.Join(root, "state", "isolated", "memory")) + `"
`
	if err := os.WriteFile(cfgPath, []byte(configRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(config) err = %v", err)
	}
	return cfgPath
}

func createOpenClawImportFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("sql.Open(%s) err = %v", path, err)
	}
	defer db.Close()

	for _, stmt := range []string{
		`CREATE TABLE files (
			path TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			hash TEXT NOT NULL DEFAULT '',
			mtime INTEGER NOT NULL DEFAULT 0,
			size INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL,
			source TEXT NOT NULL,
			start_line INTEGER NOT NULL DEFAULT 0,
			end_line INTEGER NOT NULL DEFAULT 0,
			hash TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			embedding TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("Exec(%q) err = %v", stmt, err)
		}
	}

	if _, err := db.Exec(`
		INSERT INTO files (path, source, hash, mtime, size)
		VALUES (?, ?, ?, ?, ?)
	`, "memory/knowledge.md", "memory", "", int64(1712798400000), 128); err != nil {
		t.Fatalf("insert files row err = %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO chunks (id, path, source, start_line, end_line, hash, model, text, embedding, updated_at)
		VALUES
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?),
			(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"chunk-a", "memory/knowledge.md", "memory", 1, 2, "", "text-embedding-3-small", "- Imported feature preference.", "[0.1, 0.2]", int64(1712798400000),
		"chunk-b", "memory/knowledge.md", "memory", 4, 5, "", "text-embedding-3-small", "- Imported operational decision.", "[0.3, 0.4]", int64(1712798460000),
	); err != nil {
		t.Fatalf("insert chunks rows err = %v", err)
	}
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	captureStdoutMu.Lock()
	defer captureStdoutMu.Unlock()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() err = %v", err)
	}
	defer r.Close()

	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom(pipe) err = %v", err)
	}
	return buf.String(), runErr
}
