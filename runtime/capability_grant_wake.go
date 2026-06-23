//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) HandleCapabilityGrantActivated(ctx context.Context, key session.SessionKey, grant session.CapabilityGrant) {
	if r == nil || r.store == nil {
		return
	}
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.Status != session.CapabilityGrantStatusActive {
		return
	}
	agentID, ok := core.DurableAgentIDFromPrincipal(grant.GrantedTo)
	if !ok {
		return
	}
	if err := r.queueCapabilityGrantWake(ctx, agentID, grant); err != nil {
		r.recordCapabilityGrantWakeFailure(ctx, key, agentID, grant, err)
		return
	}
	go func() {
		if err := r.runCapabilityGrantWake(context.Background(), agentID, grant); err != nil {
			r.recordCapabilityGrantWakeFailure(context.Background(), key, agentID, grant, err)
		}
	}()
}

func (r *Runtime) queueCapabilityGrantWake(ctx context.Context, agentID string, grant session.CapabilityGrant) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("runtime store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("durable agent id is required")
	}
	agent, err := r.store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load durable agent %q for capability grant wake: %w", agentID, err)
	}
	state, err := r.store.DurableAgentState(agentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load durable agent state %q for capability grant wake: %w", agentID, err)
		}
		state = &core.DurableAgentState{AgentID: agentID}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return fmt.Errorf("parse durable agent continuity for capability grant wake: %w", err)
	}
	now := time.Now().UTC()
	taskPacketID := capabilityGrantTaskPacketID(agentID, grant)
	continuity = continuity.WithConversationMessages(core.DurableAgentConversationMessage{
		MessageID: taskPacketID,
		Role:      "parent",
		Text:      capabilityGrantWakeMessage(*agent, grant),
		CreatedAt: now,
	})
	raw, err := continuity.Marshal()
	if err != nil {
		return fmt.Errorf("marshal durable agent continuity for capability grant wake: %w", err)
	}
	state.AgentID = agentID
	state.StateJSON = raw
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return fmt.Errorf("save durable agent capability grant wake queue: %w", err)
	}
	key := r.durableAgentExecutionKey(agentID)
	inputRaw, _ := json.Marshal(map[string]any{
		"grant_id":        grant.GrantID,
		"request_id":      grant.RequestID,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"allowed_actions": grant.AllowedActions,
	})
	if _, err := r.store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:       taskPacketID,
		TaskLeaseID:    session.ChildTaskLeaseID(taskPacketID),
		AgentID:        agentID,
		Key:            key,
		TaskKind:       "capability_grant_wake",
		AuthorityKind:  "capability_grant",
		AuthorityID:    grant.GrantID,
		GrantID:        grant.GrantID,
		RequestID:      grant.RequestID,
		TargetResource: grant.TargetResource,
		RequiredAction: capabilityGrantWakeRequiredAction(grant),
		InputJSON:      string(inputRaw),
		CreatedAt:      now,
	}); err != nil {
		return fmt.Errorf("record capability grant child task packet: %w", err)
	}
	r.recordExecutionEvent(key, core.ExecutionEventCapabilityGrantWakeQueued, "capability", "wake_queued", map[string]any{
		"agent_id":        agentID,
		"grant_id":        grant.GrantID,
		"request_id":      grant.RequestID,
		"kind":            string(grant.Kind),
		"target_resource": grant.TargetResource,
		"allowed_actions": grant.AllowedActions,
		"task_packet_id":  taskPacketID,
	}, now)
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "capability_grant_wake",
		State:              session.NextActionWaitingForChild,
		SubjectKind:        "task_packet",
		SubjectRef:         taskPacketID,
		CausalRefs:         []string{"capability_grant:" + grant.GrantID, "task_packet:" + taskPacketID},
		NextAction:         "wake the child with the compact grant task packet",
		RequiredAuthority:  string(grant.Kind),
		OperatorProjection: "The grant was activated; the child has one compact task packet to incorporate it and report the typed result.",
		CreatedAt:          now,
	}); err != nil {
		return fmt.Errorf("record capability grant wake next action: %w", err)
	}
	return nil
}

