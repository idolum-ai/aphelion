//go:build linux

package main

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/childcli"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type telegramChildBotRoute = childcli.TelegramChildBotRoute
type telegramChildBotDeps = childcli.TelegramChildBotDeps
type telegramChildBotHealthStatus = childcli.TelegramChildBotHealthStatus

func defaultTelegramChildBotDeps() telegramChildBotDeps {
	return telegramChildBotDeps{
		Stat:        os.Stat,
		ReadFile:    os.ReadFile,
		RunPoller:   runTelegramChildBotPoller,
		RunDryStart: runTelegramChildBotDryStart,
	}
}

func runTelegramChildBotCommand(args []string) error {
	return runTelegramChildBotCommandWithDeps(args, defaultTelegramChildBotDeps())
}

func runTelegramChildBotCommandWithDeps(args []string, deps telegramChildBotDeps) error {
	if deps.Stat == nil {
		deps.Stat = os.Stat
	}
	if deps.ReadFile == nil {
		deps.ReadFile = os.ReadFile
	}
	if deps.RunPoller == nil {
		deps.RunPoller = runTelegramChildBotPoller
	}
	if deps.RunDryStart == nil {
		deps.RunDryStart = runTelegramChildBotDryStart
	}
	return childcli.RunTelegramChildBotCommandWithDeps(args, deps)
}

func selectTelegramChildBotRoute(cfg *config.Config, agentID string, tokenFile string, chatID int64, respondOn string, reviewTarget int64) (telegramChildBotRoute, error) {
	return childcli.SelectTelegramChildBotRoute(cfg, agentID, tokenFile, chatID, respondOn, reviewTarget)
}

func normalizeChildBotRespondOn(raw string) string {
	return childcli.NormalizeChildBotRespondOn(raw)
}

func validateTelegramChildBotTokenMetadata(path string, stat func(string) (os.FileInfo, error)) error {
	return childcli.ValidateTelegramChildBotTokenMetadata(path, stat)
}

func readTelegramChildBotToken(path string, readFile func(string) ([]byte, error)) (string, error) {
	return childcli.ReadTelegramChildBotToken(path, readFile)
}

func loadTelegramChildBotAgent(store *session.SQLiteStore, agentID string) (core.DurableAgent, error) {
	return childcli.LoadTelegramChildBotAgent(store, agentID)
}

func validateTelegramChildBotRouteAgainstAgent(route telegramChildBotRoute, agentRow core.DurableAgent) error {
	return childcli.ValidateTelegramChildBotRouteAgainstAgent(route, agentRow)
}

func buildTelegramChildBotHealthStatus(action string, configPath string, route telegramChildBotRoute, agentRow core.DurableAgent) telegramChildBotHealthStatus {
	return childcli.BuildTelegramChildBotHealthStatus(action, configPath, route, agentRow)
}

func printTelegramChildBotHealthStatus(w io.Writer, action string, configPath string, route telegramChildBotRoute, agentRow core.DurableAgent) {
	childcli.PrintTelegramChildBotHealthStatus(w, action, configPath, route, agentRow)
}

func runTelegramChildBotGetMeSmoke(ctx context.Context, client *telegram.Client, route telegramChildBotRoute, configPath string) error {
	return childcli.RunTelegramChildBotGetMeSmoke(ctx, client, route, configPath)
}

func runTelegramChildBotPoller(ctx context.Context, client *telegram.Client, agentRow core.DurableAgent, route telegramChildBotRoute, cfg *config.Config, store *session.SQLiteStore) error {
	if client == nil {
		return fmt.Errorf("telegram client is unavailable")
	}
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if store == nil {
		return fmt.Errorf("session store is nil")
	}

	var botUser *telegram.User
	if route.RespondOn != "all" {
		getMeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var err error
		botUser, err = client.GetMe(getMeCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("telegram child bot getMe failed: %w", err)
		}
	}

	rt, err := newTelegramChildBotRuntime(cfg, store, client, route.NoSend)
	if err != nil {
		return err
	}
	routeConfig := []config.TelegramDurableGroupConfig{{
		ChatID:    route.ChatID,
		AgentID:   route.AgentID,
		RespondOn: route.RespondOn,
	}}
	poller := telegram.NewPoller(client, func(parent context.Context, msg core.InboundMessage) error {
		if strings.TrimSpace(msg.DurableAgentID) == "" {
			return nil
		}
		_, err := rt.HandleInbound(parent, msg)
		return err
	},
		telegram.WithPollerTimeout(cfg.Telegram.PollTimeout),
		telegram.WithMediaConfig(cfg.Telegram.Media),
		telegram.WithDurableGroups(routeConfig),
		telegram.WithBotIdentity(botUser),
	)

	fmt.Fprintf(os.Stdout, "action: telegram-child-bot run\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", route.AgentID)
	fmt.Fprintf(os.Stdout, "chat_id: %d\n", route.ChatID)
	fmt.Fprintf(os.Stdout, "respond_on: %s\n", route.RespondOn)
	fmt.Fprintf(os.Stdout, "no_send: %t\n", route.NoSend)
	fmt.Fprintf(os.Stdout, "status: running\n")
	return poller.Run(ctx)
}

