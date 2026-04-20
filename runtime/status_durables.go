//go:build linux

package runtime

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
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
	sort.Slice(agents, func(i, j int) bool {
		return strings.TrimSpace(agents[i].AgentID) < strings.TrimSpace(agents[j].AgentID)
	})

	for _, agent := range agents {
		row := core.DurableAgentStatusSnapshot{
			AgentID:                strings.TrimSpace(agent.AgentID),
			ChannelKind:            strings.TrimSpace(agent.ChannelKind),
			Status:                 firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			ReviewTargetChatID:     agent.ReviewTargetChatID,
			ParentScopeKind:        strings.TrimSpace(agent.ParentScopeKind),
			ParentScopeID:          strings.TrimSpace(agent.ParentScopeID),
			WakeupMode:             strings.TrimSpace(agent.WakeupMode),
			NetworkPolicy:          strings.TrimSpace(agent.NetworkPolicy),
			PolicyVersion:          agent.PolicyVersion,
			PolicyHash:             strings.TrimSpace(agent.PolicyHash),
			PolicyOutboundMode:     strings.TrimSpace(agent.LivePolicy.OutboundMode),
			PolicyDrift:            strings.TrimSpace(agent.LivePolicy.DriftPolicy),
			CapabilityEnvelope:     append([]string(nil), agent.LivePolicy.CapabilityEnvelope...),
			AllowedTelegramUserIDs: append([]int64(nil), agent.AllowedTelegramUserIDs...),
		}

		state, err := r.store.DurableAgentState(agent.AgentID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return core.DurableAgentsStatusSnapshot{}, err
			}
		} else {
			row.LastWakeAt = state.LastWakeAt
			row.LastReviewAt = state.LastReviewAt
			row.DormantAt = state.DormantAt
			row.LastAppliedPolicyVersion = state.LastAppliedPolicyVersion
			row.LastAppliedPolicyAt = state.LastAppliedPolicyAt
			row.LastApplyStatus = strings.TrimSpace(state.LastApplyStatus)
			row.LastApplyError = strings.TrimSpace(state.LastApplyError)
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

		row.Health = durableAgentHealthFromStatus(row)
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
