//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/commandeffect"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	runtimecodex "github.com/idolum-ai/aphelion/runtime/codex"
	runtimecontinuation "github.com/idolum-ai/aphelion/runtime/continuation"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

type WorkMode = runtimecontinuation.WorkMode

const (
	WorkModeReadOnly       = runtimecontinuation.WorkModeReadOnly
	WorkModeWorkspaceWrite = runtimecontinuation.WorkModeWorkspaceWrite
	WorkModeCommit         = runtimecontinuation.WorkModeCommit
	WorkModeDeploy         = runtimecontinuation.WorkModeDeploy
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
	TurnRunID        int64
	ThreadID         string
	TurnID           string
	Summary          string
	RecoveryKind     string
	RecoverySummary  string
	RecoveryDelivery string
	ProviderFailure  string
	ProviderEvents   []core.ProviderEvent
	Recovery         *core.TurnRecovery
	ChangedFiles     []string
	Commands         []string
	CodexEvents      []session.WorkCodexEvent
	PatchPreview     string
	CommitLaneStatus string
	ApprovalLog      []runtimecodex.ApprovalDecision
	CompletionKind   string
	SideEffects      bool
	ToolSuccesses    int
	ToolFailures     int
	ToolFailure      string
	ToolFailureTexts []string
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
	ctx = toolpkg.WithContinuationExecAuthority(ctx, req.State)
	if admission, ok := workRequestAuthorityAdmission(req, key, time.Now().UTC()); ok {
		ctx = toolpkg.WithExecutionAuthorityAdmission(ctx, admission)
	}
	msg := continuationInboundForKey(key, req.Actor, approvedContinuationEventTextForState(req.State), core.InboundOriginTurnAuthorization, string(session.TurnAuthorizationKindContinuation))
	result, err := e.runtime.handleInternalContinuationTurnWithOptions(ctx, req.Actor, msg, internalContinuationOptions{})
	out := WorkResult{ExecutorName: "native", CompletionKind: "native_turn"}
	if result != nil {
		out.TurnRunID = result.RunID
		out.RecoveryDelivery = strings.TrimSpace(result.Delivery.Kind)
		if result.Turn != nil {
			out.Summary = strings.TrimSpace(result.Turn.Text)
			out.ProviderFailure = strings.TrimSpace(result.Turn.ProviderFailure)
			out.ProviderEvents = append([]core.ProviderEvent(nil), result.Turn.ProviderEvents...)
		}
		if recovery, ok := nativeWorkTurnRecovery(result.Turn); ok {
			recoveryCopy := *recovery
			out.Recovery = &recoveryCopy
			out.RecoveryKind = strings.TrimSpace(string(recovery.Kind))
			out.RecoverySummary = strings.TrimSpace(recovery.Summary)
			out.CompletionKind = "native_turn_budget_recovery"
			out.SideEffects = true
			switch out.RecoveryDelivery {
			case "budget_recovery_scheduled":
				out.CompletionKind = "native_turn_budget_recovery_scheduled"
			case "budget_recovery_blocked":
				out.CompletionKind = "native_turn_budget_recovery_blocked"
			case "budget_recovery_deferred_to_work_retry":
				out.CompletionKind = "native_turn_budget_recovery_deferred"
			}
		}
		if out.ProviderFailure != "" {
			out.CompletionKind = "native_turn_provider_failed"
			out.SideEffects = true
		}
	}
	e.runtime.attachNativeWorkTurnEvidence(key, req, &out)
	if err != nil {
		return out, err
	}
	if out.ProviderFailure != "" {
		return out, nativeWorkProviderFailureError{Failure: out.ProviderFailure}
	}
	if workResultBudgetRecoveryScheduled(out) || workResultBudgetRecoveryBlocked(out) {
		return out, nil
	}
	if out.RecoveryKind != "" {
		return out, nativeWorkRecoveryError{Kind: out.RecoveryKind, Summary: out.RecoverySummary}
	}
	return out, nil
}

