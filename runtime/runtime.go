//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/governorbackend"
	"github.com/idolum-ai/aphelion/media"
	"github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/voice"
)

type OutboundSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
}

type chatActionSender interface {
	SendChatAction(ctx context.Context, chatID int64, action string) error
}

const approvedContinuationEventText = "[user pressed continue button: resume the previous task]"

type inboundArtifactFetcher interface {
	DownloadFileChecked(ctx context.Context, fileID string, maxBytes int64) ([]byte, error)
}

type Runtime struct {
	cfg          *config.Config
	store        *session.SQLiteStore
	provider     agent.Provider
	native       agent.Provider
	tools        agent.ToolRegistry
	outbound     OutboundSender
	resolver     *principal.Resolver
	inbound      inboundArtifactFetcher
	workExecutor *WorkExecutorSelector

	faceBackend face.Backend
	faceModel   face.Renderer
	faceModels  map[string]face.Renderer
	voiceMode   string
	transcriber media.TranscriptionProvider
	synth       voice.Synthesizer
	semantic    *memory.SemanticEngine

	governorBackend     string
	streamEditInterval  time.Duration
	streamCursor        string
	toolProgressMode    string
	toolProgressStyle   string
	toolProgressWindow  int
	toolProgressCleanup bool

	idleExpiry time.Duration
	expireIdle func(maxIdle time.Duration) (int, error)

	staleTurnThreshold       time.Duration
	staleTurnLimit           int
	staleTurnWatchdogEnabled bool
	staleTurnSweep           func(cutoff time.Time, limit int) ([]session.TurnRun, error)
	interruptRunningTurnRuns func() ([]session.TurnRun, error)
	interruptStaleTurnRuns   func(ids []int64, reason string) ([]session.TurnRun, error)
	staleWatchdogTriggered   atomic.Bool
	staleWatchdogNextAttempt atomic.Int64
	activeTurnMu             sync.Mutex
	activeTurnCancels        map[int64]*activeTurnRun

	scopeResolver          *sandbox.Resolver
	durableGroupChild      durableGroupChildExecutor
	durableWakeChild       durableWakeChildExecutor
	durableWakeAdapters    []durableWakeIngressAdapter
	constitutionGate       TurnConstitutionGate
	turnAuditSink          func(TurnAudit)
	interactiveDMAssembler interactiveDMTurnAssembler
	maintenanceAssembler   maintenanceTurnAssembler
	operationalAlertMu     sync.Mutex
	operationalAlerts      map[string]operationalAlertState
	operationalAlertClock  func() time.Time
	operationalAlertWindow time.Duration
	sessionMu              sync.Mutex
	sessionLocks           map[string]*sync.Mutex
	statusReadableMu       sync.Mutex
	statusReadableProvider agent.Provider
	statusReadableReady    bool
	tailnetBackend         tailnet.Backend
	tailnetParentStatus    func() core.TailnetParentStatus
	modelProviderMu        sync.Mutex
	modelProviderCache     map[string]agent.Provider
	streamControlMu        sync.Mutex
	streamControls         map[string]activeStreamControl
	streamControlSeq       atomic.Uint64
	faceModelsMu           sync.Mutex
	recipeMu               sync.Mutex
	recipeFileMu           sync.Mutex
	recipePath             string
	recipeState            runtimeRecipeState
	shuttingDown           atomic.Bool
}

func (r *Runtime) ConfigureVoice(cfg config.VoiceConfig, transcriber media.TranscriptionProvider, synth voice.Synthesizer) {
	if r == nil {
		return
	}
	r.voiceMode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	r.transcriber = transcriber
	r.synth = synth
}

var ErrPrincipalDenied = errors.New("principal is not admitted")

func newCodexHTTPClient(responseHeaderTimeoutValues ...time.Duration) *http.Client {
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		return &http.Client{}
	}
	responseHeaderTimeout := time.Duration(0)
	if len(responseHeaderTimeoutValues) > 0 {
		responseHeaderTimeout = responseHeaderTimeoutValues[0]
	}
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = 90 * time.Second
	}
	clone := transport.Clone()
	clone.ResponseHeaderTimeout = responseHeaderTimeout
	clone.TLSHandshakeTimeout = 10 * time.Second
	clone.ExpectContinueTimeout = time.Second
	clone.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	return &http.Client{Transport: clone}
}

