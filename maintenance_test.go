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
	"github.com/idolum-ai/aphelion/tool"
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "idolum-email")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1",
		ReviewTargetChatID: 1,
		ChannelKind:        "email",
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
	rawSummary := "Email wake blocked because gog_cli keyring backend requires an interactive passphrase prompt; no TTY is available."
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
		Summary:           "durable_agent=idolum-email channel=email\nsummary: [REDACTED: summary]\nrisks: external_channel",
		MetadataJSON:      `{"agent_id":"idolum-email","summary":"[REDACTED: summary]","interval_label":"2026-05-08T02:50:01Z","risk_flags":["external_channel"],"artifact_refs":["` + ref + `"],"metadata":{"channel_kind":"email","external_channel_status":"wake_blocked","forensic_ref":"` + ref + `","redacted_fields":"summary","redaction_action":"quarantined_fields","redaction_source":"deterministic","redaction_reason":"concrete_secret_value"}}`,
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

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, "idolum-email")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1",
		ReviewTargetChatID: 1,
		ChannelKind:        "email",
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
			"summary": "Email adapter printed token: sk-testSECRETabcdef123456",
		},
	})
	if err != nil {
		t.Fatalf("WriteForensicRecord() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: agent.AgentID, DurableAgentID: agent.AgentID},
		TargetAdminChatID: 1,
		Summary:           "durable_agent=idolum-email channel=email\nsummary: [REDACTED: summary]",
		MetadataJSON:      `{"agent_id":"idolum-email","summary":"[REDACTED: summary]","metadata":{"channel_kind":"email","forensic_ref":"` + ref + `","redacted_fields":"summary"}}`,
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

func TestRepairCapabilityGrantDriftDryRunLeavesMissingRuntimeActive(t *testing.T) {
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

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-missing-runtime"
	if _, err := store.UpsertCapabilityGrant(testRepairCapabilityGrant(grantID, "missing_tool", now)); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, nil, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: true,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RevokeCandidates != 1 || result.RevokesApplied != 0 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want dry-run revoke candidate only", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive || !updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want active and not revoked", updated)
	}
}

func TestRepairCapabilityGrantDriftRevokesExpiredActiveGrant(t *testing.T) {
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

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-expired-runtime"
	grant := testRepairCapabilityGrant(grantID, "expired_tool", now)
	grant.ExpiresAt = now.Add(-time.Minute)
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, nil, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: false,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RevokeCandidates != 1 || result.RevokesApplied != 1 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one revoked expired grant", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusRevoked || updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want revoked with revoked_at", updated)
	}
	if !strings.Contains(updated.StaleReason, "expired") {
		t.Fatalf("stale reason = %q, want expired reason", updated.StaleReason)
	}
}

func TestRepairCapabilityGrantDriftRepairsFromManifest(t *testing.T) {
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

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-repair-runtime"
	grant := testRepairCapabilityGrant(grantID, "repair_tool", now)
	grant.Constraints = `{"child_runtime":{"executable":"relative/path"},"max_runtime_seconds":10}`
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	executable := filepath.Join(root, "bin", "repair-tool")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(executable) err = %v", err)
	}
	manifestPath := filepath.Join(root, "external-tools", "repair_tool", "manifest.json")
	manifest := capabilityGrantRepairManifest{
		Path: manifestPath,
		Manifest: tool.ExternalToolManifest{
			Name:  "repair_tool",
			Owner: "test",
			Execution: tool.ExternalToolManifestExecution{
				Mode:  "process",
				Entry: executable,
			},
		},
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, []capabilityGrantRepairManifest{manifest}, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: false,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RepairCandidates != 1 || result.RepairsApplied != 1 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one repaired grant", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive {
		t.Fatalf("updated grant = %#v, want active repaired grant", updated)
	}
	material, found, err := core.ExtractChildRuntimeContract(updated.Contract, updated.Constraints)
	if err != nil {
		t.Fatalf("ExtractChildRuntimeContract() err = %v", err)
	}
	if !found || material.Executable != executable {
		t.Fatalf("child runtime = %#v found=%t, want executable %s", material, found, executable)
	}
	var constraints map[string]json.RawMessage
	if err := json.Unmarshal([]byte(updated.Constraints), &constraints); err != nil {
		t.Fatalf("decode updated constraints err = %v", err)
	}
	if _, ok := constraints["child_runtime"]; ok {
		t.Fatalf("updated constraints = %q, want stale child_runtime removed", updated.Constraints)
	}
	if _, ok := constraints["max_runtime_seconds"]; !ok {
		t.Fatalf("updated constraints = %q, want other constraints preserved", updated.Constraints)
	}
	if updated.BaselinePolicyHash == "" || updated.BaselinePolicyHash != updated.CurrentPolicyHash || updated.AnchorFingerprint != updated.CurrentPolicyHash {
		t.Fatalf("policy hashes = baseline %q current %q anchor %q, want repaired hash copied to all", updated.BaselinePolicyHash, updated.CurrentPolicyHash, updated.AnchorFingerprint)
	}
}

