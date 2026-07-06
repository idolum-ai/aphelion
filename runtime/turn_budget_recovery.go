//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

const (
	turnBudgetRecoveryDefaultMaxHops = 3
	turnBudgetRecoveryTimeout        = 10 * time.Minute
	turnBudgetRecoveryOriginDetail   = "budget_recovery"
	turnBudgetRecoveryHandoffPrefix  = "Budget recovery handoff:"
	turnBudgetRecoveryDigestEvents   = 6
	turnBudgetRecoveryDigestLines    = 10
)

type turnBudgetRecoveryDigest struct {
	RunID int64
	Lines []string
}

func turnResultBudgetRecovery(result *core.TurnResult) (*core.TurnRecovery, bool) {
	if result == nil || result.Recovery == nil {
		return nil, false
	}
	recovery := *result.Recovery
	if recovery.Kind == "" || !recovery.Recoverable || !recovery.ReplanRequired {
		return nil, false
	}
	return &recovery, true
}

func turnResultBudgetRecoveryFromTurnResult(result *turn.Result) (*core.TurnRecovery, bool) {
	if result == nil {
		return nil, false
	}
	return turnResultBudgetRecovery(result.Turn)
}

func turnBudgetRecoveryHandoffText(recovery *core.TurnRecovery) string {
	if recovery == nil {
		return ""
	}
	summary := strings.TrimSpace(recovery.Summary)
	if summary == "" {
		summary = "The turn exhausted its execution budget before a final response."
	}
	return turnBudgetRecoveryHandoffPrefix + " " + summary
}

func turnBudgetRecoveryMaxHops(recovery *core.TurnRecovery) int {
	if recovery == nil || recovery.MaxAutoHops <= 0 {
		return turnBudgetRecoveryDefaultMaxHops
	}
	if recovery.MaxAutoHops > turnBudgetRecoveryDefaultMaxHops {
		return turnBudgetRecoveryDefaultMaxHops
	}
	return recovery.MaxAutoHops
}

func (p *turnDeliveryPort) deliverBudgetRecovery(ctx context.Context, req turn.DeliveryRequest) (*turn.DeliveryResult, error) {
	recovery, ok := turnResultBudgetRecoveryFromTurnResult(req.Result)
	if !ok {
		return nil, nil
	}
	maxHops := turnBudgetRecoveryMaxHops(recovery)
	scope, scopePayload := p.runtime.turnBudgetRecoveryScope(p.key, p.msg, req.Result)
	if p.deferBudgetRecoveryToWorkFailureRetry {
		payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, 0, maxHops)
		payload["reason"] = "work_executor_retry_path"
		if req.Result != nil && req.Result.Turn != nil {
			appendTokenUsagePayload(payload, req.Result.Turn.TokenUsage)
		}
		p.runtime.recordExecutionEvent(p.key, core.ExecutionEventTurnBudgetRecovery, "turn", "deferred", payload, time.Now().UTC())
		if p.audit != nil {
			p.audit.RecordFinalReply("", nil, "budget_recovery_deferred_to_work_retry")
		}
		return &turn.DeliveryResult{Kind: "budget_recovery_deferred_to_work_retry"}, nil
	}
	attempts, err := p.runtime.turnBudgetRecoveryScheduledAttempts(p.key, scope)
	if err != nil {
		return p.deliverBudgetRecoveryBlocked(ctx, req, recovery, scope, scopePayload, maxHops, "retry_counter_unavailable", err)
	}
	nextHop := attempts + 1
	if attempts >= maxHops {
		return p.deliverBudgetRecoveryBlocked(ctx, req, recovery, scope, scopePayload, maxHops, "max_auto_hops_reached", nil)
	}
	actor, err := p.runtime.turnBudgetRecoveryActor(p.key, p.msg)
	if err != nil {
		return p.deliverBudgetRecoveryBlocked(ctx, req, recovery, scope, scopePayload, maxHops, "actor_unavailable", err)
	}
	if p.runtime.isShuttingDown() {
		return p.deliverBudgetRecoveryBlocked(ctx, req, recovery, scope, scopePayload, maxHops, "runtime_shutting_down", nil)
	}

	digest := p.runtime.turnBudgetRecoveryDigest(p.key, p.currentRunID())
	payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, nextHop, maxHops)
	appendTurnBudgetRecoveryDigestPayload(payload, digest)
	appendTokenUsagePayload(payload, req.Result.Turn.TokenUsage)
	p.runtime.recordExecutionEvent(p.key, core.ExecutionEventTurnBudgetRecovery, "turn", "scheduled", payload, time.Now().UTC())
	if p.audit != nil {
		p.audit.RecordFinalReply("", nil, "budget_recovery_scheduled")
	}
	p.runtime.scheduleTurnBudgetRecoveryContinuation(p.key, p.msg, actor, recovery, scope, nextHop, maxHops, digest)
	return &turn.DeliveryResult{Kind: "budget_recovery_scheduled"}, nil
}

