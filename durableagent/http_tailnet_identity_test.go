//go:build linux

package durableagent

import (
	"net/http"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestHTTPControlRequestRejectsDifferentTailnetNode(t *testing.T) {
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
		TailnetIdentity: core.TailnetPeerIdentity{
			StableNodeID: "node-family-child",
			NodeName:     "family-child.example.ts.net",
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       "poll-wrong-node-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-intruder",
		NodeName:     "other.example.ts.net",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.LastSequence != 0 {
		t.Fatalf("LastSequence = %d, want rejected request not accepted", enrollment.LastSequence)
	}
}

func TestHTTPControlRequestBindsMissingTailnetIdentityAfterEnvelopeAcceptance(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.LivePolicy.TailnetMode = "tsnet"
	agent.LivePolicy.TailnetHostname = "family-child"
	agent.LivePolicy.TailnetTags = []string{"tag:aphelion-child"}
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

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       "poll-bind-tailnet-1",
			Sequence:        1,
			Timestamp:       now,
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		NodeName:     "family-child.example.ts.net",
		ComputedName: "family-child",
		Tags:         []string{"tag:aphelion-child"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.TailnetIdentity.StableNodeID != "node-family-child" {
		t.Fatalf("TailnetIdentity.StableNodeID = %q, want node-family-child", enrollment.TailnetIdentity.StableNodeID)
	}

	reqBody.Envelope.MessageID = "poll-bind-tailnet-2"
	reqBody.Envelope.Sequence = 2
	reqBody.Envelope.Signature, err = SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll second) err = %v", err)
	}
	rec = performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-other-child",
		NodeName:     "family-child.example.ts.net",
		ComputedName: "family-child",
		Tags:         []string{"tag:aphelion-child"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("different node status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestHTTPControlRequestDoesNotBindTailnetIdentityForStaleEnvelope(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.LivePolicy.TailnetMode = "tsnet"
	agent.LivePolicy.TailnetHostname = "family-child"
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

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       "poll-stale-tailnet-1",
			Sequence:        1,
			Timestamp:       now.Add(-20 * time.Minute),
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		ComputedName: "family-child",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.TailnetIdentity.StableNodeID != "" {
		t.Fatalf("TailnetIdentity.StableNodeID = %q, want no bind", enrollment.TailnetIdentity.StableNodeID)
	}
	if enrollment.LastSequence != 0 {
		t.Fatalf("LastSequence = %d, want stale request not accepted", enrollment.LastSequence)
	}
}

func TestHTTPControlRequestDoesNotBindTailnetIdentityWhenPolicyRejectsPeer(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.LivePolicy.TailnetMode = "tsnet"
	agent.LivePolicy.TailnetHostname = "family-child"
	agent.LivePolicy.TailnetTags = []string{"tag:aphelion-child"}
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

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       "poll-policy-reject-1",
			Sequence:        1,
			Timestamp:       now,
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-other-child",
		ComputedName: "other-child",
		Tags:         []string{"tag:other"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.TailnetIdentity.StableNodeID != "" {
		t.Fatalf("TailnetIdentity.StableNodeID = %q, want no bind", enrollment.TailnetIdentity.StableNodeID)
	}
	if enrollment.LastSequence != 0 {
		t.Fatalf("LastSequence = %d, want rejected request not accepted", enrollment.LastSequence)
	}
}
