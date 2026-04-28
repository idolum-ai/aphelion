//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
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

type telegramCommandControl struct {
	router                 *core.Router
	ingress                *ingressSequencer
	rt                     *runtime.Runtime
	resolver               *principal.Resolver
	decisionDetacher       pendingDecisionDetacher
	detachPendingOnRestart bool
	durableTools           durableWizardToolExecutor
	statusMiniAppPublicURL string
	statusMiniAppBotToken  string
}

type pendingDecisionDetacher interface {
	DetachByOwner(ctx context.Context, ownerKey string) (int, error)
	DetachAll(ctx context.Context) (int, error)
}

type durableWizardToolExecutor interface {
	ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error)
}

func newTurnContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func (c telegramCommandControl) Stop(chatID int64) core.StopResult {
	result := core.StopResult{}
	if c.router != nil {
		result = c.router.Stop(chatID)
	}
	if c.rt != nil {
		revoke, err := c.rt.RevokeContinuation(chatID)
		if err == nil {
			result.ContinuationRevoked = revoke.Revoked
		}
	}
	c.maybeFlushMemory(chatID, "stop")
	return result
}

func (c telegramCommandControl) MarkStreamControlStopping(streamID string, chatID int64) bool {
	if c.rt == nil {
		return false
	}
	return c.rt.MarkStreamControlStopping(streamID, chatID)
}

func (c telegramCommandControl) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	stopped := c.Stop(chatID)
	result := core.NewSessionResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.decisionDetacher != nil {
		ownerKey := decision.OwnerKey(chatID, senderID)
		removed, err := c.decisionDetacher.DetachByOwner(context.Background(), ownerKey)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.PendingDecisionsDetached = removed
	}
	if c.rt != nil {
		cleared, err := c.rt.ClearChatSessionContext(chatID)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.ContextCleared = cleared
	}
	return result, nil
}

func (c telegramCommandControl) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	stopped := c.Stop(chatID)
	result := core.DetachResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.decisionDetacher == nil {
		return result, nil
	}
	ownerKey := decision.OwnerKey(chatID, senderID)
	removed, err := c.decisionDetacher.DetachByOwner(context.Background(), ownerKey)
	if err != nil {
		return core.DetachResult{}, err
	}
	result.PendingDecisionsDetached = removed
	return result, nil
}

func (c telegramCommandControl) Restart(chatID int64) error {
	_ = c.Stop(chatID)
	if c.detachPendingOnRestart && c.decisionDetacher != nil {
		removed, err := c.decisionDetacher.DetachAll(context.Background())
		if err != nil {
			log.Printf("WARN restart detach pending decisions failed err=%v", err)
		} else if removed > 0 {
			log.Printf("WARN restart detached %d pending decision(s) before exit", removed)
		}
	}
	log.Printf("WARN restart requested via telegram chat_id=%d", chatID)
	go func() {
		time.Sleep(restartExitWait)
		processExit(exitCodeFailure)
	}()
	return nil
}

func (c telegramCommandControl) maybeFlushMemory(chatID int64, reason string) {
	if c.rt == nil {
		return
	}
	if err := c.rt.FlushChatMemory(context.Background(), chatID, reason); err != nil {
		log.Printf("WARN memory flush skipped chat_id=%d reason=%s err=%v", chatID, strings.TrimSpace(reason), err)
		c.rt.ReportOperationalIssue(context.Background(), "memory_flush", fmt.Errorf("chat_id=%d reason=%s: %w", chatID, strings.TrimSpace(reason), err))
	}
}

func (c telegramCommandControl) Status(chatID int64) core.SessionStatus {
	status := c.router.Status(chatID)
	if c.rt == nil {
		return status
	}
	diagnostics, err := c.rt.StatusDiagnostics(chatID)
	if err != nil {
		log.Printf("WARN telegram status diagnostics failed chat_id=%d err=%v", chatID, err)
		status.Diagnostics = append(status.Diagnostics, "Runtime diagnostics are temporarily unavailable.")
		return status
	}
	status.Diagnostics = append(status.Diagnostics, diagnostics...)
	return status
}