var newCodexProvider = func(bundle governorauth.Bundle, cfg *config.Config) (agent.Provider, error) {
	var loadTokens func() (governorauth.CodexTokens, error)
	var saveTokens func(governorauth.CodexTokens, time.Time) error
	if strings.TrimSpace(bundle.AuthPath) != "" {
		authPath := bundle.AuthPath
		loadTokens = func() (governorauth.CodexTokens, error) {
			if bundle.Source == "aphelion-auth-json" {
				return governorauth.LoadAphelionCodexAuth(authPath)
			}
			return governorauth.LoadCodexCLIAuth(authPath)
		}
		saveTokens = func(tokens governorauth.CodexTokens, refreshedAt time.Time) error {
			if bundle.Source == "aphelion-auth-json" {
				return governorauth.SaveAphelionCodexAuth(authPath, tokens, refreshedAt)
			}
			return governorauth.SaveCodexCLIAuth(authPath, tokens, refreshedAt)
		}
	}
	responseHeaderTimeout, err := time.ParseDuration(strings.TrimSpace(cfg.Governor.Codex.ResponseHeaderTimeout))
	if err != nil {
		return nil, fmt.Errorf("parse governor.codex.response_header_timeout: %w", err)
	}
	return governorbackend.NewCodex(governorbackend.CodexOptions{
		BaseURL:          bundle.BaseURL,
		AccessToken:      bundle.AccessToken,
		RefreshToken:     bundle.RefreshToken,
		AccountID:        bundle.AccountID,
		RefreshURL:       bundle.RefreshURL,
		Model:            cfg.Governor.Codex.Model,
		StoreResponses:   cfg.Governor.Codex.StoreResponses,
		MaxContinuations: cfg.Governor.Codex.MaxContinuations,
		TransportRetries: cfg.Governor.Codex.TransportRetries,
		HTTPClient:       newCodexHTTPClient(responseHeaderTimeout),
		UserAgent:        config.EffectiveUserAgent(cfg, ""),
		LoadTokens:       loadTokens,
		SaveTokens:       saveTokens,
	})
}

var resolveGovernorAuth = governorauth.ResolveFromConfig
var newFaceRenderer = face.NewProviderRenderer

