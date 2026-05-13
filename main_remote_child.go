//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
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
	fs := flag.NewFlagSet("durable-agent remote", flag.ContinueOnError)
	bootstrapPath := fs.String("bootstrap", "", "path to remote child bootstrap json")
	dbPath := fs.String("db", "", "path to child state sqlite db")
	messagePath := fs.String("message", "", "path to inbound message json for run-once")
	inboxDir := fs.String("inbox-dir", "", "path to inbound message queue dir for loop")
	pollInterval := fs.String("poll-interval", "", "remote child loop poll interval")
	iterations := fs.Int("iterations", 0, "maximum loop iterations before exit; 0 runs until canceled")
	if err := fs.Parse(args); err != nil {
		return err
	}

	action := "sync"
	if fs.NArg() > 0 {
		action = strings.ToLower(strings.TrimSpace(fs.Arg(0)))
	}
	if strings.TrimSpace(*bootstrapPath) == "" {
		return fmt.Errorf("durable-agent remote requires --bootstrap")
	}
	if strings.TrimSpace(*dbPath) == "" {
		return fmt.Errorf("durable-agent remote requires --db")
	}

	store, err := session.NewSQLiteStore(strings.TrimSpace(*dbPath))
	if err != nil {
		return err
	}
	defer store.Close()

	remote := durableagent.NewRemoteRuntime(store, durableAgentRemoteClientFactory)
	switch action {
	case "", "sync":
		result, err := remote.Sync(context.Background(), *bootstrapPath)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote sync\n")
		fmt.Fprintf(os.Stdout, "enrolled: %t\n", result.Enrolled)
		fmt.Fprintf(os.Stdout, "policy_changed: %t\n", result.PolicyChanged)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.PolicyVersion)
		return nil
	case "run-once":
		if strings.TrimSpace(*messagePath) == "" {
			return fmt.Errorf("durable-agent remote run-once requires --message")
		}
		var msg core.InboundMessage
		if err := decodeJSONFile(*messagePath, &msg); err != nil {
			return fmt.Errorf("load remote child message: %w", err)
		}
		runner := durableagent.NewRemoteChildRunner(store, remote, durableAgentRemoteExecutorFactory(store, strings.TrimSpace(*dbPath)))
		result, err := runner.RunOnce(context.Background(), *bootstrapPath, msg)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote run-once\n")
		fmt.Fprintf(os.Stdout, "enrolled: %t\n", result.Sync.Enrolled)
		fmt.Fprintf(os.Stdout, "policy_changed: %t\n", result.Sync.PolicyChanged)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.Sync.PolicyVersion)
		fmt.Fprintf(os.Stdout, "uploaded_review_artifacts: %d\n", result.UploadedReviewArtifacts)
		fmt.Fprintf(os.Stdout, "acknowledged_parent_conversation: %t\n", result.AcknowledgedParent)
		return nil
	case "loop":
		if strings.TrimSpace(*inboxDir) == "" {
			return fmt.Errorf("durable-agent remote loop requires --inbox-dir")
		}
		interval, err := parseRemotePollInterval(*pollInterval)
		if err != nil {
			return err
		}
		loop := durableagent.NewRemoteChildLoopRunner(durableagent.NewRemoteChildRunner(store, remote, durableAgentRemoteExecutorFactory(store, strings.TrimSpace(*dbPath))))
		result, err := loop.Run(context.Background(), *bootstrapPath, *inboxDir, interval, *iterations)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "action: durable-agent remote loop\n")
		fmt.Fprintf(os.Stdout, "syncs: %d\n", result.Syncs)
		fmt.Fprintf(os.Stdout, "messages_processed: %d\n", result.MessagesProcessed)
		fmt.Fprintf(os.Stdout, "uploaded_review_artifacts: %d\n", result.UploadedReviewArtifacts)
		fmt.Fprintf(os.Stdout, "policy_version: %d\n", result.LastPolicyVersion)
		return nil
	default:
		return fmt.Errorf("durable-agent remote action must be one of sync|run-once|loop")
	}
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse durable-agent remote poll interval: %w", err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("durable-agent remote poll interval must be > 0")
	}
	return value, nil
}
