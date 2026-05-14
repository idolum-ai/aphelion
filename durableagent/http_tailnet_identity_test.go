//go:build linux

package durableagent

import (
	"net/http"
	"strings"
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

func TestHTTPControlRequestRejectsBoundTailnetPeerMissingRequiredTag(t *testing.T) {
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
		TailnetIdentity: core.TailnetPeerIdentity{
			StableNodeID: "node-family-child",
			NodeName:     "family-child.example.ts.net",
			ComputedName: "family-child",
			LoginName:    "old-admin@example.com",
			Tags:         []string{"tag:aphelion-child"},
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := signedPolicyPollRequest(t, agent, "poll-bound-missing-tag-1", 1, now)

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		NodeName:     "family-child.example.ts.net",
		ComputedName: "family-child",
		LoginName:    "new-admin@example.com",
		Tags:         []string{"tag:other"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing required tag") {
		t.Fatalf("body = %s, want missing tag error", rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.LastSequence != 0 {
		t.Fatalf("LastSequence = %d, want rejected request not accepted", enrollment.LastSequence)
	}
	if enrollment.TailnetIdentity.LoginName != "old-admin@example.com" {
		t.Fatalf("TailnetIdentity.LoginName = %q, want unchanged stored identity", enrollment.TailnetIdentity.LoginName)
	}
	if got := strings.Join(enrollment.TailnetIdentity.Tags, ","); got != "tag:aphelion-child" {
		t.Fatalf("TailnetIdentity.Tags = %q, want unchanged required tag", got)
	}
}

func TestHTTPControlRequestRejectsBoundTailnetPeerWrongHostname(t *testing.T) {
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
		TailnetIdentity: core.TailnetPeerIdentity{
			StableNodeID: "node-family-child",
			NodeName:     "family-child.example.ts.net",
			ComputedName: "family-child",
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := signedPolicyPollRequest(t, agent, "poll-bound-wrong-host-1", 1, now)

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		NodeName:     "other-child.example.ts.net",
		ComputedName: "other-child",
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

func TestHTTPControlRequestRefreshesBoundTailnetPeerIdentity(t *testing.T) {
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
		TailnetIdentity: core.TailnetPeerIdentity{
			StableNodeID: "node-family-child",
			NodeName:     "family-child-old.example.ts.net",
			ComputedName: "family-child",
			LoginName:    "old-admin@example.com",
			Tags:         []string{"tag:aphelion-child"},
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	handler.clock = func() time.Time { return now }
	reqBody := signedPolicyPollRequest(t, agent, "poll-refresh-tailnet-1", 1, now)

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlanePolicyPollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		NodeName:     "family-child.example.ts.net",
		ComputedName: "family-child",
		LoginName:    "new-admin@example.com",
		Tags:         []string{"tag:aphelion-child", "tag:fresh"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.LastSequence != 1 {
		t.Fatalf("LastSequence = %d, want accepted request", enrollment.LastSequence)
	}
	if enrollment.TailnetIdentity.NodeName != "family-child.example.ts.net" {
		t.Fatalf("TailnetIdentity.NodeName = %q, want refreshed node name", enrollment.TailnetIdentity.NodeName)
	}
	if enrollment.TailnetIdentity.LoginName != "new-admin@example.com" {
		t.Fatalf("TailnetIdentity.LoginName = %q, want refreshed login", enrollment.TailnetIdentity.LoginName)
	}
	if got := strings.Join(enrollment.TailnetIdentity.Tags, ","); got != "tag:aphelion-child,tag:fresh" {
		t.Fatalf("TailnetIdentity.Tags = %q, want refreshed tags", got)
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

func signedPolicyPollRequest(t *testing.T, agent core.DurableAgent, messageID string, sequence int64, timestamp time.Time) core.DurableAgentPolicyPollRequest {
	t.Helper()
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       messageID,
			Sequence:        sequence,
			Timestamp:       timestamp,
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
	return reqBody
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
