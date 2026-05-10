//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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
	staleTurnSweep           func(cutoff time.Time, limit int) ([]session.TurnRun, error)
	interruptRunningTurnRuns func() ([]session.TurnRun, error)
	staleTurnWatchdogHook    func(runs []session.TurnRun)
	staleWatchdogTriggered   atomic.Bool

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
	memoryFocusMu          sync.RWMutex
	memoryFocusByChat      map[int64]core.MemoryFocus
	shuttingDown           atomic.Bool
}

func (r *Runtime) ContinuationState(chatID int64) (session.ContinuationState, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.store.ContinuationState(key)
}

func (r *Runtime) ClearChatSessionContext(chatID int64) (bool, error) {
	if r == nil {
		return false, nil
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	removed, err := r.store.DeleteSession(key)
	if err != nil {
		return false, err
	}
	r.ClearMemoryFocus(chatID)
	return removed > 0, nil
}

func (r *Runtime) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return session.ContinuationState{}, err
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return state, core.ErrContinuationNotPending
	}
	if state.RemainingTurns <= 0 {
		return state, core.ErrContinuationNoTurns
	}
	now := time.Now().UTC()
	state, err = continuationStateWithLeaseApproved(state, approverID, now)
	if err != nil {
		if updateErr := r.store.UpdateContinuationState(key, state); updateErr != nil {
			return session.ContinuationState{}, updateErr
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), now)
		return state, err
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		state = continuationStateWithPlanLeaseApprovalConsumed(state, now)
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return session.ContinuationState{}, err
	}
	r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusApproved)
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approverID
	r.recordExecutionEvent(key, core.ExecutionEventContinuationApproved, "continuation", "approved", payload, now)
	return state, nil
}

type ContinuationRevokeResult struct {
	State             session.ContinuationState
	Revoked           bool
	ContinuationLabel string
}

func (r *Runtime) RevokeContinuation(chatID int64) (ContinuationRevokeResult, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return ContinuationRevokeResult{}, err
	}
	state = session.NormalizeContinuationState(state)
	revoked := state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	if revoked {
		state = continuationStateWithLeaseRevoked(state, time.Now().UTC())
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return ContinuationRevokeResult{}, err
		}
		r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationRevoked, "continuation", "revoked", continuationExecutionPayload(state), time.Now().UTC())
	}
	return ContinuationRevokeResult{State: state, Revoked: revoked, ContinuationLabel: continuationUserFacingPlanLabel(state)}, nil
}

