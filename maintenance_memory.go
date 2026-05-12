//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

func runGCCommand(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	expired := 0
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
		d, err := time.ParseDuration(cfg.Sessions.IdleExpiry)
		if err != nil {
			return fmt.Errorf("parse sessions.idle_expiry: %w", err)
		}
		expired, err = store.ExpireIdle(d)
		if err != nil {
			return err
		}
	}

	removedTemps, err := cleanupTempTrees(cfg)
	if err != nil {
		return err
	}
	archivedNotes, err := archiveColdDailyNotes(cfg, time.Now())
	if err != nil {
		return err
	}
	archivedCurated, err := archiveOversizedCuratedMemory(cfg, time.Now())
	if err != nil {
		return err
	}
	prunedTESEvents, tesRetentionState, err := pruneExecutionEventsForRetention(cfg, time.Now().UTC())
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "expired_sessions: %d\n", expired)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	fmt.Fprintf(os.Stdout, "archived_daily_notes: %d\n", archivedNotes)
	fmt.Fprintf(os.Stdout, "archived_curated_memory: %d\n", archivedCurated)
	fmt.Fprintf(os.Stdout, "tes_retention: %s\n", tesRetentionState)
	fmt.Fprintf(os.Stdout, "pruned_execution_events: %d\n", prunedTESEvents)
	return nil
}

