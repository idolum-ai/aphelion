//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableEmailChildExecutor interface {
	Supports(scope sandbox.Scope, agent core.DurableAgent) bool
	Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, now time.Time) error
}

type sandboxDurableEmailChildExecutor struct {
	cfg        *config.Config
	binaryPath string
	runner     *sandbox.Runner
	supported  bool
}

func newSandboxDurableEmailChildExecutor(cfg *config.Config) durableEmailChildExecutor {
	if cfg == nil {
		return nil
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return nil
	}
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		return nil
	}
	return &sandboxDurableEmailChildExecutor{
		cfg:        cfg,
		binaryPath: binaryPath,
		runner:     sandbox.NewRunner(),
		supported:  true,
	}
}

func (e *sandboxDurableEmailChildExecutor) Supports(scope sandbox.Scope, agent core.DurableAgent) bool {
	if e == nil || !e.supported || e.runner == nil {
		return false
	}
	if !core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
		return false
	}
	return e.runner.Supports(scope)
}

func (e *sandboxDurableEmailChildExecutor) Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, now time.Time) error {
	if !e.Supports(scope, agent) {
		return fmt.Errorf("durable email child executor is unavailable for scope %q", scope.Principal.Role)
	}
	payloadRoot := filepath.Join(scope.SharedMemoryRoot, ".aphelion", "child-email-run")
	if err := os.MkdirAll(payloadRoot, 0o700); err != nil {
		return fmt.Errorf("create durable email child payload root: %w", err)
	}

	bootstrapPath, err := writeJSONTemp(payloadRoot, "bootstrap-*.json", DurableAgentChildBootstrap{
		Config: *durableAgentChildConfig(e.cfg, agent, scope),
	})
	if err != nil {
		return err
	}
	defer os.Remove(bootstrapPath)

	stateRoot := filepath.Dir(strings.TrimSpace(e.cfg.Sessions.DBPath))
	extraReadonly := []string{e.binaryPath}
	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if bootstrap.Backend == "codex" && strings.TrimSpace(bootstrap.CodexHome) != "" {
		extraReadonly = append(extraReadonly, strings.TrimSpace(bootstrap.CodexHome))
	}

	command := durableAgentEmailChildCommand(e.binaryPath, bootstrapPath, agent.AgentID, now)
	res, err := e.runner.Run(ctx, sandbox.ExecRequest{
		Scope:              scope,
		Command:            command,
		Workdir:            scope.WorkingRoot,
		ExtraReadonlyPaths: extraReadonly,
		ExtraWritablePaths: []string{stateRoot},
	})
	if err != nil {
		if strings.TrimSpace(res.Stderr) != "" {
			return fmt.Errorf("durable email child runner failed: %w: %s", err, strings.TrimSpace(res.Stderr))
		}
		return fmt.Errorf("durable email child runner failed: %w", err)
	}
	return nil
}

func durableAgentEmailChildCommand(binaryPath string, bootstrapPath string, agentID string, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return strings.Join([]string{
		shellQuote(binaryPath),
		"durable-agent",
		"child-run",
		"--bootstrap",
		shellQuote(bootstrapPath),
		"--agent",
		shellQuote(strings.TrimSpace(agentID)),
		"--now",
		shellQuote(now.UTC().Format(time.RFC3339Nano)),
	}, " ")
}
