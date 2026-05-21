//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type WorkMode string

const (
	WorkModeReadOnly       WorkMode = "read_only"
	WorkModeWorkspaceWrite WorkMode = "workspace_write"
	WorkModeCommit         WorkMode = "commit"
	WorkModeDeploy         WorkMode = "deploy"
)

type WorkRequest struct {
	OperationID string
	RepoRoot    string
	Workdir     string
	Prompt      string
	Mode        WorkMode
	LeaseID     string
	ThreadID    string
	Key         session.SessionKey
	ChatID      int64
	Actor       principal.Principal
	State       session.ContinuationState
	Operation   session.OperationState
}

type WorkResult struct {
	ExecutorName     string
	ThreadID         string
	TurnID           string
	Summary          string
	ProviderFailure  string
	ProviderEvents   []core.ProviderEvent
	ChangedFiles     []string
	Commands         []string
	CodexEvents      []session.WorkCodexEvent
	PatchPreview     string
	CommitLaneStatus string
	ApprovalLog      []codexAppServerApprovalDecision
	CompletionKind   string
	SideEffects      bool
}

type WorkAvailability struct {
	Available bool
	Reason    string
}

type WorkExecutor interface {
	Name() string
	Available(ctx context.Context, req WorkRequest) WorkAvailability
	Run(ctx context.Context, req WorkRequest) (WorkResult, error)
}

type WorkExecutorStatus struct {
	Configured     string
	Preferred      string
	Active         string
	LastAttempted  string
	FallbackReason string
	LastError      string
	UpdatedAt      time.Time
}

type WorkExecutorSelector struct {
	mu        sync.Mutex
	cfg       config.WorkConfig
	executors map[string]WorkExecutor
	status    WorkExecutorStatus
}

func newWorkExecutorSelector(cfg config.WorkConfig, executors []WorkExecutor) *WorkExecutorSelector {
	cfg.Executor = normalizeRuntimeWorkExecutor(cfg.Executor)
	cfg.AutoOrder = normalizeRuntimeWorkExecutorList(cfg.AutoOrder)
	if len(cfg.AutoOrder) == 0 {
		cfg.AutoOrder = []string{"native", "codex"}
	}
	byName := make(map[string]WorkExecutor, len(executors))
	for _, executor := range executors {
		if executor == nil {
			continue
		}
		name := normalizeRuntimeWorkExecutor(executor.Name())
		if name == "" || name == "auto" {
			continue
		}
		byName[name] = executor
	}
	return &WorkExecutorSelector{
		cfg:       cfg,
		executors: byName,
		status: WorkExecutorStatus{
			Configured: cfg.Executor,
			Preferred:  firstRuntimeWorkExecutor(cfg),
			UpdatedAt:  time.Now().UTC(),
		},
	}
}

func (s *WorkExecutorSelector) Status() WorkExecutorStatus {
	if s == nil {
		return WorkExecutorStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *WorkExecutorSelector) Run(ctx context.Context, req WorkRequest) (WorkResult, error) {
	if s == nil {
		return WorkResult{}, fmt.Errorf("work executor selector is unavailable")
	}
	candidates := s.candidates()
	if len(candidates) == 0 {
		return WorkResult{}, fmt.Errorf("work executor has no candidates")
	}
	strict := normalizeRuntimeWorkExecutor(s.cfg.Executor)
	if strict == "" {
		strict = "auto"
	}
	var fallbackReasons []string
	var lastErr error
	for _, name := range candidates {
		executor := s.executors[name]
		if executor == nil {
			reason := fmt.Sprintf("%s unavailable: executor not registered", name)
			fallbackReasons = append(fallbackReasons, reason)
			if strict != "auto" {
				s.updateStatus(name, "", strings.Join(fallbackReasons, "; "), reason)
				return WorkResult{}, errors.New(reason)
			}
			continue
		}
		availability := executor.Available(ctx, req)
		if !availability.Available {
			reason := fmt.Sprintf("%s unavailable: %s", name, firstRuntimeWorkNonEmpty(availability.Reason, "not ready"))
			fallbackReasons = append(fallbackReasons, reason)
			if strict != "auto" {
				s.updateStatus(name, "", strings.Join(fallbackReasons, "; "), reason)
				return WorkResult{}, errors.New(reason)
			}
			continue
		}
		s.updateStatus(name, name, strings.Join(fallbackReasons, "; "), "")
		result, err := executor.Run(ctx, req)
		if strings.TrimSpace(result.ExecutorName) == "" {
			result.ExecutorName = name
		}
		if err == nil {
			s.updateStatus(name, name, strings.Join(fallbackReasons, "; "), "")
			return result, nil
		}
		lastErr = err
		reason := fmt.Sprintf("%s failed before side effects: %v", name, err)
		if result.SideEffects {
			reason = fmt.Sprintf("%s failed after side effects: %v", name, err)
		}
		fallbackReasons = append(fallbackReasons, reason)
		s.updateStatus(name, name, strings.Join(fallbackReasons, "; "), err.Error())
		if strict != "auto" || result.SideEffects {
			return result, err
		}
	}
	reason := strings.Join(fallbackReasons, "; ")
	if reason == "" && lastErr != nil {
		reason = lastErr.Error()
	}
	if reason == "" {
		reason = "no work executor completed"
	}
	s.updateStatus("", "", reason, reason)
	return WorkResult{}, errors.New(reason)
}

func (s *WorkExecutorSelector) candidates() []string {
	if s == nil {
		return nil
	}
	mode := normalizeRuntimeWorkExecutor(s.cfg.Executor)
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" {
		return []string{mode}
	}
	order := normalizeRuntimeWorkExecutorList(s.cfg.AutoOrder)
	if len(order) == 0 {
		return []string{"native", "codex"}
	}
	return order
}