func workRequestAuthorityAdmission(req WorkRequest, key session.SessionKey, now time.Time) (session.ExecutionRunAuthority, bool) {
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return session.ExecutionRunAuthority{}, false
	}
	sessionID := session.SessionIDForKey(key)

	lease := session.NormalizeContinuationLease(req.State.ContinuationLease)
	if strings.TrimSpace(lease.ID) == "" {
		lease.ID = strings.TrimSpace(req.LeaseID)
	}
	continuationActive := strings.TrimSpace(lease.ID) != "" && lease.ActiveAt(now)

	planLease := session.NormalizeOperationPlanLease(req.Operation.PlanLease)
	planActive := workRequestOperationPlanLeaseUsable(planLease, now)
	if continuationActive == planActive {
		return session.ExecutionRunAuthority{}, false
	}
	record := session.ExecutionRunAuthority{
		SessionID:        sessionID,
		ChatID:           key.ChatID,
		UserID:           key.UserID,
		Scope:            key.Scope,
		Principal:        runtimeExecutionPrincipalID(req.Actor),
		PrincipalRole:    string(req.Actor.Role),
		ExecutionSpecies: "native_continuation",
		AdmittedAt:       now.UTC(),
	}
	if continuationActive {
		record.LeaseKind = session.ExecutionAuthorityLeaseKindContinuation
		record.ContinuationLeaseID = lease.ID
		record.LeaseStatus = string(lease.Status)
		record.LeaseRemainingTurns = lease.RemainingTurns
		record.LeaseExpiresAt = lease.ExpiresAt
		return session.NormalizeExecutionRunAuthority(record), true
	}
	record.ExecutionSpecies = "operation_plan_continuation"
	record.LeaseKind = session.ExecutionAuthorityLeaseKindOperationPlan
	record.OperationPlanLeaseID = planLease.ID
	record.LeaseStatus = string(planLease.Status)
	record.LeaseRemainingTurns = planLease.RemainingTurns
	record.LeaseExpiresAt = planLease.ExpiresAt
	return session.NormalizeExecutionRunAuthority(record), true
}

func workRequestOperationPlanLeaseUsable(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if strings.TrimSpace(lease.ID) == "" || lease.RemainingTurns <= 0 {
		return false
	}
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive:
		return true
	default:
		return false
	}
}

func runtimeExecutionPrincipalID(actor principal.Principal) string {
	switch actor.Role {
	case principal.RoleDurableAgent:
		if id := strings.TrimSpace(actor.DurableAgentID); id != "" {
			return "durable_agent:" + id
		}
	case principal.RoleAdmin, principal.RoleApprovedUser:
		if actor.TelegramUserID > 0 {
			return fmt.Sprintf("telegram:%d", actor.TelegramUserID)
		}
		if actor.Role == principal.RoleAdmin {
			return "admin"
		}
	}
	if actor.TelegramUserID > 0 {
		return fmt.Sprintf("telegram:%d", actor.TelegramUserID)
	}
	return strings.TrimSpace(string(actor.Role))
}

func workResultBudgetRecoveryScheduled(result WorkResult) bool {
	return strings.TrimSpace(result.CompletionKind) == "native_turn_budget_recovery_scheduled"
}

func workResultBudgetRecoveryBlocked(result WorkResult) bool {
	return strings.TrimSpace(result.CompletionKind) == "native_turn_budget_recovery_blocked"
}

func nativeWorkResultFromTurnResult(result *core.TurnResult) WorkResult {
	out := WorkResult{ExecutorName: "native", CompletionKind: "native_turn"}
	if result == nil {
		return out
	}
	out.Summary = strings.TrimSpace(result.Text)
	out.ProviderFailure = strings.TrimSpace(result.ProviderFailure)
	out.ProviderEvents = append([]core.ProviderEvent(nil), result.ProviderEvents...)
	if recovery, ok := nativeWorkTurnRecovery(result); ok {
		recoveryCopy := *recovery
		out.Recovery = &recoveryCopy
		out.RecoveryKind = strings.TrimSpace(string(recovery.Kind))
		out.RecoverySummary = strings.TrimSpace(recovery.Summary)
		out.CompletionKind = "native_turn_budget_recovery"
		out.SideEffects = true
	}
	if out.ProviderFailure != "" {
		out.CompletionKind = "native_turn_provider_failed"
		out.SideEffects = true
	}
	return out
}

