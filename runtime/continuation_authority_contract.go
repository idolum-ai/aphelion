//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationAuthorityCompilation(state session.ContinuationState) session.AuthorityContractCompilation {
	return session.CompileContinuationAuthorityContract(state)
}

func continuationAuthorityContractInvalidReason(compilation session.AuthorityContractCompilation) string {
	return session.AuthorityContractCompilationSummary(compilation)
}

func continuationStateWithInvalidAuthorityContract(state session.ContinuationState, compilation session.AuthorityContractCompilation, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	reason := continuationAuthorityContractInvalidReason(compilation)
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.HandshakeBlockedReason = reason
	state.ParkedAt = now
	state.ParkedReason = reason
	state.ParkedSource = "authority_contract_compiler"
	state.UpdatedAt = now
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusSuperseded
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.RevokedAt = now
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
		state.ApprovalBundle.RevokedAt = now
		state.ApprovalBundle.UpdatedAt = now
		for i := range state.ApprovalBundle.Phases {
			state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusRevoked
		}
	}
	return session.NormalizeContinuationState(state)
}

func (r *Runtime) blockInvalidContinuationAuthorityContract(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, source string, now time.Time, notify bool) (session.ContinuationState, bool, error) {
	compilation := continuationAuthorityCompilation(state)
	if compilation.Valid() {
		return session.NormalizeContinuationState(state), false, nil
	}
	if r == nil || r.store == nil {
		return session.NormalizeContinuationState(state), true, fmt.Errorf("continuation authority contract invalid: %s", continuationAuthorityContractInvalidReason(compilation))
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	blocked := continuationStateWithInvalidAuthorityContract(state, compilation, now)
	if err := r.store.UpdateContinuationState(key, blocked); err != nil {
		return blocked, true, fmt.Errorf("persist invalid continuation authority state: %w", err)
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "authority_contract_compiler"
	}
	payload := continuationExecutionPayload(blocked)
	payload["reason"] = "invalid_authority_contract"
	payload["authority_contract_status"] = string(compilation.Status)
	payload["authority_contract_work_action"] = strings.TrimSpace(compilation.WorkAction)
	payload["authority_contract_suggested_repair"] = strings.TrimSpace(compilation.SuggestedRepair)
	payload["authority_contract_contradictions"] = compilation.Contradictions
	payload["materialization_source"] = source
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           source,
		"subject_id":        strings.TrimSpace(blocked.DecisionID),
		"operator_label":    "Invalid continuation authority blocked",
		"visible_action":    "request_fresh_narrower_proposal",
		"decision":          "blocked_invalid_authority_contract",
		"findings": []core.RuntimeFinding{{
			Kind:             "invalid_authority_contract",
			EvidenceStatus:   "compiled_from_authority_contract",
			Detail:           continuationAuthorityContractInvalidReason(compilation),
			RequiredBehavior: "Do not show approval buttons for a lease whose allowed actions are forbidden by the same authority contract.",
		}},
	}, now)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, now)
	if notify && r.outbound != nil && msg.ChatID != 0 {
		text := r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), renderInvalidContinuationAuthorityContractStatus(blocked, compilation))
		_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: text})
	}
	return blocked, true, nil
}

func (r *Runtime) blockInvalidMaterializedContinuationAuthority(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, state session.ContinuationState, source string, now time.Time) (session.OperationState, bool, error) {
	blockedState, blocked, err := r.blockInvalidContinuationAuthorityContract(ctx, key, msg, state, source, now, true)
	if err != nil || !blocked {
		return session.NormalizeOperationState(opState), blocked, err
	}
	opState = operationStateWithInvalidApprovalCleared(opState, blockedState, now)
	if r != nil && r.store != nil {
		if updateErr := r.store.UpdateOperationState(key, opState); updateErr != nil {
			return opState, true, fmt.Errorf("clear invalid materialized operation authority chat_id=%d: %w", key.ChatID, updateErr)
		}
	}
	return opState, true, nil
}

func renderInvalidContinuationAuthorityContractStatus(state session.ContinuationState, compilation session.AuthorityContractCompilation) string {
	state = session.NormalizeContinuationState(state)
	title := firstNonEmptyContinuation(state.ActionProposal.OperatorTitle, state.ActionProposal.PlanTitle, state.ActionProposal.Summary, state.StageSummary, "Continuation")
	lines := []string{"I can't offer that approval yet.", "", "Plan: " + truncatePreview(title, 96), "", "Reason:", "The authority contract is internally contradictory."}
	if summary := continuationAuthorityContractInvalidReason(compilation); summary != "" && summary != "authority contract valid" {
		lines = append(lines, truncatePreview(summary, 180))
	}
	lines = append(lines, "", "Next:", "Send a fresh narrower proposal that names the intended authority and stop conditions.")
	return strings.Join(lines, "\n")
}
