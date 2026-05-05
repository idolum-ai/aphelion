//go:build linux

package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type telegramChildBotRoute struct {
	AgentID            string
	TokenFile          string
	ChatID             int64
	RespondOn          string
	ReviewTargetChatID int64
	Enabled            bool
}

type telegramChildBotDeps struct {
	Stat      func(string) (os.FileInfo, error)
	ReadFile  func(string) ([]byte, error)
	RunPoller func(context.Context, *telegram.Client, core.DurableAgent, telegramChildBotRoute, *config.Config, *session.SQLiteStore) error
}

func defaultTelegramChildBotDeps() telegramChildBotDeps {
	return telegramChildBotDeps{
		Stat:      os.Stat,
		ReadFile:  os.ReadFile,
		RunPoller: runTelegramChildBotPoller,
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

	fs := flag.NewFlagSet("telegram-child-bot", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentFlag := fs.String("agent", "", "durable telegram_group agent id")
	tokenFileFlag := fs.String("token-file", "", "path to the child bot token file")
	chatIDFlag := fs.Int64("chat-id", 0, "single admitted Telegram group/supergroup chat id")
	respondOnFlag := fs.String("respond-on", "", "group admission mode: mentions or all")
	reviewTargetFlag := fs.Int64("review-target-chat-id", 0, "parent review Telegram chat id")
	preflight := fs.Bool("preflight", false, "validate config/token metadata/durable agent and exit without reading token or calling Telegram")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	route, err := selectTelegramChildBotRoute(cfg, *agentFlag, *tokenFileFlag, *chatIDFlag, *respondOnFlag, *reviewTargetFlag)
	if err != nil {
		return err
	}
	if err := validateTelegramChildBotTokenMetadata(route.TokenFile, deps.Stat); err != nil {
		return err
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	agentRow, err := loadTelegramChildBotAgent(store, route.AgentID)
	if err != nil {
		return err
	}
	if route.ReviewTargetChatID == 0 {
		route.ReviewTargetChatID = agentRow.ReviewTargetChatID
	}
	if route.ReviewTargetChatID == 0 {
		return fmt.Errorf("telegram child bot %q requires a review target chat id", route.AgentID)
	}
	if err := validateTelegramChildBotRouteAgainstAgent(route, agentRow); err != nil {
		return err
	}

	if *preflight {
		fmt.Fprintf(os.Stdout, "action: telegram-child-bot preflight\n")
		fmt.Fprintf(os.Stdout, "config: %s\n", configPath)
		fmt.Fprintf(os.Stdout, "agent_id: %s\n", route.AgentID)
		fmt.Fprintf(os.Stdout, "chat_id: %d\n", route.ChatID)
		fmt.Fprintf(os.Stdout, "respond_on: %s\n", route.RespondOn)
		fmt.Fprintf(os.Stdout, "review_target_chat_id: %d\n", route.ReviewTargetChatID)
		fmt.Fprintf(os.Stdout, "token_file_status: metadata_ok\n")
		fmt.Fprintf(os.Stdout, "status: ok\n")
		return nil
	}

	token, err := readTelegramChildBotToken(route.TokenFile, deps.ReadFile)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	client := telegram.NewClient(token, telegram.WithHTTPClient(httpClient), telegram.WithPollTimeout(cfg.Telegram.PollTimeout))
	return deps.RunPoller(context.Background(), client, agentRow, route, cfg, store)
}

func selectTelegramChildBotRoute(cfg *config.Config, agentID string, tokenFile string, chatID int64, respondOn string, reviewTarget int64) (telegramChildBotRoute, error) {
	if cfg == nil {
		return telegramChildBotRoute{}, fmt.Errorf("config is nil")
	}
	agentID = strings.TrimSpace(agentID)
	tokenFile = strings.TrimSpace(tokenFile)
	respondOn = strings.TrimSpace(respondOn)

	var selected *config.TelegramChildBotConfig
	if agentID != "" {
		for i := range cfg.Telegram.ChildBots {
			if cfg.Telegram.ChildBots[i].AgentID == agentID {
				selected = &cfg.Telegram.ChildBots[i]
				break
			}
		}
	} else {
		for i := range cfg.Telegram.ChildBots {
			if cfg.Telegram.ChildBots[i].Enabled {
				if selected != nil {
					return telegramChildBotRoute{}, fmt.Errorf("telegram child bot --agent is required when multiple child bots are enabled")
				}
				selected = &cfg.Telegram.ChildBots[i]
			}
		}
	}

	route := telegramChildBotRoute{}
	if selected != nil {
		route = telegramChildBotRoute{
			AgentID:            selected.AgentID,
			TokenFile:          selected.TokenFile,
			ChatID:             selected.ChatID,
			RespondOn:          normalizeChildBotRespondOn(selected.RespondOn),
			ReviewTargetChatID: selected.ReviewTargetChatID,
			Enabled:            selected.Enabled,
		}
	} else if agentID != "" && tokenFile != "" && chatID != 0 {
		route = telegramChildBotRoute{AgentID: agentID, TokenFile: tokenFile, ChatID: chatID, RespondOn: "mentions", ReviewTargetChatID: reviewTarget, Enabled: true}
	} else {
		return telegramChildBotRoute{}, fmt.Errorf("telegram child bot route is required; configure [[telegram.child_bots]] or pass --agent, --token-file, and --chat-id")
	}

	if agentID != "" {
		route.AgentID = agentID
	}
	if tokenFile != "" {
		route.TokenFile = tokenFile
	}
	if chatID != 0 {
		route.ChatID = chatID
	}
	if respondOn != "" {
		route.RespondOn = normalizeChildBotRespondOn(respondOn)
	}
	if reviewTarget != 0 {
		route.ReviewTargetChatID = reviewTarget
	}
	if route.RespondOn == "" {
		route.RespondOn = "mentions"
	}
	switch route.RespondOn {
	case "all", "mentions":
	default:
		return telegramChildBotRoute{}, fmt.Errorf("telegram child bot respond_on must be one of all|mentions")
	}
	if !route.Enabled && selected != nil {
		return telegramChildBotRoute{}, fmt.Errorf("telegram child bot %q is not enabled", route.AgentID)
	}
	if strings.TrimSpace(route.AgentID) == "" || strings.TrimSpace(route.TokenFile) == "" || route.ChatID == 0 {
		return telegramChildBotRoute{}, fmt.Errorf("telegram child bot route requires agent_id, token_file, and chat_id")
	}
	return route, nil
}

func normalizeChildBotRespondOn(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "mentions":
		return "mentions"
	case "all":
		return "all"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func validateTelegramChildBotTokenMetadata(path string, stat func(string) (os.FileInfo, error)) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("telegram child bot token_file is required")
	}
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(path)
	if err != nil {
		return fmt.Errorf("telegram child bot token_file metadata check failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("telegram child bot token_file must be a regular file")
	}
	if info.Size() <= 0 {
		return fmt.Errorf("telegram child bot token_file must not be empty")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("telegram child bot token_file permissions must not grant group/other access")
	}
	return nil
}

func readTelegramChildBotToken(path string, readFile func(string) ([]byte, error)) (string, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}
	raw, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("read telegram child bot token_file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("telegram child bot token_file must not be empty")
	}
	return token, nil
}

func loadTelegramChildBotAgent(store *session.SQLiteStore, agentID string) (core.DurableAgent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return core.DurableAgent{}, fmt.Errorf("telegram child bot agent id is required")
	}
	agentRow, err := store.DurableAgent(agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.DurableAgent{}, fmt.Errorf("durable agent %q not found", agentID)
		}
		return core.DurableAgent{}, fmt.Errorf("load durable agent %q: %w", agentID, err)
	}
	if agentRow == nil {
		return core.DurableAgent{}, fmt.Errorf("durable agent %q not found", agentID)
	}
	return *agentRow, nil
}