func TestLoadCapabilityRepairManifestsResolvesNestedRepoRelativeEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestDir := filepath.Join(root, "external-tools")
	executable := filepath.Join(manifestDir, "nested_tool", "bin", "nested-tool")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(executable) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(executable), "payload.json"), []byte(`{"not":"a manifest"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(payload) err = %v", err)
	}
	manifestPath := filepath.Join(manifestDir, "nested_tool", "manifest.json")
	raw := `{
  "name": "nested_tool",
  "owner": "test",
  "execution": {
    "mode": "process",
    "entry": "external-tools/nested_tool/bin/nested-tool"
  },
  "io": {}
}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) err = %v", err)
	}

	manifests, err := loadCapabilityRepairManifests(manifestDir)
	if err != nil {
		t.Fatalf("loadCapabilityRepairManifests() err = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests len = %d, want 1", len(manifests))
	}
	material, ok, reason := childRuntimeFromRepairManifest(manifests[0])
	if !ok {
		t.Fatalf("childRuntimeFromRepairManifest() ok=false reason=%q", reason)
	}
	if material.Executable != executable {
		t.Fatalf("material executable = %q, want %q", material.Executable, executable)
	}
}

func TestRunRepairCapabilityGrantsCommandDefaultsToDryRun(t *testing.T) {
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
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-command-dry-run"
	if _, err := store.UpsertCapabilityGrant(testRepairCapabilityGrant(grantID, "command_tool", now)); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	store.Close()

	out, err := captureStdout(t, func() error {
		return runRepairCapabilityGrantsCommand([]string{"--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runRepairCapabilityGrantsCommand() err = %v", err)
	}
	for _, needle := range []string{"action: repair-capability-grants", "dry_run: true", "revoke_candidates: 1", "revokes_applied: 0"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output = %q, want %q", out, needle)
		}
	}
	reopened, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	updated, ok, err := reopened.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive || !updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want unchanged active grant after dry-run", updated)
	}
}

func TestRunRepairCapabilityGrantsCommandRequiresApplyForMutation(t *testing.T) {
	err := runRepairCapabilityGrantsCommand([]string{"--dry-run=false"})
	if err == nil || !strings.Contains(err.Error(), "requires --apply") {
		t.Fatalf("runRepairCapabilityGrantsCommand() err = %v, want --apply requirement", err)
	}
}

func TestRunAuthorityCommandsReportRepairPreview(t *testing.T) {
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
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 77710, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77710"}}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-authority-cli",
			Status:    session.ProposalStatusApproved,
			ExpiresAt: now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-authority-cli",
			ProposalID:     "proposal-authority-cli",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	doctorOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"doctor", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(doctor) err = %v", err)
	}
	for _, needle := range []string{"action: authority-doctor", "status: needs_attention", "code=expired_continuation_lease"} {
		if !strings.Contains(doctorOut, needle) {
			t.Fatalf("doctor output = %q, want %q", doctorOut, needle)
		}
	}

	repairOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair) err = %v", err)
	}
	for _, needle := range []string{"action: authority-repair", "dry_run: true", "repair_action=expire_continuation_lease", "repairable=true"} {
		if !strings.Contains(repairOut, needle) {
			t.Fatalf("repair output = %q, want %q", repairOut, needle)
		}
	}
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