func (p *turnDeliveryPort) deliverBudgetRecoveryBlocked(ctx context.Context, req turn.DeliveryRequest, recovery *core.TurnRecovery, scope string, scopePayload map[string]any, maxHops int, reason string, cause error) (*turn.DeliveryResult, error) {
	now := time.Now().UTC()
	decision := p.runtime.recoveryDecisionForInterruption(p.key, "budget_recovery_blocked", reason, now)
	payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, 0, maxHops)
	payload["reason"] = strings.TrimSpace(reason)
	for key, value := range decision.payload() {
		payload[key] = value
	}
	if cause != nil {
		payload["error"] = trimError(cause.Error())
	}
	if req.Result != nil && req.Result.Turn != nil {
		appendTokenUsagePayload(payload, req.Result.Turn.TokenUsage)
	}
	p.runtime.recordExecutionEvent(p.key, core.ExecutionEventTurnBudgetRecovery, "turn", "blocked", payload, now)
	p.runtime.recordRecoveryDecision(p.key, decision, now)

	text := turnBudgetRecoveryBlockedText(recovery, maxHops, reason, decision)
	if p.audit != nil {
		p.audit.RecordFinalReply(text, nil, "budget_recovery_blocked")
	}
	if !p.deliver {
		return &turn.DeliveryResult{Kind: "budget_recovery_blocked"}, nil
	}
	outboundID, outboundType, sendErr := p.runtime.sendReply(ctx, p.msg, text, nil, false)
	if sendErr != nil {
		p.runtime.recordExecutionEvent(p.key, core.ExecutionEventDeliveryFinalFailed, "delivery", "failed", map[string]any{
			"error": trimError(sendErr.Error()),
		}, time.Now().UTC())
		if p.sendErrCtx == "" {
			return nil, sendErr
		}
		return nil, fmt.Errorf("%s: %w", p.sendErrCtx, sendErr)
	}
	p.runtime.recordExecutionEvent(p.key, core.ExecutionEventDeliveryFinalSent, "delivery", "sent", map[string]any{
		"message_id": outboundID,
		"kind":       strings.TrimSpace(outboundType),
		"reason":     "budget_recovery_blocked",
	}, time.Now().UTC())
	if p.recordOutbound {
		if err := p.recordOutboundWithContext(ctx, p.currentSession(), p.key, outboundID, outboundType); err != nil {
			return nil, err
		}
	}
	return &turn.DeliveryResult{MessageID: outboundID, Kind: outboundType}, nil
}

func (r *Runtime) scheduleTurnBudgetRecoveryContinuation(key session.SessionKey, msg core.InboundMessage, actor principal.Principal, recovery *core.TurnRecovery, scope string, hop int, maxHops int, digest turnBudgetRecoveryDigest) {
	if r == nil {
		return
	}
	r.backgroundLoopsWG.Add(1)
	go func() {
		defer r.backgroundLoopsWG.Done()
		runCtx, cancel := context.WithTimeout(context.Background(), turnBudgetRecoveryTimeout)
		defer cancel()
		if err := r.runTurnBudgetRecoveryContinuation(runCtx, key, msg, actor, recovery, scope, hop, maxHops, digest); err != nil {
			log.Printf("WARN turn budget recovery failed chat_id=%d scope=%s hop=%d err=%v", key.ChatID, scope, hop, err)
		}
	}()
}