func (s *WorkExecutorSelector) updateStatus(attempted string, active string, fallbackReason string, lastErr string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = WorkExecutorStatus{
		Configured:     normalizeRuntimeWorkExecutor(s.cfg.Executor),
		Preferred:      firstRuntimeWorkExecutor(s.cfg),
		Active:         strings.TrimSpace(active),
		LastAttempted:  strings.TrimSpace(attempted),
		FallbackReason: strings.TrimSpace(fallbackReason),
		LastError:      strings.TrimSpace(lastErr),
		UpdatedAt:      time.Now().UTC(),
	}
}

type nativeWorkExecutor struct {
	runtime *Runtime
}

func (e nativeWorkExecutor) Name() string { return "native" }

func (e nativeWorkExecutor) Available(_ context.Context, _ WorkRequest) WorkAvailability {
	if e.runtime == nil {
		return WorkAvailability{Reason: "runtime unavailable"}
	}
	if e.runtime.provider == nil {
		return WorkAvailability{Reason: "provider unavailable"}
	}
	return WorkAvailability{Available: true}
}

func (e nativeWorkExecutor) Run(ctx context.Context, req WorkRequest) (WorkResult, error) {
	if e.runtime == nil {
		return WorkResult{}, fmt.Errorf("runtime unavailable")
	}
	key := req.Key
	if key.ChatID == 0 {
		key.ChatID = req.ChatID
	}
	if key.ChatID != 0 && strings.TrimSpace(string(key.Scope.Kind)) == "" && strings.TrimSpace(key.Scope.ID) == "" {
		key.Scope = telegramDMScopeRef(key.ChatID)
	}
	msg := continuationInboundForKey(key, req.Actor, approvedContinuationEventTextForState(req.State), core.InboundOriginTurnAuthorization, string(session.TurnAuthorizationKindContinuation))
	result, err := e.runtime.handleInternalContinuation(ctx, req.Actor, msg)
	out := WorkResult{ExecutorName: "native", CompletionKind: "native_turn"}
	if result != nil {
		out.Summary = strings.TrimSpace(result.Text)
		out.ProviderFailure = strings.TrimSpace(result.ProviderFailure)
		out.ProviderEvents = append([]core.ProviderEvent(nil), result.ProviderEvents...)
		if out.ProviderFailure != "" {
			out.CompletionKind = "native_turn_provider_failed"
			out.SideEffects = true
		}
	}
	if err != nil {
		return out, err
	}
	if out.ProviderFailure != "" {
		return out, nativeWorkProviderFailureError{Failure: out.ProviderFailure}
	}
	return out, nil
}

type nativeWorkProviderFailureError struct {
	Failure string
}

func (e nativeWorkProviderFailureError) Error() string {
	failure := strings.TrimSpace(e.Failure)
	if failure == "" {
		return "native work turn failed because the inference backend failed"
	}
	return "native work turn failed because the inference backend failed: " + failure
}

type codexWorkExecutor struct {
	address                  string
	check                    func(context.Context, string) error
	rpcTimeout               time.Duration
	firstNotificationTimeout time.Duration
}

const (
	codexWorkDefaultRPCOperationTimeout   = 20 * time.Second
	codexWorkDefaultFirstNotificationWait = 45 * time.Second
)

func newCodexWorkExecutor(cfg config.WorkCodexConfig) WorkExecutor {
	return codexWorkExecutor{address: strings.TrimSpace(cfg.AppServerAddress), rpcTimeout: codexWorkDefaultRPCOperationTimeout, firstNotificationTimeout: codexWorkDefaultFirstNotificationWait, check: func(ctx context.Context, address string) error {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return checkCodexWorkAppServerReady(checkCtx, address)
	}}
}

