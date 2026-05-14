//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

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

	fmt.Fprintf(os.Stdout, "action: init\n")
	fmt.Fprintf(os.Stdout, "status: in_progress\n")
	fmt.Fprintf(os.Stdout, "changed: created_files=%d\n", len(created))
	fmt.Fprintf(os.Stdout, "evidence:\n")
	fmt.Fprintf(os.Stdout, "prompt_root: %s\n", cfg.Agent.PromptRoot)
	fmt.Fprintf(os.Stdout, "created_files: %d\n", len(created))
	for _, path := range created {
		fmt.Fprintf(os.Stdout, "  - %s\n", path)
	}

	importResult, err := importCodexSessionsForConfig(context.Background(), cfg, defaultCodexSessionImportCommandOptions(cfg))
	if err != nil {
		return err
	}
	printCodexSessionImportResult(os.Stdout, importResult, memstore.SemanticImportStateQuarantine)

	dailyReviewResult, err := installDailyReviewRecipeForConfig(cfg, installDailyReviewRecipeOptions{})
	if err != nil {
		return err
	}
	printDailyReviewRecipeInstallResult(os.Stdout, dailyReviewResult)

	reconcileResult, err := reconcileDurableAgentsForConfig(cfg, durableAgentReconcileOptions{
		QueueGrowthPrompt: true,
		Now:               time.Now().UTC(),
	})
	if err != nil {
		printDurableAgentReconcileResult(os.Stdout, reconcileResult)
		return err
	}
	printDurableAgentReconcileResult(os.Stdout, reconcileResult)
	fmt.Fprintf(os.Stdout, "status: ready\n")
	fmt.Fprintf(os.Stdout, "next: run --check-config or start the service\n")
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

	fmt.Fprintf(os.Stdout, "action: paths\n")
	fmt.Fprintf(os.Stdout, "status: ready\n")
	fmt.Fprintf(os.Stdout, "evidence:\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "prompt_root: %s\n", cfg.Agent.PromptRoot)
	fmt.Fprintf(os.Stdout, "exec_root: %s\n", cfg.Agent.ExecRoot)
	fmt.Fprintf(os.Stdout, "shared_memory_root: %s\n", cfg.Agent.SharedMemoryRoot)
	fmt.Fprintf(os.Stdout, "user_workspace_root: %s\n", cfg.Agent.UserWorkspaceRoot)
	fmt.Fprintf(os.Stdout, "user_memory_root: %s\n", cfg.Agent.UserMemoryRoot)
	fmt.Fprintf(os.Stdout, "sessions_db: %s\n", cfg.Sessions.DBPath)
	policy := config.EffectiveAutonomyPolicy(cfg)
	fmt.Fprintf(os.Stdout, "autonomy_default_mode: %s\n", policy.DefaultMode)
	fmt.Fprintf(os.Stdout, "autonomy_ceiling: %s\n", policy.Ceiling)
	fmt.Fprintf(os.Stdout, "autonomy_live_overrides: %t\n", policy.AllowLiveOverrides)
	fmt.Fprintf(os.Stdout, "autonomy_max_override_duration: %s\n", policy.MaxOverrideDuration.Truncate(time.Second).String())
	printPathGroup("loaded_bootstrap_files", stable)
	printPathGroup("loaded_dynamic_files", dynamic)
	printPathGroup("loaded_idolum_stable_files", idolumStable)
	printPathGroup("loaded_idolum_dynamic_files", idolumDynamic)
	fmt.Fprintf(os.Stdout, "next: verify these roots before broadening exec or memory scope\n")
	return nil
}

func runParkRestartCommand(args []string) error {
	fs := flag.NewFlagSet("park-restart", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	sourceFlag := fs.String("source", "deploy_restart", "restart source label recorded in cleanup events")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	result, err := aphruntime.ParkStoreActiveWorkForRestart(context.Background(), store, *sourceFlag, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: park-restart\n")
	fmt.Fprintf(os.Stdout, "status: parked\n")
	fmt.Fprintf(os.Stdout, "config_path: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "source: %s\n", strings.TrimSpace(*sourceFlag))
	fmt.Fprintf(os.Stdout, "turn_runs_interrupted: %d\n", result.TurnRunsInterrupted)
	fmt.Fprintf(os.Stdout, "continuations_parked: %d\n", result.ContinuationsParked)
	fmt.Fprintf(os.Stdout, "pending_continuations_parked: %d\n", result.PendingContinuationsParked)
	fmt.Fprintf(os.Stdout, "approved_continuations_parked: %d\n", result.ApprovedContinuationsParked)
	fmt.Fprintf(os.Stdout, "expired_approved_continuations: %d\n", result.ExpiredApprovedContinuations)
	fmt.Fprintf(os.Stdout, "already_parked_continuations: %d\n", result.AlreadyParkedContinuations)
	fmt.Fprintf(os.Stdout, "skipped_continuations: %d\n", result.SkippedContinuations)
	fmt.Fprintf(os.Stdout, "next: restart service only after build and config checks pass\n")
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
