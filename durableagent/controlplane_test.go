//go:build linux

package durableagent

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRemoteControlPlaneRejectsReplay(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		Status:           "active",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	cp := NewControlPlane(store, 10*time.Minute)
	now := time.Now().UTC()
	envelope := core.DurableAgentControlEnvelope{
		ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
		AgentID:         agent.AgentID,
		ParentAgentID:   "house",
		MessageKind:     core.DurableAgentControlMessagePolicyPoll,
		MessageID:       "msg-1",
		Sequence:        1,
		Timestamp:       now,
		Signature:       "signed-envelope",
	}
	if err := cp.AcceptEnvelope(envelope, now); err != nil {
		t.Fatalf("AcceptEnvelope() first err = %v", err)
	}
	if err := cp.AcceptEnvelope(envelope, now); err == nil {
		t.Fatal("AcceptEnvelope() replay err = nil, want replay rejection")
	} else if !strings.Contains(err.Error(), "replay") {
		t.Fatalf("AcceptEnvelope() replay err = %v, want replay rejection", err)
	}

	outOfOrder := envelope
	outOfOrder.MessageID = "msg-2"
	if err := cp.AcceptEnvelope(outOfOrder, now); err == nil {
		t.Fatal("AcceptEnvelope() out-of-order err = nil, want replay rejection")
	} else if !strings.Contains(err.Error(), "out-of-order") {
		t.Fatalf("AcceptEnvelope() out-of-order err = %v, want out-of-order rejection", err)
	}
}

func TestRemotePolicyAcknowledgementCarriesAppliedVersion(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	updated, _, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface bounded family coordination, but allow reviewed drafting.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 0, "offer remote narrowed policy")
	if err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		Status:           "active",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	cp := NewControlPlane(store, 10*time.Minute)
	now := time.Now().UTC()
	envelope := core.DurableAgentControlEnvelope{
		ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
		AgentID:         agent.AgentID,
		ParentAgentID:   "house",
		MessageKind:     core.DurableAgentControlMessagePolicyAck,
		MessageID:       "ack-1",
		Sequence:        1,
		Timestamp:       now,
		Signature:       "signed-envelope",
	}
	ack := core.DurableAgentPolicyAcknowledgement{
		AgentID:             agent.AgentID,
		AcknowledgedVersion: updated.PolicyVersion,
		AcknowledgedHash:    updated.PolicyHash,
		AppliedVersion:      updated.PolicyVersion,
		AppliedHash:         updated.PolicyHash,
		Status:              "applied",
		AcknowledgedAt:      now,
	}
	if err := cp.AcceptPolicyAcknowledgement(envelope, ack, now); err != nil {
		t.Fatalf("AcceptPolicyAcknowledgement() err = %v", err)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastAcknowledgedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAcknowledgedPolicyVersion = %d, want %d", state.LastAcknowledgedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastAppliedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAppliedPolicyVersion = %d, want %d", state.LastAppliedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastApplyStatus != "applied" {
		t.Fatalf("LastApplyStatus = %q, want applied", state.LastApplyStatus)
	}
}

func TestRemotePolicyAcknowledgementRejectsStalePolicyVersion(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	initial, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(initial) err = %v", err)
	}
	if _, _, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Narrowed policy that supersedes the initial child snapshot.",
		CapabilityEnvelope: []string{"bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 0, "supersede remote policy before stale ack"); err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		Status:           "active",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	cp := NewControlPlane(store, 10*time.Minute)
	now := time.Now().UTC()
	envelope := core.DurableAgentControlEnvelope{
		ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
		AgentID:         agent.AgentID,
		ParentAgentID:   "house",
		MessageKind:     core.DurableAgentControlMessagePolicyAck,
		MessageID:       "stale-ack-1",
		Sequence:        1,
		Timestamp:       now,
		Signature:       "signed-envelope",
	}
	err = cp.AcceptPolicyAcknowledgement(envelope, core.DurableAgentPolicyAcknowledgement{
		AgentID:             agent.AgentID,
		AcknowledgedVersion: initial.PolicyVersion,
		AcknowledgedHash:    initial.PolicyHash,
		AppliedVersion:      initial.PolicyVersion,
		AppliedHash:         initial.PolicyHash,
		Status:              "applied",
		AcknowledgedAt:      now,
	}, now)
	if err == nil {
		t.Fatal("AcceptPolicyAcknowledgement() err = nil, want stale policy rejection")
	}
	if !strings.Contains(err.Error(), "stale durable agent policy acknowledgement") {
		t.Fatalf("AcceptPolicyAcknowledgement() err = %v, want stale policy rejection", err)
	}

	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.LastSequence != 0 {
		t.Fatalf("LastSequence = %d, want stale acknowledgement rejected before sequence acceptance", enrollment.LastSequence)
	}
}

func TestRemotePolicyPollReturnsCurrentPolicySnapshot(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	cp := NewControlPlane(store, 10*time.Minute)
	snapshot, err := cp.PolicySnapshot(agent.AgentID)
	if err != nil {
		t.Fatalf("PolicySnapshot() err = %v", err)
	}
	if snapshot.AgentID != agent.AgentID {
		t.Fatalf("PolicySnapshot().AgentID = %q, want %q", snapshot.AgentID, agent.AgentID)
	}
	if snapshot.PolicyVersion != 1 {
		t.Fatalf("PolicySnapshot().PolicyVersion = %d, want 1", snapshot.PolicyVersion)
	}
	if snapshot.PolicyHash == "" {
		t.Fatal("PolicySnapshot().PolicyHash is empty")
	}
	if snapshot.LivePolicy.Charter != agent.LivePolicy.Charter {
		t.Fatalf("PolicySnapshot().LivePolicy.Charter = %q, want %q", snapshot.LivePolicy.Charter, agent.LivePolicy.Charter)
	}
}

func testRemoteDurableAgent() core.DurableAgent {
	return core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		ControlPlaneSecret: "enroll-token-1",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: testDurableAgentBootstrapLLM(),
		Status:       "active",
	}
}