func (r *Runtime) attachNativeWorkTurnEvidence(key session.SessionKey, req WorkRequest, result *WorkResult) {
	if r == nil || r.store == nil || result == nil || result.TurnRunID <= 0 {
		return
	}
	r.attachEffectAttemptsToWorkResult(key, req, result)
	if run, err := r.store.TurnRun(result.TurnRunID); err == nil && run != nil {
		if failure := strings.TrimSpace(run.LastToolError); failure != "" {
			result.ToolFailureTexts = appendUniqueRuntimeWorkString(result.ToolFailureTexts, failure)
			if strings.TrimSpace(result.ToolFailure) == "" {
				result.ToolFailure = failure
			}
		}
	}
	events, err := r.store.ExecutionEventsByTurnRun(key, result.TurnRunID, 500)
	if err != nil {
		return
	}
	startedExecPreviews := map[string][]string{}
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventToolStarted {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if !strings.EqualFold(workPayloadString(payload, "tool"), "exec") {
			continue
		}
		if preview := workPayloadString(payload, "preview"); preview != "" {
			eventKey := workToolEventKey(payload)
			startedExecPreviews[eventKey] = append(startedExecPreviews[eventKey], preview)
		}
	}
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventToolSucceeded:
			result.ToolSuccesses++
			if execEffectPayloadHasSideEffects(event) {
				result.SideEffects = true
			}
			if cmd := successfulExecCommandFromToolEvent(event, startedExecPreviews); cmd != "" {
				result.Commands = appendUniqueRuntimeWorkString(result.Commands, cmd)
				if commandeffect.Classify(cmd).SideEffects {
					result.SideEffects = true
				}
			}
		case core.ExecutionEventToolFailed:
			result.ToolFailures++
			discardStartedExecPreviewForToolEvent(event, startedExecPreviews)
			failure := toolFailureSummaryFromEvent(event)
			if failure == "" {
				continue
			}
			result.ToolFailureTexts = appendUniqueRuntimeWorkString(result.ToolFailureTexts, failure)
			if strings.TrimSpace(result.ToolFailure) == "" ||
				(!workResultFailureTextInvalidatesMaterialCompletion(result.ToolFailure) &&
					workResultFailureTextInvalidatesMaterialCompletion(failure)) {
				result.ToolFailure = failure
			}
		}
	}
}

func successfulExecCommandFromToolEvent(event session.ExecutionEvent, startedExecPreviews map[string][]string) string {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return ""
	}
	if !strings.EqualFold(workPayloadString(payload, "tool"), "exec") {
		return ""
	}
	if cmd := execEffectPayloadCommand(payload); cmd != "" {
		_ = popStartedExecPreview(payload, startedExecPreviews)
		return cmd
	}
	preview := workPayloadString(payload, "preview")
	if fallback := popStartedExecPreview(payload, startedExecPreviews); preview == "" {
		preview = fallback
	}
	if preview == "" {
		return ""
	}
	input := map[string]any{}
	if err := json.Unmarshal([]byte(preview), &input); err != nil {
		return ""
	}
	return firstRuntimeWorkNonEmpty(workPayloadString(input, "cmd"), workPayloadString(input, "command"))
}

func execEffectPayloadHasSideEffects(event session.ExecutionEvent) bool {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return false
	}
	effect := execEffectPayloadMap(payload)
	if len(effect) == 0 {
		return false
	}
	return workPayloadBool(effect, "side_effects")
}

func execEffectPayloadCommand(payload map[string]any) string {
	effect := execEffectPayloadMap(payload)
	if len(effect) == 0 {
		return ""
	}
	return workPayloadString(effect, "command")
}

func execEffectPayloadMap(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	raw := payload["exec_effect"]
	if raw == nil {
		return nil
	}
	if effect, ok := raw.(map[string]any); ok {
		return effect
	}
	if encoded, ok := raw.(string); ok && strings.TrimSpace(encoded) != "" {
		effect := map[string]any{}
		if err := json.Unmarshal([]byte(encoded), &effect); err == nil {
			return effect
		}
	}
	return nil
}

func discardStartedExecPreviewForToolEvent(event session.ExecutionEvent, startedExecPreviews map[string][]string) {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return
	}
	if !strings.EqualFold(workPayloadString(payload, "tool"), "exec") {
		return
	}
	_ = popStartedExecPreview(payload, startedExecPreviews)
}

func popStartedExecPreview(payload map[string]any, startedExecPreviews map[string][]string) string {
	if len(startedExecPreviews) == 0 {
		return ""
	}
	eventKey := workToolEventKey(payload)
	previews := startedExecPreviews[eventKey]
	if len(previews) == 0 {
		return ""
	}
	preview := previews[0]
	if len(previews) == 1 {
		delete(startedExecPreviews, eventKey)
	} else {
		startedExecPreviews[eventKey] = previews[1:]
	}
	return preview
}