func New(
	cfg *config.Config,
	store *session.SQLiteStore,
	provider agent.Provider,
	tools agent.ToolRegistry,
	outbound OutboundSender,
) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("session store is nil")
	}
	if outbound == nil {
		return nil, fmt.Errorf("outbound sender is nil")
	}
	cfg = normalizeRuntimeConfig(cfg)

	governorAuth, err := resolveGovernorAuth(cfg.Governor)
	if err != nil {
		return nil, fmt.Errorf("resolve governor auth: %w", err)
	}

	faceBackend := face.NormalizeBackend(cfg.Face.Backend)

	if provider == nil && (governorAuth.Backend == governorauth.BackendNative || faceBackend == face.BackendProvider) {
		return nil, fmt.Errorf("native provider is required for configured governor/face backends")
	}

	activeProvider := provider
	var codexProvider agent.Provider
	if governorAuth.Backend == governorauth.BackendCodex {
		codexProvider, err = newCodexProvider(governorAuth, cfg)
		if err != nil {
			return nil, fmt.Errorf("init codex governor backend: %w", err)
		}
		activeProvider = codexProvider
		if provider != nil {
			chain, err := providerpkg.NewFailoverChain([]providerpkg.NamedProvider{
				{Name: governorauth.BackendCodex, Provider: codexProvider},
				{Name: "native", Provider: provider},
			})
			if err != nil {
				return nil, fmt.Errorf("init governor failover chain: %w", err)
			}
			activeProvider = chain
		}
	}
	if codexProvider != nil {
		if setter, ok := tools.(interface{ SetCodexImageGenerationProvider(agent.Provider) }); ok {
			setter.SetCodexImageGenerationProvider(codexProvider)
		}
	}

	var faceProvider agent.Provider
	switch faceBackend {
	case face.BackendProvider:
		faceProvider = provider
	case face.BackendFloorFallback:
		faceProvider = activeProvider
	default:
		return nil, fmt.Errorf("unsupported face backend: %q", cfg.Face.Backend)
	}

	faceModel, err := newFaceRenderer(faceProvider, face.ProviderRendererConfig{
		GovernorName:  config.EffectiveGovernorName(cfg, prompt.DefaultGovernorName),
		FaceName:      config.EffectiveFaceName(cfg, face.DefaultFaceName),
		Channel:       "telegram",
		WorkspaceRoot: cfg.Agent.PromptRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("init face renderer: %w", err)
	}

	sandboxRoots := sandbox.Roots{
		GlobalRoot:        cfg.Agent.PromptRoot,
		AdminExecRoot:     cfg.Agent.ExecRoot,
		SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
		UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
		UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
	}
	sandboxProfiles, err := SandboxProfilesFromConfig(cfg.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("init sandbox profiles: %w", err)
	}
	scopeResolver, err := sandbox.NewResolver(sandboxRoots, sandboxProfiles)
	if err != nil {
		return nil, fmt.Errorf("init sandbox scope resolver: %w", err)
	}
	tailnetBackend, err := buildTailnetBackend(cfg)
	if err != nil {
		return nil, err
	}

	idleExpiry := 24 * time.Hour
	if raw := strings.TrimSpace(cfg.Sessions.IdleExpiry); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse sessions.idle_expiry: %w", err)
		}
		if d > 0 {
			idleExpiry = d
		}
	}
	streamEditInterval := 300 * time.Millisecond
	if raw := strings.TrimSpace(cfg.Telegram.StreamEditInterval); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse telegram.stream_edit_interval: %w", err)
		}
		if d > 0 {
			streamEditInterval = d
		}
	}
	streamCursor := cfg.Telegram.StreamCursor
	if strings.TrimSpace(streamCursor) == "" {
		streamCursor = " ▉"
	}
	toolProgressStyle := strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgressStyle))
	if toolProgressStyle == "" {
		toolProgressStyle = "semantic"
	}
	toolProgressWindow := cfg.Telegram.ToolProgressWindow
	if toolProgressWindow <= 0 {
		toolProgressWindow = 4
	}
	watchdogConfig := cfg.Recovery.Watchdog
	if !watchdogConfig.Enabled &&
		strings.TrimSpace(watchdogConfig.StaleTurnThreshold) == "" &&
		watchdogConfig.StaleTurnLimit == 0 {
		watchdogConfig = config.Default().Recovery.Watchdog
	}
	staleTurnThreshold := defaultStaleTurnThreshold
	if raw := strings.TrimSpace(watchdogConfig.StaleTurnThreshold); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse recovery.watchdog.stale_turn_threshold: %w", err)
		}
		if d > 0 {
			staleTurnThreshold = d
		}
	}
	staleTurnLimit := watchdogConfig.StaleTurnLimit
	if staleTurnLimit <= 0 {
		staleTurnLimit = defaultStaleTurnLimit
	}
	recipePath := recipeStatePath(cfg)
	recipeState, err := loadRuntimeRecipeState(recipePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("load runtime recipe state: %w", err)
	}

	faceModels := map[string]face.Renderer{}
	if recipeState.PersonaModel == defaultRuntimeRecipeState(cfg).PersonaModel {
		faceModels[recipeState.PersonaModel] = faceModel
	}

	var inbound inboundArtifactFetcher
	if fetcher, ok := outbound.(inboundArtifactFetcher); ok {
		inbound = fetcher
	}

	rt := &Runtime{
		cfg:      cfg,
		store:    store,
		provider: activeProvider,
		native:   provider,
		tools:    tools,
		outbound: outbound,
		inbound:  inbound,
		resolver: principal.NewResolver(
			cfg.Principals.Telegram.AdminUserIDs,
			cfg.Principals.Telegram.ApprovedUserIDs,
		),
		faceBackend: faceBackend,
		faceModel:   faceModel,
		faceModels:  faceModels,
		semantic: memory.NewSemanticEngine(memory.SemanticOptions{
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
		}),
		governorBackend:          governorAuth.Backend,
		streamEditInterval:       streamEditInterval,
		streamCursor:             streamCursor,
		toolProgressMode:         strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgress)),
		toolProgressStyle:        toolProgressStyle,
		toolProgressWindow:       toolProgressWindow,
		toolProgressCleanup:      cfg.Telegram.ToolProgressCleanup,
		idleExpiry:               idleExpiry,
		expireIdle:               store.ExpireIdle,
		staleTurnThreshold:       staleTurnThreshold,
		staleTurnLimit:           staleTurnLimit,
		staleTurnWatchdogEnabled: watchdogConfig.Enabled,
		interruptRunningTurnRuns: store.InterruptRunningTurnRuns,
		interruptStaleTurnRuns:   store.InterruptRunningTurnRunIDs,
		tailnetBackend:           tailnetBackend,
		modelProviderCache:       make(map[string]agent.Provider),
		recipePath:               recipePath,
		recipeState:              recipeState,
		scopeResolver:            scopeResolver,
		workExecutor: newWorkExecutorSelector(cfg.Work, []WorkExecutor{
			newCodexWorkExecutor(cfg.Work.Codex),
			nativeWorkExecutor{},
		}),
		durableGroupChild:      newSandboxDurableGroupChildExecutor(cfg, store),
		durableWakeChild:       newSandboxDurableWakeChildExecutor(cfg, store),
		durableWakeAdapters:    defaultDurableWakeIngressAdapters(),
		constitutionGate:       DefaultTurnConstitutionGate(),
		operationalAlerts:      make(map[string]operationalAlertState),
		operationalAlertClock:  time.Now,
		operationalAlertWindow: 10 * time.Minute,
		sessionLocks:           make(map[string]*sync.Mutex),
		activeTurnCancels:      make(map[int64]*activeTurnRun),
	}
	if rt.workExecutor != nil {
		if native, ok := rt.workExecutor.executors["native"].(nativeWorkExecutor); ok {
			native.runtime = rt
			rt.workExecutor.executors["native"] = native
		}
	}
	rt.staleTurnSweep = func(activityCutoff time.Time, limit int) ([]session.TurnRun, error) {
		unmatchedToolCutoff := time.Now().UTC().Add(-rt.unmatchedToolStaleThreshold())
		return store.StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff, unmatchedToolCutoff, limit)
	}
	rt.interactiveDMAssembler = newInteractiveDMTurnAssembler(rt)
	return rt, nil
}

