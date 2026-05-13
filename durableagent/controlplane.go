//go:build linux

package durableagent

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type ControlPlaneStore interface {
	DurableAgent(agentID string) (*core.DurableAgent, error)
	DurableAgentState(agentID string) (*core.DurableAgentState, error)
	SaveDurableAgentState(state core.DurableAgentState) error
	AcceptDurableAgentControlEnvelope(envelope core.DurableAgentControlEnvelope, receivedAt time.Time) error
	AcceptDurableAgentControlEnvelopeFromTailnetPeer(envelope core.DurableAgentControlEnvelope, identity core.TailnetPeerIdentity, receivedAt time.Time) error
	AcceptDurableAgentEnrollment(envelope core.DurableAgentControlEnvelope, enrollment core.DurableAgentRemoteEnrollment, receivedAt time.Time) error
}

type ControlPlane struct {
	store        ControlPlaneStore
	replayWindow time.Duration
}

func NewControlPlane(store ControlPlaneStore, replayWindow time.Duration) *ControlPlane {
	if replayWindow <= 0 {
		replayWindow = 10 * time.Minute
	}
	return &ControlPlane{store: store, replayWindow: replayWindow}
}

func (cp *ControlPlane) AcceptEnvelope(envelope core.DurableAgentControlEnvelope, now time.Time) error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("durable agent control plane store is nil")
	}
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	now = normalizeControlPlaneTime(now)
	if outsideReplayWindow(envelope.Timestamp, now, cp.replayWindow) {
		return fmt.Errorf("durable agent control envelope is outside the allowed replay window")
	}
	return cp.store.AcceptDurableAgentControlEnvelope(envelope, now)
}

func (cp *ControlPlane) AcceptEnvelopeFromTailnetPeer(envelope core.DurableAgentControlEnvelope, identity core.TailnetPeerIdentity, now time.Time) error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("durable agent control plane store is nil")
	}
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	now = normalizeControlPlaneTime(now)
	if outsideReplayWindow(envelope.Timestamp, now, cp.replayWindow) {
		return fmt.Errorf("durable agent control envelope is outside the allowed replay window")
	}
	return cp.store.AcceptDurableAgentControlEnvelopeFromTailnetPeer(envelope, identity, now)
}

func (cp *ControlPlane) AcceptEnrollment(envelope core.DurableAgentControlEnvelope, enrollment core.DurableAgentRemoteEnrollment, now time.Time) error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("durable agent control plane store is nil")
	}
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	now = normalizeControlPlaneTime(now)
	if outsideReplayWindow(envelope.Timestamp, now, cp.replayWindow) {
		return fmt.Errorf("durable agent control envelope is outside the allowed replay window")
	}
	return cp.store.AcceptDurableAgentEnrollment(envelope, enrollment, now)
}

func (cp *ControlPlane) PolicySnapshot(agentID string) (core.DurableAgentPolicySnapshot, error) {
	if cp == nil || cp.store == nil {
		return core.DurableAgentPolicySnapshot{}, fmt.Errorf("durable agent control plane store is nil")
	}
	agent, err := cp.store.DurableAgent(strings.TrimSpace(agentID))
	if err != nil {
		return core.DurableAgentPolicySnapshot{}, err
	}
	return core.DurableAgentPolicySnapshot{
		AgentID:       agent.AgentID,
		PolicyVersion: agent.PolicyVersion,
		PolicyHash:    agent.PolicyHash,
		IssuedAt:      agent.PolicyIssuedAt,
		LivePolicy:    agent.LivePolicy,
	}, nil
}

func (cp *ControlPlane) AcceptPolicyAcknowledgement(envelope core.DurableAgentControlEnvelope, ack core.DurableAgentPolicyAcknowledgement, now time.Time) error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("durable agent control plane store is nil")
	}
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	ack, err := cp.validatePolicyAcknowledgement(envelope, ack)
	if err != nil {
		return err
	}
	if err := cp.AcceptEnvelope(envelope, now); err != nil {
		return err
	}
	return cp.applyPolicyAcknowledgementState(ack, now)
}