func TestRunDurableAgentWakeCommandRunsOneNamedAgent(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	wakeTime := time.Date(2026, time.May, 4, 18, 30, 0, 123, time.UTC)
	fake := &fakeDurableAgentWakeRuntime{}
	cleanupCalled := false

	out, err := captureStdout(t, func() error {
		return runDurableAgentWakeCommandWithFactory([]string{
			"--config", cfgPath,
			"--agent", " image2 ",
			"--now", wakeTime.Format(time.RFC3339Nano),
		}, func(cfg *config.Config) (durableAgentWakeRuntime, func(), error) {
			if cfg == nil || !strings.HasSuffix(cfg.Sessions.DBPath, "sessions.db") {
				t.Fatalf("factory cfg = %#v, want loaded config", cfg)
			}
			return fake, func() { cleanupCalled = true }, nil
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentWakeCommandWithFactory() err = %v", err)
	}
	if fake.agentID != "image2" || !fake.now.Equal(wakeTime) {
		t.Fatalf("wake call = agent %q now %s, want image2 %s", fake.agentID, fake.now.Format(time.RFC3339Nano), wakeTime.Format(time.RFC3339Nano))
	}
	if !cleanupCalled {
		t.Fatal("cleanup was not called")
	}
	for _, needle := range []string{"action: durable-agent wake", "agent_id: image2", "status: completed"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("wake output = %q, want substring %q", out, needle)
		}
	}
}

func TestRunDurableAgentCommandAcceptsWakeSubcommand(t *testing.T) {
	err := runDurableAgentCommand([]string{"wake"})
	if err == nil || !strings.Contains(err.Error(), "durable-agent wake requires --agent") {
		t.Fatalf("runDurableAgentCommand(wake) err = %v, want --agent requirement", err)
	}
}

func TestRunDurableAgentListShowsRegisteredAgents(t *testing.T) {
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

	for _, agent := range []core.DurableAgent{
		{
			AgentID:            "family-group",
			ReviewTargetChatID: 1001,
			ChannelKind:        "telegram_group",
			Status:             "active",
			BootstrapLLM: core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "openrouter",
				APIKey:         "sk-or-group",
				Model:          "openrouter/test-model",
			},
			LivePolicy: core.DurableAgentLivePolicy{
				OutboundMode: "reply_with_parent_review",
			},
			PolicyVersion: 2,
		},
		{
			AgentID:            "mail-digest",
			ReviewTargetChatID: 1001,
			ChannelKind:        "external_channel",
			Status:             "draft",
			BootstrapLLM: core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "openrouter",
				APIKey:         "sk-or-mail",
				Model:          "openrouter/test-model",
			},
			LivePolicy: core.DurableAgentLivePolicy{
				OutboundMode: "read_only",
			},
			PolicyVersion: 1,
		},
	} {
		if err := store.UpsertDurableAgent(agent); err != nil {
			t.Fatalf("UpsertDurableAgent(%s) err = %v", agent.AgentID, err)
		}
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentListCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runDurableAgentListCommand() err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent list") || !strings.Contains(out, "count: 2") {
		t.Fatalf("durable-agent list output = %q, want action/count", out)
	}
	if !strings.Contains(out, "agent_id=family-group") || !strings.Contains(out, "agent_id=mail-digest") {
		t.Fatalf("durable-agent list output = %q, want both agents", out)
	}
}

func TestRunDurableAgentHealthShowsStateAndEnrollment(t *testing.T) {
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

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		Status:             "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		LivePolicy: core.DurableAgentLivePolicy{
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		PolicyVersion: 7,
		PolicyHash:    "abcdef1234567890",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:         agent.AgentID,
		LastApplyStatus: "failed",
		LastApplyError:  "child runtime unavailable",
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		KeyFingerprint:   "child-key-fp",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
		LastSequence:     12,
		LastSeenAt:       time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runDurableAgentHealthCommand([]string{"--config", cfgPath, "--agent", "family-group"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentHealthCommand() err = %v", err)
	}
	for _, needle := range []string{
		"action: durable-agent health",
		"agent_id: family-group",
		"health: degraded",
		"last_apply_status: failed",
		"last_apply_error: child runtime unavailable",
		"enrollment: present",
		"enrollment_status: active",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("durable-agent health output = %q, want substring %q", out, needle)
		}
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

func TestRunDurableAgentEnrollmentShowRevokeAndReactivate(t *testing.T) {
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

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
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
		ControlPlaneSecret: "enroll-token-1",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		KeyFingerprint:   "child-key-fp",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	showOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "show"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(show) err = %v", err)
	}
	if !strings.Contains(showOut, "status: active") {
		t.Fatalf("show output = %q, want active status", showOut)
	}

	revokeOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "revoke"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(revoke) err = %v", err)
	}
	if !strings.Contains(revokeOut, "status: revoked") {
		t.Fatalf("revoke output = %q, want revoked status", revokeOut)
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(revoked) err = %v", err)
	}
	if enrollment.Status != "revoked" || enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after revoke = %#v, want revoked with timestamp", enrollment)
	}

	reactivateOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "reactivate"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(reactivate) err = %v", err)
	}
	if !strings.Contains(reactivateOut, "status: active") {
		t.Fatalf("reactivate output = %q, want active status", reactivateOut)
	}
	enrollment, err = store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(reactivated) err = %v", err)
	}
	if enrollment.Status != "active" || !enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after reactivate = %#v, want active without revocation", enrollment)
	}
}

