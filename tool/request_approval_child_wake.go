//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type requestApprovalChildWakeTargetRef struct {
	AgentID        string
	GrantID        string
	TargetResource string
	PrincipalID    string
}

func (r *Registry) requestApprovalPhaseChildWakeTargetFromDurableFacts(in requestApprovalInput, key session.SessionKey, p principal.Principal, phase session.OperationPhase, agentID string, grantID string, targetResource string) (requestApprovalChildWakeTargetRef, error) {
	if r == nil || r.store == nil {
		return requestApprovalChildWakeTargetRef{}, nil
	}
	principalID := firstNonEmptyTool(requestApprovalPhaseGrantedTo(phase, targetResource), toolAuthorityCanonicalPrincipal(p))
	if principalID == "" || principalID == "unknown" {
		return requestApprovalChildWakeTargetRef{}, nil
	}
	agentID = strings.TrimSpace(agentID)
	inferredAgent := false
	if agentID == "" {
		inferredAgentID, err := r.requestApprovalPhaseMentionedDurableAgentID(in, phase)
		if err != nil {
			return requestApprovalChildWakeTargetRef{}, err
		}
		agentID = inferredAgentID
		inferredAgent = agentID != ""
	}
	if agentID == "" {
		return requestApprovalChildWakeTargetRef{}, nil
	}
	targetResource = strings.TrimSpace(targetResource)
	if targetResource == "" {
		targetResource = "durable_agent:" + agentID + ":wake_once"
	}
	grantID = strings.TrimSpace(grantID)
	if grantID != "" {
		return requestApprovalChildWakeTargetRef{
			AgentID:        agentID,
			GrantID:        grantID,
			TargetResource: targetResource,
			PrincipalID:    principalID,
		}, nil
	}
	grants, err := r.store.ActiveCapabilityGrants(session.CapabilityKindGenericDelegation, targetResource, principalID, "invoke")
	if err != nil {
		return requestApprovalChildWakeTargetRef{}, err
	}
	for _, grant := range grants {
		if grantAgent := requestApprovalAgentIDFromConstraints(grant.Constraints); grantAgent != "" && grantAgent != agentID {
			continue
		}
		return requestApprovalChildWakeTargetRef{
			AgentID:        agentID,
			GrantID:        strings.TrimSpace(grant.GrantID),
			TargetResource: strings.TrimSpace(grant.TargetResource),
			PrincipalID:    principalID,
		}, nil
	}
	if inferredAgent {
		return requestApprovalChildWakeTargetRef{}, fmt.Errorf("request_approval child_wake phase for %s requires an active exact durable_agent wake_once grant", agentID)
	}
	return requestApprovalChildWakeTargetRef{
		AgentID:        agentID,
		TargetResource: targetResource,
		PrincipalID:    principalID,
	}, nil
}

func (r *Registry) requestApprovalPhaseMentionedDurableAgentID(in requestApprovalInput, phase session.OperationPhase) (string, error) {
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return "", err
	}
	text := requestApprovalPhaseInferenceText(in, phase)
	if text == "" {
		return "", nil
	}
	matched := ""
	for _, agent := range agents {
		if strings.TrimSpace(agent.Status) != "" && strings.TrimSpace(agent.Status) != "active" {
			continue
		}
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		if !strings.Contains(text, strings.ToLower(agentID)) {
			continue
		}
		if matched != "" && matched != agentID {
			return "", fmt.Errorf("request_approval child_wake phase mentions multiple durable agents; provide an exact durable_agent wake_once grant")
		}
		matched = agentID
	}
	return matched, nil
}

func requestApprovalPhaseChildWakeTarget(phase session.OperationPhase) (string, string, string) {
	phase = session.NormalizeOperationState(session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{phase}}}).PhasePlan.Phases[0]
	for _, grant := range phase.RequiredCapabilityGrants {
		grant = session.NormalizeCapabilityGrantSpec(grant)
		agentID := requestApprovalAgentIDFromDurableWakeTarget(grant.TargetResource)
		if agentID == "" {
			agentID = requestApprovalAgentIDFromConstraints(grant.Constraints)
		}
		if agentID != "" {
			return agentID, strings.TrimSpace(grant.GrantID), strings.TrimSpace(grant.TargetResource)
		}
	}
	return "", "", ""
}

func requestApprovalPhaseGrantedTo(phase session.OperationPhase, targetResource string) string {
	targetResource = strings.TrimSpace(targetResource)
	for _, grant := range phase.RequiredCapabilityGrants {
		grant = session.NormalizeCapabilityGrantSpec(grant)
		if targetResource != "" && strings.TrimSpace(grant.TargetResource) != targetResource {
			continue
		}
		if grantedTo := strings.TrimSpace(grant.GrantedTo); grantedTo != "" {
			return grantedTo
		}
	}
	return ""
}

func requestApprovalAgentIDFromDurableWakeTarget(target string) string {
	parts := strings.Split(strings.TrimSpace(target), ":")
	if len(parts) == 3 && parts[0] == "durable_agent" && strings.TrimSpace(parts[1]) != "" && parts[2] == "wake_once" {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func requestApprovalAgentIDFromConstraints(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	if value, ok := payload["agent_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	if scope, ok := payload["tool_invocation"].(map[string]any); ok {
		if value, ok := scope["agent_id"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
