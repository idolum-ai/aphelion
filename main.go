//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/openai"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/voice"
)

const (
	turnTimeout     = 10 * time.Minute
	exitCodeFailure = 1
	exitCodeConfig  = 78
)

type configStartupError struct {
	Path string
	Err  error
}

type telegramCommandControl struct {
	router *core.Router
	rt     *runtime.Runtime
}

func (c telegramCommandControl) Stop(chatID int64) core.StopResult {
	return c.router.Stop(chatID)
}

func (c telegramCommandControl) Status(chatID int64) core.SessionStatus {
	return c.router.Status(chatID)
}

func (c telegramCommandControl) TogglePersonaEffort() (string, error) {
	return c.rt.TogglePersonaEffort()
}

func (c telegramCommandControl) ToggleGovernorEffort() (string, error) {
	return c.rt.ToggleGovernorEffort()
}

func (c telegramCommandControl) CurrentEfforts() (string, string) {
	return c.rt.CurrentEfforts()
}

func (c telegramCommandControl) CurrentPersonaModel() string {
	return c.rt.CurrentPersonaModel()
}

func (c telegramCommandControl) PersonaModelOptions() []string {
	return c.rt.PersonaModelOptions()
}

func (c telegramCommandControl) SetPersonaModel(model string) (string, error) {
	return c.rt.SetPersonaModel(model)
}

func (c telegramCommandControl) GovernorEffortOptions() []string {
	return c.rt.GovernorEffortOptions()
}

func (c telegramCommandControl) SetGovernorEffort(effort string) (string, error) {
	return c.rt.SetGovernorEffort(effort)
}

func (e *configStartupError) Error() string {
	return fmt.Sprintf("config %s: %v (run 'aphelion --config %s --check-config' to validate)", e.Path, e.Err, e.Path)
}

func (e *configStartupError) Unwrap() error {
	return e.Err
}

func main() {
	if err := run(); err != nil {
		log.Printf("ERROR aphelion exited with error: %v", err)
		os.Exit(exitCode(err))
	}
}