func (r *Runtime) runTurnBudgetRecoveryContinuation(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, recovery *core.TurnRecovery, scope string, hop int, maxHops int, digest turnBudgetRecoveryDigest) error {
	if r == nil {
		return nil
	}
	prompt := renderTurnBudgetRecoveryPrompt(recovery, scope, hop, maxHops, digest)
	recoveryMsg := continuationInboundForKey(key, actor, prompt, core.InboundOriginTurnAuthorization, turnBudgetRecoveryOriginDetail)
	recoveryMsg.ChatType = msg.ChatType
	recoveryMsg.ChatTitle = msg.ChatTitle
	recoveryMsg.MessageID = msg.MessageID
	recoveryMsg.ReplyTo = msg.ReplyTo
	recoveryMsg.DurableAgentID = msg.DurableAgentID
	recoveryMsg.Timestamp = time.Now().UTC()

	payload := turnBudgetRecoveryPayload(recovery, scope, nil, hop, maxHops)
	appendTurnBudgetRecoveryDigestPayload(payload, digest)
	r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "resuming", payload, time.Now().UTC())
	result, err := r.handleInternalContinuation(ctx, actor, recoveryMsg)
	if err != nil {
		now := time.Now().UTC()
		decision := r.recoveryDecisionForInterruption(key, "budget_recovery_turn_failed", "recovery_turn_failed", now)
		payload["error"] = trimError(err.Error())
		for key, value := range decision.payload() {
			payload[key] = value
		}
		r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "failed", payload, now)
		r.recordRecoveryDecision(key, decision, now)
		r.notifyTurnBudgetRecoveryFailure(ctx, msg, recovery, maxHops, err, decision)
		return err
	}
	if result != nil && result.Recovery != nil {
		payload["result_recovery_kind"] = string(result.Recovery.Kind)
	}
	r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "resumed", payload, time.Now().UTC())
	if result == nil || result.Recovery == nil {
		return r.reconcileConsumedContinuationAfterBudgetRecovery(ctx, key)
	}
	return nil
}

func (r *Runtime) reconcileConsumedContinuationAfterBudgetRecovery(ctx context.Context, key session.SessionKey) error {
	if r == nil || r.store == nil {
		return nil
	}
	state, err := r.store.ContinuationState(key)
	if err != nil {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	now := time.Now().UTC()
	decision := continuationLoopDecision{
		Continue: false,
		Reason:   "not_approved",
		Boundary: "budget recovery resumed; no active approved continuation remains",
		Mission:  r.assessContinuationLoopMission(key, state, now),
	}
	if !continuationBoundaryCanOfferNextOperationPhase(state, decision) {
		return nil
	}
	r.recordContinuationLoopAssessment(key, state, decision, 1)
	r.recordContinuationLoopBoundary(key, state, decision, 1)
	return r.maybeOfferNextOperationPhaseAfterContinuationBoundary(ctx, key, state, decision)
}

func (r *Runtime) notifyTurnBudgetRecoveryFailure(ctx context.Context, msg core.InboundMessage, recovery *core.TurnRecovery, maxHops int, err error, decision recoveryDecision) {
	if r == nil || r.outbound == nil || msg.ChatID == 0 || r.isShuttingDown() {
		return
	}
	text := turnBudgetRecoveryBlockedText(recovery, maxHops, "recovery_turn_failed", decision)
	if err != nil {
		text += "\n\nFailure: " + trimError(err.Error())
	}
	if _, _, sendErr := r.sendReply(ctx, msg, text, nil, false); sendErr != nil && !r.expectedShutdownNoise(ctx, sendErr) {
		log.Printf("WARN turn budget recovery failure notification failed chat_id=%d err=%v", msg.ChatID, sendErr)
	}
}

func (r *Runtime) turnBudgetRecoveryActor(key session.SessionKey, msg core.InboundMessage) (principal.Principal, error) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, ErrPrincipalDenied
	}
	candidates := []int64{msg.SenderID, key.UserID}
	if r.store != nil {
		if state, exists, err := r.store.ContinuationStateIfExists(key); err == nil && exists {
			candidates = append(candidates, state.ApprovedBy, state.ContinuationLease.ApprovedBy, state.ApprovalBundle.ApprovedBy)
		}
	}
	seen := map[int64]struct{}{}
	for _, id := range candidates {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if actor, ok := r.resolver.ResolveTelegramUser(id); ok {
			return actor, nil
		}
	}
	return principal.Principal{}, ErrPrincipalDenied
}

func (r *Runtime) turnBudgetRecoveryScheduledAttempts(key session.SessionKey, scope string) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 200)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventTurnBudgetRecovery || strings.TrimSpace(event.Status) != "scheduled" {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(payload["recovery_scope"])) == scope {
			count++
		}
	}
	return count, nil
}

