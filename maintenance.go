//go:build linux

package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/face"
	memstore "github.com/idolum-ai/aphelion/memory"
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
	case "import-audit":
		return true, runImportAuditCommand(args[1:])
	case "import-semantic":
		return true, runImportSemanticCommand(args[1:])
	case "durable-agent":
		return true, runDurableAgentCommand(args[1:])
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
	archivedCurated, err := archiveOversizedCuratedMemory(cfg, time.Now())
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "expired_sessions: %d\n", expired)
	fmt.Fprintf(os.Stdout, "removed_temp_dirs: %d\n", removedTemps)
	fmt.Fprintf(os.Stdout, "archived_daily_notes: %d\n", archivedNotes)
	fmt.Fprintf(os.Stdout, "archived_curated_memory: %d\n", archivedCurated)
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

func runDurableAgentCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("durable-agent requires a subcommand: policy, forensic, bootstrap, remote, or child-run")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "policy":
		return runDurableAgentPolicyCommand(args[1:])
	case "forensic":
		return runDurableAgentForensicCommand(args[1:])
	case "bootstrap":
		return runDurableAgentBootstrapCommand(args[1:])
	case "remote":
		return runDurableAgentRemoteCommand(args[1:])
	case "child-run":
		return runDurableAgentChildCommand(args[1:])
	default:
		return fmt.Errorf("durable-agent subcommand must be one of policy|forensic|bootstrap|remote|child-run")
	}
}

func runDurableAgentBootstrapCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent bootstrap", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	path := fs.String("path", "", "bootstrap json output path")
	parentControlURL := fs.String("parent-control-url", "", "remote parent control-plane URL")
	enrollmentToken := fs.String("enrollment-token", "", "child enrollment token")
	keyFingerprint := fs.String("key-fingerprint", "", "child key fingerprint")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "write"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if action != "write" {
		return fmt.Errorf("durable-agent bootstrap action must be write")
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --agent")
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --path")
	}
	if strings.TrimSpace(*parentControlURL) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --parent-control-url")
	}
	if strings.TrimSpace(*enrollmentToken) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --enrollment-token")
	}
	if strings.TrimSpace(*keyFingerprint) == "" {
		return fmt.Errorf("durable-agent bootstrap write requires --key-fingerprint")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agent, err := store.DurableAgent(*agentID)
	if err != nil {
		return err
	}
	bootstrap := core.DurableAgentRemoteBootstrap{
		ReviewTargetChatID: agent.ReviewTargetChatID,
		AgentID:            agent.AgentID,
		ParentAgentID:      agent.ParentAgentID,
		ChannelKind:        agent.ChannelKind,
		ParentControlURL:   strings.TrimSpace(*parentControlURL),
		EnrollmentToken:    strings.TrimSpace(*enrollmentToken),
		KeyFingerprint:     strings.TrimSpace(*keyFingerprint),
		ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:       agent.BootstrapLLM,
		BootstrapCeiling:   agent.BootstrapCeiling,
		LocalStorageRoots:  append([]string(nil), agent.LocalStorageRoots...),
		SecretScopes:       append([]string(nil), agent.SecretScopes...),
		NetworkPolicy:      agent.NetworkPolicy,
	}
	if err := durableagent.WriteRemoteBootstrap(*path, bootstrap); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: durable-agent bootstrap write\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", bootstrap.AgentID)
	fmt.Fprintf(os.Stdout, "path: %s\n", strings.TrimSpace(*path))
	fmt.Fprintf(os.Stdout, "parent_control_url: %s\n", bootstrap.ParentControlURL)
	fmt.Fprintf(os.Stdout, "protocol_version: %s\n", bootstrap.ProtocolVersion)
	return nil
}

func runDurableAgentPolicyCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent policy", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	reviewEventID := fs.Int64("review-event", 0, "source review event id for provenance")
	reason := fs.String("reason", "", "operator reason for policy change")
	charter := fs.String("charter", "", "updated charter")
	capabilities := fs.String("capabilities", "", "comma-separated capability envelope override")
	outboundMode := fs.String("outbound-mode", "", "outbound mode override")
	driftPolicy := fs.String("drift-policy", "", "drift policy override")
	publicSurfaceMode := fs.String("public-surface-mode", "", "public surface mode override")
	sharedInferenceReuse := fs.String("shared-inference-reuse", "", "shared inference reuse override")
	sharedInferenceReuseScope := fs.String("shared-inference-reuse-scope", "", "shared inference reuse scope override")
	history := fs.Int("history", 5, "recent policy update entries to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent policy requires --agent")
	}

	action := "show"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	switch action {
	case "", "show":
		agent, err := store.DurableAgent(*agentID)
		if err != nil {
			return err
		}
		updates, err := store.DurableAgentPolicyUpdates(*agentID, *history)
		if err != nil {
			return err
		}
		printDurableAgentPolicy(os.Stdout, *agent, updates)
		return nil
	case "apply":
		agent, err := store.DurableAgent(*agentID)
		if err != nil {
			return err
		}
		if *reviewEventID > 0 {
			event, err := store.ReviewEventByID(*reviewEventID)
			if err != nil {
				return err
			}
			if event.SourceScope.Kind != session.ScopeKindDurableAgent || !durableAgentReviewTargetsAgent(*agentID, event.SourceScope) {
				return fmt.Errorf("review event %d does not belong to durable agent %s", *reviewEventID, strings.TrimSpace(*agentID))
			}
		}

		policy := agent.LivePolicy
		if strings.TrimSpace(*charter) != "" {
			policy.Charter = strings.TrimSpace(*charter)
		}
		if strings.TrimSpace(*capabilities) != "" {
			policy.CapabilityEnvelope = parseCSVValues(*capabilities)
		}
		if strings.TrimSpace(*outboundMode) != "" {
			policy.OutboundMode = strings.TrimSpace(*outboundMode)
		}
		if strings.TrimSpace(*driftPolicy) != "" {
			policy.DriftPolicy = strings.TrimSpace(*driftPolicy)
		}
		if strings.TrimSpace(*publicSurfaceMode) != "" {
			policy.PublicSurfaceMode = strings.TrimSpace(*publicSurfaceMode)
		}
		if strings.TrimSpace(*sharedInferenceReuse) != "" {
			policy.SharedInferenceReuse = strings.TrimSpace(*sharedInferenceReuse)
		}
		if strings.TrimSpace(*sharedInferenceReuseScope) != "" {
			policy.SharedInferenceReuseScope = strings.TrimSpace(*sharedInferenceReuseScope)
		}

		if strings.TrimSpace(*reason) == "" && *reviewEventID > 0 {
			*reason = fmt.Sprintf("ratified from review_event=%d", *reviewEventID)
		}
		updated, update, err := store.ApplyDurableAgentLivePolicy(*agentID, policy, *reviewEventID, *reason)
		if err != nil {
			return err
		}
		if update == nil {
			fmt.Fprintf(os.Stdout, "action: durable-agent policy apply\n")
			fmt.Fprintf(os.Stdout, "agent_id: %s\n", updated.AgentID)
			fmt.Fprintf(os.Stdout, "changed: false\n")
			fmt.Fprintf(os.Stdout, "policy_version: %d\n", updated.PolicyVersion)
			fmt.Fprintf(os.Stdout, "policy_hash: %s\n", updated.PolicyHash)
			return nil
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent policy apply\n")
		fmt.Fprintf(os.Stdout, "agent_id: %s\n", updated.AgentID)
		fmt.Fprintf(os.Stdout, "changed: true\n")
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", updated.PolicyVersion)
		fmt.Fprintf(os.Stdout, "policy_hash: %s\n", updated.PolicyHash)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(os.Stdout, "source_review_event_id: %d\n", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(os.Stdout, "reason: %s\n", update.Reason)
		}
		return nil
	default:
		return fmt.Errorf("durable-agent policy action must be one of show|apply")
	}
}

