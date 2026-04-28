//go:build linux

package runtime

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) DurableAgentsStatusSnapshot() (core.DurableAgentsStatusSnapshot, error) {
	snapshot := core.DurableAgentsStatusSnapshot{
		GeneratedAt: time.Now().UTC(),
		Agents:      make([]core.DurableAgentStatusSnapshot, 0, 8),
	}
	if r == nil || r.store == nil {
		return snapshot, nil
	}

	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return core.DurableAgentsStatusSnapshot{}, err
	}
	durableEvents, err := r.durableStatusEventState(time.Now().UTC().Add(-30*24*time.Hour), 4000)
	if err != nil {
		return core.DurableAgentsStatusSnapshot{}, err
	}
	sort.Slice(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})

	for _, agent := range agents {
		livePolicy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
		row := core.DurableAgentStatusSnapshot{
			AgentID:                strings.TrimSpace(agent.AgentID),
			CanonicalPrincipal:     core.DurableAgentPrincipal(agent.AgentID),
			ChannelKind:            strings.TrimSpace(agent.ChannelKind),
			Status:                 firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			ReviewTargetChatID:     agent.ReviewTargetChatID,
			ParentScopeKind:        strings.TrimSpace(agent.ParentScopeKind),
			ParentScopeID:          strings.TrimSpace(agent.ParentScopeID),
			WakeupMode:             strings.TrimSpace(agent.WakeupMode),
			NetworkPolicy:          strings.TrimSpace(agent.NetworkPolicy),
			PolicyVersion:          agent.PolicyVersion,
			PolicyHash:             strings.TrimSpace(agent.PolicyHash),
			PolicyOutboundMode:     strings.TrimSpace(livePolicy.OutboundMode),
			PolicyDrift:            strings.TrimSpace(livePolicy.DriftPolicy),
			CapabilityEnvelope:     append([]string(nil), livePolicy.CapabilityEnvelope...),
			AllowedTelegramUserIDs: append([]int64(nil), agent.AllowedTelegramUserIDs...),
			IdentitySource:         "canonical:session.durable_agents",
			TailnetMode:            strings.TrimSpace(livePolicy.TailnetMode),
			TailnetHostname:        durableAgentTailnetHostname(agent.AgentID, livePolicy.TailnetHostname),
			TailnetTags:            append([]string(nil), livePolicy.TailnetTags...),
			TailnetSurfacePolicy:   strings.TrimSpace(livePolicy.TailnetSurfacePolicy),
		}
		if row.TailnetMode != "" {
			row.TailnetSurfaceID = durableAgentTailnetSurfaceID(row.AgentID)
		}

		hasRuntimeState := false
		runtimeState, err := r.store.DurableAgentRuntimeState(agent.AgentID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return core.DurableAgentsStatusSnapshot{}, err
			}
		} else {
			hasRuntimeState = true
			row.LastWakeAt = runtimeState.LastWakeAt
			row.LastReviewAt = runtimeState.LastReviewAt
			row.DormantAt = runtimeState.DormantAt
			row.LastApplyStatus = strings.TrimSpace(runtimeState.LastApplyStatus)
			row.LastApplyError = strings.TrimSpace(runtimeState.LastApplyError)
		}
		identityState, err := r.store.DurableAgentIdentityState(agent.AgentID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return core.DurableAgentsStatusSnapshot{}, err
			}
		} else {
			row.LastAppliedPolicyVersion = identityState.LastAppliedPolicyVersion
			row.LastAppliedPolicyAt = identityState.LastAppliedPolicyAt
			row.IdentitySource = "canonical:session.durable_agents+canonical:session.durable_agent_identity_state"
		}

		enrollment, err := r.store.DurableAgentRemoteEnrollment(agent.AgentID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return core.DurableAgentsStatusSnapshot{}, err
			}
		} else {
			row.EnrollmentStatus = strings.TrimSpace(enrollment.Status)
			row.EnrollmentLastSeenAt = enrollment.LastSeenAt
			row.EnrollmentLastSequence = enrollment.LastSequence
			row.EnrollmentRevokedAt = enrollment.RevokedAt
			row.EnrollmentParentControlURL = strings.TrimSpace(enrollment.ParentControlURL)
		}

		row.SubstrateLabels = durableChildSubstrateFor("", agent).Labels
		if row.TailnetMode != "" {
			row.SubstrateLabels = append(row.SubstrateLabels, "tailnet:"+row.TailnetMode)
		}
		row.ChildRuntimeGrantCount, row.ChildRuntimeBlockedReason, row.ChildRuntimeRepairHint = r.durableChildRuntimeGrantStatus(agent)
		row.ProfileManifestStatus, row.ProfileManifestPolicyHash, row.ProfileManifestFileCount = durableAgentProfileManifestStatus(agent, r.cfg.Sessions.DBPath)

		eventState := durableEvents[strings.TrimSpace(agent.AgentID)]
		hasEventProjection := durableStatusEventProjectionPresent(eventState)
		row.RuntimePostureSource = durableRuntimePostureSource(hasRuntimeState, hasEventProjection)
		row = overlayDurableStatusFromEvents(row, eventState)
		row.Health = durableAgentHealthFromStatusWithEvents(row, eventState)
		if strings.EqualFold(row.Status, "active") {
			snapshot.ActiveAgents++
		}
		switch row.Health {
		case "dormant":
			snapshot.DormantAgents++
		case "degraded":
			snapshot.DegradedAgents++
		case "inactive":
			snapshot.InactiveAgents++
		}

		snapshot.Agents = append(snapshot.Agents, row)
	}

	snapshot.TotalAgents = len(snapshot.Agents)
	return snapshot, nil
}