func (r *Runtime) turnBudgetRecoveryScope(key session.SessionKey, msg core.InboundMessage, result *turn.Result) (scope string, payload map[string]any) {
	opState := session.OperationState{}
	if result != nil {
		opState = session.NormalizeOperationState(result.OperationState)
	}
	defer func() {
		if err := r.recordBudgetRecoveryScopeJudgmentUse(key, msg, opState, scope, payload, time.Now().UTC()); err != nil {
			log.Printf("WARN record budget recovery scope judgment failed chat_id=%d err=%v", key.ChatID, err)
		}
	}()
	if !operationStateRecoverableForBudgetRecovery(opState) && r != nil && r.store != nil {
		if _, stored, exists, err := r.store.PlanAndOperationStateIfExists(key); err == nil && exists {
			opState = session.NormalizeOperationState(stored)
		}
	}
	if operationStateRecoverableForBudgetRecovery(opState) {
		arbitrationAt := turnBudgetRecoveryArbitrationTime(msg)
		if decision := r.operationRecoveryCandidateArbitration(key, msg, opState, arbitrationAt); !decision.Live {
			r.recordSuppressedRecoveryCandidate(key, opState, decision, "budget_recovery", arbitrationAt)
			scope, payload := turnBudgetRecoveryRequestScope(key, msg)
			payload["recovery_arbitration"] = "use_current_request"
			for field, value := range recoveryCandidateSuppressedPayload(opState, decision, "budget_recovery") {
				payload[field] = value
			}
			return scope, payload
		}
		if phase, index, ok := currentOperationPhaseForBudgetRecovery(opState); ok {
			fingerprint := operationPhaseFingerprint(opState, phase, index)
			scope := "operation:" + firstNonEmptyContinuation(opState.ID, turnBudgetRecoveryShortHash(opState.Objective)) +
				":phase:" + firstNonEmptyContinuation(phase.ID, fmt.Sprintf("%d", index+1)) +
				":authority:" + strings.TrimSpace(phase.AuthorityClass) +
				":fingerprint:" + fingerprint
			return scope, map[string]any{
				"operation_id":        strings.TrimSpace(opState.ID),
				"operation_objective": strings.TrimSpace(opState.Objective),
				"phase_id":            strings.TrimSpace(phase.ID),
				"phase_index":         index + 1,
				"authority_class":     strings.TrimSpace(phase.AuthorityClass),
				"phase_fingerprint":   fingerprint,
			}
		}
		if opState.Proposal.Active() {
			scope := "operation:" + firstNonEmptyContinuation(opState.ID, turnBudgetRecoveryShortHash(opState.Objective)) +
				":proposal:" + strings.TrimSpace(opState.Proposal.ID) +
				":kind:" + strings.TrimSpace(opState.Proposal.Kind) +
				":effect:" + turnBudgetRecoveryShortHash(opState.Proposal.BoundedEffect)
			return scope, map[string]any{
				"operation_id":        strings.TrimSpace(opState.ID),
				"operation_objective": strings.TrimSpace(opState.Objective),
				"proposal_id":         strings.TrimSpace(opState.Proposal.ID),
				"proposal_kind":       strings.TrimSpace(opState.Proposal.Kind),
			}
		}
		scope := "operation:" + firstNonEmptyContinuation(opState.ID, turnBudgetRecoveryShortHash(opState.Objective)) +
			":objective:" + turnBudgetRecoveryShortHash(opState.Objective)
		return scope, map[string]any{
			"operation_id":        strings.TrimSpace(opState.ID),
			"operation_objective": strings.TrimSpace(opState.Objective),
		}
	}

	cont := session.ContinuationState{}
	if r != nil && r.store != nil {
		if stored, exists, err := r.store.ContinuationStateIfExists(key); err == nil && exists {
			cont = session.NormalizeContinuationState(stored)
		}
	}
	if cont.Active() {
		leaseID := firstNonEmptyContinuation(cont.ContinuationLease.ID, cont.ActionProposal.ID, cont.DecisionID)
		scope := "continuation:" + firstNonEmptyContinuation(leaseID, turnBudgetRecoveryShortHash(cont.ActionProposal.Summary)) +
			":risk:" + strings.TrimSpace(cont.ActionProposal.RiskClass)
		return scope, map[string]any{
			"decision_id":        strings.TrimSpace(cont.DecisionID),
			"action_proposal_id": strings.TrimSpace(cont.ActionProposal.ID),
			"lease_id":           strings.TrimSpace(cont.ContinuationLease.ID),
			"risk_class":         strings.TrimSpace(cont.ActionProposal.RiskClass),
		}
	}

	return turnBudgetRecoveryRequestScope(key, msg)
}

func turnBudgetRecoveryRequestScope(key session.SessionKey, msg core.InboundMessage) (string, map[string]any) {
	scope := "request:" + turnBudgetRecoveryShortHash(fmt.Sprintf("%d:%d:%s", key.ChatID, msg.SenderID, strings.TrimSpace(msg.Text)))
	return scope, map[string]any{"request_hash": strings.TrimPrefix(scope, "request:")}
}