func runForgetCommand(args []string) error {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	chatID := fs.Int64("chat", 0, "chat id to forget")
	sessionUserID := fs.Int64("session-user", 0, "session user id (default 0)")
	principalID := fs.Int64("principal", 0, "telegram principal id to forget")
	sharedMemory := fs.Bool("shared-memory", false, "clear shared dynamic memory files")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	var deletedSessions int
	store, err := openStoreIfExists(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	if store != nil {
		defer store.Close()
	}

	if *chatID > 0 {
		if store == nil {
			return fmt.Errorf("sessions database %s does not exist", cfg.Sessions.DBPath)
		}
		n, err := store.DeleteSession(session.SessionKey{ChatID: *chatID, UserID: *sessionUserID})
		if err != nil {
			return err
		}
		deletedSessions += n
	}

	removedPrincipalRoots := 0
	if *principalID > 0 {
		if store != nil {
			n, err := store.DeleteSession(session.SessionKey{ChatID: *principalID, UserID: 0})
			if err != nil {
				return err
			}
			deletedSessions += n
		}
		for _, path := range []string{
			filepath.Join(cfg.Agent.UserWorkspaceRoot, fmt.Sprintf("%d", *principalID)),
			filepath.Join(cfg.Agent.UserMemoryRoot, fmt.Sprintf("%d", *principalID)),
		} {
			ok, err := removeAllIfExists(path)
			if err != nil {
				return err
			}
			if ok {
				removedPrincipalRoots++
			}
		}
		if store != nil {
			if err := store.ResetRhizome(filepath.Clean(filepath.Join(cfg.Agent.UserMemoryRoot, fmt.Sprintf("%d", *principalID)))); err != nil {
				return err
			}
		}
	}

	removedSharedFiles := 0
	if *sharedMemory {
		removedSharedFiles, err = clearSharedDynamicMemory(cfg)
		if err != nil {
			return err
		}
		if store != nil {
			if err := store.ResetRhizome(filepath.Clean(cfg.Agent.SharedMemoryRoot)); err != nil {
				return err
			}
		}
	}

	if *chatID == 0 && *principalID == 0 && !*sharedMemory {
		return fmt.Errorf("forget requires at least one target: --chat, --principal, or --shared-memory")
	}

	fmt.Fprintf(os.Stdout, "deleted_sessions: %d\n", deletedSessions)
	fmt.Fprintf(os.Stdout, "removed_principal_roots: %d\n", removedPrincipalRoots)
	fmt.Fprintf(os.Stdout, "removed_shared_memory_paths: %d\n", removedSharedFiles)
	return nil
}

func runResetCommand(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	scope := fs.String("scope", "runtime", "reset scope: runtime|memory|all")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	scopeValue := strings.ToLower(strings.TrimSpace(*scope))
	switch scopeValue {
	case "runtime", "memory", "all":
	default:
		return fmt.Errorf("reset scope must be one of runtime|memory|all")
	}

	runtimeReset := scopeValue == "runtime" || scopeValue == "all"
	memoryReset := scopeValue == "memory" || scopeValue == "all"

	if runtimeReset {
		store, err := openStoreIfExists(cfg.Sessions.DBPath)
		if err != nil {
			return err
		}
		if store != nil {
			if err := store.ResetRuntime(); err != nil {
				_ = store.Close()
				return err
			}
			if err := store.Close(); err != nil {
				return err
			}
		}
	}

	removedUserWorkspaces := 0
	if runtimeReset {
		removedUserWorkspaces, err = removeContents(cfg.Agent.UserWorkspaceRoot)
		if err != nil {
			return err
		}
	}

	removedSharedMemory := 0
	removedUserMemory := 0
	if memoryReset {
		removedSharedMemory, err = clearSharedDynamicMemory(cfg)
		if err != nil {
			return err
		}
		removedUserMemory, err = removeContents(cfg.Agent.UserMemoryRoot)
		if err != nil {
			return err
		}
		store, err := openStoreIfExists(cfg.Sessions.DBPath)
		if err != nil {
			return err
		}
		if store != nil {
			defer store.Close()
			if err := store.ResetAllRhizome(); err != nil {
				return err
			}
		}
	}

	removedTemps, err := cleanupTempTrees(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "reset_scope: %s\n", scopeValue)
	fmt.Fprintf(os.Stdout, "removed_user_workspaces: %d\n", removedUserWorkspaces)
	fmt.Fprintf(os.Stdout, "removed_shared_memory_paths: %d\n", removedSharedMemory)
	fmt.Fprintf(os.Stdout, "removed_user_memory_entries: %d\n", removedUserMemory)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	return nil
}

func runImportAuditCommand(args []string) error {
	fs := flag.NewFlagSet("import-audit", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	scope := fs.String("scope", "", "filter by scope: shared|principal")
	principalID := fs.String("principal", "", "filter by principal key")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "filter by import state")
	id := fs.Int64("id", 0, "document id for review/approve/reject")
	limit := fs.Int("limit", 20, "max documents to list")
	chunks := fs.Int("chunks", 6, "max chunk excerpts to show during review")
	maxChars := fs.Int("max_chars", 4000, "max excerpt chars during review")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "list"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return err
	}
	defer engine.Close()

	ctx := context.Background()
	switch action {
	case "", "list":
		docs, err := engine.ListImportAudit(ctx, memstore.SemanticAuditFilter{
			State:       memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state))),
			Scope:       *scope,
			PrincipalID: *principalID,
			Limit:       *limit,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: list\n")
		if len(docs) == 0 {
			fmt.Fprintf(os.Stdout, "documents: 0\n")
			return nil
		}
		fmt.Fprintf(os.Stdout, "documents: %d\n", len(docs))
		for _, doc := range docs {
			fmt.Fprintf(os.Stdout, "- id=%d scope=%s", doc.ID, doc.Scope)
			if strings.TrimSpace(doc.PrincipalID) != "" {
				fmt.Fprintf(os.Stdout, " principal=%s", doc.PrincipalID)
			}
			fmt.Fprintf(os.Stdout, " state=%s kind=%s provenance=%s source=%s\n",
				doc.ImportState,
				doc.SourceKind,
				doc.ProvenanceSource,
				doc.SourcePath,
			)
		}
		return nil
	case "review":
		if *id <= 0 {
			return fmt.Errorf("import-audit review requires --id")
		}
		review, err := engine.ReviewImportDocument(ctx, *id, *chunks, *maxChars)
		if err != nil {
			return err
		}
		doc := review.Document
		fmt.Fprintf(os.Stdout, "action: review\n")
		fmt.Fprintf(os.Stdout, "id: %d\n", doc.ID)
		fmt.Fprintf(os.Stdout, "scope: %s\n", doc.Scope)
		if strings.TrimSpace(doc.PrincipalID) != "" {
			fmt.Fprintf(os.Stdout, "principal: %s\n", doc.PrincipalID)
		}
		fmt.Fprintf(os.Stdout, "state: %s\n", doc.ImportState)
		fmt.Fprintf(os.Stdout, "kind: %s\n", doc.SourceKind)
		fmt.Fprintf(os.Stdout, "provenance: %s\n", doc.ProvenanceSource)
		fmt.Fprintf(os.Stdout, "source: %s\n", doc.SourcePath)
		fmt.Fprintf(os.Stdout, "chunks: %d\n", review.ChunkCount)
		for i, excerpt := range review.Excerpts {
			fmt.Fprintf(os.Stdout, "\n[%d]\n%s\n", i+1, excerpt)
		}
		return nil
	case "approve":
		if *id <= 0 {
			return fmt.Errorf("import-audit approve requires --id")
		}
		if err := engine.SetImportState(ctx, *id, memstore.SemanticImportStateApproved); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: approve\nid: %d\nstate: %s\n", *id, memstore.SemanticImportStateApproved)
		return nil
	case "reject":
		if *id <= 0 {
			return fmt.Errorf("import-audit reject requires --id")
		}
		if err := engine.SetImportState(ctx, *id, memstore.SemanticImportStateRejected); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: reject\nid: %d\nstate: %s\n", *id, memstore.SemanticImportStateRejected)
		return nil
	default:
		return fmt.Errorf("import-audit action must be one of list|review|approve|reject")
	}
}