func (r *Runtime) TriggerContinuation(ctx context.Context, chatID int64) error {
	if r == nil {
		return nil
	}
	state, err := r.ContinuationState(chatID)
	if err != nil {
		return err
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	if continuationLeaseExpired(state, time.Now().UTC()) {
		state = continuationStateWithLeaseExpired(state, time.Now().UTC())
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return err
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	if continuationActionIsPlanLeaseApproval(state) && !state.ApprovalBundle.Active() {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "approval_only", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", continuationExecutionPayload(state), time.Now().UTC())
		return nil
	}
	approverID := state.ApprovedBy
	if approverID <= 0 {
		return fmt.Errorf("continuation approver is not recorded")
	}
	actor, ok := r.resolver.ResolveTelegramUser(approverID)
	if !ok {
		return fmt.Errorf("continuation approver %d is not admitted", approverID)
	}
	return r.runApprovedContinuation(ctx, actor, chatID, state)
}

func (r *Runtime) runApprovedContinuation(ctx context.Context, actor principal.Principal, chatID int64, state session.ContinuationState) error {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return nil
	}
	if r.shouldRouteContinuationThroughWorkExecutor(state) {
		return r.runApprovedWorkContinuation(ctx, actor, chatID, state)
	}
	return r.runApprovedContinuationNative(ctx, actor, chatID, state)
}

func (r *Runtime) runApprovedContinuationNative(ctx context.Context, actor principal.Principal, chatID int64, state session.ContinuationState) error {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return nil
	}
	sandboxRequired := continuationRequiresApprovedUserSandbox(state)
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	continuationEventText := approvedContinuationEventTextForState(state)
	state = continuationStateAfterLeaseTurnConsumed(state, time.Now().UTC())
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approvedBy
	payload["execution_principal_role"] = string(executionActor.Role)
	if sandboxRequired {
		payload["sandbox_profile"] = organicProposalSandboxProfile
	}
	if executionActor.Role != actor.Role {
		payload["sandboxed_from_role"] = string(actor.Role)
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "consumed", payload, time.Now().UTC())
	_, err := r.handleInternalContinuation(ctx, executionActor, core.InboundMessage{
		ChatID:       chatID,
		SenderID:     executionActor.TelegramUserID,
		SenderName:   actorLabel(executionActor),
		Text:         continuationEventText,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	return err
}

func (r *Runtime) shouldRouteContinuationThroughWorkExecutor(state session.ContinuationState) bool {
	if r == nil || r.workExecutor == nil {
		return false
	}
	if continuationRequiresApprovedUserSandbox(state) {
		return false
	}
	return continuationWorkMode(state) != ""
}

func (r *Runtime) runApprovedWorkContinuation(ctx context.Context, actor principal.Principal, chatID int64, state session.ContinuationState) error {
	if r == nil || r.store == nil || r.workExecutor == nil {
		return r.runApprovedContinuationNative(ctx, actor, chatID, state)
	}
	state = session.NormalizeContinuationState(state)
	mode := continuationWorkMode(state)
	leaseDecision := continuationWorkModeAccessCheck(state, mode, time.Now().UTC())
	if !leaseDecision.Allowed {
		return r.blockContinuationForLeaseAccessDenied(chatID, state, leaseDecision)
	}
	executionActor := continuationExecutionActor(actor, state)
	approvedBy := state.ApprovedBy
	if approvedBy == 0 {
		approvedBy = actor.TelegramUserID
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	opState, _ := r.store.OperationState(key)
	opState = session.NormalizeOperationState(opState)
	req := r.workRequestForContinuation(key, chatID, executionActor, state, opState)
	state = continuationStateAfterLeaseTurnConsumed(state, time.Now().UTC())
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	payload := continuationExecutionPayload(state)
	payload["approved_by_user"] = approvedBy
	payload["execution_principal_role"] = string(executionActor.Role)
	payload["work_executor_requested"] = true
	payload["work_mode"] = string(req.Mode)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "consumed", payload, time.Now().UTC())
	r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorStarted, "work", "started", map[string]any{
		"operation_id": strings.TrimSpace(req.OperationID),
		"lease_id":     strings.TrimSpace(req.LeaseID),
		"mode":         strings.TrimSpace(string(req.Mode)),
	}, time.Now().UTC())
	result, err := r.workExecutor.Run(ctx, req)
	status := r.workExecutor.Status()
	if err != nil {
		artifact := r.persistWorkResult(key, req, result, status, err)
		payload := workResultPayload(req, result, status, err)
		if artifact.Ref != "" {
			payload["artifact_ref"] = artifact.Ref
		}
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFailed, "work", "failed", payload, time.Now().UTC())
		r.offerWorkFailureRetry(ctx, key, chatID, err)
		return err
	}
	if strings.TrimSpace(status.FallbackReason) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorFallback, "work", "fallback", map[string]any{
			"operation_id":     strings.TrimSpace(req.OperationID),
			"lease_id":         strings.TrimSpace(req.LeaseID),
			"active_executor":  strings.TrimSpace(status.Active),
			"fallback_reason":  strings.TrimSpace(status.FallbackReason),
			"last_attempted":   strings.TrimSpace(status.LastAttempted),
			"configured":       strings.TrimSpace(status.Configured),
			"preferred":        strings.TrimSpace(status.Preferred),
			"executor_warning": workExecutorFallbackWarning(status),
		}, time.Now().UTC())
		if err := r.warnWorkExecutorFallback(ctx, chatID, status); err != nil {
			log.Printf("WARN send work executor fallback warning failed chat_id=%d err=%v", chatID, err)
		}
	}
	artifact := r.persistWorkResult(key, req, result, status, nil)
	payload = workResultPayload(req, result, status, nil)
	if artifact.Ref != "" {
		payload["artifact_ref"] = artifact.Ref
	}
	r.recordExecutionEvent(key, core.ExecutionEventWorkExecutorSucceeded, "work", "succeeded", payload, time.Now().UTC())
	if err := r.deliverWorkResult(ctx, chatID, result, artifact); err != nil {
		return err
	}
	return nil
}

func (r *Runtime) warnWorkExecutorFallback(ctx context.Context, chatID int64, status WorkExecutorStatus) error {
	if r == nil || r.outbound == nil || chatID == 0 {
		return nil
	}
	text := workExecutorFallbackWarning(status)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: chatID, Text: text})
	return err
}