func turnBudgetRecoveryArbitrationTime(msg core.InboundMessage) time.Time {
	if !msg.Timestamp.IsZero() {
		return msg.Timestamp.UTC()
	}
	return time.Now().UTC()
}

func operationStateRecoverableForBudgetRecovery(opState session.OperationState) bool {
	opState = session.NormalizeOperationState(opState)
	if !opState.Active() {
		return false
	}
	switch opState.Status {
	case session.OperationStatusActive, session.OperationStatusBlocked:
		return true
	case session.OperationStatusCompleted, session.OperationStatusFailed, session.OperationStatusIdle:
		return false
	case "":
		if _, _, ok := currentOperationPhaseForBudgetRecovery(opState); ok {
			return true
		}
		return operationProposalRecoverableForBudgetRecovery(opState.Proposal)
	default:
		return false
	}
}

func operationProposalRecoverableForBudgetRecovery(proposal session.OperationProposal) bool {
	if !proposal.Active() {
		return false
	}
	switch proposal.Status {
	case "", session.ProposalStatusPending, session.ProposalStatusApproved:
		return true
	default:
		return false
	}
}

func operationPhaseRecoverableForBudgetRecovery(phase session.OperationPhase) bool {
	if strings.TrimSpace(phase.ID) == "" && strings.TrimSpace(phase.Summary) == "" {
		return false
	}
	switch phase.Status {
	case "", session.PlanStatusPending, session.PlanStatusInProgress:
		return true
	default:
		return false
	}
}

func currentOperationPhaseForBudgetRecovery(opState session.OperationState) (session.OperationPhase, int, bool) {
	opState = session.NormalizeOperationState(opState)
	plan := opState.PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, 0, false
	}
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for i, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				if operationPhaseRecoverableForBudgetRecovery(phase) {
					return phase, i, true
				}
				break
			}
		}
	}
	for _, status := range []session.PlanStatus{session.PlanStatusInProgress, session.PlanStatusPending, ""} {
		for i, phase := range plan.Phases {
			if phase.Status == status && operationPhaseRecoverableForBudgetRecovery(phase) {
				return phase, i, true
			}
		}
	}
	return session.OperationPhase{}, 0, false
}

func turnBudgetRecoveryPayload(recovery *core.TurnRecovery, scope string, scopePayload map[string]any, hop int, maxHops int) map[string]any {
	payload := map[string]any{
		"recovery_scope": strings.TrimSpace(scope),
		"recovery_hop":   hop,
		"max_auto_hops":  maxHops,
	}
	if recovery != nil {
		payload["recovery_kind"] = string(recovery.Kind)
		if summary := strings.TrimSpace(recovery.Summary); summary != "" {
			payload["summary"] = summary
		}
	}
	for key, value := range scopePayload {
		if strings.TrimSpace(key) != "" {
			payload[key] = value
		}
	}
	return payload
}

func appendTurnBudgetRecoveryDigestPayload(payload map[string]any, digest turnBudgetRecoveryDigest) {
	if payload == nil || len(digest.Lines) == 0 {
		return
	}
	data := map[string]any{
		"lines": append([]string(nil), digest.Lines...),
	}
	if digest.RunID > 0 {
		data["run_id"] = digest.RunID
	}
	payload["recovery_digest"] = data
}

func (r *Runtime) turnBudgetRecoveryDigest(key session.SessionKey, runID int64) turnBudgetRecoveryDigest {
	digest := turnBudgetRecoveryDigest{}
	if r == nil || r.store == nil {
		return digest
	}
	if runID > 0 {
		if run, err := r.store.TurnRun(runID); err == nil && run != nil {
			digest.addTurnRun(*run)
		}
	}
	if digest.RunID == 0 {
		if run, err := r.store.LatestTurnRun(key); err == nil && run != nil {
			digest.addTurnRun(*run)
			runID = run.ID
		}
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 40)
	if err == nil {
		digest.addEvents(events, runID)
	}
	if len(digest.Lines) > turnBudgetRecoveryDigestLines {
		digest.Lines = digest.Lines[:turnBudgetRecoveryDigestLines]
	}
	return digest
}