func (c telegramCommandControl) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.rt == nil {
		chat := core.ChatStatusSnapshot{
			GeneratedAt:   time.Now().UTC(),
			ChatID:        chatID,
			RestartHealth: core.RestartHealthSnapshot{},
		}
		if ids := routerSnapshot.ActiveTurnsByChat[chatID]; len(ids) > 0 {
			chat.ActiveTurnIDs = append(chat.ActiveTurnIDs, ids...)
		}
		chat.QueueDepth = routerSnapshot.QueueDepthByChat[chatID]
		return chat, nil
	}
	return c.rt.ChatStatusSnapshot(chatID, routerSnapshot)
}

func (c telegramCommandControl) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.SystemStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.rt == nil {
		return core.SystemStatusSnapshot{
			GeneratedAt:       time.Now().UTC(),
			ActiveTurnsByChat: routerSnapshot.ActiveTurnsByChat,
			QueueDepthByChat:  routerSnapshot.QueueDepthByChat,
		}, nil
	}
	return c.rt.SystemStatusSnapshot(routerSnapshot)
}

func (c telegramCommandControl) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.DurableAgentsStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	if c.rt == nil {
		return core.DurableAgentsStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
		}, nil
	}
	return c.rt.DurableAgentsStatusSnapshot()
}

func (c telegramCommandControl) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	if c.rt == nil {
		return ""
	}
	return c.rt.StatusReadableSummary(ctx, view, statusText)
}

func (c telegramCommandControl) StatusMiniAppURL(chatID int64, senderID int64) string {
	return buildTelegramStatusMiniAppURL(c.statusMiniAppPublicURL, c.statusMiniAppBotToken, chatID, senderID, time.Now().UTC())
}

func (c telegramCommandControl) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.TailnetStatusSnapshot{}, fmt.Errorf("tailnet status denied")
	}
	if c.rt == nil {
		return core.TailnetStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
			Enabled:     false,
			Backend:     "disabled",
			Status:      "disabled",
			Summary:     "Tailscale integration is disabled.",
		}, nil
	}
	return c.rt.TailnetStatusSnapshot(ctx)
}

func (c telegramCommandControl) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("tailnet surfaces denied")
	}
	if c.rt == nil {
		return nil, nil
	}
	return c.rt.TailnetSurfacesSnapshot()
}

func (c telegramCommandControl) ContinuationState(chatID int64) (session.ContinuationState, error) {
	if c.rt == nil {
		return session.ContinuationState{}, nil
	}
	return c.rt.ContinuationState(chatID)
}

func (c telegramCommandControl) CanRestart(senderID int64) bool {
	if c.rt != nil {
		return c.rt.IsTelegramAdmin(senderID)
	}
	if c.resolver == nil {
		return false
	}
	actor, ok := c.resolver.ResolveTelegramUser(senderID)
	return ok && actor.Role == principal.RoleAdmin
}

func (c telegramCommandControl) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	return c.rt.ApproveContinuation(chatID, approverID)
}

func (c telegramCommandControl) StopContinuation(chatID int64) (core.StopResult, error) {
	revoke, err := c.rt.RevokeContinuation(chatID)
	if err != nil {
		return core.StopResult{}, err
	}
	return core.StopResult{ContinuationRevoked: revoke.Revoked}, nil
}

func (c telegramCommandControl) TriggerContinuation(ctx context.Context, chatID int64) error {
	_ = ctx
	if c.rt == nil {
		return nil
	}
	go func() {
		triggerCtx, cancel := newTurnContext(context.Background(), turnTimeout)
		defer cancel()
		if err := c.rt.TriggerContinuation(triggerCtx, chatID); err != nil {
			log.Printf("WARN trigger continuation failed chat_id=%d err=%v", chatID, err)
		}
	}()
	return nil
}

