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
)

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

	payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, nextHop, maxHops)
	appendTokenUsagePayload(payload, req.Result.Turn.TokenUsage)
	p.runtime.recordExecutionEvent(p.key, core.ExecutionEventTurnBudgetRecovery, "turn", "scheduled", payload, time.Now().UTC())
	if p.audit != nil {
		p.audit.RecordFinalReply("", nil, "budget_recovery_scheduled")
	}
	p.runtime.scheduleTurnBudgetRecoveryContinuation(p.key, p.msg, actor, recovery, scope, nextHop, maxHops)
	return &turn.DeliveryResult{Kind: "budget_recovery_scheduled"}, nil
}

func (p *turnDeliveryPort) deliverBudgetRecoveryBlocked(ctx context.Context, req turn.DeliveryRequest, recovery *core.TurnRecovery, scope string, scopePayload map[string]any, maxHops int, reason string, cause error) (*turn.DeliveryResult, error) {
	payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, 0, maxHops)
	payload["reason"] = strings.TrimSpace(reason)
	if cause != nil {
		payload["error"] = trimError(cause.Error())
	}
	if req.Result != nil && req.Result.Turn != nil {
		appendTokenUsagePayload(payload, req.Result.Turn.TokenUsage)
	}
	p.runtime.recordExecutionEvent(p.key, core.ExecutionEventTurnBudgetRecovery, "turn", "blocked", payload, time.Now().UTC())

	text := turnBudgetRecoveryBlockedText(recovery, maxHops, reason)
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

func (r *Runtime) scheduleTurnBudgetRecoveryContinuation(key session.SessionKey, msg core.InboundMessage, actor principal.Principal, recovery *core.TurnRecovery, scope string, hop int, maxHops int) {
	if r == nil {
		return
	}
	r.backgroundLoopsWG.Add(1)
	go func() {
		defer r.backgroundLoopsWG.Done()
		runCtx, cancel := context.WithTimeout(context.Background(), turnBudgetRecoveryTimeout)
		defer cancel()
		if err := r.runTurnBudgetRecoveryContinuation(runCtx, key, msg, actor, recovery, scope, hop, maxHops); err != nil {
			log.Printf("WARN turn budget recovery failed chat_id=%d scope=%s hop=%d err=%v", key.ChatID, scope, hop, err)
		}
	}()
}

func (r *Runtime) runTurnBudgetRecoveryContinuation(ctx context.Context, key session.SessionKey, msg core.InboundMessage, actor principal.Principal, recovery *core.TurnRecovery, scope string, hop int, maxHops int) error {
	if r == nil {
		return nil
	}
	prompt := renderTurnBudgetRecoveryPrompt(recovery, scope, hop, maxHops)
	recoveryMsg := continuationInboundForKey(key, actor, prompt, core.InboundOriginTurnAuthorization, turnBudgetRecoveryOriginDetail)
	recoveryMsg.ChatType = msg.ChatType
	recoveryMsg.ChatTitle = msg.ChatTitle
	recoveryMsg.MessageID = msg.MessageID
	recoveryMsg.ReplyTo = msg.ReplyTo
	recoveryMsg.DurableAgentID = msg.DurableAgentID
	recoveryMsg.Timestamp = time.Now().UTC()

	payload := turnBudgetRecoveryPayload(recovery, scope, nil, hop, maxHops)
	r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "resuming", payload, time.Now().UTC())
	result, err := r.handleInternalContinuation(ctx, actor, recoveryMsg)
	if err != nil {
		payload["error"] = trimError(err.Error())
		r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "failed", payload, time.Now().UTC())
		r.notifyTurnBudgetRecoveryFailure(ctx, msg, recovery, maxHops, err)
		return err
	}
	if result != nil && result.Recovery != nil {
		payload["result_recovery_kind"] = string(result.Recovery.Kind)
	}
	r.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "resumed", payload, time.Now().UTC())
	return nil
}

func (r *Runtime) notifyTurnBudgetRecoveryFailure(ctx context.Context, msg core.InboundMessage, recovery *core.TurnRecovery, maxHops int, err error) {
	if r == nil || r.outbound == nil || msg.ChatID == 0 || r.isShuttingDown() {
		return
	}
	text := turnBudgetRecoveryBlockedText(recovery, maxHops, "recovery_turn_failed")
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

func (r *Runtime) turnBudgetRecoveryScope(key session.SessionKey, msg core.InboundMessage, result *turn.Result) (string, map[string]any) {
	opState := session.OperationState{}
	if result != nil {
		opState = result.OperationState
	}
	if !opState.Active() && r != nil && r.store != nil {
		if _, stored, exists, err := r.store.PlanAndOperationStateIfExists(key); err == nil && exists {
			opState = stored
		}
	}
	opState = session.NormalizeOperationState(opState)
	if opState.Active() {
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

	scope := "request:" + turnBudgetRecoveryShortHash(fmt.Sprintf("%d:%d:%s", key.ChatID, msg.SenderID, strings.TrimSpace(msg.Text)))
	return scope, map[string]any{
		"request_hash": strings.TrimPrefix(scope, "request:"),
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
				return phase, i, true
			}
		}
	}
	for _, status := range []session.PlanStatus{session.PlanStatusInProgress, session.PlanStatusPending, ""} {
		for i, phase := range plan.Phases {
			if phase.Status == status {
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

func renderTurnBudgetRecoveryPrompt(recovery *core.TurnRecovery, scope string, hop int, maxHops int) string {
	card := newContinuationApprovalPromptCard("Recover", fmt.Sprintf("budget hop %d/%d", hop, maxHops), 0)
	card.addListSection("Re-check", []string{
		"Re-evaluate persisted operation, session, plan, and continuation state before choosing the next action.",
		"Any tool use from current durable state; do not replay pending calls from the exhausted response.",
	})
	card.addListSection("Still allowed", []string{
		"Continue only work still needed inside the same objective, phase, authority class, and bounded effect.",
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
	return card.String()
}

func turnBudgetRecoveryBlockedText(recovery *core.TurnRecovery, maxHops int, reason string) string {
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
		return "I hit the " + label + " budget before a final response, but I could not safely resolve the approved operator for an automatic recovery turn."
	case "runtime_shutting_down":
		return "I hit the " + label + " budget before a final response, and the service is shutting down before an automatic recovery turn can start."
	case "retry_counter_unavailable":
		return "I hit the " + label + " budget before a final response, but I could not read the recovery counter safely."
	case "recovery_turn_failed":
		return "I hit the " + label + " budget before a final response, and the automatic recovery turn failed."
	default:
		return fmt.Sprintf("I hit the %s budget repeatedly before I could produce a final response.\n\nI stopped after %d automatic recovery attempts in the same work scope. Please ask for a narrower next step or approve a smaller phase.", label, maxHops)
	}
}

func turnBudgetRecoveryShortHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:16]
}
