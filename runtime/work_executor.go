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
		cfg.AutoOrder = []string{"codex", "native"}
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
		return []string{"codex", "native"}
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
	result, err := e.runtime.handleInternalContinuation(ctx, req.Actor, core.InboundMessage{
		ChatID:       req.ChatID,
		SenderID:     req.Actor.TelegramUserID,
		SenderName:   actorLabel(req.Actor),
		Text:         approvedContinuationEventTextForState(req.State),
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	out := WorkResult{ExecutorName: "native", CompletionKind: "native_turn"}
	if result != nil {
		out.Summary = strings.TrimSpace(result.Text)
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

type codexWorkExecutor struct {
	address string
	check   func(context.Context, string) error
}

func newCodexWorkExecutor(cfg config.WorkCodexConfig) WorkExecutor {
	return codexWorkExecutor{address: strings.TrimSpace(cfg.AppServerAddress), check: func(ctx context.Context, address string) error {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return checkCodexAppServerHTTP(checkCtx, address, "/health")
	}}
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
	if err := client.Connect(ctx); err != nil {
		return WorkResult{}, err
	}
	if err := client.Initialize(ctx); err != nil {
		return WorkResult{}, err
	}
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		created, err := client.ThreadStart(ctx, codexWorkThreadStartParams(req))
		if err != nil {
			return WorkResult{}, err
		}
		threadID = created
	} else if err := client.ThreadResume(ctx, threadID, codexWorkThreadResumeParams(req)); err != nil {
		created, createErr := client.ThreadStart(ctx, codexWorkThreadStartParams(req))
		if createErr != nil {
			return WorkResult{}, fmt.Errorf("resume codex work thread %q: %w (new thread also failed: %v)", threadID, err, createErr)
		}
		threadID = created
	}
	turnID, err := client.TurnStart(ctx, threadID, req.Prompt, codexWorkTurnStartParams(req))
	if err != nil {
		return WorkResult{}, err
	}
	result, err := client.StreamTurn(ctx, threadID, turnID)
	if err != nil {
		partial := codexWorkResultFromAppServer(req, threadID, turnID, codexAppServerResult{
			ThreadID:     threadID,
			TurnID:       turnID,
			ApprovalLog:  client.ApprovalLog(),
			CodexEvents:  client.WorkEvents(),
			PatchPreview: codexWorkPatchPreviewFromEvents(client.WorkEvents()),
		})
		partial.SideEffects = partial.SideEffects || len(client.ApprovalLog()) > 0
		return partial, err
	}
	return codexWorkResultFromAppServer(req, threadID, turnID, result), nil
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
		return "codex"
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
