//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
)

func maintenanceSetupDeps() maintenancecli.SetupDeps {
	return maintenancecli.SetupDeps{
		SeedAgentPromptFiles:      seedAgentPromptFiles,
		LoadConfiguredPromptFiles: loadConfiguredPromptFiles,
		InstallDailyReviewRecipe: func(cfg *config.Config, w io.Writer) error {
			result, err := installDailyReviewRecipeForConfig(cfg, installDailyReviewRecipeOptions{})
			if err != nil {
				return err
			}
			printDailyReviewRecipeInstallResult(w, result)
			return nil
		},
		ReconcileDurableAgentsForConfig:  reconcileDurableAgentsForConfig,
		PrintDurableAgentReconcileResult: printDurableAgentReconcileResult,
		ParkActiveWorkForRestart:         maintenanceLiveStateRepairDeps().ParkActiveWorkForRestart,
	}
}

func runInitCommand(args []string) error {
	return maintenancecli.RunInitCommand(args, maintenanceSetupDeps())
}

func runPathsCommand(args []string) error {
	return maintenancecli.RunPathsCommand(args, maintenanceSetupDeps())
}

func runParkRestartCommand(args []string) error {
	return maintenancecli.RunParkRestartCommand(args, maintenanceSetupDeps())
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