func (d *turnBudgetRecoveryDigest) addTurnRun(run session.TurnRun) {
	if d == nil || run.ID <= 0 {
		return
	}
	d.RunID = run.ID
	summary := fmt.Sprintf("turn_run=%d kind=%s status=%s tool_calls=%d/%d", run.ID, strings.TrimSpace(string(run.Kind)), strings.TrimSpace(string(run.Status)), run.ToolCallsFinished, run.ToolCallsStarted)
	metrics := []string{}
	if run.TotalToolCharsIn > 0 {
		metrics = append(metrics, fmt.Sprintf("tool_chars=%d", run.TotalToolCharsIn))
	}
	if run.TotalAssistantCharsOut > 0 {
		metrics = append(metrics, fmt.Sprintf("assistant_chars=%d", run.TotalAssistantCharsOut))
	}
	if run.ProviderInputTokens > 0 || run.ProviderOutputTokens > 0 {
		metrics = append(metrics, fmt.Sprintf("tokens=%d/%d", run.ProviderInputTokens, run.ProviderOutputTokens))
	}
	if run.ProviderCacheReadTokens > 0 || run.ProviderCacheWriteTokens > 0 {
		metrics = append(metrics, fmt.Sprintf("cache=%d/%d", run.ProviderCacheReadTokens, run.ProviderCacheWriteTokens))
	}
	if len(metrics) > 0 {
		summary += " " + strings.Join(metrics, " ")
	}
	d.addLine(summary, 220)
	if tool := redactRuntimeText(run.LastToolName, 80); tool != "" {
		line := "last_tool=" + tool
		if preview := redactRuntimeText(run.LastToolPreview, 140); preview != "" {
			line += " input=" + fmt.Sprintf("%q", preview)
		}
		d.addLine(line, 240)
	}
	if result := redactRuntimeText(run.LastToolResultPreview, 180); result != "" {
		d.addLine("last_tool_result="+fmt.Sprintf("%q", result), 240)
	}
	if errText := redactRuntimeText(run.LastToolError, 180); errText != "" {
		d.addLine("last_tool_error="+fmt.Sprintf("%q", errText), 240)
	}
}

func (d *turnBudgetRecoveryDigest) addEvents(events []session.ExecutionEvent, runID int64) {
	if d == nil || len(events) == 0 {
		return
	}
	added := 0
	for i := len(events) - 1; i >= 0; i-- {
		if added >= turnBudgetRecoveryDigestEvents {
			break
		}
		event := events[i]
		if !turnBudgetRecoveryDigestEventRelevant(event.EventType) {
			continue
		}
		payload := map[string]any{}
		if strings.TrimSpace(event.PayloadJSON) != "" {
			_ = json.Unmarshal([]byte(event.PayloadJSON), &payload)
		}
		if runID > 0 {
			if eventRunID, ok := payloadInt64(payload, "run_id"); ok && eventRunID > 0 && eventRunID != runID {
				continue
			}
		}
		if line := turnBudgetRecoveryDigestEventLine(event, payload); line != "" {
			d.addLine(line, 240)
			added++
		}
	}
}

func (d *turnBudgetRecoveryDigest) addLine(line string, limit int) {
	if d == nil || len(d.Lines) >= turnBudgetRecoveryDigestLines {
		return
	}
	line = redactRuntimeText(line, limit)
	if line == "" {
		return
	}
	for _, existing := range d.Lines {
		if existing == line {
			return
		}
	}
	d.Lines = append(d.Lines, line)
}

func turnBudgetRecoveryDigestEventRelevant(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventToolStarted,
		core.ExecutionEventToolSucceeded,
		core.ExecutionEventToolFailed,
		core.ExecutionEventToolBatchStarted,
		core.ExecutionEventToolBatchCompleted,
		core.ExecutionEventModelRequestStarted,
		core.ExecutionEventModelRequestSucceeded,
		core.ExecutionEventModelRequestFailed,
		core.ExecutionEventProviderAttemptStarted,
		core.ExecutionEventProviderAttemptRetried,
		core.ExecutionEventProviderAttemptSucceeded,
		core.ExecutionEventProviderAttemptFailed,
		core.ExecutionEventProviderPartial:
		return true
	default:
		return false
	}
}

