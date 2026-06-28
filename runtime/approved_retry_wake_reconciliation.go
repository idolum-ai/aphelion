//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func (r *Runtime) reconcileApprovedRetryWakeWaitersForSession(key session.SessionKey) error {
	if r == nil || r.store == nil {
		return nil
	}
	waiters, err := r.store.OpenNextActionsBySessionOperation(key, session.NextActionWaitingForChild, "durable_agent", "durable_agent_wake_once", 50)
	if err != nil {
		return err
	}
	for _, waiter := range waiters {
		if strings.TrimSpace(waiter.Owner) != "approved_retry" {
			continue
		}
		leaseID := continuationLeaseIDFromCausalRefs(waiter.CausalRefs)
		if leaseID == "" {
			continue
		}
		packetID, childResult, ok, err := r.approvedRetryWakeTerminalChildResultForLease(leaseID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := r.recordApprovedRetryWakeTerminalChildResultForAction(waiter, leaseID, packetID, childResult, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func continuationLeaseIDFromCausalRefs(refs []string) string {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(ref, "continuation:") {
			return strings.TrimSpace(strings.TrimPrefix(ref, "continuation:"))
		}
	}
	return ""
}

func (r *Runtime) approvedRetryWakeTerminalChildResultForLease(leaseID string) (string, session.ChildTaskResult, bool, error) {
	if r == nil || r.store == nil {
		return "", session.ChildTaskResult{}, false, nil
	}
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return "", session.ChildTaskResult{}, false, nil
	}
	claim, ok, err := r.store.DurableAgentWakeClaimByLeaseID(leaseID)
	if err != nil {
		return "", session.ChildTaskResult{}, false, err
	}
	if !ok || strings.TrimSpace(claim.ClaimID) == "" {
		return "", session.ChildTaskResult{}, false, nil
	}
	packetID := durableWakeTaskPacketIDForWakeClaim(claim.AgentID, claim.MessageIDs, claim.ClaimID)
	packet, ok, err := r.store.ChildTaskPacket(packetID)
	if err != nil {
		return "", session.ChildTaskResult{}, false, err
	}
	if !ok || !session.ChildTaskPacketStatusTerminal(packet.Status) || strings.TrimSpace(packet.ResultID) == "" {
		return "", session.ChildTaskResult{}, false, nil
	}
	result, ok, err := r.store.ChildTaskResult(packet.ResultID)
	if err != nil {
		return "", session.ChildTaskResult{}, false, err
	}
	if !ok || result.Status == session.ChildTaskResultUpdate {
		return "", session.ChildTaskResult{}, false, nil
	}
	return packetID, result, true, nil
}

func (r *Runtime) recordApprovedRetryWakeTerminalChildResultForAction(action session.NextActionRecord, leaseID string, packetID string, result session.ChildTaskResult, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := session.SessionKey{ChatID: action.ChatID, UserID: action.UserID, Scope: action.Scope}
	agentID := strings.TrimSpace(result.AgentID)
	var agent core.DurableAgent
	if agentID != "" {
		if loaded, err := r.store.DurableAgent(agentID); err == nil && loaded != nil {
			agent = *loaded
		} else if err != nil {
			return err
		}
	}
	if strings.TrimSpace(agent.AgentID) == "" {
		agent.AgentID = agentID
	}

	switch result.Status {
	case session.ChildTaskResultCompleted:
		if err := r.updateApprovedRetryOperationFromChildResult(key, result, "approved_retry_child_completed", session.OperationStatusCompleted, now); err != nil {
			return err
		}
		return r.store.ResolveNextAction(session.NextActionResolutionInput{
			Key:         key,
			RecordID:    action.RecordID,
			Owner:       "approved_retry",
			SubjectKind: action.SubjectKind,
			SubjectRef:  action.SubjectRef,
			Reason:      "durable_child_task_completed",
			ResolvedAt:  now,
		})
	default:
		return r.recordApprovedRetryChildResultBlocker(key, action, agent, result, packetID, leaseID, now)
	}
}

func (r *Runtime) recordApprovedRetryWakeTerminalChildResult(key session.SessionKey, reservation approvedContinuationReservation, retry session.ContinuationRetryOperation, wakeResult toolpkg.DurableAgentWakeOnceRenderedResult, packetID string, result session.ChildTaskResult, monitor *turnMonitor) error {
	now := time.Now().UTC()
	subjectKind, subjectRef := approvedRetrySubject(retry, wakeResult)
	action := session.NextActionRecord{
		ChatID:        key.ChatID,
		UserID:        key.UserID,
		Scope:         key.Scope,
		Owner:         "approved_retry",
		State:         session.NextActionWaitingForChild,
		SubjectKind:   subjectKind,
		SubjectRef:    subjectRef,
		CausalRefs:    approvedRetryCausalRefs(reservation, retry, wakeResult),
		OperationKind: "durable_agent_wake_once",
		OperationTool: strings.TrimSpace(retry.Tool),
	}
	if monitor != nil {
		action.TurnRunID = monitor.runID
	}
	return r.recordApprovedRetryWakeTerminalChildResultForAction(action, reservation.State.ContinuationLease.ID, packetID, result, now)
}

func (r *Runtime) recordApprovedRetryChildResultBlocker(key session.SessionKey, action session.NextActionRecord, agent core.DurableAgent, result session.ChildTaskResult, packetID string, leaseID string, now time.Time) error {
	input := session.ChildTaskResultInput{
		ResultID:     result.ResultID,
		PacketID:     result.PacketID,
		AttemptID:    result.AttemptID,
		AgentID:      result.AgentID,
		Key:          key,
		Status:       result.Status,
		ResultKind:   result.ResultKind,
		Summary:      result.Summary,
		BlockerKind:  result.BlockerKind,
		ErrorText:    result.ErrorText,
		EvidenceRefs: result.EvidenceRefs,
		NextState:    result.NextState,
		CreatedAt:    result.CreatedAt,
	}
	classification := durableWakeChildTaskBlockerClassification(agent, input)
	state := classification.State
	if state == "" {
		state = result.NextState
	}
	if state == "" {
		state = session.NextActionBlockedNeedsResourceRepair
	}
	nextAction := classification.NextAction
	if strings.TrimSpace(nextAction) == "" {
		nextAction = "repair the child task blocker before retrying"
	}
	retryPolicy := classification.RetryPolicy
	if strings.TrimSpace(retryPolicy) == "" {
		retryPolicy = "retry_after_blocker_resolution"
	}
	if err := r.updateApprovedRetryOperationFromChildResult(key, result, "approved_retry_child_blocked", session.OperationStatusBlocked, now); err != nil {
		return err
	}
	causalRefs := append([]string(nil), action.CausalRefs...)
	for _, ref := range []string{
		"continuation:" + strings.TrimSpace(leaseID),
		"task_packet:" + strings.TrimSpace(packetID),
		"child_task_result:" + strings.TrimSpace(result.ResultID),
		"durable_agent:" + strings.TrimSpace(result.AgentID),
	} {
		if strings.TrimSpace(ref) != "" && !stringSliceContains(causalRefs, ref) {
			causalRefs = append(causalRefs, ref)
		}
	}
	operatorProjection := strings.TrimSpace(classification.OperatorProjection)
	if operatorProjection == "" {
		operatorProjection = strings.TrimSpace(result.Summary)
	}
	if operatorProjection == "" {
		operatorProjection = "The approved child wake ran, but the child reported a blocker."
	}
	_, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		TurnRunID:          action.TurnRunID,
		Owner:              "approved_retry",
		State:              state,
		SubjectKind:        action.SubjectKind,
		SubjectRef:         action.SubjectRef,
		CausalRefs:         causalRefs,
		NextAction:         nextAction,
		RequiredAuthority:  classification.RequiredAuthority,
		ResourceBlocker:    firstNonEmpty(strings.TrimSpace(classification.ResourceBlocker), strings.TrimSpace(result.BlockerKind)),
		Verifier:           classification.Verifier,
		RetryPolicy:        retryPolicy,
		OperationKind:      classification.OperationKind,
		OperationTool:      classification.OperationTool,
		OperationInputJSON: classification.OperationInputJSON,
		OperatorProjection: operatorProjection,
		CreatedAt:          now,
	})
	if err != nil {
		return fmt.Errorf("record approved retry child result blocker: %w", err)
	}
	return nil
}

func (r *Runtime) updateApprovedRetryOperationFromChildResult(key session.SessionKey, result session.ChildTaskResult, stage string, status session.OperationStatus, now time.Time) error {
	opState, err := r.store.OperationState(key)
	if err != nil {
		return nil
	}
	opState = session.NormalizeOperationState(opState)
	opState.Status = status
	opState.Stage = strings.TrimSpace(stage)
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = strings.TrimSpace(result.BlockerKind)
	}
	switch status {
	case session.OperationStatusCompleted:
		opState.Summary = "Approved child wake retry completed."
	case session.OperationStatusBlocked:
		opState.Summary = "Approved child wake retry reached the child, but the child reported a blocker."
		if summary != "" {
			opState.Summary += " " + summary
		}
	default:
		opState.Summary = summary
	}
	opState.Proposal.Status = session.ProposalStatusApproved
	opState.Proposal.UpdatedAt = now
	opState.UpdatedAt = now
	return r.store.UpdateOperationState(key, opState)
}
