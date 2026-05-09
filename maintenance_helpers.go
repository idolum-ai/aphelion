//go:build linux

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/face"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

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