func runImportSemanticCommand(args []string) error {
	fs := flag.NewFlagSet("import-semantic", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	dbPath := fs.String("db", "", "path to foreign semantic sqlite db")
	scope := fs.String("scope", "shared", "target scope: shared|principal")
	principalID := fs.String("principal", "", "target principal key when scope=principal")
	provenance := fs.String("provenance", "", "provenance label override")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "initial import state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("import-semantic requires a source type such as openclaw or host")
	}
	sourceType := strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("import-semantic requires --db")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return err
	}
	defer engine.Close()

	importState := memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state)))
	switch sourceType {
	case "openclaw", "host":
		prov := strings.TrimSpace(*provenance)
		if prov == "" {
			if sourceType == "host" {
				prov = "host_archive"
			} else {
				prov = "openclaw_import"
			}
		}
		summary, err := engine.ImportOpenClaw(context.Background(), memstore.SemanticOpenClawImportRequest{
			DBPath:           *dbPath,
			Scope:            *scope,
			PrincipalID:      *principalID,
			ProvenanceSource: prov,
			ImportState:      importState,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: import-semantic\n")
		fmt.Fprintf(os.Stdout, "source: %s\n", summary.Source)
		fmt.Fprintf(os.Stdout, "contract: %s\n", summary.Contract)
		fmt.Fprintf(os.Stdout, "provenance: %s\n", summary.Provenance)
		fmt.Fprintf(os.Stdout, "scope: %s\n", summary.Scope)
		if strings.TrimSpace(summary.PrincipalID) != "" {
			fmt.Fprintf(os.Stdout, "principal: %s\n", summary.PrincipalID)
		}
		fmt.Fprintf(os.Stdout, "documents: %d\n", summary.Documents)
		fmt.Fprintf(os.Stdout, "chunks: %d\n", summary.Chunks)
		fmt.Fprintf(os.Stdout, "embedding_chunks: %d\n", summary.EmbeddedChunkCount)
		fmt.Fprintf(os.Stdout, "embedding_use: %s\n", summary.EmbeddingUse)
		fmt.Fprintf(os.Stdout, "state: %s\n", importState)
		return nil
	default:
		return fmt.Errorf("import-semantic source must be one of openclaw|host")
	}
}

type codexSessionImportCommandOptions struct {
	CodexHome   string
	Lookback    time.Duration
	ActiveGrace time.Duration
	MaxSessions int
	Scope       string
	PrincipalID string
	ImportState memstore.SemanticImportState
}

func defaultCodexSessionImportCommandOptions(cfg *config.Config) codexSessionImportCommandOptions {
	opts := codexSessionImportCommandOptions{
		Lookback:    14 * 24 * time.Hour,
		ActiveGrace: 5 * time.Minute,
		MaxSessions: 50,
		Scope:       "shared",
		ImportState: memstore.SemanticImportStateQuarantine,
	}
	if cfg != nil {
		opts.CodexHome = strings.TrimSpace(cfg.Governor.Codex.CodexHome)
	}
	return opts
}

func runImportCodexSessionsCommand(args []string) error {
	fs := flag.NewFlagSet("import-codex-sessions", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	codexHome := fs.String("codex-home", "", "Codex home directory; defaults to governor.codex.codex_home, CODEX_HOME, or ~/.codex")
	lookback := fs.Duration("lookback", 14*24*time.Hour, "session mtime lookback window")
	activeGrace := fs.Duration("active-grace", 5*time.Minute, "skip sessions modified more recently than this")
	maxSessions := fs.Int("max", 50, "max newest sessions to import")
	scope := fs.String("scope", "shared", "target scope: shared|principal")
	principalID := fs.String("principal", "", "target principal key when scope=principal")
	state := fs.String("state", string(memstore.SemanticImportStateQuarantine), "initial import state: quarantine|approved|rejected")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	opts := defaultCodexSessionImportCommandOptions(cfg)
	if strings.TrimSpace(*codexHome) != "" {
		opts.CodexHome = strings.TrimSpace(*codexHome)
	}
	opts.Lookback = *lookback
	opts.ActiveGrace = *activeGrace
	opts.MaxSessions = *maxSessions
	opts.Scope = *scope
	opts.PrincipalID = *principalID
	opts.ImportState = memstore.SemanticImportState(strings.ToLower(strings.TrimSpace(*state)))
	if err := validateCodexSessionImportState(opts.ImportState); err != nil {
		return err
	}

	result, err := importCodexSessionsForConfig(context.Background(), cfg, opts)
	if err != nil {
		return err
	}
	printCodexSessionImportResult(os.Stdout, result, opts.ImportState)
	return nil
}

func validateCodexSessionImportState(state memstore.SemanticImportState) error {
	switch state {
	case memstore.SemanticImportStateQuarantine, memstore.SemanticImportStateApproved, memstore.SemanticImportStateRejected:
		return nil
	default:
		return fmt.Errorf("state must be one of quarantine|approved|rejected")
	}
}

func importCodexSessionsForConfig(ctx context.Context, cfg *config.Config, opts codexSessionImportCommandOptions) (*memstore.CodexSessionImportResult, error) {
	engine, err := newSemanticEngineForConfig(cfg, true)
	if err != nil {
		return nil, err
	}
	defer engine.Close()
	return engine.ImportCodexSessions(ctx, memstore.CodexSessionImportOptions{
		CodexHome:   opts.CodexHome,
		Lookback:    opts.Lookback,
		ActiveGrace: opts.ActiveGrace,
		MaxSessions: opts.MaxSessions,
		Scope:       opts.Scope,
		PrincipalID: opts.PrincipalID,
		ImportState: opts.ImportState,
	})
}

func printCodexSessionImportResult(w io.Writer, result *memstore.CodexSessionImportResult, state memstore.SemanticImportState) {
	if result == nil {
		return
	}
	fmt.Fprintf(w, "action: import-codex-sessions\n")
	fmt.Fprintf(w, "codex_home: %s\n", result.CodexHome)
	if strings.TrimSpace(result.SessionsDir) != "" {
		fmt.Fprintf(w, "sessions_dir: %s\n", result.SessionsDir)
	}
	fmt.Fprintf(w, "state: %s\n", state)
	fmt.Fprintf(w, "scanned: %d\n", result.Scanned)
	fmt.Fprintf(w, "eligible: %d\n", result.Eligible)
	fmt.Fprintf(w, "imported: %d\n", result.Imported)
	fmt.Fprintf(w, "updated: %d\n", result.Updated)
	fmt.Fprintf(w, "skipped_already_imported: %d\n", result.SkippedAlreadyImported)
	fmt.Fprintf(w, "skipped_old: %d\n", result.SkippedOld)
	fmt.Fprintf(w, "skipped_active: %d\n", result.SkippedActive)
	fmt.Fprintf(w, "skipped_empty: %d\n", result.SkippedEmpty)
	fmt.Fprintf(w, "failed: %d\n", result.Failed)
	for _, failure := range result.Failures {
		fmt.Fprintf(w, "  - path=%s error=%s\n", failure.Path, failure.Error)
	}
}

func clearSharedDynamicMemory(cfg *config.Config) (int, error) {
	root := strings.TrimSpace(cfg.Agent.SharedMemoryRoot)
	if root == "" {
		return 0, nil
	}

	removed := 0
	memoryPath := filepath.Join(root, "MEMORY.md")
	preserved, err := preserveMemoryIdentitySections(memoryPath)
	if err != nil {
		return removed, err
	}
	if preserved {
		removed++
	}

	paths := make([]string, 0, len(cfg.Agent.DynamicFiles)+2)
	for _, name := range cfg.Agent.DynamicFiles {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "MEMORY.md") {
			continue
		}
		paths = append(paths, filepath.Join(root, filepath.FromSlash(trimmed)))
	}
	paths = append(paths, filepath.Join(root, "memory.md"))
	paths = append(paths,
		filepath.Join(root, "memory", "knowledge.md"),
		filepath.Join(root, "memory", "decisions.md"),
		filepath.Join(root, "memory", "questions.md"),
		filepath.Join(root, "memory", "rhizome.md"),
	)
	n, err := removeMany(paths)
	if err != nil {
		return removed, err
	}
	removed += n

	noteRemoved, err := clearDailyNotesUnderRoot(root, cfg.Agent.DailyNotesDir)
	if err != nil {
		return removed, err
	}
	return removed + noteRemoved, nil
}

func cleanupTempTrees(cfg *config.Config) (int, error) {
	paths := []string{
		filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "tmp"),
		filepath.Join(cfg.Agent.SharedMemoryRoot, ".aphelion", "tmp"),
	}

	for _, root := range []string{cfg.Agent.UserWorkspaceRoot, cfg.Agent.UserMemoryRoot} {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("read temp root %s: %w", root, err)
		}
		for _, entry := range entries {
			paths = append(paths, filepath.Join(root, entry.Name(), ".aphelion", "tmp"))
		}
	}
	return removeMany(paths)
}