func workExecutorFallbackWarning(status WorkExecutorStatus) string {
	if strings.TrimSpace(status.FallbackReason) == "" {
		return ""
	}
	active := firstRuntimeWorkNonEmpty(status.Active, "native")
	preferred := firstRuntimeWorkNonEmpty(status.Preferred, status.LastAttempted, "preferred executor")
	if active == preferred {
		return ""
	}
	return fmt.Sprintf("Work executor fallback: %s unavailable; using %s.", preferred, active)
}

func (r *Runtime) blockContinuationForLeaseAccessDenied(chatID int64, state session.ContinuationState, decision session.ContinuationLeaseAccessDecision) error {
	if r == nil || r.store == nil {
		return nil
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	prior := session.NormalizeContinuationState(state)
	state = continuationStateWithLeaseRevoked(state, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return err
	}
	r.syncOperationProposalStatusFromContinuation(key, state, session.ProposalStatusDenied)
	payload := continuationExecutionPayload(state)
	payload["reason"] = "lease_action_denied"
	payload["lease_action"] = strings.TrimSpace(decision.Action)
	payload["lease_access_reason"] = strings.TrimSpace(decision.Reason)
	payload["lease_id"] = strings.TrimSpace(decision.LeaseID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, now)
	r.offerLeaseActionDeniedRepair(context.Background(), key, chatID, prior, decision, now)
	return nil
}

func (r *Runtime) workRequestForContinuation(key session.SessionKey, chatID int64, actor principal.Principal, state session.ContinuationState, opState session.OperationState) WorkRequest {
	mode := continuationWorkMode(state)
	if mode == "" {
		mode = WorkModeReadOnly
	}
	repoRoot := firstNonEmptyContinuation(opState.Work.RepoRoot, opState.Work.Workdir)
	if repoRoot == "" && r != nil && r.cfg != nil {
		repoRoot = r.cfg.Agent.ExecRoot
	}
	workdir := firstNonEmptyContinuation(opState.Work.Workdir, repoRoot)
	threadID := strings.TrimSpace(opState.Work.CodexThreadID)
	return WorkRequest{
		OperationID: firstNonEmptyContinuation(opState.ID, state.ActionProposal.OperationID),
		RepoRoot:    repoRoot,
		Workdir:     workdir,
		Prompt:      workPromptForContinuation(state, opState),
		Mode:        mode,
		LeaseID:     state.ContinuationLease.ID,
		ThreadID:    threadID,
		Key:         key,
		ChatID:      chatID,
		Actor:       actor,
		State:       state,
		Operation:   opState,
	}
}

func continuationWorkMode(state session.ContinuationState) WorkMode {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if mode := workModeFromStructuredAuthority(phase.AuthorityClass); mode != "" {
			return mode
		}
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode := strongestWorkMode(
		workModeFromStructuredAuthority(proposal.RiskClass),
		workModeFromStructuredAuthorityList(proposal.AllowedActions),
		workModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions),
	)
	if mode != "" {
		return mode
	}

	lower := strings.ToLower(strings.Join([]string{
		proposal.Summary,
		proposal.BoundedEffect,
		state.StageSummary,
	}, " "))
	switch {
	case strings.Contains(lower, "read_only") || strings.Contains(lower, "status_check"):
		return WorkModeReadOnly
	default:
		return ""
	}
}

func continuationWorkModeAccessCheck(state session.ContinuationState, mode WorkMode, now time.Time) session.ContinuationLeaseAccessDecision {
	state = session.NormalizeContinuationState(state)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	action := strings.TrimSpace(string(mode))
	decision := session.CheckContinuationLeaseAction(state.ContinuationLease, action, now)
	if decision.Allowed {
		return decision
	}
	if decision.Reason != "action_not_allowed" {
		return decision
	}
	requestedRank := workModeRank(mode)
	if requestedRank <= 0 {
		decision.Reason = "work_mode_required"
		return decision
	}
	if continuationWorkModeForbiddenByLease(state, mode) {
		decision.Reason = "action_forbidden"
		return decision
	}
	if continuationAllowedWorkModeRank(state) >= requestedRank {
		decision.Allowed = true
		decision.Reason = "allowed_by_structured_authority"
		return decision
	}
	return decision
}

func continuationAllowedWorkModeRank(state session.ContinuationState) int {
	state = session.NormalizeContinuationState(state)
	mode := WorkMode("")
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		mode = strongestWorkMode(mode, workModeFromStructuredAuthority(phase.AuthorityClass))
		mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(phase.AllowedActions))
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode = strongestWorkMode(mode, workModeFromStructuredAuthority(proposal.RiskClass))
	mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(proposal.AllowedActions))
	mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions))
	return workModeRank(mode)
}

