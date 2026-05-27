//go:build linux

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/childcli"
	"github.com/idolum-ai/aphelion/memory"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableChildNoopOutbound struct{}

func (durableChildNoopOutbound) SendMessage(context.Context, core.OutboundMessage) (int64, error) {
	return 0, fmt.Errorf("outbound delivery is unavailable in durable child mode")
}

func runDurableTelegramGroupChildBootstrap(ctx context.Context, bootstrap runtimepkg.DurableAgentChildBootstrap, msg core.InboundMessage) (*runtimepkg.DurableGroupChildResult, error) {
	cfg := &bootstrap.Config
	if err := validateDurableChildBootstrapConfig(cfg); err != nil {
		return nil, err
	}
	if err := prepareFilesystem(cfg); err != nil {
		return nil, err
	}
	if _, err := seedAgentPromptFiles(cfg); err != nil {
		return nil, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	httpClient := &http.Client{Timeout: 90 * time.Second}
	var llm agent.Provider
	if strings.EqualFold(strings.TrimSpace(cfg.Governor.Backend), "native") {
		nativeProvider, err := buildNativeProviderChain(cfg, httpClient)
		if err != nil {
			return nil, err
		}
		if nativeProvider == nil {
			return nil, fmt.Errorf("durable child bootstrap does not define a usable native provider")
		}
		llm = nativeProvider
	}

	rt, err := runtimepkg.New(cfg, store, llm, nil, durableChildNoopOutbound{})
	if err != nil {
		return nil, err
	}

	result, err := rt.RunDurableTelegramGroupChild(ctx, msg)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func runDurableAgentChildWakeBootstrap(ctx context.Context, bootstrap runtimepkg.DurableAgentChildBootstrap, agentID string, now time.Time) error {
	cfg := &bootstrap.Config
	if err := validateDurableChildBootstrapConfig(cfg); err != nil {
		return err
	}
	if err := prepareFilesystem(cfg); err != nil {
		return err
	}
	if _, err := seedAgentPromptFiles(cfg); err != nil {
		return err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		return err
	}
	sandboxResolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		return err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithUserAgent(config.EffectiveUserAgent(cfg, tool.DefaultNativeFetchUserAgent)).
		WithSessionStore(store).
		WithRemoteHostSSH(cfg.Tailscale.SSHPath, remoteHostSSHTimeoutFromConfig(cfg)).
		WithDurableAgentPrincipalFallback().
		WithConfiguredCapabilityVisibility(configuredCapabilityVisibilityFromConfig(cfg))
	tools.WithSemanticEngine(memory.NewSemanticEngine(memory.SemanticOptions{
		Enabled:             cfg.Memory.Semantic.Enabled,
		DBPath:              memory.DefaultSemanticDBPath(cfg.Sessions.DBPath),
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
		return err
	}
	if fileStore != nil {
		tools.WithFileStore(fileStore, cfg.OpenAI.Files.Purpose)
	}
	if retrievalStore != nil {
		tools.WithRetrievalStore(retrievalStore, cfg.OpenAI.VectorStores.DefaultStore)
	}
	rt, err := runtimepkg.New(cfg, store, llm, tools, durableChildNoopOutbound{})
	if err != nil {
		return err
	}
	return rt.RunDurableAgentChildWake(ctx, agentID, now)
}

func validateDurableChildBootstrapConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("durable child bootstrap config is required")
	}
	if strings.TrimSpace(cfg.Telegram.BotToken) != "" {
		return fmt.Errorf("durable child bootstrap must not include telegram.bot_token")
	}
	if len(cfg.Telegram.DurableGroups) > 0 {
		return fmt.Errorf("durable child bootstrap must not include telegram.durable_groups")
	}
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 || len(cfg.Principals.Telegram.ApprovedUserIDs) > 0 {
		return fmt.Errorf("durable child bootstrap must not include principals.telegram")
	}
	return nil
}

func runDurableAgentChildCommand(args []string) error {
	return childcli.RunDurableAgentChildCommand(args, childcli.DurableAgentChildDeps{
		RunTelegramGroupChild: func(ctx context.Context, cfg config.Config, msg core.InboundMessage) (any, error) {
			return runDurableTelegramGroupChildBootstrap(ctx, runtimepkg.DurableAgentChildBootstrap{Config: cfg}, msg)
		},
		RunChildWake: func(ctx context.Context, cfg config.Config, agentID string, now time.Time) error {
			return runDurableAgentChildWakeBootstrap(ctx, runtimepkg.DurableAgentChildBootstrap{Config: cfg}, agentID, now)
		},
	})
}

func decodeJSONFile(path string, out any) error {
	return childcli.DecodeJSONFile(path, out)
}

func parseDurableChildWakeTime(raw string) (time.Time, error) {
	return childcli.ParseDurableChildWakeTime(raw)
}
