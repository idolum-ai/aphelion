//go:build linux

package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/internal/maintenancecli"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableAgentWakeRuntime = maintenancecli.DurableAgentWakeRuntime
type durableAgentWakeRuntimeFactory = maintenancecli.DurableAgentWakeRuntimeFactory

func runDurableAgentWakeCommand(args []string) error {
	return runDurableAgentWakeCommandWithFactory(args, newDurableAgentWakeRuntimeForCommand)
}

func runDurableAgentWakeCommandWithFactory(args []string, factory durableAgentWakeRuntimeFactory) error {
	return maintenancecli.RunDurableAgentWakeCommand(args, factory)
}

func newDurableAgentWakeRuntimeForCommand(cfg *config.Config) (durableAgentWakeRuntime, func(), error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	if err := prepareFilesystem(cfg); err != nil {
		return nil, nil, err
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = store.Close() }

	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		cleanup()
		return nil, nil, err
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
		cleanup()
		return nil, nil, err
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandboxProfiles)
	if err != nil {
		cleanup()
		return nil, nil, err
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
			cleanup()
			return nil, nil, fmt.Errorf("load external tool manifests: %w", err)
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
		cleanup()
		return nil, nil, err
	}
	if fileStore != nil {
		tools.WithFileStore(fileStore, cfg.OpenAI.Files.Purpose)
	}
	if retrievalStore != nil {
		tools.WithRetrievalStore(retrievalStore, cfg.OpenAI.VectorStores.DefaultStore)
	}
	rt, err := aphruntime.New(cfg, store, llm, tools, durableChildNoopOutbound{})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	tools.WithCapabilityGrantObserver(rt.HandleCapabilityGrantActivated)
	return rt, cleanup, nil
}