func durableAgentHealthFromStatus(snapshot core.DurableAgentStatusSnapshot) string {
	if !strings.EqualFold(strings.TrimSpace(snapshot.Status), "active") {
		return "inactive"
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.LastApplyStatus), "failed") || strings.TrimSpace(snapshot.LastApplyError) != "" {
		return "degraded"
	}
	if enrollment := strings.ToLower(strings.TrimSpace(snapshot.EnrollmentStatus)); enrollment != "" && enrollment != "active" {
		return "degraded"
	}
	if !snapshot.DormantAt.IsZero() {
		return "dormant"
	}
	return "ok"
}

type durableStatusEventProjection struct {
	LastWakeStartedAt   time.Time
	LastWakeCompletedAt time.Time
	LastWakeFailedAt    time.Time
	LastAwakeAt         time.Time
	LastDormantAt       time.Time
	LastPolicyAppliedAt time.Time
	LastPolicyFailedAt  time.Time
	LastPolicyError     string
	LastParentAckAt     time.Time
}

func (r *Runtime) durableStatusEventState(since time.Time, limit int) (map[string]durableStatusEventProjection, error) {
	if r == nil || r.store == nil {
		return map[string]durableStatusEventProjection{}, nil
	}
	events, err := r.store.ExecutionEventsByTypes([]string{
		core.ExecutionEventDurableWakeStarted,
		core.ExecutionEventDurableWakeCompleted,
		core.ExecutionEventDurableWakeFailed,
		core.ExecutionEventDurableStateAwake,
		core.ExecutionEventDurableStateDormant,
		core.ExecutionEventDurablePolicyApplied,
		core.ExecutionEventDurablePolicyApplyFailed,
		core.ExecutionEventDurableParentAck,
	}, since, limit)
	if err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })

	out := make(map[string]durableStatusEventProjection, 16)
	for _, event := range events {
		payload := executionEventPayload(event.PayloadJSON)
		agentID := strings.TrimSpace(event.Scope.DurableAgentID)
		if agentID == "" {
			agentID = strings.TrimSpace(payloadString(payload, "agent_id"))
		}
		if agentID == "" {
			continue
		}
		state := out[agentID]
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventDurableWakeStarted:
			state.LastWakeStartedAt = event.CreatedAt
		case core.ExecutionEventDurableWakeCompleted:
			state.LastWakeCompletedAt = event.CreatedAt
		case core.ExecutionEventDurableWakeFailed:
			state.LastWakeFailedAt = event.CreatedAt
		case core.ExecutionEventDurableStateAwake:
			state.LastAwakeAt = event.CreatedAt
		case core.ExecutionEventDurableStateDormant:
			state.LastDormantAt = event.CreatedAt
		case core.ExecutionEventDurablePolicyApplied:
			state.LastPolicyAppliedAt = event.CreatedAt
			state.LastPolicyError = ""
		case core.ExecutionEventDurablePolicyApplyFailed:
			state.LastPolicyFailedAt = event.CreatedAt
			if errText := strings.TrimSpace(payloadString(payload, "error")); errText != "" {
				state.LastPolicyError = errText
			}
		case core.ExecutionEventDurableParentAck:
			state.LastParentAckAt = event.CreatedAt
		}
		out[agentID] = state
	}
	return out, nil
}

func durableStatusEventProjectionPresent(state durableStatusEventProjection) bool {
	return !state.LastWakeStartedAt.IsZero() ||
		!state.LastWakeCompletedAt.IsZero() ||
		!state.LastWakeFailedAt.IsZero() ||
		!state.LastAwakeAt.IsZero() ||
		!state.LastDormantAt.IsZero() ||
		!state.LastPolicyAppliedAt.IsZero() ||
		!state.LastPolicyFailedAt.IsZero() ||
		!state.LastParentAckAt.IsZero() ||
		strings.TrimSpace(state.LastPolicyError) != ""
}