func TestRunDurableAgentEnrollmentRotateSecretAndDecommission(t *testing.T) {
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

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
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
		ControlPlaneSecret: "enroll-token-1",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		KeyFingerprint:   "child-key-fp",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	rotateOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "--secret", "enroll-token-2", "rotate-secret"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(rotate-secret) err = %v", err)
	}
	if !strings.Contains(rotateOut, "status: active") {
		t.Fatalf("rotate-secret output = %q, want active status", rotateOut)
	}
	updatedAgent, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(rotated) err = %v", err)
	}
	if updatedAgent.ControlPlaneSecret != "enroll-token-2" {
		t.Fatalf("ControlPlaneSecret = %q, want enroll-token-2", updatedAgent.ControlPlaneSecret)
	}

	decommissionOut, err := captureStdout(t, func() error {
		return runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "decommission"})
	})
	if err != nil {
		t.Fatalf("runDurableAgentEnrollmentCommand(decommission) err = %v", err)
	}
	if !strings.Contains(decommissionOut, "status: decommissioned") {
		t.Fatalf("decommission output = %q, want decommissioned status", decommissionOut)
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment(decommissioned) err = %v", err)
	}
	if enrollment.Status != "decommissioned" || enrollment.RevokedAt.IsZero() {
		t.Fatalf("enrollment after decommission = %#v, want decommissioned with timestamp", enrollment)
	}

	if err := runDurableAgentEnrollmentCommand([]string{"--config", cfgPath, "--agent", "family-group", "reactivate"}); err == nil {
		t.Fatal("runDurableAgentEnrollmentCommand(reactivate) err = nil, want decommissioned refusal")
	} else if !strings.Contains(err.Error(), "decommissioned") {
		t.Fatalf("runDurableAgentEnrollmentCommand(reactivate) err = %v, want decommissioned refusal", err)
	}
}

func TestRunDurableAgentRemoteRunOnceSyncsAndUploadsArtifacts(t *testing.T) {
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

func TestRunDurableAgentRemoteLoopProcessesQueuedMessages(t *testing.T) {
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

	workspaceRoot := filepath.Join(root, "durable-work")
	memoryRoot := filepath.Join(root, "durable-memory")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o700); err != nil {
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

	inboxDir := filepath.Join(root, "remote-inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox) err = %v", err)
	}
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
	if err := os.WriteFile(filepath.Join(inboxDir, "0001.json"), msgRaw, 0o600); err != nil {
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
			"--inbox-dir", inboxDir,
			"--iterations", "1",
			"loop",
		})
	})
	if err != nil {
		t.Fatalf("runDurableAgentRemoteCommand(loop) err = %v", err)
	}
	if !strings.Contains(out, "action: durable-agent remote loop") {
		t.Fatalf("remote loop output = %q, want action line", out)
	}
	if !strings.Contains(out, "messages_processed: 1") {
		t.Fatalf("remote loop output = %q, want processed count", out)
	}

	parentEvents, err := parentStore.PendingReviewEvents(1, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
	if _, err := os.Stat(filepath.Join(inboxDir, "0001.json")); !os.IsNotExist(err) {
		t.Fatalf("message file still exists, err=%v", err)
	}
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

func TestVerifyDeploymentSuccessRunsGoldenPathAndCleansProbeSession(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs: []int64{42},
			},
		},
		Sessions: config.SessionsConfig{
			DBPath: filepath.Join(root, "state", "sessions.db"),
			TESRetention: config.SessionsTESRetentionConfig{
				Enabled:         true,
				MaxAge:          "168h",
				MinRetainedRows: 300,
				MaxDeletePerGC:  50,
				ExportDir:       filepath.Join(root, "state", "tes-exports"),
			},
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

	origBuilder := deployVerificationRuntimeBuilder
	defer func() { deployVerificationRuntimeBuilder = origBuilder }()

	deployVerificationRuntimeBuilder = func(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
		sender := &deployVerificationSender{}
		reply := "DEPLOYMENT VERIFIED: the service is ready."
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
				state := session.PlanState{
					Explanation: "tool probe",
					Steps: []session.PlanStep{
						{Step: "tool path", Status: session.PlanStatusInProgress},
					},
				}
				if err := store.UpdatePlanStateWithEvent(key, state, session.PlanEventKindToolUpdated); err != nil {
					return "", err
				}
				return "tool probe persisted plan state", nil
			},
		}, nil
	}

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err != nil {
		t.Fatalf("verifyDeployment() err = %v", err)
	}
	if report.Status != "passed" {
		t.Fatalf("report.Status = %q, want passed", report.Status)
	}
	if !report.Blessed {
		t.Fatal("report.Blessed = false, want true")
	}
	if len(report.Probes) != 5 {
		t.Fatalf("probe len = %d, want 5", len(report.Probes))
	}
	bootProbe := report.Probes[0]
	if bootProbe.Name != "boot" {
		t.Fatalf("first probe = %q, want boot", bootProbe.Name)
	}
	if !strings.Contains(bootProbe.Detail, "export_dir=") {
		t.Fatalf("boot probe detail = %q, want retention summary with export_dir", bootProbe.Detail)
	}

	db, err := sql.Open("sqlite3", cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("sql.Open() err = %v", err)
	}
	defer db.Close()

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, report.ProbeSessionID).Scan(&remaining); err != nil {
		t.Fatalf("query probe session cleanup: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("probe session rows = %d, want 0 after successful cleanup", remaining)
	}
}

