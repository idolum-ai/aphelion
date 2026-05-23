//go:build linux

package standalonecli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/idolum-ai/aphelion/config"
)

var captureStdoutMu sync.Mutex

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

func (e *ConfigStartupError) Unwrap() error         { return e.Err }
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

func captureStdoutForTest(fn func() error) (string, error) {
	captureStdoutMu.Lock()
	defer captureStdoutMu.Unlock()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	err = fn()
	_ = w.Close()
	os.Stdout = old
	raw, readErr := io.ReadAll(r)
	_ = r.Close()
	if readErr != nil {
		return "", readErr
	}
	return string(raw), err
}