func durableRuntimePostureSource(hasRuntimeState bool, hasEventProjection bool) string {
	if hasRuntimeState && hasEventProjection {
		return "operational_current_state_store:session.durable_agent_state+projection:tes_execution_events"
	}
	if hasRuntimeState {
		return "operational_current_state_store:session.durable_agent_state"
	}
	if hasEventProjection {
		return "projection:tes_execution_events"
	}
	return "operational_current_state_store:session.durable_agent_state"
}

func overlayDurableStatusFromEvents(
	row core.DurableAgentStatusSnapshot,
	state durableStatusEventProjection,
) core.DurableAgentStatusSnapshot {
	if !state.LastAwakeAt.IsZero() && state.LastAwakeAt.After(row.LastWakeAt) {
		row.LastWakeAt = state.LastAwakeAt
	}
	if !state.LastDormantAt.IsZero() && state.LastDormantAt.After(row.DormantAt) {
		row.DormantAt = state.LastDormantAt
	}
	if !state.LastPolicyAppliedAt.IsZero() && state.LastPolicyAppliedAt.After(row.LastAppliedPolicyAt) {
		row.LastAppliedPolicyAt = state.LastPolicyAppliedAt
		row.LastApplyStatus = "applied"
		row.LastApplyError = ""
	}
	if !state.LastPolicyFailedAt.IsZero() && state.LastPolicyFailedAt.After(row.LastAppliedPolicyAt) {
		row.LastApplyStatus = "failed"
		if strings.TrimSpace(state.LastPolicyError) != "" {
			row.LastApplyError = strings.TrimSpace(state.LastPolicyError)
		}
	}
	return row
}

func durableAgentHealthFromStatusWithEvents(
	row core.DurableAgentStatusSnapshot,
	state durableStatusEventProjection,
) string {
	health := durableAgentHealthFromStatus(row)
	if !strings.EqualFold(strings.TrimSpace(row.Status), "active") {
		return health
	}
	if !state.LastWakeFailedAt.IsZero() {
		lastSuccess := coalesceTime(state.LastWakeCompletedAt, state.LastPolicyAppliedAt)
		if lastSuccess.IsZero() || state.LastWakeFailedAt.After(lastSuccess) {
			return "degraded"
		}
	}
	if !state.LastDormantAt.IsZero() && (state.LastAwakeAt.IsZero() || state.LastDormantAt.After(state.LastAwakeAt)) {
		return "dormant"
	}
	return health
}

type durableStatusProfileManifest struct {
	PolicyHash string `json:"policy_hash,omitempty"`
	Files      []struct {
		Path string `json:"path,omitempty"`
	} `json:"files,omitempty"`
}

func (r *Runtime) durableChildRuntimeGrantStatus(agent core.DurableAgent) (int, string, string) {
	if r == nil || r.store == nil {
		return 0, "", ""
	}
	principal := core.DurableAgentPrincipal(agent.AgentID)
	grants, err := r.store.CapabilityGrants(100, session.CapabilityGrantStatusActive, "", principal)
	if err != nil {
		return 0, "grant_status_unavailable", "inspect capability grant store"
	}
	count := 0
	for _, grant := range grants {
		if _, ok, err := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints); err != nil {
			return count, "invalid_child_runtime_contract", "repair or revoke invalid child_runtime grant " + strings.TrimSpace(grant.GrantID)
		} else if !ok {
			continue
		}
		if err := durableChildGrantFreshnessError(grant); err != nil {
			return count, err.Error(), "repair, refresh, or revoke child_runtime grant " + strings.TrimSpace(grant.GrantID)
		}
		count++
	}
	return count, "", ""
}

func durableAgentProfileManifestStatus(agent core.DurableAgent, dbPath string) (string, string, int) {
	_, memoryRoot := durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
	if memoryRoot == "" {
		_, memoryRoot = durableagent.DefaultLocalRoots(dbPath, strings.TrimSpace(agent.AgentID))
	}
	path := filepath.Join(memoryRoot, "profile", "PROFILE.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "missing", "", 0
	}
	var manifest durableStatusProfileManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "invalid", "", 0
	}
	policyHash := strings.TrimSpace(manifest.PolicyHash)
	status := "present"
	if policyHash != "" && strings.TrimSpace(agent.PolicyHash) != "" && policyHash != strings.TrimSpace(agent.PolicyHash) {
		status = "policy_hash_mismatch"
	}
	return status, policyHash, len(manifest.Files)
}
