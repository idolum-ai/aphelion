//go:build linux

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	aphruntime "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableAgentWakeRuntime interface {
	RunDurableAgentChildWake(context.Context, string, time.Time) error
}

type durableAgentWakeRuntimeFactory func(*config.Config) (durableAgentWakeRuntime, func(), error)

func runDurableAgentWakeCommand(args []string) error {
	return runDurableAgentWakeCommandWithFactory(args, newDurableAgentWakeRuntimeForCommand)
}

func runDurableAgentWakeCommandWithFactory(args []string, factory durableAgentWakeRuntimeFactory) error {
	fs := flag.NewFlagSet("durable-agent wake", flag.ContinueOnError)
	configFlag := fs.String("config", "", "path to config.toml")
	agentID := fs.String("agent", "", "durable agent id")
	nowRaw := fs.String("now", "", "override wake timestamp (RFC3339 or RFC3339Nano)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*agentID) == "" {
		return fmt.Errorf("durable-agent wake requires --agent")
	}
	now, err := parseDurableChildWakeTime(*nowRaw)
	if err != nil {
		return err
	}
	cfg, configPath, err := loadConfigForCommand(*configFlag)
	if err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("durable-agent wake runtime factory is unavailable")
	}
	rt, cleanup, err := factory(cfg)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	started := time.Now().UTC()
	if err := rt.RunDurableAgentChildWake(context.Background(), strings.TrimSpace(*agentID), now); err != nil {
		return err
	}
	completed := time.Now().UTC()
	fmt.Fprintf(os.Stdout, "action: durable-agent wake\n")
	fmt.Fprintf(os.Stdout, "agent_id: %s\n", strings.TrimSpace(*agentID))
	fmt.Fprintf(os.Stdout, "config: %s\n", configPath)
	fmt.Fprintf(os.Stdout, "wake_time: %s\n", now.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "started_at: %s\n", started.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "completed_at: %s\n", completed.Format(time.RFC3339Nano))
	fmt.Fprintf(os.Stdout, "status: completed\n")
	return nil
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
		WithDurableAgentPrincipalFallback().
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