func validateTelegramChildBotRouteAgainstAgent(route telegramChildBotRoute, agentRow core.DurableAgent) error {
	if strings.TrimSpace(agentRow.AgentID) != strings.TrimSpace(route.AgentID) {
		return fmt.Errorf("telegram child bot route agent %q does not match durable agent %q", route.AgentID, agentRow.AgentID)
	}
	if strings.TrimSpace(agentRow.ChannelKind) != "telegram_group" {
		return fmt.Errorf("telegram child bot agent %q must have channel_kind telegram_group", route.AgentID)
	}
	if status := strings.ToLower(strings.TrimSpace(agentRow.Status)); status != "" && status != "active" {
		return fmt.Errorf("telegram child bot agent %q is not active", route.AgentID)
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agentRow.BootstrapLLM)
	if !bootstrap.Configured() {
		return fmt.Errorf("telegram child bot agent %q requires configured child-local bootstrap", route.AgentID)
	}
	if strings.TrimSpace(agentRow.LivePolicy.Charter) == "" {
		return fmt.Errorf("telegram child bot agent %q requires a live policy charter", route.AgentID)
	}
	return nil
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

	rt, err := newTelegramChildBotRuntime(cfg, store, client)
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
	fmt.Fprintf(os.Stdout, "status: running\n")
	return poller.Run(ctx)
}

func newTelegramChildBotRuntime(cfg *config.Config, store *session.SQLiteStore, client *telegram.Client) (*aphruntime.Runtime, error) {
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
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, aphruntime.SandboxProfilesFromConfig(cfg.Sandbox))
	if err != nil {
		return nil, err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithSessionStore(store).
		WithDurableAgentPrincipalFallback().
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
	rt, err := aphruntime.New(cfg, store, llm, agent.ToolRegistry(tools), newTelegramUIClient(client))
	if err != nil {
		return nil, err
	}
	tools.WithCapabilityGrantObserver(rt.HandleCapabilityGrantActivated)
	return rt, nil
}
