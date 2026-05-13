//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/telegram"
)

func newTurnContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func (e *configStartupError) Error() string {
	return fmt.Sprintf("config %s: %v (run 'aphelion --config %s --check-config' to validate)", e.Path, e.Err, e.Path)
}

func (e *configStartupError) Unwrap() error {
	return e.Err
}

func logConfigWarnings(configPath string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	for _, warning := range cfg.Warnings() {
		log.Printf("WARN config ignored_key path=%s key=%s message=%s", configPath, strings.TrimSpace(warning.Path), strings.TrimSpace(warning.Message))
	}
}

func logSandboxReadinessWarnings(configPath string, cfg *config.Config) {
	if cfg == nil {
		return
	}
	for _, issue := range runtime.SandboxReadinessSnapshot(cfg).Issues {
		log.Printf(
			"WARN sandbox readiness path=%s role=%s mode=%s network=%s code=%s severity=%s summary=%s next_repair=%s",
			configPath,
			strings.TrimSpace(issue.Role),
			strings.TrimSpace(issue.Mode),
			strings.TrimSpace(issue.Network),
			strings.TrimSpace(issue.Code),
			strings.TrimSpace(issue.Severity),
			strings.TrimSpace(issue.Summary),
			strings.TrimSpace(issue.NextRepairAction),
		)
	}
}

func shouldAllowUnresolvedPrivateDurableRelayMessage(msg *telegram.Message) bool {
	if msg == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	_, _, ok := parseDurableRelayIntent(text)
	return ok
}

func prepareFilesystem(cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.Sessions.DBPath), 0o700); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}
	for _, root := range []string{
		cfg.Agent.PromptRoot,
		cfg.Agent.ExecRoot,
		cfg.Agent.SharedMemoryRoot,
		cfg.Agent.UserWorkspaceRoot,
		cfg.Agent.UserMemoryRoot,
	} {
		if root == "" {
			continue
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("create root %s: %w", root, err)
		}
	}
	return nil
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

func exitCode(err error) int {
	var cfgErr *configStartupError
	if errors.As(err, &cfgErr) {
		return exitCodeConfig
	}
	return exitCodeFailure
}
