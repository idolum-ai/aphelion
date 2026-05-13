//go:build linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	"github.com/idolum-ai/aphelion/principal"
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

func TestRunImportCodexSessionsCommandImportsAndDedupes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	cfgPath := writeMaintenanceConfigWithCodexHome(t, root, codexHome)
	writeCodexSessionMaintenanceFixture(t, codexHome, time.Now().UTC().Add(-time.Hour), "command import should enter quarantine")

	out, err := captureStdout(t, func() error {
		return runImportCodexSessionsCommand([]string{
			"--config", cfgPath,
			"--lookback", "48h",
			"--active-grace", "1m",
		})
	})
	if err != nil {
		t.Fatalf("runImportCodexSessionsCommand() err = %v", err)
	}
	for _, needle := range []string{
		"action: import-codex-sessions",
		"state: quarantine",
		"imported: 1",
		"skipped_already_imported: 0",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("import output = %q, want substring %q", out, needle)
		}
	}

	again, err := captureStdout(t, func() error {
		return runImportCodexSessionsCommand([]string{
			"--config", cfgPath,
			"--lookback", "48h",
			"--active-grace", "1m",
		})
	})
	if err != nil {
		t.Fatalf("runImportCodexSessionsCommand(second) err = %v", err)
	}
	if !strings.Contains(again, "imported: 0") || !strings.Contains(again, "skipped_already_imported: 1") {
		t.Fatalf("second import output = %q, want dedupe skip", again)
	}
}

func TestRunInitCommandImportsCodexSessions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	cfgPath := writeMaintenanceConfigWithCodexHome(t, root, codexHome)
	writeCodexSessionMaintenanceFixture(t, codexHome, time.Now().UTC().Add(-time.Hour), "init import should run during reinstall")

	out, err := captureStdout(t, func() error {
		return runInitCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runInitCommand() err = %v", err)
	}
	for _, needle := range []string{
		"prompt_root:",
		"action: import-codex-sessions",
		"imported: 1",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("init output = %q, want substring %q", out, needle)
		}
	}
}

func TestRunPathsCommandPrintsAutonomyPolicy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	if err := runInitCommand([]string{"--config", cfgPath}); err != nil {
		t.Fatalf("runInitCommand() err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runPathsCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runPathsCommand() err = %v", err)
	}
	for _, want := range []string{
		"autonomy_default_mode: ask_first",
		"autonomy_ceiling: leased",
		"autonomy_live_overrides: true",
		"autonomy_max_override_duration: 4h0m0s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("paths output = %q, want %q", out, want)
		}
	}
}

func TestDurableAgentReconcileRepairsActiveChildAndQueuesGrowthPrompt(t *testing.T) {
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
	agent := core.DurableAgent{
		AgentID:     "paper-scout",
		ChannelKind: "external_channel",
		Status:      "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review incoming reports and negotiate useful surfaces.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-gog",
		GrantedTo:      core.DurableAgentPrincipal(agent.AgentID),
		Kind:           session.CapabilityKindTool,
		TargetResource: "gog",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID: agent.AgentID,
		Status:  "awake",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	result, err := reconcileDurableAgentsForConfig(cfg, durableAgentReconcileOptions{
		QueueGrowthPrompt: true,
		Now:               time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("reconcileDurableAgentsForConfig() err = %v", err)
	}
	if result.Count != 1 || result.Active != 1 || result.RootsRepaired != 1 || result.BootstrapRepaired != 1 || result.ProfilesSynced != 1 || result.GrowthPromptsQueued != 1 || result.StatesReset != 1 || result.GrantIssues != 1 {
		t.Fatalf("reconcile result = %#v, want repaired active child with one growth prompt and one grant issue", result)
	}

	reopened, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	repaired, err := reopened.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	_, memoryRoot := durableagent.LocalRoots(repaired.AgentID, repaired.LocalStorageRoots)
	for _, name := range []string{"growth.md", "capability-ledger.md", "scorecard.md"} {
		if _, err := os.Stat(filepath.Join(memoryRoot, "profile", name)); err != nil {
			t.Fatalf("Stat(profile/%s) err = %v", name, err)
		}
	}
	if !core.NormalizeNodeLLMBootstrap(repaired.BootstrapLLM).Configured() {
		t.Fatalf("repaired bootstrap = %#v, want configured", repaired.BootstrapLLM)
	}
	state, err := reopened.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.Status != "dormant" || !strings.Contains(state.StateJSON, durableAgentReconcileGrowthMarker) {
		t.Fatalf("state after reconcile = %#v, want dormant with growth marker", state)
	}

	second, err := reconcileDurableAgentsForConfig(cfg, durableAgentReconcileOptions{
		QueueGrowthPrompt: true,
		Now:               time.Date(2026, 4, 26, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("reconcileDurableAgentsForConfig(second) err = %v", err)
	}
	if second.GrowthPromptsQueued != 0 || second.RootsRepaired != 0 || second.BootstrapRepaired != 0 {
		t.Fatalf("second reconcile = %#v, want idempotent repairs and no duplicate growth prompt", second)
	}
	state, err = reopened.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(second) err = %v", err)
	}
	if strings.Count(state.StateJSON, durableAgentReconcileGrowthMarker) != 1 {
		t.Fatalf("state.StateJSON = %q, want one growth marker", state.StateJSON)
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

func authorityFindingIDFromOutput(t *testing.T, out string, code string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "code="+code) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "finding_id=") {
				return strings.TrimPrefix(field, "finding_id=")
			}
		}
	}
	t.Fatalf("output = %q, want finding_id for code %s", out, code)
	return ""
}

func executionEventsContainAuthorityRepair(events []session.ExecutionEvent, findingID string, status string) bool {
	for _, event := range events {
		if event.Stage != "authority_repair" || event.Status != status {
			continue
		}
		if strings.Contains(event.PayloadJSON, `"finding_id":"`+findingID+`"`) {
			return true
		}
	}
	return false
}

func executionEventsContainStatus(events []session.ExecutionEvent, eventType string, stage string, status string, payloadNeedle string) bool {
	for _, event := range events {
		if event.EventType != eventType || event.Stage != stage || event.Status != status {
			continue
		}
		if payloadNeedle == "" || strings.Contains(event.PayloadJSON, payloadNeedle) {
			return true
		}
	}
	return false
}

type fakeDurableAgentWakeRuntime struct {
	agentID string
	now     time.Time
}

func (f *fakeDurableAgentWakeRuntime) RunDurableAgentChildWake(_ context.Context, agentID string, now time.Time) error {
	f.agentID = strings.TrimSpace(agentID)
	f.now = now.UTC()
	return nil
}

type deployTurnRunnerFunc func(context.Context, core.InboundMessage) (*core.TurnResult, error)

func (f deployTurnRunnerFunc) HandleInbound(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
	return f(ctx, msg)
}

func installSuccessfulDeployVerificationBuilder(t *testing.T, reply string, setup func(*session.SQLiteStore) error, wake func(context.Context, string, time.Time) error) {
	t.Helper()
	origBuilder := deployVerificationRuntimeBuilder
	t.Cleanup(func() { deployVerificationRuntimeBuilder = origBuilder })

	deployVerificationRuntimeBuilder = func(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
		if setup != nil {
			if err := setup(store); err != nil {
				return builtDeployVerificationRuntime{}, err
			}
		}
		sender := &deployVerificationSender{}
		runner := deployTurnRunnerFunc(func(ctx context.Context, msg core.InboundMessage) (*core.TurnResult, error) {
			key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
			sess, err := store.Load(key)
			if err != nil {
				return nil, err
			}
			sess.ChatType = "dm"
			sess.UserName = msg.SenderName
			sess.TurnCount++
			sess.LastFloorText = "Verification floor."
			newMessages := []session.Message{
				{
					Role:         "user",
					Content:      msg.Text,
					ContentChars: len(msg.Text),
					TurnIndex:    sess.TurnCount,
				},
				{
					Role:         "assistant",
					Content:      reply,
					ContentChars: len(reply),
					TurnIndex:    sess.TurnCount,
				},
			}
			if err := store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
				return nil, err
			}
			if _, err := sender.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: reply}); err != nil {
				return nil, err
			}
			return &core.TurnResult{Text: "Verification floor."}, nil
		})
		return builtDeployVerificationRuntime{
			Runner: runner,
			Sender: sender,
			Probe: func(ctx context.Context, key session.SessionKey, p principal.Principal) (string, error) {
				return "tool probe persisted plan state", nil
			},
			DurableChildWake: wake,
		}, nil
	}
}

func newVerifyDeployTestConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs: []int64{42},
			},
		},
		Sessions: config.SessionsConfig{
			DBPath: filepath.Join(root, "state", "sessions.db"),
		},
		Agent: config.AgentConfig{
			PromptRoot:        filepath.Join(root, "agent"),
			ExecRoot:          filepath.Join(root, "workspace"),
			SharedMemoryRoot:  filepath.Join(root, "agent"),
			UserWorkspaceRoot: filepath.Join(root, "state", "isolated", "workspaces"),
			UserMemoryRoot:    filepath.Join(root, "state", "isolated", "memory"),
			ToolTimeout:       30,
		},
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func writeMaintenanceConfigWithCodexHome(t *testing.T, root string, codexHome string) string {
	t.Helper()
	cfgPath := writeMaintenanceConfig(t, root)
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(config append) err = %v", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[governor.codex]\ncodex_home = %q\n", filepath.ToSlash(codexHome)); err != nil {
		t.Fatalf("append codex_home config err = %v", err)
	}
	return cfgPath
}

func testRepairCapabilityGrant(grantID string, target string, now time.Time) session.CapabilityGrant {
	return session.CapabilityGrant{
		GrantID:        grantID,
		RequestID:      "req-" + grantID,
		GrantedBy:      "telegram:1",
		GrantedTo:      core.DurableAgentPrincipal("agent-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: target,
		AllowedActions: []string{"invoke"},
		Contract:       "{}",
		Constraints:    "{}",
		Status:         session.CapabilityGrantStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	}
}

func writeCodexSessionMaintenanceFixture(t *testing.T, codexHome string, modTime time.Time, userText string) string {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "04", "25")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(codex sessions) err = %v", err)
	}
	path := filepath.Join(dir, "rollout-"+modTime.UTC().Format("20060102T150405.000000000")+".jsonl")
	events := []map[string]any{
		{
			"type":      "session_meta",
			"timestamp": modTime.Add(-time.Minute).UTC().Format(time.RFC3339Nano),
			"payload": map[string]any{
				"id":             "maintenance-session",
				"source":         "codex_cli",
				"model_provider": "openai",
				"cwd":            "/workspace/aphelion",
			},
		},
		{
			"type":      "response_item",
			"timestamp": modTime.UTC().Format(time.RFC3339Nano),
			"payload": map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]string{{
					"type": "input_text",
					"text": userText,
				}},
			},
		},
	}
	lines := make([]string, 0, len(events))
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal(codex event) err = %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(codex session) err = %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(codex session) err = %v", err)
	}
	return path
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