func runTelegramChildBotDryStart(ctx context.Context, _ *telegram.Client, agentRow core.DurableAgent, route telegramChildBotRoute, cfg *config.Config, store *session.SQLiteStore) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if store == nil {
		return fmt.Errorf("session store is nil")
	}
	route.NoSend = true
	if _, err := newTelegramChildBotRuntime(cfg, store, nil, true); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "action: telegram-child-bot dry-start\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", route.AgentID)
	fmt.Fprintf(os.Stdout, "chat_id: %d\n", route.ChatID)
	fmt.Fprintf(os.Stdout, "respond_on: %s\n", route.RespondOn)
	fmt.Fprintf(os.Stdout, "durable_agent_status: %s\n", strings.TrimSpace(agentRow.Status))
	fmt.Fprintf(os.Stdout, "no_send: true\n")
	fmt.Fprintf(os.Stdout, "polling: not_started\n")
	fmt.Fprintf(os.Stdout, "telegram_api: not_called\n")
	fmt.Fprintf(os.Stdout, "status: ready\n")
	return nil
}

type telegramChildBotNoSendOutbound struct{}

func (telegramChildBotNoSendOutbound) SendMessage(context.Context, core.OutboundMessage) (int64, error) {
	return 0, nil
}

func newTelegramChildBotRuntime(cfg *config.Config, store *session.SQLiteStore, client *telegram.Client, noSend bool) (*aphruntime.Runtime, error) {
	if err := prepareFilesystem(cfg); err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		return nil, err
	}
	sandboxRoots := sandbox.Roots{
		GlobalRoot:        cfg.Agent.PromptRoot,
		AdminExecRoot:     cfg.Agent.ExecRoot,
		SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
		UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
		UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
	}
	sandboxProfiles, err := aphruntime.SandboxProfilesFromConfig(cfg.Sandbox)
	if err != nil {
		return nil, err
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandboxProfiles)
	if err != nil {
		return nil, err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithUserAgent(config.EffectiveUserAgent(cfg, tool.DefaultNativeFetchUserAgent)).
		WithSessionStore(store).
		WithRemoteHostSSH(cfg.Tailscale.SSHPath, remoteHostSSHTimeoutFromConfig(cfg)).
		WithDurableAgentPrincipalFallback().
		WithWebSearchOptions(tool.WebSearchOptionsFromConfig(cfg.Tools.WebSearch)).
		WithConfiguredCapabilityVisibility(configuredCapabilityVisibilityFromConfig(cfg)).
		WithDurableAgentBootstrapLLM(defaultDurableAgentBootstrapFromConfig(cfg))
	if manifestDir := strings.TrimSpace(cfg.Tools.ExternalManifestDir); manifestDir != "" {
		if _, err := tools.WithExternalToolManifestDir(manifestDir); err != nil {
			return nil, fmt.Errorf("load external tool manifests: %w", err)
		}
	}
	tools.WithSemanticEngine(memstore.NewSemanticEngine(memstore.SemanticOptions{
		Enabled:             cfg.Memory.Semantic.Enabled,
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
	}))
	fileStore, retrievalStore, err := buildOpenAIPlatformServices(cfg, httpClient)
	if err != nil {
		return nil, err
	}
	if fileStore != nil {
		tools.WithFileStore(fileStore, cfg.OpenAI.Files.Purpose)
	}
	if retrievalStore != nil {
		tools.WithRetrievalStore(retrievalStore, cfg.OpenAI.VectorStores.DefaultStore)
	}
	var outbound aphruntime.OutboundSender = telegramruntime.NewUIClient(client)
	if noSend {
		outbound = telegramChildBotNoSendOutbound{}
	}
	rt, err := aphruntime.New(cfg, store, llm, agent.ToolRegistry(tools), outbound)
	if err != nil {
		return nil, err
	}
	tools.WithCapabilityGrantObserver(rt.HandleCapabilityGrantActivated)
	return rt, nil
}
