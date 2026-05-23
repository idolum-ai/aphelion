//go:build linux

package maintenancecli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
)

const (
	commandOutputHuman = "human"
	commandOutputKV    = "kv"
	commandOutputJSON  = "json"
)

type ConfigStartupError struct {
	Path string
	Err  error
}

func (e *ConfigStartupError) Error() string {
	return fmt.Sprintf("config %s: %v (run 'aphelion --config %s --check-config' to validate)", e.Path, e.Err, e.Path)
}

func (e *ConfigStartupError) Unwrap() error {
	return e.Err
}

func (e *ConfigStartupError) IsConfigStartupError() {}

func loadConfigForCommand(override string) (*config.Config, string, error) {
	configPath, err := config.ResolveConfigPath(override)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, "", &ConfigStartupError{Path: configPath, Err: err}
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

func normalizeCommandOutputFormat(raw string, jsonAlias bool) (string, error) {
	if jsonAlias {
		return commandOutputJSON, nil
	}
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return commandOutputHuman, nil
	}
	switch format {
	case commandOutputHuman, commandOutputKV, commandOutputJSON:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q; use human, kv, or json", raw)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositionalArg(args []string) (string, bool) {
	for _, raw := range args {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		return trimmed, true
	}
	return "", false
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
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