func (r *Runtime) SetTurnAuditSink(sink func(TurnAudit)) {
	if r == nil {
		return
	}
	r.turnAuditSink = sink
}

func (r *Runtime) SetConstitutionGate(gate TurnConstitutionGate) {
	if r == nil {
		return
	}
	if gate == nil {
		r.constitutionGate = DefaultTurnConstitutionGate()
		return
	}
	r.constitutionGate = gate
}

func normalizeRuntimeConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	copy.Agent = cfg.Agent
	copy.Face = cfg.Face
	copy.Face.Backend = string(face.NormalizeBackend(cfg.Face.Backend))
	copy.Work.Executor = normalizeRuntimeWorkExecutor(cfg.Work.Executor)
	copy.Work.AutoOrder = normalizeRuntimeWorkExecutorList(cfg.Work.AutoOrder)
	if len(copy.Work.AutoOrder) == 0 {
		copy.Work.AutoOrder = []string{"native", "codex"}
	}
	copy.Work.Codex.AppServerAddress = strings.TrimSpace(cfg.Work.Codex.AppServerAddress)
	copy.Agent.PromptRoot = cfg.Agent.EffectivePromptRoot()
	copy.Agent.ExecRoot = cfg.Agent.EffectiveExecRoot()
	copy.Agent.SharedMemoryRoot = cfg.Agent.EffectiveSharedMemoryRoot()
	copy.Agent.UserWorkspaceRoot = cfg.Agent.EffectiveUserWorkspaceRoot()
	copy.Agent.UserMemoryRoot = cfg.Agent.EffectiveUserMemoryRoot()
	if strings.TrimSpace(copy.Agent.UserWorkspaceRoot) == "" || strings.TrimSpace(copy.Agent.UserMemoryRoot) == "" {
		stateRoot := filepath.Join(filepath.Dir(copy.Sessions.DBPath), "isolated")
		if strings.TrimSpace(copy.Agent.UserWorkspaceRoot) == "" {
			copy.Agent.UserWorkspaceRoot = filepath.Join(stateRoot, "workspaces")
		}
		if strings.TrimSpace(copy.Agent.UserMemoryRoot) == "" {
			copy.Agent.UserMemoryRoot = filepath.Join(stateRoot, "memory")
		}
	}
	return &copy
}