func (c telegramCommandControl) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	if c.router == nil {
		return fmt.Errorf("router is not configured")
	}
	queued := msg
	queued.Text = reinstallTemplateMessage
	queued.Raw = nil
	if c.ingress != nil {
		c.ingress.Enqueue(ctx, queued)
		return nil
	}
	turnCtx, cancel := newTurnContext(ctx, turnTimeout)
	c.router.Route(turnCtx, queued)
	cancel()
	return nil
}

func (c telegramCommandControl) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	if c.rt == nil {
		return fmt.Errorf("runtime is not configured")
	}
	return c.rt.StartDoctor(ctx, msg)
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

func (c telegramCommandControl) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if c.rt == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ModelSlotStatuses()
}

func (c telegramCommandControl) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	if c.rt == nil {
		validation := core.ValidateModelSlotConfig(cfg, core.ModelSlotUsesTools(cfg.Slot))
		validation.Valid = false
		validation.Error = "runtime is not configured"
		return validation
	}
	return c.rt.ValidateModelSlotConfig(cfg)
}

func (c telegramCommandControl) SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string, ttl time.Duration) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.SetModelSlotOverride(cfg, actor, reason, ttl)
}

func (c telegramCommandControl) RollbackModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.RollbackModelSlot(slot, actor, reason)
}

func (c telegramCommandControl) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ClearModelSlot(slot, actor, reason)
}

func (c telegramCommandControl) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	if c.rt == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ModelSlotHistory(slot, limit)
}

func (c telegramCommandControl) RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable wizard controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable wizard controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable wizard controls are admin only")
		}
		actor = resolved
	}

	request := map[string]any{
		"action":   strings.TrimSpace(action),
		"agent_id": strings.TrimSpace(agentID),
	}
	if len(wizardAnswers) > 0 {
		request["wizard_answers"] = wizardAnswers
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	return c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload)
}

func (c telegramCommandControl) DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("durable-agent controls are admin only")
	}
	if c.rt == nil {
		return nil, nil
	}
	snapshot, err := c.rt.DurableAgentsStatusSnapshot()
	if err != nil {
		return nil, err
	}
	return append([]core.DurableAgentStatusSnapshot(nil), snapshot.Agents...), nil
}

func (c telegramCommandControl) StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable-agent controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable-agent controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable-agent controls are admin only")
		}
		actor = resolved
	}

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("durable agent id is required")
	}
	request := map[string]any{
		"action":   "conversation_send",
		"agent_id": agentID,
		"message":  "Scheduled parent-child check-in from /agents. Share current status, blockers, and concrete next actions.",
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	if _, err := c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload); err != nil {
		return "", err
	}

	wakeStatus := "queued for next child wake"
	if c.rt != nil {
		go func(agent string) {
			wakeCtx, cancel := newTurnContext(context.Background(), turnTimeout)
			defer cancel()
			if err := c.rt.RunDurableAgentChildWake(wakeCtx, agent, time.Now().UTC()); err != nil {
				log.Printf("WARN durable-agent background wake failed agent_id=%s err=%v", strings.TrimSpace(agent), err)
			}
		}(agentID)
		wakeStatus = "wake attempt started"
	}
	return fmt.Sprintf("Started background conversation with durable agent %s (%s).", agentID, wakeStatus), nil
}

func (c telegramCommandControl) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error) {
	if c.rt == nil {
		return memoryReviewSnapshot{
			GeneratedAt: time.Now().UTC(),
			Source:      core.NormalizeMemoryReviewSource(string(source)),
			Query:       "",
		}, nil
	}
	return c.rt.MemoryReviewSnapshot(ctx, chatID, senderID, core.NormalizeMemoryReviewSource(string(source)))
}

func (c telegramCommandControl) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if c.rt == nil {
		return core.MemoryFocus{}, false
	}
	return c.rt.MemoryFocus(chatID)
}

