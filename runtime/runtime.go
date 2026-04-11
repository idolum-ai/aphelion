//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/voice"
)

type OutboundSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
}

type chatActionSender interface {
	SendChatAction(ctx context.Context, chatID int64, action string) error
}

type Runtime struct {
	cfg      *config.Config
	store    *session.SQLiteStore
	provider agent.Provider
	native   agent.Provider
	tools    agent.ToolRegistry
	outbound OutboundSender
	resolver *principal.Resolver

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

	scopeResolver *sandbox.Resolver
	sessionMu     sync.Mutex
	sessionLocks  map[string]*sync.Mutex
	faceModelsMu  sync.Mutex
	recipeMu      sync.Mutex
	recipeFileMu  sync.Mutex
	recipePath    string
	recipeState   runtimeRecipeState
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

var newCodexProvider = func(bundle governorauth.Bundle, cfg *config.Config) (agent.Provider, error) {
	var loadTokens func() (governorauth.CodexTokens, error)
	var saveTokens func(governorauth.CodexTokens, time.Time) error
	if strings.TrimSpace(bundle.AuthPath) != "" {
		authPath := bundle.AuthPath
		loadTokens = func() (governorauth.CodexTokens, error) {
			return governorauth.LoadCodexCLIAuth(authPath)
		}
		saveTokens = func(tokens governorauth.CodexTokens, refreshedAt time.Time) error {
			return governorauth.SaveCodexCLIAuth(authPath, tokens, refreshedAt)
		}
	}
	return governorbackend.NewCodex(governorbackend.CodexOptions{
		BaseURL:      bundle.BaseURL,
		AccessToken:  bundle.AccessToken,
		RefreshToken: bundle.RefreshToken,
		AccountID:    bundle.AccountID,
		RefreshURL:   bundle.RefreshURL,
		HTTPClient:   &http.Client{Timeout: 90 * time.Second},
		UserAgent:    cfg.Identity.UserAgent,
		LoadTokens:   loadTokens,
		SaveTokens:   saveTokens,
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

	faceBackend := face.Backend(strings.ToLower(strings.TrimSpace(cfg.Face.Backend)))
	if faceBackend == "" {
		faceBackend = face.BackendProvider
	}

	if provider == nil && (governorAuth.Backend == governorauth.BackendNative || faceBackend == face.BackendProvider) {
		return nil, fmt.Errorf("native provider is required for configured governor/face backends")
	}

	activeProvider := provider
	if governorAuth.Backend == governorauth.BackendCodex {
		codexProvider, err := newCodexProvider(governorAuth, cfg)
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

	var faceProvider agent.Provider
	switch faceBackend {
	case face.BackendProvider:
		faceProvider = provider
	case face.BackendGovernorPassthrough:
		faceProvider = activeProvider
	default:
		return nil, fmt.Errorf("unsupported face backend: %q", cfg.Face.Backend)
	}

	faceModel, err := newFaceRenderer(faceProvider, face.ProviderRendererConfig{
		GovernorName:  prompt.DefaultGovernorName,
		FaceName:      face.DefaultFaceName,
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
	scopeResolver, err := sandbox.NewResolver(sandboxRoots, sandbox.DefaultProfiles())
	if err != nil {
		return nil, fmt.Errorf("init sandbox scope resolver: %w", err)
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
	recipePath := recipeStatePath(cfg)
	recipeState, err := loadRuntimeRecipeState(recipePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("load runtime recipe state: %w", err)
	}

	faceModels := map[string]face.Renderer{}
	if recipeState.PersonaEffort == defaultRuntimeRecipeState(cfg).PersonaEffort {
		faceModels[recipeState.PersonaEffort] = faceModel
	}

	return &Runtime{
		cfg:      cfg,
		store:    store,
		provider: activeProvider,
		native:   provider,
		tools:    tools,
		outbound: outbound,
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
		governorBackend:     governorAuth.Backend,
		streamEditInterval:  streamEditInterval,
		streamCursor:        streamCursor,
		toolProgressMode:    strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgress)),
		toolProgressStyle:   toolProgressStyle,
		toolProgressWindow:  toolProgressWindow,
		toolProgressCleanup: cfg.Telegram.ToolProgressCleanup,
		idleExpiry:          idleExpiry,
		expireIdle:          store.ExpireIdle,
		recipePath:          recipePath,
		recipeState:         recipeState,
		scopeResolver:       scopeResolver,
		sessionLocks:        make(map[string]*sync.Mutex),
	}, nil
}

func normalizeRuntimeConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	copy := *cfg
	copy.Agent = cfg.Agent
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
	if strings.TrimSpace(copy.Agent.Workspace) == "" {
		copy.Agent.Workspace = copy.Agent.ExecRoot
	}
	return &copy
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
			return
		}
		if expired > 0 {
			logger("INFO expired %d idle session(s)", expired)
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
