//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var captureStdoutMu sync.Mutex

func TestTelegramGlueFilesStayFocused(t *testing.T) {
	t.Parallel()

	limits := map[string]int{
		"internal/telegramcommands/commands.go":                        420,
		"internal/telegramcommands/commands_callback_helpers.go":       220,
		"internal/telegramcommands/commands_callback_router.go":        220,
		"internal/telegramcommands/commands_continuation_callbacks.go": 320,
		"internal/telegramcommands/commands_tailnet_callbacks.go":      180,
		"internal/telegramcommands/commands_threads.go":                520,
		"telegram_command_control.go":                                  1100,
		"telegram_command_threads.go":                                  300,
		"internal/telegramruntime/ingress_replay.go":                   180,
		"internal/telegramruntime/work_surfaces.go":                    140,
	}
	for path, limit := range limits {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) err = %v", path, err)
		}
		lines := strings.Count(string(raw), "\n")
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			lines++
		}
		if lines > limit {
			t.Fatalf("%s has %d lines, over focused-file limit %d", path, lines, limit)
		}
	}
}

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

func TestRunInitCommandInstallsDailyReviewRecipe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	out, err := captureStdout(t, func() error {
		return runInitCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runInitCommand() err = %v", err)
	}
	for _, want := range []string{
		"scheduled_review_recipe: installed",
		"scheduled_review_agent_id: idolum-daily-review",
		"scheduled_review_recipe_id: daily-review",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output = %q, want %q", out, want)
		}
	}

	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	agent, err := store.DurableAgent("idolum-daily-review")
	if err != nil {
		t.Fatalf("DurableAgent(%s) err = %v", "idolum-daily-review", err)
	}
	if agent.ChannelKind != "scheduled_review" || agent.WakeupMode != "poll" || agent.Status != "active" {
		t.Fatalf("daily review agent = %#v, want installed active poll recipe", agent)
	}
}

func TestRunInitCommandMigratesChildMemorySnapshots(t *testing.T) {
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
	workspaceRoot := filepath.Join(root, "child", "workspace")
	memoryRoot := filepath.Join(root, "child", "memory")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:           "paper-scout",
		ChannelKind:       "external_channel",
		Status:            "active",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review incoming reports and negotiate useful surfaces.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	manifest, err := durableagent.CreateSnapshot(agent, &core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: `{"conversation":{"messages":[{"role":"child","text":"saved"}]}}`,
	}, cfg.Sessions.DBPath, "saved", time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CreateSnapshot() err = %v", err)
	}
	targetBase, err := durableagent.SnapshotBaseDir(agent, cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("SnapshotBaseDir() err = %v", err)
	}
	childSnapshotBase := filepath.Join(memoryRoot, ".snapshots")
	if err := os.MkdirAll(childSnapshotBase, 0o755); err != nil {
		t.Fatalf("MkdirAll(childSnapshotBase) err = %v", err)
	}
	if err := os.Rename(filepath.Join(targetBase, manifest.SnapshotID), filepath.Join(childSnapshotBase, manifest.SnapshotID)); err != nil {
		t.Fatalf("Rename(snapshot to child memory source) err = %v", err)
	}
	if err := os.RemoveAll(targetBase); err != nil {
		t.Fatalf("RemoveAll(targetBase) err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	out, err := captureStdout(t, func() error {
		return runInitCommand([]string{"--config", cfgPath})
	})
	if err != nil {
		t.Fatalf("runInitCommand() err = %v", err)
	}
	for _, needle := range []string{
		"snapshots_scanned: 1",
		"snapshots_migrated: 1",
		"snapshot_roots_removed: 1",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("init output = %q, want substring %q", out, needle)
		}
	}
	if _, err := os.Stat(childSnapshotBase); !os.IsNotExist(err) {
		t.Fatalf("child snapshot base stat err = %v, want removed source", err)
	}
	loaded, _, err := durableagent.LoadSnapshot(agent, cfg.Sessions.DBPath, manifest.SnapshotID)
	if err != nil {
		t.Fatalf("LoadSnapshot(migrated) err = %v", err)
	}
	if loaded.SnapshotID != manifest.SnapshotID || loaded.State == nil || !strings.Contains(loaded.State.StateJSON, "saved") {
		t.Fatalf("loaded migrated snapshot = %#v, want saved state", loaded)
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