func continuationWorkModeForbiddenByLease(state session.ContinuationState, mode WorkMode) bool {
	state = session.NormalizeContinuationState(state)
	requestedRank := workModeRank(mode)
	if requestedRank <= 0 {
		return false
	}
	for _, forbidden := range continuationForbiddenWorkModeActions(state) {
		forbiddenMode := workModeFromBroadForbiddenAuthority(forbidden)
		forbiddenRank := workModeRank(forbiddenMode)
		if forbiddenRank > 0 && requestedRank >= forbiddenRank {
			return true
		}
		if normalizeWorkModeAuthorityToken(forbidden) == normalizeWorkModeAuthorityToken(string(mode)) {
			return true
		}
	}
	return false
}

func continuationForbiddenWorkModeActions(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	out := make([]string, 0, len(state.ActionProposal.ForbiddenActions)+len(state.ContinuationLease.ForbiddenActions)+8)
	out = append(out, state.ActionProposal.ForbiddenActions...)
	out = append(out, state.ContinuationLease.ForbiddenActions...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		out = append(out, phase.ForbiddenActions...)
	}
	return out
}

func workModeFromBroadForbiddenAuthority(value string) WorkMode {
	token := normalizeWorkModeAuthorityToken(value)
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart":
		return WorkModeDeploy
	case "commit", "git_commit", "repo_history_mutation":
		return WorkModeCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "edit", "edit_files", "patch", "run_tests", "test", "tests":
		return WorkModeWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return WorkModeReadOnly
	default:
		return ""
	}
}

func workModeFromStructuredAuthorityList(values []string) WorkMode {
	mode := WorkMode("")
	for _, value := range values {
		mode = strongestWorkMode(mode, workModeFromStructuredAuthority(value))
	}
	return mode
}

func workModeFromStructuredAuthority(value string) WorkMode {
	token := normalizeWorkModeAuthorityToken(value)
	if contract, ok := session.AuthorityContractForToken(token); ok {
		if mode := workModeFromAuthorityContractAction(contract.WorkAction); mode != "" {
			return mode
		}
	}
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart":
		return WorkModeDeploy
	case "commit", "git_commit", "repo_history_mutation":
		return WorkModeCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "edit", "edit_files", "patch", "run_tests", "test", "tests":
		return WorkModeWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return WorkModeReadOnly
	default:
		switch {
		case strings.HasPrefix(token, "deploy") ||
			strings.HasPrefix(token, "live_deploy") ||
			strings.HasPrefix(token, "run_deploy") ||
			strings.HasPrefix(token, "system_change") ||
			strings.HasPrefix(token, "restart") ||
			strings.HasPrefix(token, "service_restart"):
			return WorkModeDeploy
		case strings.HasPrefix(token, "commit") ||
			strings.HasPrefix(token, "git_commit") ||
			strings.HasPrefix(token, "repo_history_mutation"):
			return WorkModeCommit
		case strings.HasPrefix(token, "workspace_write") ||
			strings.HasPrefix(token, "workspace") ||
			strings.HasPrefix(token, "code_change") ||
			strings.HasPrefix(token, "edit_files") ||
			strings.HasPrefix(token, "patch") ||
			strings.HasPrefix(token, "run_tests"):
			return WorkModeWorkspaceWrite
		case strings.HasPrefix(token, "read_only") ||
			strings.HasPrefix(token, "status_check") ||
			strings.HasPrefix(token, "inspect_readonly_state"):
			return WorkModeReadOnly
		default:
			return ""
		}
	}
}

func workModeFromAuthorityContractAction(action string) WorkMode {
	switch normalizeWorkModeAuthorityToken(action) {
	case session.AuthorityWorkActionDeploy:
		return WorkModeDeploy
	case session.AuthorityWorkActionCommit:
		return WorkModeCommit
	case session.AuthorityWorkActionWorkspaceWrite:
		return WorkModeWorkspaceWrite
	case session.AuthorityWorkActionReadOnly:
		return WorkModeReadOnly
	default:
		return ""
	}
}

func normalizeWorkModeAuthorityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func strongestWorkMode(modes ...WorkMode) WorkMode {
	strongest := WorkMode("")
	strongestRank := 0
	for _, mode := range modes {
		rank := workModeRank(mode)
		if rank > strongestRank {
			strongest = mode
			strongestRank = rank
		}
	}
	return strongest
}

