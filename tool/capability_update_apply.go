//go:build linux

package tool

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

type capabilityUpdatePlanApplyResult struct {
	PolicyUpdateApplied bool
	PolicyChanged       bool
	AgentID             string
	PolicyVersion       int64
	PolicyHash          string
	PolicyUpdateID      int64
}

func (r *Registry) applyCapabilityUpdatePlanForGrant(request session.CapabilityRequest, grant session.CapabilityGrant) (*capabilityUpdatePlanApplyResult, error) {
	plan, hasPlan, err := capabilityUpdatePlanFromContract(grant.Contract)
	if err != nil {
		return nil, err
	}
	if !hasPlan || !capabilityUpdatePlanHasDurablePolicyPatch(plan) {
		return nil, nil
	}
	agentID, err := r.resolveCapabilityUpdatePlanAgentID(plan, request, grant)
	if err != nil {
		return nil, err
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return nil, err
	}
	patch := effectiveDurableAgentPolicyPatchFromInput(durableAgentInput{
		PolicyPatch:     plan.PolicyPatch,
		PolicyOverrides: plan.PolicyOverrides,
	})
	policy := agent.LivePolicy
	if err := applyDurableAgentPolicyPatch(&policy, patch); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(plan.Reason)
	if reason == "" {
		reason = fmt.Sprintf("applied capability_update_plan from grant %s for request %s", grant.GrantID, request.RequestID)
	}
	updated, update, err := r.store.ApplyDurableAgentLivePolicy(agent.AgentID, policy, 0, reason)
	if err != nil {
		return nil, err
	}
	result := &capabilityUpdatePlanApplyResult{
		PolicyUpdateApplied: true,
		AgentID:             updated.AgentID,
		PolicyVersion:       updated.PolicyVersion,
		PolicyHash:          updated.PolicyHash,
	}
	if update != nil {
		result.PolicyChanged = true
		result.PolicyUpdateID = update.ID
	}
	return result, nil
}

func (r *Registry) resolveCapabilityUpdatePlanAgentID(plan capabilityUpdatePlanInput, request session.CapabilityRequest, grant session.CapabilityGrant) (string, error) {
	plan = normalizeCapabilityUpdatePlan(plan)
	if plan.AgentID != "" {
		return plan.AgentID, nil
	}
	candidates := []string{
		strings.TrimPrefix(strings.TrimSpace(request.TargetResource), "durable_agent:"),
		strings.TrimPrefix(strings.TrimSpace(grant.TargetResource), "durable_agent:"),
		durableAgentIDFromCapabilityPrincipal(request.RequestedFor),
		durableAgentIDFromCapabilityPrincipal(grant.GrantedTo),
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, err := r.store.DurableAgent(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("capability_update_plan with policy_patch requires agent_id")
}