func archiveColdDailyNotes(cfg *config.Config, now time.Time) (int, error) {
	if cfg == nil || !cfg.Memory.Decay.Enabled || cfg.Memory.Decay.ColdDays <= 0 {
		return 0, nil
	}

	roots := []string{cfg.Agent.SharedMemoryRoot}
	entries, err := os.ReadDir(cfg.Agent.UserMemoryRoot)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read user memory root %s: %w", cfg.Agent.UserMemoryRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(cfg.Agent.UserMemoryRoot, entry.Name()))
		}
	}

	archived := 0
	cutoff := now.AddDate(0, 0, -cfg.Memory.Decay.ColdDays)
	for _, root := range uniqueStrings(roots) {
		n, err := archiveNotesUnderRoot(root, cfg.Agent.DailyNotesDir, cutoff)
		if err != nil {
			return archived, err
		}
		archived += n
	}
	return archived, nil
}

func archiveNotesUnderRoot(root string, notesDir string, cutoff time.Time) (int, error) {
	root = strings.TrimSpace(root)
	notesDir = strings.TrimSpace(notesDir)
	if root == "" || notesDir == "" {
		return 0, nil
	}

	sourceRoot := filepath.Join(root, filepath.FromSlash(notesDir))
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read daily notes dir %s: %w", sourceRoot, err)
	}

	archiveRoot := notesArchiveRoot(root, notesDir)
	archived := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		ts, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".md"))
		if err != nil {
			continue
		}
		if !ts.Before(cutoff) {
			continue
		}
		if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
			return archived, fmt.Errorf("create daily archive dir %s: %w", archiveRoot, err)
		}
		src := filepath.Join(sourceRoot, name)
		dst := filepath.Join(archiveRoot, name)
		if err := os.Rename(src, dst); err != nil {
			return archived, fmt.Errorf("archive daily note %s -> %s: %w", src, dst, err)
		}
		archived++
	}
	return archived, nil
}