func workModeRank(mode WorkMode) int {
	switch mode {
	case WorkModeDeploy:
		return 4
	case WorkModeCommit:
		return 3
	case WorkModeWorkspaceWrite:
		return 2
	case WorkModeReadOnly:
		return 1
	default:
		return 0
	}
}

func workPromptForContinuation(state session.ContinuationState, opState session.OperationState) string {
	state = session.NormalizeContinuationState(state)
	opState = session.NormalizeOperationState(opState)
	lines := []string{
		"Role: You are the bounded work executor for a runtime-approved continuation.",
		"",
		"## Goal",
		"Complete only the approved next step and return evidence the parent runtime can store and summarize.",
		"",
		"## Success Criteria",
		"- Stay within the lease, work mode, repository, and sandbox implied by this request.",
		"- Preserve durable operation context and do not collapse a broad objective into a one-step plan.",
		"- Validate meaningful edits, generated artifacts, service actions, or conclusions with the narrowest relevant check available.",
		"- Report changed files, commands, tests, evidence, residual risk, and any blocked validation.",
		"",
		"## Constraints",
		"- Do not expand authority, credentials, network use, deploy, restart, commit, or external effects beyond this approved lease.",
		"- Do not ask for approval to make a plan. If more work remains, propose concrete bounded next phases or lanes.",
		"",
		"## Stop Rules",
		"- Stop before any action outside the lease or any action whose failure could create irreversible, external, privacy, or credential risk.",
		"- If required evidence or validation is unavailable, report that limitation instead of inventing certainty.",
	}
	if objective := firstNonEmptyContinuation(opState.Objective, state.Objective); objective != "" {
		lines = append(lines, "Objective: "+objective)
	}
	if summary := firstNonEmptyContinuation(state.ActionProposal.Summary, state.StageSummary); summary != "" {
		lines = append(lines, "Next step: "+summary)
	}
	if effect := strings.TrimSpace(state.ActionProposal.BoundedEffect); effect != "" {
		lines = append(lines, "Bounded effect: "+effect)
	}
	if opState.PhasePlan.Active() {
		lines = append(lines, "Durable phase plan: "+firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.PhasePlan.ID))
		if current := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID); current != "" {
			lines = append(lines, "Current phase id: "+current)
		}
		for _, phase := range opState.PhasePlan.Phases {
			label := strings.TrimSpace(phase.ID)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				if label == "" {
					label = summary
				} else {
					label += ": " + summary
				}
			}
			if label == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Phase [%s] %s", phase.Status, label))
		}
	}
	lines = append(lines, "Stop after this bounded step and report changed files, commands, tests, evidence, and remaining risk.")
	return strings.Join(lines, "\n")
}

func (r *Runtime) persistWorkResult(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error) session.OperationArtifact {
	if r == nil || r.store == nil {
		return session.OperationArtifact{}
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return session.OperationArtifact{}
	}
	opState = session.NormalizeOperationState(opState)
	if strings.TrimSpace(opState.ID) == "" {
		opState.ID = strings.TrimSpace(req.OperationID)
	}
	opState.Work.Executor = firstRuntimeWorkNonEmpty(result.ExecutorName, status.Active)
	opState.Work.ConfiguredExecutor = status.Configured
	opState.Work.PreferredExecutor = status.Preferred
	opState.Work.FallbackReason = status.FallbackReason
	opState.Work.CodexThreadID = firstRuntimeWorkNonEmpty(result.ThreadID, opState.Work.CodexThreadID)
	opState.Work.CodexLastTurnID = firstRuntimeWorkNonEmpty(result.TurnID, opState.Work.CodexLastTurnID)
	opState.Work.CodexLaneMode = string(req.Mode)
	opState.Work.RepoRoot = firstRuntimeWorkNonEmpty(req.RepoRoot, opState.Work.RepoRoot)
	opState.Work.Workdir = firstRuntimeWorkNonEmpty(req.Workdir, opState.Work.Workdir)
	opState.Work.ChangedFiles = append([]string(nil), result.ChangedFiles...)
	opState.Work.Commands = append([]string(nil), result.Commands...)
	opState.Work.CodexEvents = append([]session.WorkCodexEvent(nil), result.CodexEvents...)
	opState.Work.PatchPreview = strings.TrimSpace(result.PatchPreview)
	opState.Work.CommitLaneStatus = strings.TrimSpace(result.CommitLaneStatus)
	opState.Work.LastSummary = strings.TrimSpace(result.Summary)
	opState.Work.LastError = ""
	if cause != nil {
		opState.Work.LastError = cause.Error()
	}
	opState.Work.LastExecutorUpdatedAt = time.Now().UTC()
	if cause == nil {
		opState.Work.LastCompletedAt = opState.Work.LastExecutorUpdatedAt
	}
	artifact := r.writeWorkResultArtifact(key, req, result, status, cause, opState.Work.LastExecutorUpdatedAt)
	if artifact.Ref != "" {
		opState.Artifacts = appendOperationArtifact(opState.Artifacts, artifact)
	}
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		log.Printf("WARN persist work result failed chat_id=%d err=%v", key.ChatID, err)
	}
	return artifact
}