func turnBudgetRecoveryDigestEventLine(event session.ExecutionEvent, payload map[string]any) string {
	eventType := strings.TrimSpace(event.EventType)
	parts := []string{fmt.Sprintf("event#%d %s", event.Seq, eventType)}
	if status := strings.TrimSpace(event.Status); status != "" {
		parts = append(parts, "status="+status)
	}
	switch eventType {
	case core.ExecutionEventToolStarted:
		appendBudgetDigestPayloadPart(&parts, "tool", payloadString(payload, "tool"), 80)
		appendBudgetDigestPayloadPart(&parts, "input", payloadString(payload, "preview"), 120)
	case core.ExecutionEventToolSucceeded, core.ExecutionEventToolFailed:
		appendBudgetDigestPayloadPart(&parts, "tool", payloadString(payload, "tool"), 80)
		if digest, ok := payloadMap(payload, "result_digest"); ok {
			appendBudgetDigestPayloadPart(&parts, "result_digest", turnBudgetRecoveryDigestSummary(digest), 180)
		} else {
			appendBudgetDigestPayloadPart(&parts, "result", payloadString(payload, "result_preview"), 140)
		}
		appendBudgetDigestPayloadPart(&parts, "error", payloadString(payload, "error"), 140)
	case core.ExecutionEventToolBatchStarted, core.ExecutionEventToolBatchCompleted:
		appendBudgetDigestPayloadPart(&parts, "mode", payloadString(payload, "mode"), 40)
		if size, ok := payloadInt64(payload, "batch_size"); ok && size > 0 {
			parts = append(parts, fmt.Sprintf("batch_size=%d", size))
		}
		if failed, ok := payloadInt64(payload, "failed_count"); ok && failed > 0 {
			parts = append(parts, fmt.Sprintf("failed=%d", failed))
		}
		if tools := payloadStringSlice(payload, "tools"); len(tools) > 0 {
			appendBudgetDigestPayloadPart(&parts, "tools", strings.Join(tools, ","), 120)
		}
	case core.ExecutionEventModelRequestStarted:
		if attempt, ok := payloadInt64(payload, "attempt"); ok && attempt > 0 {
			parts = append(parts, fmt.Sprintf("attempt=%d", attempt))
		}
		if estimated, ok := payloadInt64(payload, "estimated_input_tokens"); ok && estimated > 0 {
			parts = append(parts, fmt.Sprintf("estimated_input=%d", estimated))
		}
		if history, ok := payloadInt64(payload, "history_count"); ok && history > 0 {
			parts = append(parts, fmt.Sprintf("history=%d", history))
		}
	case core.ExecutionEventModelRequestSucceeded, core.ExecutionEventModelRequestFailed:
		if attempt, ok := payloadInt64(payload, "attempt"); ok && attempt > 0 {
			parts = append(parts, fmt.Sprintf("attempt=%d", attempt))
		}
		if calls, ok := payloadInt64(payload, "tool_call_count"); ok && calls > 0 {
			parts = append(parts, fmt.Sprintf("tool_calls=%d", calls))
		}
		if out, ok := payloadInt64(payload, "output_chars"); ok && out > 0 {
			parts = append(parts, fmt.Sprintf("output_chars=%d", out))
		}
		appendBudgetDigestPayloadPart(&parts, "failure", payloadString(payload, "failure_kind"), 80)
		appendBudgetDigestPayloadPart(&parts, "error", payloadString(payload, "error"), 140)
		appendBudgetDigestTokenParts(&parts, payload)
	case core.ExecutionEventProviderAttemptStarted, core.ExecutionEventProviderAttemptRetried, core.ExecutionEventProviderAttemptSucceeded, core.ExecutionEventProviderAttemptFailed, core.ExecutionEventProviderPartial:
		appendBudgetDigestPayloadPart(&parts, "provider", payloadString(payload, "provider"), 80)
		appendBudgetDigestPayloadPart(&parts, "model", payloadString(payload, "model"), 80)
		appendBudgetDigestPayloadPart(&parts, "failure", payloadString(payload, "failure_kind"), 80)
		appendBudgetDigestPayloadPart(&parts, "error", payloadString(payload, "error"), 140)
		appendBudgetDigestTokenParts(&parts, payload)
	}
	return strings.Join(parts, " ")
}

func appendBudgetDigestPayloadPart(parts *[]string, key string, value string, limit int) {
	if parts == nil {
		return
	}
	value = redactRuntimeText(value, limit)
	if value == "" {
		return
	}
	*parts = append(*parts, key+"="+fmt.Sprintf("%q", value))
}