func clearDailyNotesUnderRoot(root string, notesDir string) (int, error) {
	root = strings.TrimSpace(root)
	notesDir = strings.TrimSpace(notesDir)
	if root == "" || notesDir == "" {
		return 0, nil
	}

	sourceRoot := filepath.Join(root, filepath.FromSlash(notesDir))
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read daily notes dir %s: %w", sourceRoot, err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isDailyNoteFilename(name) {
			continue
		}
		if err := os.Remove(filepath.Join(sourceRoot, name)); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove daily note %s: %w", filepath.Join(sourceRoot, name), err)
		}
		removed++
	}
	return removed, nil
}

func notesArchiveRoot(root string, notesDir string) string {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(notesDir)))
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || strings.TrimSpace(base) == "" {
		base = "daily"
	}
	if dir == "." {
		return filepath.Join(root, "archive", base)
	}
	return filepath.Join(root, dir, "archive", base)
}

func isDailyNoteFilename(name string) bool {
	if filepath.Ext(name) != ".md" {
		return false
	}
	_, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".md"))
	return err == nil
}

func removeContents(root string) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read directory %s: %w", root, err)
	}

	removed := 0
	for _, entry := range entries {
		ok, err := removeAllIfExists(filepath.Join(root, entry.Name()))
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

func removeMany(paths []string) (int, error) {
	removed := 0
	for _, path := range uniqueStrings(paths) {
		ok, err := removeAllIfExists(path)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

func removeAllIfExists(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, nil
}

func preserveMemoryIdentitySections(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	content, ok := memstore.PreserveMemoryIdentity(string(raw))
	if !ok {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("remove %s: %w", path, err)
		}
		return true, nil
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("rewrite %s: %w", path, err)
	}
	return true, nil
}

func archiveOversizedCuratedMemory(cfg *config.Config, now time.Time) (int, error) {
	if cfg == nil || !cfg.Memory.Decay.Enabled {
		return 0, nil
	}

	roots := []string{cfg.Agent.SharedMemoryRoot}
	entries, err := os.ReadDir(cfg.Agent.UserMemoryRoot)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read user memory root %s: %w", cfg.Agent.UserMemoryRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(cfg.Agent.UserMemoryRoot, entry.Name()))
		}
	}

	archived := 0
	for _, root := range uniqueStrings(roots) {
		n, err := archiveOversizedCuratedUnderRoot(root, now)
		if err != nil {
			return archived, err
		}
		archived += n
	}
	return archived, nil
}