func runDurableAgentForensicCommand(args []string) error {
	fs := flag.NewFlagSet("durable-agent forensic", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	ref := fs.String("ref", "", "forensic reference to inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	action := "show"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if action != "show" {
		return fmt.Errorf("durable-agent forensic action must be show")
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent forensic show requires --agent")
	}
	if strings.TrimSpace(*ref) == "" {
		return fmt.Errorf("durable-agent forensic show requires --ref")
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agent, err := store.DurableAgent(*agentID)
	if err != nil {
		return err
	}
	record, err := durableagent.ReadForensicRecord(*agent, *ref)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: durable-agent forensic show\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", record.AgentID)
	fmt.Fprintf(os.Stdout, "ref: %s\n", strings.TrimSpace(*ref))
	fmt.Fprintf(os.Stdout, "reason: %s\n", record.Reason)
	fmt.Fprintf(os.Stdout, "created_at: %s\n", record.CreatedAt.UTC().Format(time.RFC3339Nano))
	if len(record.RedactedFields) > 0 {
		fmt.Fprintf(os.Stdout, "redacted_fields: %s\n", strings.Join(record.RedactedFields, ","))
	}
	for _, key := range sortedMapKeys(record.Payload) {
		fmt.Fprintf(os.Stdout, "payload.%s: %s\n", key, record.Payload[key])
	}
	return nil
}

func printDurableAgentPolicy(w *os.File, agent core.DurableAgent, updates []session.DurableAgentPolicyUpdate) {
	fmt.Fprintf(w, "action: durable-agent policy show\n")
	fmt.Fprintf(w, "agent_id: %s\n", agent.AgentID)
	fmt.Fprintf(w, "channel_kind: %s\n", agent.ChannelKind)
	fmt.Fprintf(w, "policy_version: %d\n", agent.PolicyVersion)
	fmt.Fprintf(w, "policy_hash: %s\n", agent.PolicyHash)
	if !agent.PolicyIssuedAt.IsZero() {
		fmt.Fprintf(w, "policy_issued_at: %s\n", agent.PolicyIssuedAt.UTC().Format(time.RFC3339Nano))
	}
	fmt.Fprintf(w, "charter: %s\n", agent.LivePolicy.Charter)
	fmt.Fprintf(w, "capabilities: %s\n", strings.Join(agent.LivePolicy.CapabilityEnvelope, ","))
	fmt.Fprintf(w, "outbound_mode: %s\n", agent.LivePolicy.OutboundMode)
	fmt.Fprintf(w, "drift_policy: %s\n", agent.LivePolicy.DriftPolicy)
	fmt.Fprintf(w, "public_surface_mode: %s\n", agent.LivePolicy.PublicSurfaceMode)
	fmt.Fprintf(w, "shared_inference_reuse: %s\n", agent.LivePolicy.SharedInferenceReuse)
	fmt.Fprintf(w, "shared_inference_reuse_scope: %s\n", agent.LivePolicy.SharedInferenceReuseScope)
	fmt.Fprintf(w, "bootstrap_capabilities: %s\n", strings.Join(agent.BootstrapCeiling.CapabilityEnvelope, ","))
	fmt.Fprintf(w, "bootstrap_allowed_outbound_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedOutboundModes, ","))
	fmt.Fprintf(w, "bootstrap_allowed_public_surface_modes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedPublicSurfaceModes, ","))
	fmt.Fprintf(w, "bootstrap_allowed_shared_inference_reuse: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceReuse, ","))
	fmt.Fprintf(w, "bootstrap_allowed_shared_inference_scopes: %s\n", strings.Join(agent.BootstrapCeiling.AllowedSharedInferenceScopes, ","))
	fmt.Fprintf(w, "bootstrap_llm_backend: %s\n", agent.BootstrapLLM.Backend)
	fmt.Fprintf(w, "bootstrap_native_provider: %s\n", agent.BootstrapLLM.NativeProvider)
	fmt.Fprintf(w, "bootstrap_model: %s\n", agent.BootstrapLLM.Model)
	if strings.TrimSpace(agent.BootstrapLLM.CodexHome) != "" {
		fmt.Fprintf(w, "bootstrap_codex_home: %s\n", agent.BootstrapLLM.CodexHome)
	}
	fmt.Fprintf(w, "policy_updates: %d\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(w, "- id=%d previous=%d new=%d", update.ID, update.PreviousVersion, update.NewVersion)
		if update.SourceReviewEventID > 0 {
			fmt.Fprintf(w, " review_event=%d", update.SourceReviewEventID)
		}
		if strings.TrimSpace(update.Reason) != "" {
			fmt.Fprintf(w, " reason=%s", update.Reason)
		}
		fmt.Fprintf(w, " applied_at=%s\n", update.AppliedAt.UTC().Format(time.RFC3339Nano))
	}
}

func durableAgentReviewTargetsAgent(agentID string, scope session.ScopeRef) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(scope.DurableAgentID) == agentID || strings.TrimSpace(scope.ID) == agentID
}

func parseCSVValues(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func newSemanticEngineForConfig(cfg *config.Config, force bool) (*memstore.SemanticEngine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	opts := memstore.SemanticOptions{
		Enabled:             cfg.Memory.Semantic.Enabled || force,
		DBPath:              memstore.DefaultSemanticDBPath(cfg.Sessions.DBPath),
		Sources:             cfg.Memory.Semantic.Sources,
		IncludeDailyNotes:   cfg.Memory.Semantic.IncludeDailyNotes,
		IncludeQuestions:    cfg.Memory.Semantic.IncludeQuestions,
		IncludeRhizome:      cfg.Memory.Semantic.IncludeRhizome,
		InteractiveTopK:     cfg.Memory.Semantic.InteractiveTopK,
		HeartbeatTopK:       cfg.Memory.Semantic.HeartbeatTopK,
		InteractiveMaxChars: cfg.Memory.Semantic.InteractiveMaxChars,
		HeartbeatMaxChars:   cfg.Memory.Semantic.HeartbeatMaxChars,
		DailyNotesDir:       cfg.Agent.DailyNotesDir,
	}
	return memstore.NewSemanticEngine(opts), nil
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
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