func (r *Runtime) deliverWorkResult(ctx context.Context, chatID int64, result WorkResult, artifact session.OperationArtifact) error {
	if r == nil || r.outbound == nil || chatID == 0 {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(result.ExecutorName), "native") {
		return nil
	}
	text := renderWorkResultMessage(result, artifact)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: chatID, Text: text}); err != nil {
		return fmt.Errorf("send work executor result: %w", err)
	}
	return nil
}

func renderWorkResultMessage(result WorkResult, artifact session.OperationArtifact) string {
	executor := firstRuntimeWorkNonEmpty(result.ExecutorName, "work executor")
	lines := []string{"Work executor finished via " + executor + "."}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "", truncatePreview(summary, 900))
	}
	if len(result.ChangedFiles) > 0 {
		lines = append(lines, "", "Changed files:")
		for _, file := range result.ChangedFiles {
			lines = append(lines, "- "+strings.TrimSpace(file))
		}
	}
	if len(result.Commands) > 0 {
		lines = append(lines, "", "Commands:")
		for _, command := range result.Commands {
			lines = append(lines, "- "+strings.TrimSpace(command))
		}
	}
	if status := strings.TrimSpace(result.CommitLaneStatus); status != "" {
		lines = append(lines, "", "Commit lane: "+status)
	}
	if preview := strings.TrimSpace(result.PatchPreview); preview != "" {
		lines = append(lines, "", "Patch preview:", truncatePreview(preview, 900))
	}
	if artifact.Ref != "" {
		lines = append(lines, "", "Full evidence artifact:", artifact.Ref)
	}
	if strings.TrimSpace(result.Summary) == "" && len(result.ChangedFiles) == 0 && len(result.Commands) == 0 {
		lines = append(lines, "", "No detailed summary was returned.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (r *Runtime) offerWorkFailureRetry(ctx context.Context, key session.SessionKey, chatID int64, cause error) {
	if r == nil || cause == nil || chatID == 0 {
		return
	}
	if r.isShuttingDown() || errors.Is(cause, context.Canceled) {
		return
	}
	reason := "The approved work run failed before completion; approve this fresh lease to retry the same bounded action after reviewing the failure evidence."
	if _, sent, refreshErr := r.refreshContinuationProposal(ctx, chatID, reason, "work_executor_failure", false); refreshErr != nil {
		log.Printf("WARN refresh continuation after work failure failed chat_id=%d err=%v", chatID, refreshErr)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "retry_offer_failed", map[string]any{
			"reason": "work_executor_failure_retry_offer_failed",
			"error":  trimError(refreshErr.Error()),
		}, time.Now().UTC())
	} else if sent {
		r.recordExecutionEvent(key, core.ExecutionEventRecoveryIssued, "work", "retry_offered", map[string]any{
			"reason": "work_executor_failure",
			"error":  trimError(cause.Error()),
		}, time.Now().UTC())
	}
}

func appendOperationArtifact(values []session.OperationArtifact, artifact session.OperationArtifact) []session.OperationArtifact {
	artifact.Ref = strings.TrimSpace(artifact.Ref)
	if artifact.Ref == "" {
		return values
	}
	artifact.Label = strings.TrimSpace(artifact.Label)
	out := make([]session.OperationArtifact, 0, len(values)+1)
	seen := false
	for _, value := range values {
		if strings.TrimSpace(value.Ref) == artifact.Ref {
			out = append(out, artifact)
			seen = true
			continue
		}
		out = append(out, value)
	}
	if !seen {
		out = append(out, artifact)
	}
	return out
}