func (cp *ControlPlane) ApplyPolicyAcknowledgement(envelope core.DurableAgentControlEnvelope, ack core.DurableAgentPolicyAcknowledgement, now time.Time) error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("durable agent control plane store is nil")
	}
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	ack, err := cp.validatePolicyAcknowledgement(envelope, ack)
	if err != nil {
		return err
	}
	return cp.applyPolicyAcknowledgementState(ack, now)
}

func (cp *ControlPlane) validatePolicyAcknowledgement(envelope core.DurableAgentControlEnvelope, ack core.DurableAgentPolicyAcknowledgement) (core.DurableAgentPolicyAcknowledgement, error) {
	ack = core.NormalizeDurableAgentPolicyAcknowledgement(ack)
	if ack.AgentID == "" {
		ack.AgentID = strings.TrimSpace(envelope.AgentID)
	}
	if ack.AgentID != strings.TrimSpace(envelope.AgentID) {
		return core.DurableAgentPolicyAcknowledgement{}, fmt.Errorf("durable agent policy acknowledgement agent_id does not match envelope")
	}
	if ack.AcknowledgedVersion <= 0 || ack.AcknowledgedHash == "" {
		return core.DurableAgentPolicyAcknowledgement{}, fmt.Errorf("durable agent policy acknowledgement must include acknowledged version and hash")
	}
	agent, err := cp.store.DurableAgent(ack.AgentID)
	if err != nil {
		return core.DurableAgentPolicyAcknowledgement{}, err
	}
	if ack.AcknowledgedVersion != agent.PolicyVersion || strings.TrimSpace(ack.AcknowledgedHash) != strings.TrimSpace(agent.PolicyHash) {
		return core.DurableAgentPolicyAcknowledgement{}, fmt.Errorf("stale durable agent policy acknowledgement for %s: acknowledged version/hash %d/%s does not match current %d/%s",
			ack.AgentID,
			ack.AcknowledgedVersion,
			strings.TrimSpace(ack.AcknowledgedHash),
			agent.PolicyVersion,
			strings.TrimSpace(agent.PolicyHash),
		)
	}
	if ack.AppliedVersion > 0 || strings.TrimSpace(ack.AppliedHash) != "" {
		if ack.AppliedVersion != ack.AcknowledgedVersion || strings.TrimSpace(ack.AppliedHash) != strings.TrimSpace(ack.AcknowledgedHash) {
			return core.DurableAgentPolicyAcknowledgement{}, fmt.Errorf("durable agent policy acknowledgement applied version/hash must match acknowledged version/hash")
		}
	}
	return ack, nil
}

func (cp *ControlPlane) applyPolicyAcknowledgementState(ack core.DurableAgentPolicyAcknowledgement, now time.Time) error {
	now = normalizeControlPlaneTime(now)
	if ack.AcknowledgedAt.IsZero() {
		ack.AcknowledgedAt = now
	}
	state, err := cp.store.DurableAgentState(ack.AgentID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: ack.AgentID}
	}
	if state.LastOfferedPolicyVersion < ack.AcknowledgedVersion || strings.TrimSpace(state.LastOfferedPolicyHash) == "" {
		state.LastOfferedPolicyVersion = ack.AcknowledgedVersion
		state.LastOfferedPolicyHash = ack.AcknowledgedHash
		if state.LastOfferedPolicyAt.IsZero() {
			state.LastOfferedPolicyAt = ack.AcknowledgedAt
		}
	}
	state.LastAcknowledgedPolicyVersion = ack.AcknowledgedVersion
	state.LastAcknowledgedPolicyHash = ack.AcknowledgedHash
	state.LastAcknowledgedPolicyAt = ack.AcknowledgedAt.UTC()
	state.LastApplyStatus = ack.Status
	state.LastApplyError = ack.Error
	if ack.AppliedVersion > 0 && ack.AppliedHash != "" {
		state.LastAppliedPolicyVersion = ack.AppliedVersion
		state.LastAppliedPolicyHash = ack.AppliedHash
		state.LastAppliedPolicyAt = ack.AcknowledgedAt.UTC()
	}
	return cp.store.SaveDurableAgentState(*state)
}

func normalizeControlPlaneTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func outsideReplayWindow(timestamp time.Time, now time.Time, window time.Duration) bool {
	if timestamp.IsZero() {
		return true
	}
	delta := now.Sub(timestamp.UTC())
	if delta < 0 {
		delta = -delta
	}
	return delta > window
}