func TestVerifyDeploymentFailsRequiredDurableChildWake(t *testing.T) {
	cfg := newVerifyDeployTestConfig(t)
	installSuccessfulDeployVerificationBuilder(t,
		"DEPLOYMENT VERIFIED: the service is ready.",
		func(store *session.SQLiteStore) error {
			return store.UpsertDurableAgent(core.DurableAgent{
				AgentID:     "paper-scout",
				ChannelKind: "external_channel",
				Status:      "active",
				LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
					Charter:      "Review reports.",
					OutboundMode: "read_only",
				}),
			})
		},
		func(context.Context, string, time.Time) error {
			return fmt.Errorf("child wake unavailable")
		},
	)

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err == nil {
		t.Fatal("verifyDeployment() err = nil, want durable child failure")
	}
	if !strings.Contains(err.Error(), "durable child wake failed") {
		t.Fatalf("verifyDeployment() err = %v, want durable child wake failure", err)
	}
	last := report.Probes[len(report.Probes)-1]
	if last.Name != "durable_children" || last.Status != deployProbeStatusFail {
		t.Fatalf("last probe = %#v, want failed durable_children", last)
	}
}

func TestVerifyDeploymentWarnsDurableChildWake(t *testing.T) {
	cfg := newVerifyDeployTestConfig(t)
	installSuccessfulDeployVerificationBuilder(t,
		"DEPLOYMENT VERIFIED: the service is ready.",
		func(store *session.SQLiteStore) error {
			return store.UpsertDurableAgent(core.DurableAgent{
				AgentID:     "paper-scout",
				ChannelKind: "external_channel",
				Status:      "active",
				LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
					Charter:      "Review reports.",
					OutboundMode: "read_only",
				}),
			})
		},
		func(context.Context, string, time.Time) error {
			return fmt.Errorf("child wake unavailable")
		},
	)

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath:      "/tmp/aphelion.toml",
		DurableChildren: "warn",
	})
	if err != nil {
		t.Fatalf("verifyDeployment() err = %v, want warning-only pass", err)
	}
	if report.Status != "passed" {
		t.Fatalf("report.Status = %q, want passed", report.Status)
	}
	last := report.Probes[len(report.Probes)-1]
	if last.Name != "durable_children" || last.Status != deployProbeStatusPass || !strings.Contains(last.Detail, "warning:") {
		t.Fatalf("last probe = %#v, want warning durable_children pass", last)
	}
}