func archiveOversizedCuratedUnderRoot(root string, now time.Time) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, nil
	}

	type limit struct {
		store string
		chars int
	}
	limits := []limit{
		{store: memstore.StoreKnowledge, chars: 12000},
		{store: memstore.StoreDecisions, chars: 12000},
		{store: memstore.StoreQuestions, chars: 8000},
		{store: memstore.StoreRhizome, chars: 8000},
	}

	archived := 0
	for _, item := range limits {
		path, _, err := memstore.ResolveStorePath(root, item.store)
		if err != nil {
			return archived, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return archived, fmt.Errorf("read curated memory %s: %w", path, err)
		}
		content := strings.TrimSpace(string(raw))
		if len(content) <= item.chars {
			continue
		}

		archiveDir := filepath.Join(filepath.Dir(path), "archive")
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return archived, fmt.Errorf("create curated archive dir %s: %w", archiveDir, err)
		}
		archivePath := filepath.Join(archiveDir, fmt.Sprintf("%s-%s.md", item.store, now.UTC().Format("20060102T150405")))
		if err := os.WriteFile(archivePath, raw, 0o600); err != nil {
			return archived, fmt.Errorf("write curated archive %s: %w", archivePath, err)
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return archived, fmt.Errorf("derive curated path %s: %w", path, err)
		}
		compacted := workspace.CompactStructuredMemoryForPrompt(filepath.ToSlash(relPath), content, item.chars)
		if strings.TrimSpace(compacted) == "" {
			compacted = content[:min(item.chars, len(content))]
		}
		if err := os.WriteFile(path, []byte(strings.TrimSpace(compacted)+"\n"), 0o600); err != nil {
			return archived, fmt.Errorf("rewrite curated memory %s: %w", path, err)
		}
		archived++
	}
	return archived, nil
}
