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
	"strings"
	"syscall"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/standalonecli"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/internal/telegramdecision"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/openai"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/voice"
	_ "github.com/mattn/go-sqlite3"
)

const (
	// turnTimeout <= 0 disables per-turn deadlines so long deliberations can run until
	// explicit user control (/stop, /detach, or thinking-card controls) interrupts them.
	turnTimeout     = 0
	exitCodeFailure = 1
	exitCodeConfig  = 78
	restartExitWait = 250 * time.Millisecond
)

var processExit = os.Exit

const reinstallTemplateMessage = "Rebuild, reinstall, restart, and verify the aphelion user service on this host using the current checked-out branch state. Use the normal local deploy path for a source install: build the binary, run --check-config, run init including Codex session import, restart the systemd user service, and run verify-deploy. Treat this as an operational change: inspect the current service/install state first, then execute the bounded redeploy steps, and report what happened truthfully."

type configStartupError struct {
	Path string
	Err  error
}

func main() {
	if err := run(); err != nil {
		var usageErr *cliUsageError
		if errors.As(err, &usageErr) {
			fmt.Fprintln(os.Stderr, usageErr.Error())
			os.Exit(exitCode(err))
		}
		log.Printf("ERROR aphelion exited with error: %v", err)
		os.Exit(exitCode(err))
	}
}