func TestVerifyDeploymentRejectsInternalLayerLeakInBlessingReply(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
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

	origBuilder := deployVerificationRuntimeBuilder
	defer func() { deployVerificationRuntimeBuilder = origBuilder }()

	deployVerificationRuntimeBuilder = func(cfg *config.Config, store *session.SQLiteStore) (builtDeployVerificationRuntime, error) {
		sender := &deployVerificationSender{}
		reply := "DEPLOYMENT VERIFIED: governor and Idolum are ready."
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
		}, nil
	}

	report, err := verifyDeployment(context.Background(), cfg, deployVerificationOptions{
		ConfigPath: "/tmp/aphelion.toml",
	})
	if err == nil {
		t.Fatal("verifyDeployment() err = nil, want leaked internal layer failure")
	}
	if !strings.Contains(err.Error(), "leaked internal layer markers") {
		t.Fatalf("verifyDeployment() err = %v, want leaked internal layer markers", err)
	}
	if report.Status != "failed" {
		t.Fatalf("report.Status = %q, want failed", report.Status)
	}
}

func TestRunVerifyDeployCommandPrintsFailureReport(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)

	origRunner := deployVerificationRunner
	defer func() { deployVerificationRunner = origRunner }()

	deployVerificationRunner = func(_ context.Context, _ *config.Config, _ deployVerificationOptions) (deployVerificationReport, error) {
		return deployVerificationReport{
			Status:         "failed",
			Blessed:        false,
			ProbeChatID:    -9100000001,
			ProbeSessionID: "telegram_dm:-9100000001",
			Diagnosis:      "deployment verification failed on the live governed reply path: no outbound reply",
			Probes: []deployProbeResult{
				{Name: "boot", Status: deployProbeStatusPass, DurationMS: 12, Detail: "runtime initialized"},
				{Name: "golden_path", Status: deployProbeStatusFail, DurationMS: 18, Detail: "no outbound reply"},
			},
		}, fmt.Errorf("no outbound reply")
	}

	out, err := captureStdout(t, func() error {
		return runVerifyDeployCommand([]string{"--config", cfgPath})
	})
	if err == nil {
		t.Fatal("runVerifyDeployCommand() err = nil, want failure")
	}
	if !strings.Contains(out, "action: verify-deploy") {
		t.Fatalf("verify-deploy output = %q, want action header", out)
	}
	if !strings.Contains(out, "status: failed") {
		t.Fatalf("verify-deploy output = %q, want failed status", out)
	}
	if !strings.Contains(out, "golden_path: fail") {
		t.Fatalf("verify-deploy output = %q, want golden_path failure", out)
	}
}