func run() error {
	handled, err := runMaintenanceCommand(os.Args[1:])
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	configPathFlag := flags.String("config", "", "path to config.toml")
	checkConfig := flags.Bool("check-config", false, "validate config and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	configPath, err := config.ResolveConfigPath(*configPathFlag)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return &configStartupError{Path: configPath, Err: err}
	}

	if err := prepareFilesystem(cfg); err != nil {
		return &configStartupError{Path: configPath, Err: err}
	}
	if *checkConfig {
		log.Printf("INFO config ok path=%s", configPath)
		return nil
	}

	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := syncConfiguredTelegramDurableGroups(cfg, store); err != nil {
		return err
	}

	httpClient := &http.Client{Timeout: 90 * time.Second}
	llm, err := buildNativeProviderChain(cfg, httpClient)
	if err != nil {
		return err
	}

	sandboxRoots := sandbox.Roots{
		GlobalRoot:        cfg.Agent.PromptRoot,
		AdminExecRoot:     cfg.Agent.ExecRoot,
		SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
		UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
		UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandbox.DefaultProfiles())
	if err != nil {
		return err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).WithSessionStore(store)
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
	principalResolver := principal.NewResolver(
		cfg.Principals.Telegram.AdminUserIDs,
		cfg.Principals.Telegram.ApprovedUserIDs,
	)
	tgClient := telegram.NewClient(
		cfg.Telegram.BotToken,
		telegram.WithHTTPClient(httpClient),
		telegram.WithPollTimeout(cfg.Telegram.PollTimeout),
	)
	var botUser *telegram.User
	if durableGroupsConfigured(cfg) && durableGroupsNeedBotIdentity(cfg.Telegram.DurableGroups) {
		getMeCtx, cancelGetMe := context.WithTimeout(context.Background(), 15*time.Second)
		botUser, err = tgClient.GetMe(getMeCtx)
		cancelGetMe()
		if err != nil {
			return err
		}
	}

	rt, err := runtime.New(cfg, store, llm, tools, tgClient)
	if err != nil {
		return err
	}

	if cfg.Voice.Mode != "" && cfg.Voice.Mode != "off" {
		openaiClient, err := openai.NewClient(openai.ClientOptions{
			APIKey:     cfg.Voice.OpenAIAPIKey,
			BaseURL:    cfg.Voice.OpenAIBaseURL,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
		})
		if err != nil {
			return err
		}
		transcriber, err := openai.NewTranscriptionClient(openaiClient, openai.TranscriptionOptions{
			Model: cfg.Voice.OpenAIModel,
		})
		if err != nil {
			return err
		}
		synth, err := voice.NewElevenLabs(voice.ElevenLabsOptions{
			APIKey:     cfg.Voice.ElevenLabsAPIKey,
			BaseURL:    cfg.Voice.ElevenLabsBaseURL,
			VoiceID:    cfg.Voice.ElevenLabsVoiceID,
			ModelID:    cfg.Voice.ElevenLabsModelID,
			HTTPClient: httpClient,
		})
		if err != nil {
			return err
		}
		rt.ConfigureVoice(cfg.Voice, transcriber, synth)
	}

	router := core.NewRouter(rt.AgentFunc())
	commandControl := telegramCommandControl{router: router, rt: rt}
	decisionBroker := newTelegramDecisionBroker(tgClient)
	decisionHandler := newTelegramDecisionHandler(tgClient, router, decisionBroker)
	tools.WithExecApprover(newTelegramExecApprover(tgClient, decisionBroker))

	registerCtx, cancelRegister := context.WithTimeout(context.Background(), 15*time.Second)
	if err := registerTelegramCommands(registerCtx, tgClient); err != nil {
		log.Printf("WARN telegram command registration failed: %v", err)
	}
	cancelRegister()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := startDurableAgentControlPlane(ctx, durableAgentControlPlaneServer(cfg, store)); err != nil {
		return err
	}
	rt.StartStartupRecovery(ctx, log.Printf)
	rt.StartIdleExpiryLoop(ctx, log.Printf)
	rt.StartHeartbeatLoop(ctx, log.Printf)
	rt.StartDurableEmailLoop(ctx, log.Printf)
	rt.StartCronLoop(ctx, log.Printf)

	poller := telegram.NewPoller(tgClient, func(parent context.Context, msg core.InboundMessage) error {
		handled, err := handleTelegramCommand(parent, tgClient, commandControl, msg)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		if busyHandled, busyErr := decisionHandler.HandleBusyMessage(parent, msg); busyErr != nil {
			return busyErr
		} else if busyHandled {
			return nil
		}

		turnCtx, cancel := context.WithTimeout(parent, turnTimeout)
		go func() {
			defer cancel()
			router.Route(turnCtx, msg)
		}()
		return nil
	},
		telegram.WithPollerTimeout(cfg.Telegram.PollTimeout),
		telegram.WithMediaConfig(cfg.Telegram.Media),
		telegram.WithPrincipalResolver(principalResolver),
		telegram.WithDurableGroups(cfg.Telegram.DurableGroups),
		telegram.WithBotIdentity(botUser),
		telegram.WithCallbackHandler(func(parent context.Context, cb telegram.CallbackQuery) error {
			if handled, err := handleTelegramCommandCallback(parent, tgClient, commandControl, cb); err != nil {
				return err
			} else if handled {
				return nil
			}
			return decisionHandler.HandleCallbackQuery(parent, cb)
		}),
	)

	log.Printf(
		"INFO aphelion started config_path=%s prompt_root=%s exec_root=%s shared_memory_root=%s user_workspace_root=%s user_memory_root=%s db_path=%s model=%s native_provider=%s fallback_chain=%s",
		configPath,
		cfg.Agent.PromptRoot,
		cfg.Agent.ExecRoot,
		cfg.Agent.SharedMemoryRoot,
		cfg.Agent.UserWorkspaceRoot,
		cfg.Agent.UserMemoryRoot,
		cfg.Sessions.DBPath,
		activeNativeModel(cfg),
		resolveNativeProviderName(cfg),
		strings.Join(cfg.Providers.FallbackChain, ","),
	)
	return poller.Run(ctx)
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

func buildNativeProviderChain(cfg *config.Config, httpClient *http.Client) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	names := orderedNativeProviderNames(cfg)
	entries := make([]provider.NamedProvider, 0, len(names))
	required := nativeProviderRequired(cfg)
	for idx, name := range names {
		if !isConfiguredProvider(name, cfg) {
			if idx == 0 && required {
				return nil, fmt.Errorf("native provider %q is enabled but not configured", name)
			}
			continue
		}
		p, err := buildNamedProvider(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		entries = append(entries, provider.NamedProvider{Name: name, Provider: p})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return provider.NewFailoverChain(entries)
}

func buildOpenAIPlatformServices(cfg *config.Config, httpClient *http.Client) (memory.FileStore, memory.RetrievalStore, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	if !cfg.OpenAI.Files.Enabled && !cfg.OpenAI.VectorStores.Enabled {
		return nil, nil, nil
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	client, err := openai.NewClient(openai.ClientOptions{
		APIKey:     cfg.Providers.OpenAI.APIKey,
		BaseURL:    cfg.Providers.OpenAI.BaseURL,
		HTTPClient: httpClient,
		UserAgent:  cfg.Identity.UserAgent,
	})
	if err != nil {
		return nil, nil, err
	}

	var fileStore memory.FileStore
	if cfg.OpenAI.Files.Enabled {
		files, err := openai.NewFilesClient(client)
		if err != nil {
			return nil, nil, err
		}
		fileStore = files
	}

	var retrievalStore memory.RetrievalStore
	if cfg.OpenAI.VectorStores.Enabled {
		vectorStores, err := openai.NewVectorStoresClient(client)
		if err != nil {
			return nil, nil, err
		}
		retrievalStore = vectorStores
	}

	return fileStore, retrievalStore, nil
}

func buildNamedProvider(name string, cfg *config.Config, httpClient *http.Client) (agent.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return provider.NewAnthropic(provider.AnthropicOptions{
			APIKey:     cfg.Providers.Anthropic.APIKey,
			Model:      cfg.Providers.Anthropic.Model,
			MaxTokens:  cfg.Providers.Anthropic.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
		})
	case "openrouter":
		return provider.NewOpenRouter(provider.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      cfg.Providers.OpenRouter.Model,
			MaxTokens:  cfg.Providers.OpenRouter.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
		})
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
}

func orderedNativeProviderNames(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(cfg.Providers.FallbackChain))
	for _, raw := range append([]string{resolveNativeProviderName(cfg)}, cfg.Providers.FallbackChain...) {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func resolveNativeProviderName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if name := strings.ToLower(strings.TrimSpace(cfg.Governor.NativeProvider)); name != "" {
		return name
	}
	if name := strings.ToLower(strings.TrimSpace(cfg.Providers.Default)); name != "" {
		return name
	}
	return "anthropic"
}

func activeNativeModel(cfg *config.Config) string {
	switch resolveNativeProviderName(cfg) {
	case "openrouter":
		return cfg.Providers.OpenRouter.Model
	default:
		return cfg.Providers.Anthropic.Model
	}
}

func nativeProviderRequired(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	governorBackend := strings.ToLower(strings.TrimSpace(cfg.Governor.Backend))
	faceBackend := config.NormalizeFaceBackendValue(cfg.Face.Backend)
	return governorBackend == "native" || faceBackend == "" || faceBackend == "provider"
}

func isConfiguredProvider(name string, cfg *config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return strings.TrimSpace(cfg.Providers.Anthropic.APIKey) != ""
	case "openrouter":
		return strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) != ""
	default:
		return false
	}
}

func exitCode(err error) int {
	var cfgErr *configStartupError
	if errors.As(err, &cfgErr) {
		return exitCodeConfig
	}
	return exitCodeFailure
}