func (c telegramCommandControl) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if c.rt == nil {
		return
	}
	c.rt.SetMemoryFocus(chatID, focus)
}

func (c telegramCommandControl) ClearMemoryFocus(chatID int64) bool {
	if c.rt == nil {
		return false
	}
	return c.rt.ClearMemoryFocus(chatID)
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
	if extra, ok := firstPositionalArg(flags.Args()); ok {
		return fmt.Errorf("unknown command %q (known maintenance commands: init|paths|gc|forget|reset|import-audit|import-semantic|verify-deploy|durable-agent|version)", extra)
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
	if err := syncDefaultDailyReviewDurableAgent(cfg, store); err != nil {
		return err
	}
	if err := syncDurableAgentBootstrapInheritance(cfg, store); err != nil {
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

	tgOutbound := newTelegramUIClient(tgClient)

	rt, err := runtime.New(cfg, store, llm, tools, tgOutbound)
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
	router.SetEventHandler(rt.RouterEventHandler())
	ingress := newIngressSequencer(router, turnTimeout)
	decisionBroker := newTelegramDecisionBroker(
		tgOutbound,
		decision.WithDurableStore(newTelegramDecisionDurableStore(store)),
		decision.WithObserver(rt.DecisionEventObserver()),
	)
	commandControl := telegramCommandControl{
		router:                 router,
		ingress:                ingress,
		rt:                     rt,
		resolver:               principalResolver,
		decisionDetacher:       decisionBroker,
		detachPendingOnRestart: cfg.Telegram.DetachPendingOnRestart,
		durableTools:           tools,
		statusMiniAppPublicURL: telegramMiniAppPublicURL(cfg),
		statusMiniAppBotToken:  cfg.Telegram.BotToken,
	}
	tailnetParent, err := tailnetParentService(cfg, commandControl)
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
	decisionHandler := newTelegramDecisionHandler(tgOutbound, router, decisionBroker, store)
	tools.WithExecApprover(newTelegramExecApprover(tgOutbound, decisionBroker))
	tools.WithDurableMemoryDelegationApprover(newTelegramDurableMemoryDelegationApprover(tgOutbound, decisionBroker))
	tools.WithDurableSnapshotRestoreApprover(newTelegramDurableSnapshotRestoreApprover(tgOutbound, decisionBroker))

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
	if err := startTelegramMiniApp(ctx, telegramMiniAppServer(cfg, commandControl)); err != nil {
		return err
	}
	if err := startTailnetParent(ctx, tailnetParent); err != nil {
		return err
	}
	rt.StartStartupRecovery(ctx, log.Printf)
	rt.StartIdleExpiryLoop(ctx, log.Printf)
	rt.SetStaleTurnWatchdogHook(func(runs []session.TurnRun) {
		log.Printf("WARN stale turn watchdog requesting process restart after interrupting %d run(s)", len(runs))
		go func() {
			time.Sleep(restartExitWait)
			processExit(exitCodeFailure)
		}()
	})
	rt.StartStaleTurnWatchdogLoop(ctx, log.Printf)
	rt.StartHeartbeatLoop(ctx, log.Printf)
	rt.StartDurableWakeLoop(ctx, log.Printf)
	rt.StartCronLoop(ctx, log.Printf)

	poller := telegram.NewPoller(tgClient, func(parent context.Context, msg core.InboundMessage) error {
		msg = rewriteDurableWizardIntent(msg, commandControl)
		msg = rewriteDurableRelayIntent(msg)
		msg = rewriteMemoryFocusInbound(msg, commandControl)
		handled, err := handleTelegramCommand(parent, tgOutbound, commandControl, msg)
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
		if retentionHandled, retentionErr := decisionHandler.HandleArtifactRetentionMessage(parent, msg); retentionErr != nil {
			return retentionErr
		} else if retentionHandled {
			return nil
		}

		ingress.Enqueue(parent, msg)
		return nil
	},
		telegram.WithPollerTimeout(cfg.Telegram.PollTimeout),
		telegram.WithMediaConfig(cfg.Telegram.Media),
		telegram.WithPrincipalResolver(principalResolver),
		telegram.WithDurableGroups(cfg.Telegram.DurableGroups),
		telegram.WithUnresolvedPrivatePredicate(shouldAllowUnresolvedPrivateDurableRelayMessage),
		telegram.WithBotIdentity(botUser),
		telegram.WithCallbackHandler(func(parent context.Context, cb telegram.CallbackQuery) error {
			if handled, err := handleTelegramCommandCallback(parent, tgOutbound, commandControl, cb); err != nil {
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

func shouldAllowUnresolvedPrivateDurableRelayMessage(msg *telegram.Message) bool {
	if msg == nil {
		return false
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.Caption)
	}
	_, _, ok := parseDurableRelayIntent(text)
	return ok
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
	entries := make([]provider.NamedProvider, 0, len(names)+len(cfg.Providers.OpenAI.FallbackModels))
	required := nativeProviderRequired(cfg)
	for idx, name := range names {
		if !isConfiguredProvider(name, cfg) {
			if idx == 0 && required {
				return nil, fmt.Errorf("native provider %q is enabled but not configured", name)
			}
			continue
		}
		built, err := buildNamedProviderEntries(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		entries = append(entries, built...)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return provider.NewFailoverChain(entries)
}

func buildNamedProviderEntries(name string, cfg *config.Config, httpClient *http.Client) ([]provider.NamedProvider, error) {
	if strings.EqualFold(strings.TrimSpace(name), "openai") {
		models := openAIModelChain(cfg.Providers.OpenAI)
		entries := make([]provider.NamedProvider, 0, len(models))
		for _, model := range models {
			p, err := provider.NewOpenAI(provider.OpenAIOptions{
				APIKey:     cfg.Providers.OpenAI.APIKey,
				BaseURL:    cfg.Providers.OpenAI.BaseURL,
				Model:      model,
				MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
				HTTPClient: httpClient,
				UserAgent:  cfg.Identity.UserAgent,
			})
			if err != nil {
				return nil, err
			}
			entries = append(entries, provider.NamedProvider{Name: "openai:" + model, Provider: p})
		}
		return entries, nil
	}
	p, err := buildNamedProvider(name, cfg, httpClient)
	if err != nil || p == nil {
		return nil, err
	}
	return []provider.NamedProvider{{Name: strings.ToLower(strings.TrimSpace(name)), Provider: p}}, nil
}

func openAIModelChain(cfg config.OpenAIProviderConfig) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(cfg.FallbackModels))
	for _, raw := range append([]string{cfg.Model}, cfg.FallbackModels...) {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
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
	case "openai":
		return provider.NewOpenAI(provider.OpenAIOptions{
			APIKey:     cfg.Providers.OpenAI.APIKey,
			BaseURL:    cfg.Providers.OpenAI.BaseURL,
			Model:      cfg.Providers.OpenAI.Model,
			MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
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
	return config.EffectiveProviderChain(cfg)
}

func resolveNativeProviderName(cfg *config.Config) string {
	return config.EffectiveNativeProvider(cfg)
}

func activeNativeModel(cfg *config.Config) string {
	switch resolveNativeProviderName(cfg) {
	case "openai":
		return cfg.Providers.OpenAI.Model
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
	case "openai":
		return strings.TrimSpace(cfg.Providers.OpenAI.APIKey) != ""
	case "openrouter":
		return strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) != ""
	default:
		return false
	}
}

func firstPositionalArg(args []string) (string, bool) {
	for _, raw := range args {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		return trimmed, true
	}
	return "", false
}

func exitCode(err error) int {
	var cfgErr *configStartupError
	if errors.As(err, &cfgErr) {
		return exitCodeConfig
	}
	return exitCodeFailure
}