func TestRunParkRestartCommandParksLiveContinuation(t *testing.T) {
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
	key := session.SessionKey{
		ChatID: 9901,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9901"},
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "deploy-decision",
		Objective:      "Resume after service reinstall.",
		StageSummary:   "Approved before a deploy restart.",
		RemainingTurns: 1,
		ApprovedBy:     1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-deploy-decision",
			Summary:       "Resume after deploy",
			BoundedEffect: "One bounded follow-up after restart.",
			Status:        session.ProposalStatusApproved,
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-deploy-decision",
			ProposalID:     "aprop-deploy-decision",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1,
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	out, err := captureStdout(t, func() error {
		return runParkRestartCommand([]string{"--config", cfgPath, "--source", "test_reinstall"})
	})
	if err != nil {
		t.Fatalf("runParkRestartCommand() err = %v", err)
	}
	if !strings.Contains(out, "action: park-restart") || !strings.Contains(out, "approved_continuations_parked: 1") {
		t.Fatalf("park-restart output = %q, want parked approved continuation", out)
	}
	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()
	parked, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if parked.ParkedSource != "test_reinstall" || parked.Status != session.ContinuationStatusApproved {
		t.Fatalf("parked continuation = %#v, want approved test_reinstall marker", parked)
	}
}

func TestRepairLiveStateClosesContinuationsPlanLeasesAndPendingDecisions(t *testing.T) {
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
	key := session.SessionKey{
		ChatID: 9902,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9902"},
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "stale-decision",
		Objective:      "Old turn-by-turn recovery.",
		StageSummary:   "Generated before live repair.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-stale-decision",
			Summary:       "Run one more stale turn",
			BoundedEffect: "Continue old turn-by-turn work.",
			Status:        session.ProposalStatusPending,
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-stale-decision",
			ProposalID:     "aprop-stale-decision",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      time.Now().UTC().Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "op-stale",
		Status:  session.OperationStatusActive,
		Stage:   "old_live_loop",
		Summary: "Old state",
		PlanLease: session.OperationPlanLease{
			ID:             "plan-lease-stale",
			Summary:        "Old one-step lease bundle",
			Status:         session.PlanLeaseStatusActive,
			TurnBudget:     1,
			RemainingTurns: 1,
			Lanes: []session.OperationPlanLeaseLane{{
				ID:             "lane-1",
				Summary:        "Old lane",
				AuthorityClass: "read_only_review",
				ExpectedTurns:  1,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:          "decision-stale",
		OwnerKey:    "telegram:9902",
		Kind:        "exec",
		ChatID:      9902,
		Prompt:      "Approve stale tool call?",
		ChoicesJSON: `[{"id":"approve","label":"Approve"}]`,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}

	result, err := repairLiveState(context.Background(), store, "test_live_repair", true, time.Now().UTC())
	if err != nil {
		t.Fatalf("repairLiveState() err = %v", err)
	}
	if result.ContinuationsClosed != 1 || result.PlanLeasesRevoked != 1 || result.PendingDecisionsCleared != 1 {
		t.Fatalf("repair result = %#v, want continuation, plan lease, and decision cleaned", result)
	}
	continuation, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if continuation.Status != session.ContinuationStatusRevoked || continuation.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation = %#v, want revoked lease", continuation)
	}
	_, op, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if !exists || op.PlanLease.Status != session.PlanLeaseStatusRevoked || !strings.Contains(op.Summary, "Live state repair revoked") {
		t.Fatalf("operation = %#v exists=%t, want revoked plan lease repair note", op, exists)
	}
	decisions, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("pending decisions = %#v, want cleared", decisions)
	}
}

func TestRepairLiveStateRepairsMetadataAuthorityDriftWithoutClosingLive(t *testing.T) {
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
	key := session.SessionKey{
		ChatID: 9903,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "9903"},
	}
	leaseID := "lease-metadata-drift"
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-metadata-drift",
		Objective: "Resume metadata-only preflight.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval",
		Proposal: session.OperationProposal{
			ID:      "phase-op-metadata-drift-phase-metadata",
			Kind:    session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			Summary: "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
			Status:  session.ProposalStatusApproved,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-metadata-drift",
			CurrentPhaseID: "phase-metadata",
			Phases: []session.OperationPhase{{
				ID:             "phase-metadata",
				Summary:        "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
				Status:         session.PlanStatusPending,
				AuthorityClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
				BoundedEffect:  "No action under this phase until approval is real and visible.",
				AllowedActions: []string{"report_button_diagnosis"},
				LeaseID:        leaseID,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:       session.ContinuationStatusRevoked,
		StageSummary: "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
		ActionProposal: session.ActionProposal{
			ID:        "aprop-phase-op-metadata-drift-phase-metadata",
			RiskClass: session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			Summary:   "Live-adjacent metadata preflight. BLOCKED: approval button render failed; auto-approved lease was revoked after action_not_allowed/workspace_write mismatch.",
			Status:    session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             leaseID,
			ProposalID:     "aprop-phase-op-metadata-drift-phase-metadata",
			Status:         session.ContinuationLeaseStatusRevoked,
			MaxTurns:       1,
			RemainingTurns: 0,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	result, err := repairLiveState(context.Background(), store, "test_authority_repair", false, time.Now().UTC())
	if err != nil {
		t.Fatalf("repairLiveState() err = %v", err)
	}
	if result.AuthorityContractsRepaired != 1 || result.ContinuationsClosed != 0 {
		t.Fatalf("repair result = %#v, want one authority repair without broad close", result)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	phase := op.PhasePlan.Phases[0]
	if strings.Contains(phase.Summary, "workspace_write mismatch") || phase.LeaseID != "" || phase.Status != session.PlanStatusPending {
		t.Fatalf("phase = %#v, want cleaned pending phase without stale lease", phase)
	}
	if !stringSliceContains(phase.AllowedActions, session.AuthorityWorkActionReadOnly) || stringSliceContains(phase.AllowedActions, "workspace_write") {
		t.Fatalf("allowed actions = %#v, want read_only but not workspace_write", phase.AllowedActions)
	}
	if !stringSliceContains(phase.ForbiddenActions, "telegram_api_call") || !stringSliceContains(phase.ForbiddenActions, "read_token_contents") {
		t.Fatalf("forbidden actions = %#v, want metadata/live-effect denials", phase.ForbiddenActions)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ActionProposal.Status != session.ProposalStatusSuperseded || strings.Contains(cont.StageSummary, "workspace_write mismatch") {
		t.Fatalf("continuation = %#v, want superseded cleaned prior proposal", cont)
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

func TestPruneExecutionEventsForRetentionExportsThenPrunes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: 4242,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "telegram_dm:4242"},
	}
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 130; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: fmt.Sprintf(`{"index":%d}`, i),
			CreatedAt:   now.Add(-72*time.Hour + time.Duration(i)*time.Minute),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}

	cfg := config.Default()
	cfg.Sessions.DBPath = dbPath
	cfg.Sessions.TESRetention.Enabled = true
	cfg.Sessions.TESRetention.MaxAge = "24h"
	cfg.Sessions.TESRetention.MinRetainedRows = 100
	cfg.Sessions.TESRetention.MaxDeletePerGC = 3
	cfg.Sessions.TESRetention.ExportDir = filepath.Join(root, "tes-exports")

	removed, status, err := pruneExecutionEventsForRetention(&cfg, now)
	if err != nil {
		t.Fatalf("pruneExecutionEventsForRetention() err = %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}
	if !strings.Contains(status, "export=") {
		t.Fatalf("status = %q, want export path", status)
	}

	remaining, err := store.ExecutionEventsBySession(key, 0, 1000)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(remaining) != 127 {
		t.Fatalf("remaining events = %d, want 127", len(remaining))
	}
	remainingIDs := make(map[int64]struct{}, len(remaining))
	for _, event := range remaining {
		remainingIDs[event.ID] = struct{}{}
	}

	exports, err := filepath.Glob(filepath.Join(cfg.Sessions.TESRetention.ExportDir, "*", "*.json"))
	if err != nil {
		t.Fatalf("Glob(export files) err = %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("export files = %#v, want one export file", exports)
	}
	raw, err := os.ReadFile(exports[0])
	if err != nil {
		t.Fatalf("ReadFile(%s) err = %v", exports[0], err)
	}
	var bundle tesRetentionExportBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("json.Unmarshal(export) err = %v", err)
	}
	if bundle.SchemaVersion != "tes_retention_export.v1" {
		t.Fatalf("SchemaVersion = %q, want tes_retention_export.v1", bundle.SchemaVersion)
	}
	if bundle.Count != 3 || len(bundle.Events) != 3 {
		t.Fatalf("bundle count/events = %d/%d, want 3/3", bundle.Count, len(bundle.Events))
	}
	if bundle.FirstID == 0 || bundle.LastID == 0 {
		t.Fatalf("bundle id bounds = %d/%d, want non-zero", bundle.FirstID, bundle.LastID)
	}
	if bundle.Events[0].ID != bundle.FirstID || bundle.Events[len(bundle.Events)-1].ID != bundle.LastID {
		t.Fatalf("bundle event bounds mismatch: first=%d last=%d events=%#v", bundle.FirstID, bundle.LastID, bundle.Events)
	}
	for _, event := range bundle.Events {
		if _, ok := remainingIDs[event.ID]; ok {
			t.Fatalf("exported event id %d still present after prune", event.ID)
		}
	}
}

func TestPruneExecutionEventsForRetentionNoOpDoesNotExport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: 5151,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "telegram_dm:5151"},
	}
	now := time.Date(2026, time.April, 22, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: fmt.Sprintf(`{"index":%d}`, i),
			CreatedAt:   now.Add(-2 * time.Hour).Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("AppendExecutionEvent(%d) err = %v", i, err)
		}
	}

	cfg := config.Default()
	cfg.Sessions.DBPath = dbPath
	cfg.Sessions.TESRetention.Enabled = true
	cfg.Sessions.TESRetention.MaxAge = "24h"
	cfg.Sessions.TESRetention.MinRetainedRows = 100
	cfg.Sessions.TESRetention.MaxDeletePerGC = 2
	cfg.Sessions.TESRetention.ExportDir = filepath.Join(root, "tes-exports")

	removed, status, err := pruneExecutionEventsForRetention(&cfg, now)
	if err != nil {
		t.Fatalf("pruneExecutionEventsForRetention() err = %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if !strings.Contains(status, "result=no-op") {
		t.Fatalf("status = %q, want result=no-op", status)
	}
	exports, err := filepath.Glob(filepath.Join(cfg.Sessions.TESRetention.ExportDir, "*", "*.json"))
	if err != nil {
		t.Fatalf("Glob(export files) err = %v", err)
	}
	if len(exports) != 0 {
		t.Fatalf("export files = %#v, want none for no-op prune", exports)
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
