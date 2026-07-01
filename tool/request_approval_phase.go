//go:build linux

package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func requestApprovalPhaseNeedsContinuationLease(phase session.OperationPhase) bool {
	for _, value := range []string{phase.AuthorityClass, phase.GateReasonCode} {
		if session.NormalizeContinuationLeaseClass(session.ContinuationLeaseClass(value)) == session.ContinuationLeaseClassChildWake {
			return true
		}
	}
	return false
}

func (r *Registry) requestApprovalPhaseContinuationLease(ctx context.Context, in requestApprovalInput, key session.SessionKey, p principal.Principal, phase session.OperationPhase) (string, error) {
	if strings.TrimSpace(phase.Summary) == "" {
		return "", fmt.Errorf("request_approval phase summary is required")
	}
	agentID, grantID, targetResource := requestApprovalPhaseChildWakeTarget(phase)
	principalID := firstNonEmptyTool(requestApprovalPhaseGrantedTo(phase, targetResource), toolAuthorityCanonicalPrincipal(p))
	if inferred, err := r.requestApprovalPhaseChildWakeTargetFromDurableFacts(in, key, p, phase, agentID, grantID, targetResource); err != nil {
		return "", err
	} else if inferred.AgentID != "" {
		if agentID == "" {
			agentID = inferred.AgentID
		}
		if grantID == "" {
			grantID = inferred.GrantID
		}
		if targetResource == "" {
			targetResource = inferred.TargetResource
		}
		if inferred.PrincipalID != "" {
			principalID = inferred.PrincipalID
		}
	}
	if agentID == "" {
		return "", fmt.Errorf("request_approval child_wake phase requires exact durable_agent wake_once target")
	}
	if targetResource == "" {
		targetResource = "durable_agent:" + agentID + ":wake_once"
	}
	principalID = firstNonEmptyTool(requestApprovalPhaseGrantedTo(phase, targetResource), principalID, toolAuthorityCanonicalPrincipal(p))
	if principalID == "" || principalID == "unknown" {
		return "", fmt.Errorf("request_approval child_wake phase requires operator principal")
	}
	allowed := append([]string(nil), phase.AllowedActions...)
	if !requestApprovalStringSliceContains(allowed, durableAgentWakeOnceAction) {
		allowed = append(allowed, durableAgentWakeOnceAction)
	}
	if !requestApprovalStringSliceContains(allowed, "wake_named_child") {
		allowed = append(allowed, "wake_named_child")
	}
	constraints := map[string]string{"agent_id": agentID, "principal": principalID}
	if grantID != "" {
		constraints["grant_id"] = grantID
	}
	if targetResource != "" {
		constraints["grant_target_resource"] = targetResource
		constraints["target_resource"] = targetResource
	}
	retryInput := map[string]any{"action": "wake_once", "agent_id": agentID}
	if guidance := strings.TrimSpace(firstNonEmptyTool(phase.Summary, phase.BoundedEffect, phase.PlanTitle, phase.OperatorTitle)); guidance != "" {
		retryInput["reason"] = guidance
	}
	retryJSON, err := json.Marshal(retryInput)
	if err != nil {
		return "", fmt.Errorf("compile child_wake retry input: %w", err)
	}
	now := time.Now().UTC()
	requestInstanceID := requestApprovalPhaseRequestInstanceID(ctx, key, phase, agentID, now)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   requestInstanceID,
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		Principal:           principalID,
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      allowed,
		Constraints:         constraints,
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             grantID,
		GrantTargetResource: targetResource,
		RetryOperation: session.ContinuationRetryOperation{
			Contract:          session.ContinuationRecoveryRetryVersion,
			OperationKind:     "durable_agent_wake_once",
			Tool:              "durable_agent",
			InputJSON:         string(retryJSON),
			SubjectKind:       "continuation_lease_request",
			RequestInstanceID: requestInstanceID,
		},
		CreatedAt: now,
	})
	if err != nil {
		return "", err
	}
	contract, err = r.store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		return "", fmt.Errorf("store request_approval child_wake contract: %w", err)
	}
	next := in
	next.Action = "request_continuation_lease"
	next.ContractID = contract.ContractID
	if strings.TrimSpace(next.Objective) == "" {
		next.Objective = firstNonEmptyTool(phase.Summary, "Approve one bounded child_wake continuation.")
	}
	return r.requestContinuationLeaseApproval(next, key)
}

func requestApprovalPhaseInferenceText(in requestApprovalInput, phase session.OperationPhase) string {
	parts := []string{
		in.Action,
		in.Objective,
		phase.ID,
		phase.Summary,
		phase.AuthorityClass,
		phase.GateReasonCode,
		phase.OperatorTitle,
		phase.PlanTitle,
		phase.WhyNow,
		phase.BoundedEffect,
	}
	parts = append(parts, phase.AllowedActions...)
	parts = append(parts, phase.ForbiddenActions...)
	parts = append(parts, phase.ValidationPlan...)
	return strings.ToLower(strings.Join(parts, "\n"))
}

func requestApprovalStringSliceContains(items []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}

func requestApprovalPhaseRequestInstanceID(ctx context.Context, key session.SessionKey, phase session.OperationPhase, agentID string, now time.Time) string {
	seedParts := []string{
		session.SessionIDForKey(key),
		"request_approval_phase_child_wake",
		strings.TrimSpace(phase.ID),
		strings.TrimSpace(agentID),
	}
	if ref, ok := ToolInvocationRefFromContext(ctx); ok && strings.TrimSpace(ref.InvocationID) != "" {
		seedParts = append(seedParts, strings.TrimSpace(ref.InvocationID))
	} else {
		if now.IsZero() {
			now = time.Now().UTC()
		}
		seedParts = append(seedParts, now.UTC().Format(time.RFC3339Nano))
	}
	sum := sha256.Sum256([]byte(strings.Join(seedParts, "\x00")))
	return "request-approval-phase-child-wake-" + hex.EncodeToString(sum[:12])
}