func run() error {
	args := os.Args[1:]
	if topLevelHelpRequested(args) {
		printTopLevelHelp(os.Stdout, "")
		return nil
	}
	if topLevelVersionRequested(args) {
		return standalonecli.RunVersionCommand(topLevelVersionArgs(args))
	}
	if flagName, ok := unknownTopLevelFlag(args); ok {
		return &cliUsageError{Text: renderUnknownFlagHelp(flagName)}
	}
	handled, err := runMaintenanceCommand(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if handled {
		return nil
	}

	flags := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	configPathFlag := flags.String("config", "", "path to config.toml")
	checkConfig := flags.Bool("check-config", false, "validate config and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if extra, ok := firstPositionalArg(flags.Args()); ok {
		return &cliUsageError{Text: renderUnknownCommandHelp(extra)}
	}

	configPath, err := config.ResolveConfigPath(*configPathFlag)
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return &configStartupError{Path: configPath, Err: err}
	}
	logConfigWarnings(configPath, cfg)
	logSandboxReadinessWarnings(configPath, cfg)

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
	if err := syncRuntimeDurableAgentsAtStartup(cfg, store); err != nil {
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
	sandboxProfiles, err := runtime.SandboxProfilesFromConfig(cfg.Sandbox)
	if err != nil {
		return err
	}
	sandboxResolver, err := sandbox.NewResolver(sandboxRoots, sandboxProfiles)
	if err != nil {
		return err
	}
	tools := tool.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Duration(cfg.Agent.ToolTimeout)*time.Second, sandboxResolver).
		WithUserAgent(config.EffectiveUserAgent(cfg, tool.DefaultNativeFetchUserAgent)).
		WithSessionStore(store).
		WithWebSearchOptions(tool.WebSearchOptionsFromConfig(cfg.Tools.WebSearch)).
		WithConfiguredCapabilityVisibility(configuredCapabilityVisibilityFromConfig(cfg)).
		WithRemoteHostSSH(cfg.Tailscale.SSHPath, remoteHostSSHTimeoutFromConfig(cfg))
	if manifestDir := strings.TrimSpace(cfg.Tools.ExternalManifestDir); manifestDir != "" {
		if _, err := tools.WithExternalToolManifestDir(manifestDir); err != nil {
			return fmt.Errorf("load external tool manifests: %w", err)
		}
	}
	tools.WithDurableAgentBootstrapLLM(defaultDurableAgentBootstrapFromConfig(cfg))
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

	tgOutbound := telegramruntime.NewUIClient(tgClient)

	rt, err := runtime.New(cfg, store, llm, tools, tgOutbound)
	if err != nil {
		return err
	}
	defer rt.BeginShutdown()
	tools.WithCapabilityGrantObserver(rt.HandleCapabilityGrantActivated)

	if cfg.Voice.Mode != "" && cfg.Voice.Mode != "off" {
		openaiClient, err := openai.NewClient(openai.ClientOptions{
			APIKey:     cfg.Voice.OpenAIAPIKey,
			BaseURL:    cfg.Voice.OpenAIBaseURL,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
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
	router.SetEventHandler(rt.RouterEventHandler())
	ingress := telegramcontrol.NewIngressSequencer(router, turnTimeout)
	decisionBroker := telegramdecision.NewBrokerWithSummaryAndUI(
		tgOutbound,
		func(ctx context.Context, pending decision.PendingDecision) string {
			if pending.Kind != decision.KindProposalApproval || rt == nil {
				return ""
			}
			return rt.StatusReadableSummary(ctx, "approval", telegramdecision.RenderPendingDecisionExpanded(pending))
		},
		telegramdecision.BrokerUIOptions{
			ApprovalWindows: rt,
			ThreadResolver:  store,
			ThreadRecorder:  store,
		},
		decision.WithDurableStore(telegramdecision.NewDurableStore(store)),
		decision.WithObserver(rt.DecisionEventObserver()),
		decision.WithAutoResolver(rt.AutoResolveDecision),
	)
	commandControl := telegramCommandControl{
		router:                 router,
		ingress:                ingress,
		rt:                     rt,
		store:                  store,
		resolver:               principalResolver,
		decisionDetacher:       decisionBroker,
		detachPendingOnRestart: cfg.Telegram.DetachPendingOnRestart,
		durableTools:           tools,
	}
	if ingress != nil {
		ingress.SetDropHandler(commandControl.MarkDroppedIngress)
	}
	tailnetParent, err := tailnetParentService(cfg, commandControl, store)
	if err != nil {
		return err
	}
	if tailnetParent != nil {
		rt.SetTailnetParentStatusProvider(tailnetParent.Status)
	}
	loadDecisionCtx, cancelDecisionLoad := context.WithTimeout(context.Background(), 5*time.Second)
	if err := decisionBroker.Load(loadDecisionCtx); err != nil {
		cancelDecisionLoad()
		return fmt.Errorf("load pending decisions: %w", err)
	}
	cancelDecisionLoad()
	decisionHandler := telegramdecision.NewDecisionHandler(tgOutbound, commandControl, decisionBroker, store, rt)
	execApprover := telegramdecision.NewExecApprover(tgOutbound, decisionBroker, telegramdecision.DefaultExecApprovalTimeout, rt)
	execApprover.SetPresentation(store)
	tools.WithExecApprover(execApprover)
	memoryApprover := telegramdecision.NewDurableMemoryDelegationApprover(tgOutbound, decisionBroker, telegramdecision.DefaultMemoryDelegationTimeout)
	memoryApprover.SetPresentation(store)
	tools.WithDurableMemoryDelegationApprover(memoryApprover)
	snapshotApprover := telegramdecision.NewDurableSnapshotRestoreApprover(tgOutbound, decisionBroker, telegramdecision.DefaultSnapshotRestoreTimeout)
	snapshotApprover.SetPresentation(store)
	tools.WithDurableSnapshotRestoreApprover(snapshotApprover)

	registerCtx, cancelRegister := context.WithTimeout(context.Background(), 15*time.Second)
	if err := telegramcommands.RegisterTelegramCommands(registerCtx, tgClient); err != nil {
		log.Printf("WARN telegram command registration failed: %v", err)
	}
	cancelRegister()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	controlPlaneServer, err := durableAgentControlPlaneServer(cfg, store)
	if err != nil {
		return err
	}
	if err := startDurableAgentControlPlane(ctx, controlPlaneServer); err != nil {
		return err
	}
	if err := startTailnetParent(ctx, tailnetParent); err != nil {
		return err
	}
	rt.StartStartupRecovery(ctx, log.Printf)
	rt.StartIdleExpiryLoop(ctx, log.Printf)
	rt.StartStaleTurnWatchdogLoop(ctx, log.Printf)
	rt.StartHeartbeatLoop(ctx, log.Printf)
	rt.StartDurableWakeLoop(ctx, log.Printf)
	rt.StartCronLoop(ctx, log.Printf)
	rt.StartNocturneLoop(ctx, log.Printf)

	telegramHandler := func(parent context.Context, msg core.InboundMessage) error {
		msg = telegramruntime.RewriteDurableWizardIntent(msg, commandControl)
		msg = telegramruntime.RewriteDurableRelayIntent(msg)
		if routed, handled, err := telegramcommands.ResolveTelegramThreadPrefix(parent, tgOutbound, commandControl, msg); err != nil {
			return err
		} else if handled {
			return nil
		} else {
			msg = routed
		}
		threadCommandPayload := false
		if routed, retargeted, handled, err := telegramcommands.ResolveTelegramThreadStartCommand(parent, tgOutbound, commandControl, msg); err != nil {
			return err
		} else if handled {
			return nil
		} else if retargeted {
			msg = routed
			threadCommandPayload = true
		}
		if !threadCommandPayload {
			handled, err := telegramcommands.HandleTelegramCommand(parent, tgOutbound, commandControl, msg)
			if err != nil {
				return err
			}
			if handled {
				return nil
			}
		}
		if routed, handled, err := telegramcommands.ResolveTelegramThreadReply(parent, tgOutbound, commandControl, msg); err != nil {
			return err
		} else if handled {
			return nil
		} else {
			msg = routed
		}
		if handled, err := telegramcommands.ResolveTelegramAgentReply(parent, tgOutbound, commandControl, msg); err != nil {
			return err
		} else if handled {
			return nil
		}
		if busyHandled, busyErr := decisionHandler.HandleBusyMessage(parent, msg); busyErr != nil {
			return busyErr
		} else if busyHandled {
			return nil
		}
		if retentionHandled, retentionErr := decisionHandler.HandleArtifactRetentionMessage(parent, msg); retentionErr != nil {
			return retentionErr
		} else if retentionHandled {
			return nil
		}

		return commandControl.RouteAccepted(parent, msg)
	}
	checkpoint, err := telegramruntime.ReplayStartupIngress(ctx, store, telegramHandler, log.Printf)
	if err != nil {
		return err
	}
	if err := decisionHandler.ReconcileRestartLoadedDecisions(ctx); err != nil {
		return err
	}
	poller := telegram.NewPoller(tgClient, telegramHandler,
		telegram.WithPollerTimeout(cfg.Telegram.PollTimeout),
		telegram.WithMediaConfig(cfg.Telegram.Media),
		telegram.WithPrincipalResolver(principalResolver),
		telegram.WithDurableGroups(cfg.Telegram.DurableGroups),
		telegram.WithUnresolvedPrivatePredicate(shouldAllowUnresolvedPrivateDurableRelayMessage),
		telegram.WithBotIdentity(botUser),
		telegram.WithCheckpoint(checkpoint),
		telegram.WithIngressSurface(telegramruntime.PrimaryIngressSurface),
		telegram.WithCallbackHandler(func(parent context.Context, cb telegram.CallbackQuery) error {
			if handled, err := telegramcommands.HandleTelegramCommandCallback(parent, tgOutbound, commandControl, cb); err != nil {
				commandControl.RecordTelegramCallbackError(telegramdecision.CallbackChatID(cb), "command", err)
				return err
			} else if handled {
				return nil
			}
			if err := decisionHandler.HandleCallbackQuery(parent, cb); err != nil {
				commandControl.RecordTelegramCallbackError(telegramdecision.CallbackChatID(cb), "decision", err)
				return err
			}
			return nil
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
