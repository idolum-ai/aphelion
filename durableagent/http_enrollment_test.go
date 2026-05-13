//go:build linux

package durableagent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestHTTPEnrollRegistersRemoteChildAndReturnsPolicy(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example/control",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	reqBody := core.DurableAgentEnrollmentRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessageEnrollment,
			MessageID:       "enroll-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
		Payload: bootstrap.EnrollmentPayload(),
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, reqBody.Payload)
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(enroll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature
	rec := performJSONRequest(t, handler, http.MethodPost, ControlPlaneEnrollPath, reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp core.DurableAgentEnrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal(response) err = %v", err)
	}
	if resp.Enrollment.AgentID != agent.AgentID {
		t.Fatalf("Enrollment.AgentID = %q, want %q", resp.Enrollment.AgentID, agent.AgentID)
	}
	if resp.Policy.PolicyVersion != 1 {
		t.Fatalf("Policy.PolicyVersion = %d, want 1", resp.Policy.PolicyVersion)
	}
	gotEnrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if gotEnrollment.AgentID != agent.AgentID {
		t.Fatalf("stored enrollment agent_id = %q, want %q", gotEnrollment.AgentID, agent.AgentID)
	}
}

func TestHTTPEnrollRequiresAndStoresTailnetPeerIdentity(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.LivePolicy.TailnetMode = "tsnet"
	agent.LivePolicy.TailnetHostname = "family-child"
	agent.LivePolicy.TailnetTags = []string{"tag:aphelion-child", "tag:family"}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store)
	handler.RequirePeerIdentity = true
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example/control",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	reqBody := core.DurableAgentEnrollmentRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessageEnrollment,
			MessageID:       "enroll-identity-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
		Payload: bootstrap.EnrollmentPayload(),
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, reqBody.Payload)
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(enroll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequestWithIdentity(t, handler.Handler(), http.MethodPost, ControlPlaneEnrollPath, reqBody, core.TailnetPeerIdentity{
		StableNodeID: "node-family-child",
		NodeName:     "family-child.example.ts.net",
		ComputedName: "family-child",
		LoginName:    "child-admin@example.com",
		Tags:         []string{"tag:family", "tag:aphelion-child"},
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
	if enrollment.TailnetIdentity.LoginName != "child-admin@example.com" {
		t.Fatalf("TailnetIdentity.LoginName = %q, want child-admin@example.com", enrollment.TailnetIdentity.LoginName)
	}
}

func TestHTTPEnrollDoesNotPersistEnrollmentForStaleEnvelope(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	now := time.Now().UTC()
	handler := NewHTTPHandler(store)
	handler.clock = func() time.Time { return now }
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example/control",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	reqBody := core.DurableAgentEnrollmentRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessageEnrollment,
			MessageID:       "enroll-stale-1",
			Sequence:        1,
			Timestamp:       now.Add(-20 * time.Minute),
		},
		Payload: bootstrap.EnrollmentPayload(),
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, reqBody.Payload)
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(enroll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	rec := performJSONRequest(t, handler.Handler(), http.MethodPost, ControlPlaneEnrollPath, reqBody)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
	if _, err := store.DurableAgentRemoteEnrollment(agent.AgentID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v, want sql.ErrNoRows", err)
	}
}
