//go:build linux

package tool

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) parkDurableAgent(in durableAgentInput, key session.SessionKey) (string, error) {
	agent, err := r.lifecycleDurableAgent(in.AgentID)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	agent.Status = "parked"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
		return "", err
	}
	if err := r.markDurableAgentLifecycleDormant(agent.AgentID, "parked", now); err != nil {
		return "", err
	}
	if err := r.appendDurableAgentLifecycleEvent(key, "parked", *agent, map[string]any{
		"reason": firstNonEmpty(strings.TrimSpace(in.Reason), "telegram durable-agent park"),
	}); err != nil {
		return "", err
	}
	return renderDurableAgentLifecycleControl("park", *agent, []string{
		"runtime marked dormant",
		"scheduled and poll wakes stop while status is parked",
		"memory, profile, policy history, and audit records were preserved",
	}), nil
}

func (r *Registry) resumeDurableAgent(in durableAgentInput, key session.SessionKey) (string, error) {
	agent, err := r.lifecycleDurableAgent(in.AgentID)
	if err != nil {
		return "", err
	}
	if err := validateDurableAgentActivation(*agent); err != nil {
		return "", err
	}
	r.inheritDurableAgentBootstrapIfMissing(agent)
	agent.Status = "active"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
		return "", err
	}
	if _, err := syncDurableAgentProfileFiles(*agent, r.store); err != nil {
		return "", err
	}
	if err := r.appendDurableAgentLifecycleEvent(key, "resumed", *agent, map[string]any{
		"reason": firstNonEmpty(strings.TrimSpace(in.Reason), "telegram durable-agent resume"),
	}); err != nil {
		return "", err
	}
	return renderDurableAgentLifecycleControl("resume", *agent, []string{
		"activation requirements passed",
		"profile files synced",
		"scheduled and poll wakes are eligible again",
	}), nil
}

func (r *Registry) retireDurableAgent(in durableAgentInput, key session.SessionKey) (string, error) {
	agent, err := r.lifecycleDurableAgent(in.AgentID)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	reason := firstNonEmpty(strings.TrimSpace(in.Reason), "telegram durable-agent retire")

	agent.Status = "retired"
	if err := r.store.UpsertDurableAgent(*agent); err != nil {
		return "", err
	}
	if err := r.markDurableAgentLifecycleDormant(agent.AgentID, "retired", now); err != nil {
		return "", err
	}
	grantIDs, err := r.revokeDurableAgentCapabilityGrants(agent.AgentID, reason, key, now)
	if err != nil {
		return "", err
	}
	enrollmentStatus, err := r.decommissionDurableAgentEnrollment(agent.AgentID, now)
	if err != nil {
		return "", err
	}
	tailnetStatus, err := r.revokeDurableAgentTailnetSurface(agent.AgentID, reason, now)
	if err != nil {
		return "", err
	}
	if err := r.appendDurableAgentLifecycleEvent(key, "retired", *agent, map[string]any{
		"reason":                    reason,
		"revoked_capability_grants": grantIDs,
		"enrollment":                enrollmentStatus,
		"tailnet_surface":           tailnetStatus,
	}); err != nil {
		return "", err
	}
	evidence := []string{
		"runtime marked dormant",
		fmt.Sprintf("revoked capability grants: %d", len(grantIDs)),
		"memory, files, parent conversation, and audit history were preserved",
	}
	if enrollmentStatus != "" {
		evidence = append(evidence, "remote enrollment: "+enrollmentStatus)
	}
	if tailnetStatus != "" {
		evidence = append(evidence, "tailnet surface: "+tailnetStatus)
	}
	return renderDurableAgentLifecycleControl("retire", *agent, evidence), nil
}

func (r *Registry) lifecycleDurableAgent(agentID string) (*core.DurableAgent, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable_agent agent_id is required for lifecycle action")
	}
	return r.resolveDurableAgent(agentID)
}

