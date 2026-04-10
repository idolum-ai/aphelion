//go:build linux

package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

//go:embed defaults/agent/* defaults/agent/memory/*
var defaultAgentFilesFS embed.FS

var defaultPromptSeedFiles = []string{
	"SOUL.md",
	"IDENTITY.md",
	"USER.md",
	"AGENTS.md",
	"TOOLS.md",
	"BOOTSTRAP.md",
	"IDOLUM.md",
	"QUESTIONS-TO-IDOLUM.md",
}

var defaultSharedMemorySeedFiles = []string{
	"MEMORY.md",
	"HEARTBEAT.md",
	"memory/knowledge.md",
	"memory/decisions.md",
	"memory/questions.md",
	"memory/rhizome.md",
}

const (
	memoryIdentityBegin = "<!-- APHELION:IDENTITY-BEGIN -->"
	memoryIdentityEnd   = "<!-- APHELION:IDENTITY-END -->"
)

func runMaintenanceCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "init":
		return true, runInitCommand(args[1:])
	case "paths":
		return true, runPathsCommand(args[1:])
	case "gc":
		return true, runGCCommand(args[1:])
	case "forget":
		return true, runForgetCommand(args[1:])
	case "reset":
		return true, runResetCommand(args[1:])
	default:
		return false, nil
	}
}

func runInitCommand(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	created, err := seedAgentPromptFiles(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "prompt_root: %s\n", cfg.Agent.PromptRoot)
	fmt.Fprintf(os.Stdout, "created_files: %d\n", len(created))
	for _, path := range created {
		fmt.Fprintf(os.Stdout, "  - %s\n", path)
	}
	return nil
}

func runPathsCommand(args []string) error {
	fs := flag.NewFlagSet("paths", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}

	stable, dynamic, idolumStable, idolumDynamic, err := loadConfiguredPromptFiles(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "legacy_config_path: %s\n", config.LegacyConfigPath())
	fmt.Fprintf(os.Stdout, "prompt_root: %s\n", cfg.Agent.PromptRoot)
	fmt.Fprintf(os.Stdout, "exec_root: %s\n", cfg.Agent.ExecRoot)
	fmt.Fprintf(os.Stdout, "shared_memory_root: %s\n", cfg.Agent.SharedMemoryRoot)
	fmt.Fprintf(os.Stdout, "user_workspace_root: %s\n", cfg.Agent.UserWorkspaceRoot)
	fmt.Fprintf(os.Stdout, "user_memory_root: %s\n", cfg.Agent.UserMemoryRoot)
	fmt.Fprintf(os.Stdout, "sessions_db: %s\n", cfg.Sessions.DBPath)
	printPathGroup("loaded_bootstrap_files", stable)
	printPathGroup("loaded_dynamic_files", dynamic)
	printPathGroup("loaded_idolum_stable_files", idolumStable)
	printPathGroup("loaded_idolum_dynamic_files", idolumDynamic)
	return nil
}

func seedAgentPromptFiles(cfg *config.Config) ([]string, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	promptRoot := strings.TrimSpace(cfg.Agent.PromptRoot)
	sharedMemoryRoot := strings.TrimSpace(cfg.Agent.SharedMemoryRoot)
	if promptRoot == "" {
		return nil, fmt.Errorf("agent.prompt_root is required")
	}
	if sharedMemoryRoot == "" {
		return nil, fmt.Errorf("agent.shared_memory_root is required")
	}

	roots := []string{promptRoot}
	if sharedMemoryRoot != promptRoot {
		roots = append(roots, sharedMemoryRoot)
	}
	for _, root := range roots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, fmt.Errorf("create root %s: %w", root, err)
		}
	}

	created := make([]string, 0, len(defaultPromptSeedFiles)+len(defaultSharedMemorySeedFiles))
	promptCreated, err := seedDefaultFiles(promptRoot, defaultPromptSeedFiles)
	if err != nil {
		return nil, err
	}
	created = append(created, promptCreated...)

	sharedCreated, err := seedDefaultFiles(sharedMemoryRoot, defaultSharedMemorySeedFiles)
	if err != nil {
		return nil, err
	}
	created = append(created, sharedCreated...)

	if cfg.Agent.DailyNotes {
		notesRoot := filepath.Join(sharedMemoryRoot, filepath.FromSlash(cfg.Agent.DailyNotesDir))
		if err := os.MkdirAll(notesRoot, 0o755); err != nil {
			return nil, fmt.Errorf("create daily_notes_dir %s: %w", notesRoot, err)
		}
	}

	sort.Strings(created)
	return uniqueStrings(created), nil
}