func checkCodexWorkAppServerReady(ctx context.Context, address string) error {
	if err := checkCodexAppServerHTTP(ctx, address, "/healthz"); err == nil {
		return nil
	} else {
		healthzErr := err
		if err := checkCodexAppServerHTTP(ctx, address, "/health"); err == nil {
			return nil
		} else {
			return fmt.Errorf("healthz failed: %v; health failed: %w", healthzErr, err)
		}
	}
}

func (e codexWorkExecutor) Name() string { return "codex" }

func (e codexWorkExecutor) Available(ctx context.Context, _ WorkRequest) WorkAvailability {
	if strings.TrimSpace(e.address) == "" {
		return WorkAvailability{Reason: "app-server address not configured"}
	}
	if e.check != nil {
		if err := e.check(ctx, e.address); err != nil {
			return WorkAvailability{Reason: err.Error()}
		}
	}
	return WorkAvailability{Available: true}
}

func (e codexWorkExecutor) Run(ctx context.Context, req WorkRequest) (WorkResult, error) {
	if strings.TrimSpace(e.address) == "" {
		return WorkResult{}, fmt.Errorf("codex app-server address not configured")
	}
	client := newCodexAppServerClient(e.address, codexWorkApprovalHandler(req))
	defer client.Close(websocket.StatusNormalClosure, "done")
	if err := e.withRPCTimeout(ctx, client.Connect); err != nil {
		return WorkResult{}, err
	}
	if err := e.withRPCTimeout(ctx, client.Initialize); err != nil {
		return WorkResult{}, err
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		var created string
		err := e.withRPCTimeout(ctx, func(callCtx context.Context) error {
			var createErr error
			created, createErr = client.ThreadStart(callCtx, codexWorkThreadStartParams(req))
			return createErr
		})
		if err != nil {
			return WorkResult{}, err
		}
		threadID = created
	} else if err := e.withRPCTimeout(ctx, func(callCtx context.Context) error {
		return client.ThreadResume(callCtx, threadID, codexWorkThreadResumeParams(req))
	}); err != nil {
		var created string
		createErr := e.withRPCTimeout(ctx, func(callCtx context.Context) error {
			var err error
			created, err = client.ThreadStart(callCtx, codexWorkThreadStartParams(req))
			return err
		})
		if createErr != nil {
			return WorkResult{}, fmt.Errorf("resume codex work thread %q: %w (new thread also failed: %v)", threadID, err, createErr)
		}
		threadID = created
	}
	var turnID string
	err := e.withRPCTimeout(ctx, func(callCtx context.Context) error {
		var turnErr error
		turnID, turnErr = client.TurnStart(callCtx, threadID, req.Prompt, codexWorkTurnStartParams(req))
		return turnErr
	})
	if err != nil {
		return WorkResult{}, err
	}
	result, err := client.StreamTurnWithOptions(ctx, threadID, turnID, codexAppServerStreamOptions{FirstNotificationTimeout: e.firstNotificationWait()})
	if err != nil {
		partial := codexWorkResultFromAppServer(req, threadID, turnID, codexAppServerResult{
			ThreadID:     threadID,
			TurnID:       turnID,
			ApprovalLog:  client.ApprovalLog(),
			CodexEvents:  client.WorkEvents(),
			PatchPreview: codexWorkPatchPreviewFromEvents(client.WorkEvents()),
		})
		return partial, err
	}
	return codexWorkResultFromAppServer(req, threadID, turnID, result), nil
}

func (e codexWorkExecutor) withRPCTimeout(ctx context.Context, call func(context.Context) error) error {
	if call == nil {
		return nil
	}
	timeout := e.rpcOperationTimeout()
	if timeout <= 0 {
		return call(ctx)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return call(callCtx)
}

func (e codexWorkExecutor) rpcOperationTimeout() time.Duration {
	if e.rpcTimeout > 0 {
		return e.rpcTimeout
	}
	return codexWorkDefaultRPCOperationTimeout
}

func (e codexWorkExecutor) firstNotificationWait() time.Duration {
	if e.firstNotificationTimeout > 0 {
		return e.firstNotificationTimeout
	}
	return codexWorkDefaultFirstNotificationWait
}

func normalizeRuntimeWorkExecutor(value string) string {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.ReplaceAll(name, "-", "_")
	switch name {
	case "", "auto":
		return "auto"
	case "codex", "native":
		return name
	default:
		return name
	}
}

func normalizeRuntimeWorkExecutorList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		name := normalizeRuntimeWorkExecutor(raw)
		if name == "" || name == "auto" {
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

func firstRuntimeWorkExecutor(cfg config.WorkConfig) string {
	mode := normalizeRuntimeWorkExecutor(cfg.Executor)
	if mode != "" && mode != "auto" {
		return mode
	}
	order := normalizeRuntimeWorkExecutorList(cfg.AutoOrder)
	if len(order) == 0 {
		return "native"
	}
	return order[0]
}

func firstRuntimeWorkNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