func (r *Runtime) writeWorkResultArtifact(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error, now time.Time) session.OperationArtifact {
	if r == nil || r.cfg == nil {
		return session.OperationArtifact{}
	}
	body := workResultArtifactMarkdown(key, req, result, status, cause, now)
	if strings.TrimSpace(body) == "" {
		return session.OperationArtifact{}
	}
	root := firstRuntimeWorkNonEmpty(r.cfg.Agent.SharedMemoryRoot, r.cfg.Agent.Workspace, r.cfg.Agent.ExecRoot)
	if strings.TrimSpace(root) == "" {
		return session.OperationArtifact{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	dir := filepath.Join(root, "memory", "work-evidence", now.Format("2006-01-02"), fmt.Sprintf("chat-%d", key.ChatID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("WARN write work evidence artifact mkdir failed chat_id=%d err=%v", key.ChatID, err)
		return session.OperationArtifact{}
	}
	base := sanitizeWorkArtifactName(firstRuntimeWorkNonEmpty(req.OperationID, req.LeaseID, "work"))
	if base == "" {
		base = "work"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, now.UnixNano()))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		log.Printf("WARN write work evidence artifact failed chat_id=%d err=%v", key.ChatID, err)
		return session.OperationArtifact{}
	}
	return session.OperationArtifact{Label: "Work evidence", Ref: path}
}

func workResultArtifactMarkdown(key session.SessionKey, req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if strings.TrimSpace(result.Summary) == "" &&
		strings.TrimSpace(result.ProviderFailure) == "" &&
		len(result.ProviderEvents) == 0 &&
		len(result.ChangedFiles) == 0 &&
		len(result.Commands) == 0 &&
		len(result.CodexEvents) == 0 &&
		strings.TrimSpace(result.PatchPreview) == "" &&
		len(result.ApprovalLog) == 0 &&
		cause == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Work Evidence\n\n")
	fmt.Fprintf(&b, "- captured_at: %s\n", now.Format(time.RFC3339))
	fmt.Fprintf(&b, "- chat_id: %d\n", key.ChatID)
	if req.OperationID != "" {
		fmt.Fprintf(&b, "- operation_id: %s\n", req.OperationID)
	}
	if req.LeaseID != "" {
		fmt.Fprintf(&b, "- lease_id: %s\n", req.LeaseID)
	}
	if result.ExecutorName != "" {
		fmt.Fprintf(&b, "- executor: %s\n", result.ExecutorName)
	}
	if status.Configured != "" {
		fmt.Fprintf(&b, "- configured_executor: %s\n", status.Configured)
	}
	if status.FallbackReason != "" {
		fmt.Fprintf(&b, "- fallback_reason: %s\n", status.FallbackReason)
	}
	if result.ProviderFailure != "" {
		fmt.Fprintf(&b, "- provider_failure: %s\n", trimError(result.ProviderFailure))
	}
	if cause != nil {
		fmt.Fprintf(&b, "- error: %s\n", trimError(cause.Error()))
	}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		b.WriteString("\n## Summary\n\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if len(result.ChangedFiles) > 0 {
		b.WriteString("\n## Changed Files\n\n")
		for _, file := range result.ChangedFiles {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(file))
		}
	}
	if len(result.Commands) > 0 {
		b.WriteString("\n## Commands\n\n")
		for _, command := range result.Commands {
			fmt.Fprintf(&b, "- `%s`\n", strings.TrimSpace(command))
		}
	}
	if len(result.CodexEvents) > 0 {
		b.WriteString("\n## Codex Events\n\n")
		for _, event := range result.CodexEvents {
			parts := []string{}
			for _, part := range []string{event.Kind, event.Method, event.Status, event.Subject, event.Path, event.Command, event.Server, event.Tool, event.ThreadID, event.TurnID} {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			if len(parts) == 0 {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.Join(parts, " | "))
			if preview := strings.TrimSpace(event.Preview); preview != "" {
				b.WriteString("\n```text\n")
				b.WriteString(preview)
				b.WriteString("\n```\n")
			}
		}
	}
	if len(result.ProviderEvents) > 0 {
		b.WriteString("\n## Provider Events\n\n")
		for _, event := range result.ProviderEvents {
			parts := []string{}
			for _, part := range []string{event.EventType, event.Provider, event.FromProvider, event.ToProvider, event.Reason, event.ResponseID} {
				if trimmed := strings.TrimSpace(part); trimmed != "" {
					parts = append(parts, trimmed)
				}
			}
			if event.Attempt > 0 {
				parts = append(parts, fmt.Sprintf("attempt=%d", event.Attempt))
			}
			if event.MaxRetries > 0 {
				parts = append(parts, fmt.Sprintf("max_retries=%d", event.MaxRetries))
			}
			if event.PartialContentChars > 0 {
				parts = append(parts, fmt.Sprintf("partial_content_chars=%d", event.PartialContentChars))
			}
			if event.PartialToolCalls > 0 {
				parts = append(parts, fmt.Sprintf("partial_tool_calls=%d", event.PartialToolCalls))
			}
			if len(parts) == 0 && strings.TrimSpace(event.Error) == "" {
				continue
			}
			if len(parts) > 0 {
				fmt.Fprintf(&b, "- %s\n", strings.Join(parts, " | "))
			}
			if errText := strings.TrimSpace(event.Error); errText != "" {
				fmt.Fprintf(&b, "  error: %s\n", trimError(errText))
			}
		}
	}
	if len(result.ApprovalLog) > 0 {
		b.WriteString("\n## Approval Log\n\n")
		for _, item := range result.ApprovalLog {
			fmt.Fprintf(&b, "- %s: %s", strings.TrimSpace(item.Method), strings.TrimSpace(item.Decision))
			if item.Command != "" {
				fmt.Fprintf(&b, " `%s`", item.Command)
			}
			if item.Reason != "" {
				fmt.Fprintf(&b, " (%s)", item.Reason)
			}
			b.WriteString("\n")
		}
	}
	if preview := strings.TrimSpace(result.PatchPreview); preview != "" {
		b.WriteString("\n## Patch Preview\n\n```diff\n")
		b.WriteString(preview)
		b.WriteString("\n```\n")
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func sanitizeWorkArtifactName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func workResultPayload(req WorkRequest, result WorkResult, status WorkExecutorStatus, cause error) map[string]any {
	payload := map[string]any{
		"operation_id":          strings.TrimSpace(req.OperationID),
		"lease_id":              strings.TrimSpace(req.LeaseID),
		"mode":                  strings.TrimSpace(string(req.Mode)),
		"executor":              strings.TrimSpace(result.ExecutorName),
		"configured_executor":   strings.TrimSpace(status.Configured),
		"preferred_executor":    strings.TrimSpace(status.Preferred),
		"active_executor":       strings.TrimSpace(status.Active),
		"fallback_reason":       strings.TrimSpace(status.FallbackReason),
		"provider_events_count": len(result.ProviderEvents),
		"changed_files_count":   len(result.ChangedFiles),
		"commands_count":        len(result.Commands),
		"codex_events_count":    len(result.CodexEvents),
		"approval_events_count": len(result.ApprovalLog),
	}
	if strings.TrimSpace(result.ThreadID) != "" {
		payload["thread_id"] = strings.TrimSpace(result.ThreadID)
	}
	if strings.TrimSpace(result.TurnID) != "" {
		payload["turn_id"] = strings.TrimSpace(result.TurnID)
	}
	if strings.TrimSpace(result.CommitLaneStatus) != "" {
		payload["commit_lane_status"] = strings.TrimSpace(result.CommitLaneStatus)
	}
	if strings.TrimSpace(result.ProviderFailure) != "" {
		payload["provider_failure"] = trimError(result.ProviderFailure)
	}
	if cause != nil {
		payload["error"] = trimError(cause.Error())
	}
	return payload
}

func actorLabel(actor principal.Principal) string {
	if actor.Role == principal.RoleAdmin {
		return "admin"
	}
	if actor.Role == principal.RoleApprovedUser {
		return "approved_user"
	}
	if strings.TrimSpace(actor.DurableAgentID) != "" {
		return strings.TrimSpace(actor.DurableAgentID)
	}
	return "machine"
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

func newCodexHTTPClient() *http.Client {
	transport, _ := http.DefaultTransport.(*http.Transport)
	if transport == nil {
		return &http.Client{}
	}
	clone := transport.Clone()
	clone.ResponseHeaderTimeout = 30 * time.Second
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
		HTTPClient:       newCodexHTTPClient(),
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
	scopeResolver, err := sandbox.NewResolver(sandboxRoots, SandboxProfilesFromConfig(cfg.Sandbox))
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
		staleTurnThreshold:       defaultStaleTurnThreshold,
		staleTurnLimit:           defaultStaleTurnLimit,
		interruptRunningTurnRuns: store.InterruptRunningTurnRuns,
		tailnetBackend:           tailnetBackend,
		modelProviderCache:       make(map[string]agent.Provider),
		recipePath:               recipePath,
		recipeState:              recipeState,
		memoryFocusByChat:        make(map[int64]core.MemoryFocus),
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
	if strings.TrimSpace(copy.Agent.Workspace) == "" {
		copy.Agent.Workspace = copy.Agent.ExecRoot
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