func workToolEventKey(payload map[string]any) string {
	runID := workPayloadString(payload, "run_id")
	toolName := strings.ToLower(workPayloadString(payload, "tool"))
	return runID + "\x00" + toolName
}

func toolFailureSummaryFromEvent(event session.ExecutionEvent) string {
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return ""
	}
	return trimError(firstRuntimeWorkNonEmpty(
		workPayloadString(payload, "error"),
		workPayloadString(payload, "result_preview"),
	))
}

func workPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func workPayloadBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true
		default:
			return false
		}
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
	}
}

func appendUniqueRuntimeWorkString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}

func nativeWorkTurnRecovery(result *core.TurnResult) (*core.TurnRecovery, bool) {
	if recovery, ok := turnResultBudgetRecovery(result); ok {
		return recovery, true
	}
	if result == nil {
		return nil, false
	}
	return turnBudgetRecoveryFromHandoffText(result.Text)
}

func turnBudgetRecoveryFromHandoffText(text string) (*core.TurnRecovery, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, turnBudgetRecoveryHandoffPrefix) {
		return nil, false
	}
	summary := strings.TrimSpace(strings.TrimPrefix(text, turnBudgetRecoveryHandoffPrefix))
	if idx := strings.Index(summary, "\n"); idx >= 0 {
		summary = strings.TrimSpace(summary[:idx])
	}
	kind := core.TurnRecoveryKind("budget_recovery_handoff")
	lower := strings.ToLower(summary)
	switch {
	case strings.Contains(lower, "token budget"):
		kind = core.TurnRecoveryTokenBudgetExhausted
	case strings.Contains(lower, "tool budget"):
		kind = core.TurnRecoveryToolBudgetExhausted
	case strings.Contains(lower, "iteration budget"):
		kind = core.TurnRecoveryIterationBudgetExhausted
	}
	return &core.TurnRecovery{
		Kind:           kind,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        firstRuntimeWorkNonEmpty(summary, "Budget recovery handoff."),
		MaxAutoHops:    turnBudgetRecoveryDefaultMaxHops,
	}, true
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

type nativeWorkRecoveryError struct {
	Kind    string
	Summary string
}

func (e nativeWorkRecoveryError) Error() string {
	summary := strings.TrimSpace(e.Summary)
	if summary == "" {
		summary = "the turn requested recovery before completing the approved work"
	}
	kind := strings.TrimSpace(e.Kind)
	if kind == "" {
		return "native work turn did not complete: " + summary
	}
	return "native work turn did not complete because it requested " + kind + " recovery: " + summary
}

type codexWorkExecutor struct {
	address                  string
	runtime                  *Runtime
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

var codexWorkCheckHTTP = runtimecodex.CheckHTTP

func checkCodexWorkAppServerReady(ctx context.Context, address string) error {
	if err := codexWorkCheckHTTP(ctx, address, "/healthz"); err == nil {
		return nil
	} else {
		healthzErr := err
		if err := codexWorkCheckHTTP(ctx, address, "/health"); err == nil {
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
	client := runtimecodex.NewClient(e.address, codexWorkApprovalHandler(req, e.runtime))
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
	result, err := client.StreamTurnWithOptions(ctx, threadID, turnID, runtimecodex.StreamOptions{FirstNotificationTimeout: e.firstNotificationWait()})
	if err != nil {
		partial := WorkResult(runtimecodex.WorkResultFromAppServer(codexWorkRequest(req), threadID, turnID, runtimecodex.Result{
			ThreadID:     threadID,
			TurnID:       turnID,
			ApprovalLog:  client.ApprovalLog(),
			CodexEvents:  client.WorkEvents(),
			PatchPreview: runtimecodex.WorkPatchPreviewFromEvents(client.WorkEvents()),
		}))
		return partial, err
	}
	return WorkResult(runtimecodex.WorkResultFromAppServer(codexWorkRequest(req), threadID, turnID, result)), nil
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

func nativeWorkResultTerminalError(result WorkResult) error {
	if result.ProviderFailure != "" {
		return nativeWorkProviderFailureError{Failure: result.ProviderFailure}
	}
	if workResultBudgetRecoveryScheduled(result) || workResultBudgetRecoveryBlocked(result) {
		return nil
	}
	if result.RecoveryKind != "" {
		return nativeWorkRecoveryError{Kind: result.RecoveryKind, Summary: result.RecoverySummary}
	}
	return nil
}
