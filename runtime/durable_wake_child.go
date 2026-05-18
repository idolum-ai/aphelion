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
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableWakeChildExecutor interface {
	Supports(scope sandbox.Scope, agent core.DurableAgent) bool
	Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, now time.Time) error
}

type sandboxDurableWakeChildExecutor struct {
	cfg        *config.Config
	binaryPath string
	runner     *sandbox.Runner
	store      *session.SQLiteStore
	supported  bool
}

func newSandboxDurableWakeChildExecutor(cfg *config.Config, store *session.SQLiteStore) durableWakeChildExecutor {
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
	return &sandboxDurableWakeChildExecutor{
		cfg:        cfg,
		binaryPath: binaryPath,
		runner:     sandbox.NewRunner(),
		store:      store,
		supported:  true,
	}
}

func (r *Runtime) shouldRunDurableWakeInChild(agent core.DurableAgent) bool {
	if r == nil || r.durableWakeChild == nil {
		return false
	}
	return core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured()
}

func (r *Runtime) pollDurableAgentWakeViaChild(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	if r == nil || r.durableWakeChild == nil {
		return r.runDurableAgentChildWakeLoaded(ctx, agent, now)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if suppressed, err := r.shouldSuppressDurableWakeChildPoll(agent, now); err != nil {
		return err
	} else if suppressed {
		return nil
	}
	if err := r.preflightDurableWakeAgent(agent, now); err != nil {
		if handled, handleErr := r.recordDurableWakeChildRuntimeBlock(agent, err, now); handled {
			return handleErr
		}
		return err
	}
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		return err
	}
	if !r.durableWakeChild.Supports(scope, agent) {
		return r.runDurableAgentChildWakeLoaded(ctx, agent, now)
	}
	if err := r.durableWakeChild.Run(ctx, scope, agent, now); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if handled, handleErr := r.recordDurableWakeChildRuntimeBlock(agent, err, now); handled {
			return handleErr
		}
		if handled, handleErr := r.recordOrSuppressScheduledReviewWakeFailure(agent, err, now); handled {
			return handleErr
		}
		if handled, handleErr := r.recordDurableWakeExternalFailure(agent, err, now); handled {
			return handleErr
		}
		return err
	}
	return nil
}

func (e *sandboxDurableWakeChildExecutor) Supports(scope sandbox.Scope, agent core.DurableAgent) bool {
	if e == nil || !e.supported || e.runner == nil {
		return false
	}
	if !core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
		return false
	}
	return e.runner.Supports(scope)
}

func (e *sandboxDurableWakeChildExecutor) Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, now time.Time) error {
	if !e.Supports(scope, agent) {
		return fmt.Errorf("durable child wake executor is unavailable for scope %q", scope.Principal.Role)
	}
	payloadRoot := filepath.Join(scope.SharedMemoryRoot, ".aphelion", "child-wake-run")
	if err := os.MkdirAll(payloadRoot, 0o700); err != nil {
		return fmt.Errorf("create durable child wake payload root: %w", err)
	}

	bootstrapPath, err := writeJSONTemp(payloadRoot, "bootstrap-*.json", DurableAgentChildBootstrap{
		Config: *durableAgentChildConfig(e.cfg, agent, scope),
	})
	if err != nil {
		return err
	}
	defer os.Remove(bootstrapPath)

	stateRoot := filepath.Dir(strings.TrimSpace(e.cfg.Sessions.DBPath))
	childAccess, err := durableChildSandboxAccessFor(e.binaryPath, agent, e.store)
	if err != nil {
		return err
	}

	command := durableAgentWakeChildCommand(e.binaryPath, bootstrapPath, agent.AgentID, now)
	res, err := e.runner.Run(ctx, sandbox.ExecRequest{
		Scope:              scope,
		Command:            command,
		Workdir:            scope.WorkingRoot,
		ExtraReadonlyPaths: childAccess.readonlyPaths,
		ExtraReadonlyBinds: childAccess.readonlyBinds,
		ExtraWritablePaths: []string{stateRoot},
		ExtraEnv:           childAccess.env,
	})
	if err != nil {
		if strings.TrimSpace(res.Stderr) != "" {
			return fmt.Errorf("durable child wake runner failed: %w: %s", err, strings.TrimSpace(res.Stderr))
		}
		return fmt.Errorf("durable child wake runner failed: %w", err)
	}
	return nil
}

func durableAgentWakeChildCommand(binaryPath string, bootstrapPath string, agentID string, now time.Time) string {
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