func seedDefaultFiles(root string, names []string) ([]string, error) {
	created := make([]string, 0, len(names))
	for _, name := range names {
		target := filepath.Join(root, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat %s: %w", target, err)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create parent directory for %s: %w", target, err)
		}

		raw, err := defaultAgentFilesFS.ReadFile(filepath.ToSlash(filepath.Join("defaults", "agent", name)))
		if err != nil {
			return nil, fmt.Errorf("read default %s: %w", name, err)
		}
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			return nil, fmt.Errorf("write default %s: %w", target, err)
		}
		created = append(created, target)
	}
	return created, nil
}

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

	fmt.Fprintf(os.Stdout, "expired_sessions: %d\n", expired)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	fmt.Fprintf(os.Stdout, "archived_daily_notes: %d\n", archivedNotes)
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

func loadConfigForCommand(override string) (*config.Config, string, error) {
	configPath, err := config.ResolveConfigPath(override)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", &configStartupError{Path: configPath, Err: err}
	}
	return cfg, configPath, nil
}

func openStoreIfExists(dbPath string) (*session.SQLiteStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat sessions db %s: %w", dbPath, err)
	}
	return session.NewSQLiteStore(dbPath)
}

func loadConfiguredPromptFiles(cfg *config.Config) ([]string, []string, []string, []string, error) {
	now := time.Now()

	stableCfg := cfg.Agent
	stableCfg.Workspace = cfg.Agent.PromptRoot
	stableCfg.DynamicFiles = nil
	stableCfg.DailyNotes = false
	stable, err := workspace.LoadPromptContext(stableCfg, now)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load bootstrap files: %w", err)
	}

	dynamicCfg := cfg.Agent
	dynamicCfg.Workspace = cfg.Agent.SharedMemoryRoot
	dynamicCfg.BootstrapFiles = nil
	dynamic, err := workspace.LoadPromptContext(dynamicCfg, now)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load dynamic files: %w", err)
	}

	idolumStable, idolumDynamic, err := face.LoadIdolumPromptFiles(cfg.Agent.PromptRoot)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load idolum files: %w", err)
	}

	return filePaths(stable.Stable), filePaths(dynamic.Dynamic), filePaths(idolumStable), filePaths(idolumDynamic), nil
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

	preserved := extractMarkedBlock(string(raw), memoryIdentityBegin, memoryIdentityEnd)
	if strings.TrimSpace(preserved) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("remove %s: %w", path, err)
		}
		return true, nil
	}

	content := strings.TrimSpace(strings.Join([]string{
		"# MEMORY.md — Shared Curated Memory",
		"",
		"Keep this file concise.",
		"",
		preserved,
	}, "\n"))
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		return false, fmt.Errorf("rewrite %s: %w", path, err)
	}
	return true, nil
}

func extractMarkedBlock(raw string, startMarker string, endMarker string) string {
	start := strings.Index(raw, startMarker)
	if start < 0 {
		return ""
	}
	end := strings.Index(raw[start+len(startMarker):], endMarker)
	if end < 0 {
		return ""
	}
	end += start + len(startMarker) + len(endMarker)
	return strings.TrimSpace(raw[start:end])
}

func printPathGroup(label string, values []string) {
	fmt.Fprintf(os.Stdout, "%s:\n", label)
	if len(values) == 0 {
		fmt.Fprintln(os.Stdout, "  - (none)")
		return
	}
	for _, value := range values {
		fmt.Fprintf(os.Stdout, "  - %s\n", value)
	}
}

func filePaths(files []workspace.LoadedFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}