func (r *Registry) markDurableAgentLifecycleDormant(agentID string, status string, at time.Time) error {
	state, err := r.store.DurableAgentRuntimeState(agentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		state = &core.DurableAgentRuntimeState{
			AgentID:   strings.TrimSpace(agentID),
			StateJSON: "{}",
		}
	}
	state.Status = "dormant"
	state.DormantAt = at
	state.UpdatedAt = at
	if state.StateJSON == "" {
		state.StateJSON = "{}"
	}
	if strings.TrimSpace(status) != "" {
		state.LastApplyStatus = strings.TrimSpace(status)
		state.LastApplyError = ""
	}
	return r.store.SaveDurableAgentRuntimeState(*state)
}

func (r *Registry) revokeDurableAgentCapabilityGrants(agentID string, reason string, key session.SessionKey, now time.Time) ([]string, error) {
	principalID := core.DurableAgentPrincipal(agentID)
	grants, err := r.store.CapabilityGrants(500, session.CapabilityGrantStatusActive, "", principalID)
	if err != nil {
		return nil, err
	}
	revoked := make([]string, 0)
	for _, grant := range grants {
		if strings.TrimSpace(grant.GrantedTo) != principalID {
			continue
		}
		grant.Status = session.CapabilityGrantStatusRevoked
		grant.StaleReason = strings.TrimSpace(reason)
		grant.RevokedAt = now
		grant.UpdatedAt = now
		stored, err := r.store.UpsertCapabilityGrant(grant)
		if err != nil {
			return nil, err
		}
		revoked = append(revoked, stored.GrantID)
		if err := r.appendCapabilityEvent(key, core.ExecutionEventCapabilityGrantChanged, string(stored.Status), map[string]any{
			"grant_id":        stored.GrantID,
			"request_id":      stored.RequestID,
			"kind":            string(stored.Kind),
			"target_resource": stored.TargetResource,
			"granted_to":      stored.GrantedTo,
			"status":          string(stored.Status),
			"revoked_by":      "durable_agent.retire",
			"rationale":       strings.TrimSpace(reason),
		}); err != nil {
			return nil, err
		}
	}
	return revoked, nil
}

func (r *Registry) decommissionDurableAgentEnrollment(agentID string, now time.Time) (string, error) {
	enrollment, err := r.store.DurableAgentRemoteEnrollment(agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	enrollment.Status = "decommissioned"
	enrollment.RevokedAt = now
	if err := r.store.UpsertDurableAgentRemoteEnrollment(*enrollment); err != nil {
		return "", err
	}
	return "decommissioned", nil
}

func (r *Registry) revokeDurableAgentTailnetSurface(agentID string, reason string, now time.Time) (string, error) {
	surfaceID := durableAgentLifecycleTailnetSurfaceID(agentID)
	if surfaceID == "" {
		return "", nil
	}
	_, ok, err := r.store.RevokeTailnetSurface(surfaceID, reason, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return "revoked", nil
}

func durableAgentLifecycleTailnetSurfaceID(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	return "durable_agent:" + agentID + ":tsnet_http:status"
}

func (r *Registry) appendDurableAgentLifecycleEvent(key session.SessionKey, status string, agent core.DurableAgent, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["agent_id"] = strings.TrimSpace(agent.AgentID)
	payload["agent_status"] = strings.TrimSpace(agent.Status)
	payload["channel_kind"] = strings.TrimSpace(agent.ChannelKind)
	return r.appendToolLifecycleEvent(key, "durable_agent_lifecycle", core.ExecutionEventDurableLifecycleChanged, status, payload)
}

func renderDurableAgentLifecycleControl(action string, agent core.DurableAgent, evidence []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent %s\n", strings.TrimSpace(action))
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(agent.AgentID))
	fmt.Fprintf(&b, "status: %s\n", strings.TrimSpace(agent.Status))
	for _, item := range evidence {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		fmt.Fprintf(&b, "evidence: %s\n", item)
	}
	b.WriteString("next: inspect /agents for current state\n")
	return b.String()
}