func payloadMap(payload map[string]any, key string) (map[string]any, bool) {
	if len(payload) == 0 {
		return nil, false
	}
	raw, ok := payload[strings.TrimSpace(key)]
	if !ok || raw == nil {
		return nil, false
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return typed, true
}

func turnBudgetRecoveryDigestSummary(digest map[string]any) string {
	if len(digest) == 0 {
		return ""
	}
	parts := []string{}
	if value := payloadString(digest, "sha256"); value != "" {
		parts = append(parts, "sha256="+value)
	}
	if value := payloadString(digest, "evidence_ref"); value != "" {
		parts = append(parts, "evidence_ref="+value)
	}
	if value := payloadString(digest, "bytes"); value != "" {
		parts = append(parts, "bytes="+value)
	}
	if value := payloadString(digest, "lines"); value != "" {
		parts = append(parts, "lines="+value)
	}
	if value := payloadString(digest, "omitted_bytes"); value != "" {
		parts = append(parts, "omitted_bytes="+value)
	}
	return strings.Join(parts, " ")
}

func appendBudgetDigestTokenParts(parts *[]string, payload map[string]any) {
	if parts == nil {
		return
	}
	if total, ok := payloadInt64(payload, "total_tokens"); ok && total > 0 {
		*parts = append(*parts, fmt.Sprintf("total_tokens=%d", total))
		return
	}
	input, inputOK := payloadInt64(payload, "input_tokens")
	output, outputOK := payloadInt64(payload, "output_tokens")
	if inputOK || outputOK {
		*parts = append(*parts, fmt.Sprintf("tokens=%d/%d", input, output))
	}
}

func renderTurnBudgetRecoveryPrompt(recovery *core.TurnRecovery, scope string, hop int, maxHops int, digest turnBudgetRecoveryDigest) string {
	card := newContinuationApprovalPromptCard("Recover", fmt.Sprintf("budget hop %d/%d", hop, maxHops), 0)
	card.addListSection("Re-check", []string{
		"Reconcile the current input and working objective against persisted operation, continuation, plan, recent execution events, and evidence artifacts.",
		"Treat completed or failed operations as background evidence only; do not recover into terminal work.",
		"Do not rely on transient context from the exhausted response; discard pending tool calls and re-decide from durable state.",
		"Read the smallest current-state slice needed before acting; avoid broad logs, large artifacts, or repeated full-file sweeps.",
	})
	card.addListSection("Still allowed", []string{
		"Continue only work still needed inside the same objective, phase, authority class, and bounded effect.",
		"If the same scope keeps exhausting budget, rescope to a smaller next action instead of raw retry.",
		"Return a compact final response as soon as enough evidence is gathered.",
	})
	if recovery != nil {
		if recovery.Kind != "" {
			card.addSection("Reason", string(recovery.Kind))
		}
		if summary := strings.TrimSpace(recovery.Summary); summary != "" {
			card.addSection("Previous", summary)
		}
	}
	if scope = strings.TrimSpace(scope); scope != "" {
		card.addSection("Scope", scope)
	}
	if len(digest.Lines) > 0 {
		card.addListSectionWithLimit("Evidence digest", digest.Lines, turnBudgetRecoveryDigestLines, 220)
	}
	return card.String()
}

func turnBudgetRecoveryBlockedText(recovery *core.TurnRecovery, maxHops int, reason string, decision recoveryDecision) string {
	label := "execution"
	if recovery != nil {
		switch recovery.Kind {
		case core.TurnRecoveryTokenBudgetExhausted:
			label = "token"
		case core.TurnRecoveryToolBudgetExhausted:
			label = "tool-call"
		case core.TurnRecoveryIterationBudgetExhausted:
			label = "iteration"
		}
	}
	switch strings.TrimSpace(reason) {
	case "actor_unavailable":
		return "I ran out of " + label + " room before I could finish. I could not safely identify who approved the work, so I stopped before continuing."
	case "runtime_shutting_down":
		return "I ran out of " + label + " room before I could finish. The service is shutting down, so I stopped before continuing."
	case "retry_counter_unavailable":
		return "I ran out of " + label + " room before I could finish. I could not verify the retry state safely, so I stopped before continuing."
	case "recovery_turn_failed":
		return appendRecoveryDecisionVisibleText("I ran out of "+label+" room before I could finish, and I could not complete the recovery check cleanly.", decision)
	default:
		return appendRecoveryDecisionVisibleText(fmt.Sprintf("I ran out of %s room repeatedly before I could finish.\n\nI stopped after %d recovery attempts. Please ask for a narrower next step or approve a smaller phase.", label, maxHops), decision)
	}
}

func appendRecoveryDecisionVisibleText(text string, decision recoveryDecision) string {
	if next := recoveryDecisionVisibleText(decision); next != "" {
		return strings.TrimSpace(text) + "\n\n" + next
	}
	return text
}

func turnBudgetRecoveryShortHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:16]
}