func (r *Runtime) runCapabilityGrantWake(ctx context.Context, agentID string, grant session.CapabilityGrant) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("runtime store unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	agentID = strings.TrimSpace(agentID)
	agent, err := r.store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load durable agent %q for capability grant wake: %w", agentID, err)
	}
	now := time.Now().UTC()
	plan, err := prepareDurableParentConversationWakePlan(r, *agent, now, true)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("capability grant %s queued no durable wake plan for agent %s", strings.TrimSpace(grant.GrantID), agentID)
	}
	return r.runDurableWakeTurn(ctx, *agent, *plan, now)
}

func (r *Runtime) recordCapabilityGrantWakeFailure(ctx context.Context, key session.SessionKey, agentID string, grant session.CapabilityGrant, cause error) {
	if r == nil || cause == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID, _ = core.DurableAgentIDFromPrincipal(grant.GrantedTo)
	}
	if r.store != nil {
		_ = r.markCapabilityGrantWakeFailed(grant, cause)
	}
	if key.ChatID == 0 && agentID != "" {
		key = r.durableAgentExecutionKey(agentID)
	}
	if r.store != nil {
		r.recordExecutionEvent(key, core.ExecutionEventCapabilityGrantWakeFailed, "capability", "failed", map[string]any{
			"agent_id":   agentID,
			"grant_id":   grant.GrantID,
			"request_id": grant.RequestID,
			"error":      trimError(cause.Error()),
		}, time.Now().UTC())
	}
	r.reportOperationalIssueAsync("capability_grant_wake", fmt.Errorf("grant_id=%s agent_id=%s wake failed; repair the durable agent runtime and request a fresh grant: %w", strings.TrimSpace(grant.GrantID), agentID, cause))
}

func (r *Runtime) markCapabilityGrantWakeFailed(grant session.CapabilityGrant, cause error) error {
	if r == nil || r.store == nil || cause == nil {
		return nil
	}
	grant = session.NormalizeCapabilityGrant(grant)
	if strings.TrimSpace(grant.GrantID) == "" {
		return nil
	}
	current, ok, err := r.store.CapabilityGrant(grant.GrantID)
	if err != nil {
		return err
	}
	if ok {
		grant = current
	}
	now := time.Now().UTC()
	grant.Status = session.CapabilityGrantStatusFailed
	grant.FailureCount++
	grant.LastFailureAt = now
	grant.StaleReason = "capability_grant_wake_failed: " + trimError(cause.Error())
	grant.UpdatedAt = now
	_, err = r.store.UpsertCapabilityGrant(grant)
	return err
}

func capabilityGrantWakeMessage(agent core.DurableAgent, grant session.CapabilityGrant) string {
	parts := []string{
		"Capability grant activated.",
		"Agent: " + strings.TrimSpace(agent.AgentID),
		"Grant: " + strings.TrimSpace(grant.GrantID),
		"Request: " + firstNonEmpty(strings.TrimSpace(grant.RequestID), "-"),
		"Kind: " + string(grant.Kind),
		"Target: " + strings.TrimSpace(grant.TargetResource),
		"Allowed actions: " + strings.Join(grant.AllowedActions, ", "),
		"Incorporate this grant, validate your current health, and report what changed.",
		"If you cannot wake cleanly, ask the operator to repair the runtime and issue a fresh grant.",
	}
	return strings.Join(parts, "\n")
}

func capabilityGrantTaskPacketID(agentID string, grant session.CapabilityGrant) string {
	seed := strings.Join([]string{
		strings.TrimSpace(agentID),
		strings.TrimSpace(grant.GrantID),
		strings.TrimSpace(grant.RequestID),
		string(grant.Kind),
		strings.TrimSpace(grant.TargetResource),
	}, ":")
	return "grant_task:" + strings.TrimPrefix(session.EffectAttemptCommandHash(seed), "sha256:")[:16]
}

func capabilityGrantWakeRequiredAction(grant session.CapabilityGrant) string {
	actions := session.NormalizeCapabilityActions(grant.AllowedActions)
	for _, action := range actions {
		if action == "invoke" {
			return action
		}
	}
	if len(actions) > 0 {
		return actions[0]
	}
	return "invoke"
}