func (r *Runtime) governorName() string {
	if r == nil {
		return prompt.DefaultGovernorName
	}
	return config.EffectiveGovernorName(r.cfg, prompt.DefaultGovernorName)
}

func (r *Runtime) faceName() string {
	if r == nil {
		return face.DefaultFaceName
	}
	return config.EffectiveFaceName(r.cfg, face.DefaultFaceName)
}

func (r *Runtime) AgentFunc() core.AgentFunc {
	return func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		return r.HandleInbound(ctx, msg)
	}
}

func (r *Runtime) StartIdleExpiryLoop(ctx context.Context, logger func(string, ...any)) {
	if logger == nil {
		logger = log.Printf
	}
	cadence := idleExpirySweepCadence(r.idleExpiry)
	r.startIdleExpiryLoop(ctx, cadence, logger)
}

func (r *Runtime) startIdleExpiryLoop(ctx context.Context, cadence time.Duration, logger func(string, ...any)) {
	go runPeriodic(ctx, cadence, func(runCtx context.Context) {
		select {
		case <-runCtx.Done():
			return
		default:
		}

		expired, err := r.expireIdle(r.idleExpiry)
		if err != nil {
			logger("WARN idle expiry sweep failed: %v", err)
			r.reportOperationalIssue(runCtx, "idle_expiry", err)
			return
		}
		if expired > 0 {
			logger("INFO expired %d idle session(s)", expired)
		}
		removedAudio, cleanupErr := r.cleanupTemporaryAudioArtifacts(time.Now().UTC())
		if cleanupErr != nil {
			logger("WARN temporary audio cleanup failed: %v", cleanupErr)
			r.reportOperationalIssue(runCtx, "temporary_audio_cleanup", cleanupErr)
			return
		}
		if removedAudio > 0 {
			logger("INFO removed %d temporary audio artifact(s)", removedAudio)
		}
	})
}

func runPeriodic(ctx context.Context, cadence time.Duration, fn func(context.Context)) {
	if fn == nil {
		return
	}
	if cadence <= 0 {
		cadence = time.Minute
	}

	ticker := time.NewTicker(cadence)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}

func idleExpirySweepCadence(idleExpiry time.Duration) time.Duration {
	if idleExpiry <= 0 {
		return time.Minute
	}
	cadence := idleExpiry / 4
	if cadence < time.Minute {
		return time.Minute
	}
	if cadence > time.Hour {
		return time.Hour
	}
	return cadence
}

func (r *Runtime) startChatActionLoop(ctx context.Context, chatID int64, action string) func() {
	sender, ok := r.outbound.(chatActionSender)
	if !ok || chatID == 0 || strings.TrimSpace(action) == "" {
		return func() {}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	go func() {
		send := func() {
			if err := sender.SendChatAction(loopCtx, chatID, action); err != nil && loopCtx.Err() == nil {
				log.Printf("WARN telegram chat action failed chat_id=%d action=%s err=%v", chatID, action, err)
			}
		}

		send()

		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()

	return cancel
}
