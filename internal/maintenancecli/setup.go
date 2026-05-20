//go:build linux

package maintenancecli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

type SetupDeps struct {
	SeedAgentPromptFiles             func(*config.Config) ([]string, error)
	LoadConfiguredPromptFiles        func(*config.Config) ([]string, []string, []string, []string, error)
	InstallDailyReviewRecipe         func(*config.Config, io.Writer) error
	ReconcileDurableAgentsForConfig  func(*config.Config, DurableAgentReconcileOptions) (*DurableAgentReconcileResult, error)
	PrintDurableAgentReconcileResult func(io.Writer, *DurableAgentReconcileResult)
	ParkActiveWorkForRestart         func(context.Context, *session.SQLiteStore, string, time.Time) (RestartParkResult, error)
}

func RunInitCommand(args []string, deps SetupDeps) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, _, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	if deps.SeedAgentPromptFiles == nil {
		return fmt.Errorf("init prompt seed dependency is unavailable")
	}
	created, err := deps.SeedAgentPromptFiles(cfg)
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

	if deps.InstallDailyReviewRecipe == nil {
		return fmt.Errorf("init daily review recipe dependency is unavailable")
	}
	if err := deps.InstallDailyReviewRecipe(cfg, os.Stdout); err != nil {
		return err
	}

	if deps.ReconcileDurableAgentsForConfig == nil {
		return fmt.Errorf("init durable agent reconcile dependency is unavailable")
	}
	reconcileResult, err := deps.ReconcileDurableAgentsForConfig(cfg, DurableAgentReconcileOptions{
		QueueGrowthPrompt: true,
		Now:               time.Now().UTC(),
	})
	printReconcile := deps.PrintDurableAgentReconcileResult
	if printReconcile == nil {
		printReconcile = PrintDurableAgentReconcileResult
	}
	if err != nil {
		printReconcile(os.Stdout, reconcileResult)
		return err
	}
	printReconcile(os.Stdout, reconcileResult)
	fmt.Fprintf(os.Stdout, "status: ready\n")
	fmt.Fprintf(os.Stdout, "next: run --check-config or start the service\n")
	return nil
}

func RunPathsCommand(args []string, deps SetupDeps) error {
	fs := flag.NewFlagSet("paths", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	if deps.LoadConfiguredPromptFiles == nil {
		return fmt.Errorf("paths prompt file loader dependency is unavailable")
	}
	stable, dynamic, idolumStable, idolumDynamic, err := deps.LoadConfiguredPromptFiles(cfg)
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

func RunParkRestartCommand(args []string, deps SetupDeps) error {
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
	if deps.ParkActiveWorkForRestart == nil {
		return fmt.Errorf("park-restart dependency is unavailable")
	}

	result, err := deps.ParkActiveWorkForRestart(context.Background(), store, *sourceFlag, time.Now().UTC())
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
