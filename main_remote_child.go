//go:build linux

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/internal/childcli"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

var durableAgentRemoteClientFactory durableagent.RemoteClientFactory

var durableAgentRemoteExecutorFactory = func(store *session.SQLiteStore, dbPath string) durableagent.RemoteChildExecutor {
	return durableagent.RemoteChildExecutorFunc(func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
		if strings.TrimSpace(msg.ChatType) == "durable_parent_conversation" {
			return runRemoteDurableAgentChildWake(ctx, dbPath, bootstrap, agent, time.Now().UTC())
		}
		if strings.TrimSpace(agent.ChannelKind) != "telegram_group" {
			return fmt.Errorf("durable-agent remote run-once supports telegram_group children")
		}
		_, err := runRemoteDurableTelegramGroupChild(ctx, store, dbPath, bootstrap, agent, msg)
		return err
	})
}

func runDurableAgentRemoteCommand(args []string) error {
	return childcli.RunDurableAgentRemoteCommand(args, childcli.DurableAgentRemoteDeps{
		ClientFactory:   durableAgentRemoteClientFactory,
		ExecutorFactory: durableAgentRemoteExecutorFactory,
	})
}

func runRemoteDurableTelegramGroupChild(ctx context.Context, store *session.SQLiteStore, dbPath string, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) (*runtimepkg.DurableGroupChildResult, error) {
	cfg := remoteDurableAgentChildConfig(strings.TrimSpace(dbPath), bootstrap, agent)
	return runDurableTelegramGroupChildBootstrap(ctx, runtimepkg.DurableAgentChildBootstrap{Config: *cfg}, msg)
}

func runRemoteDurableAgentChildWake(ctx context.Context, dbPath string, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, now time.Time) error {
	cfg := remoteDurableAgentChildConfig(strings.TrimSpace(dbPath), bootstrap, agent)
	return runDurableAgentChildWakeBootstrap(ctx, runtimepkg.DurableAgentChildBootstrap{Config: *cfg}, agent.AgentID, now)
}

func remoteDurableAgentChildConfig(dbPath string, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent) *config.Config {
	cfg := config.Default()
	workspaceRoot, memoryRoot := durableagent.LocalRoots(agent.AgentID, bootstrap.LocalStorageRoots)
	if strings.TrimSpace(workspaceRoot) == "" || strings.TrimSpace(memoryRoot) == "" {
		workspaceRoot, memoryRoot = durableagent.DefaultLocalRoots(dbPath, agent.AgentID)
	}
	promptRoot := filepath.Join(memoryRoot, "agent")
	cfg.Telegram = config.TelegramConfig{}
	cfg.Principals = config.PrincipalsConfig{}
	cfg.Providers = config.ProvidersConfig{}
	cfg.OpenAI = config.OpenAIConfig{}
	cfg.Heartbeat = config.HeartbeatConfig{}
	cfg.Cron = config.CronConfig{}
	cfg.Voice = config.VoiceConfig{Mode: "off"}
	cfg.Face = config.FaceConfig{Backend: remoteFaceBackend(bootstrap.BootstrapLLM)}
	cfg.Sessions.DBPath = strings.TrimSpace(dbPath)
	cfg.Agent.PromptRoot = promptRoot
	cfg.Agent.ExecRoot = workspaceRoot
	cfg.Agent.SharedMemoryRoot = promptRoot
	cfg.Agent.UserWorkspaceRoot = ""
	cfg.Agent.UserMemoryRoot = ""
	cfg.Agent.DailyNotesDir = "memory/daily"
	applyRemoteNodeLLMBootstrap(&cfg, bootstrap.BootstrapLLM)
	return &cfg
}

func remoteFaceBackend(bootstrap core.NodeLLMBootstrap) string {
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	switch bootstrap.Backend {
	case "native":
		return config.NormalizeFaceBackendValue("provider")
	default:
		return config.NormalizeFaceBackendValue("floor_fallback")
	}
}

func applyRemoteNodeLLMBootstrap(cfg *config.Config, bootstrap core.NodeLLMBootstrap) {
	if cfg == nil {
		return
	}
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	cfg.Governor = config.GovernorConfig{}
	switch bootstrap.Backend {
	case "codex":
		cfg.Governor.Backend = "codex"
		cfg.Governor.Codex = config.GovernorCodexConfig{
			AuthSource: bootstrap.CodexAuthSource,
			CodexHome:  bootstrap.CodexHome,
			BaseURL:    bootstrap.CodexBaseURL,
		}
	case "native":
		cfg.Governor.Backend = "native"
		cfg.Governor.NativeProvider = bootstrap.NativeProvider
		cfg.Providers.Default = bootstrap.NativeProvider
		switch bootstrap.NativeProvider {
		case "anthropic":
			cfg.Providers.Anthropic = config.AnthropicConfig{
				APIKey:        bootstrap.APIKey,
				Model:         remoteFirstNonEmpty(bootstrap.Model, config.Default().Providers.Anthropic.Model),
				MaxTokens:     remoteFirstPositive(bootstrap.MaxTokens, config.Default().Providers.Anthropic.MaxTokens),
				ContextWindow: config.Default().Providers.Anthropic.ContextWindow,
			}
		case "openrouter":
			cfg.Providers.OpenRouter = config.OpenRouterConfig{
				APIKey:        bootstrap.APIKey,
				BaseURL:       remoteFirstNonEmpty(bootstrap.BaseURL, config.Default().Providers.OpenRouter.BaseURL),
				Model:         remoteFirstNonEmpty(bootstrap.Model, config.Default().Providers.OpenRouter.Model),
				MaxTokens:     remoteFirstPositive(bootstrap.MaxTokens, config.Default().Providers.OpenRouter.MaxTokens),
				ContextWindow: config.Default().Providers.OpenRouter.ContextWindow,
			}
		}
	}
}

func remoteFirstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func remoteFirstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func parseRemotePollInterval(raw string) (time.Duration, error) {
	return childcli.ParseRemotePollInterval(raw)
}
