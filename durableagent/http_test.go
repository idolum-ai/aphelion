//go:build linux

package durableagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		KeyFingerprint:   "child-key-fp",
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
	if gotEnrollment.KeyFingerprint != "child-key-fp" {
		t.Fatalf("KeyFingerprint = %q, want child-key-fp", gotEnrollment.KeyFingerprint)
	}
}

func TestHTTPPolicyPollReturnsCurrentPolicySnapshot(t *testing.T) {
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
		KeyFingerprint:   "child-key-fp",
		Status:           "active",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()
	reqBody := core.DurableAgentPolicyPollRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessagePolicyPoll,
			MessageID:       "poll-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
		KnownVersion: 0,
		KnownHash:    "",
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, struct {
		KnownVersion int64  `json:"known_version,omitempty"`
		KnownHash    string `json:"known_hash,omitempty"`
	}{
		KnownVersion: reqBody.KnownVersion,
		KnownHash:    reqBody.KnownHash,
	})
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(policy poll) err = %v", err)
	}
	reqBody.Envelope.Signature = signature
	rec := performJSONRequest(t, handler, http.MethodPost, ControlPlanePolicyPollPath, reqBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp core.DurableAgentPolicyPollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal(response) err = %v", err)
	}
	if !resp.Changed {
		t.Fatal("Changed = false, want true for unknown child policy state")
	}
	if resp.Snapshot.AgentID != agent.AgentID {
		t.Fatalf("Snapshot.AgentID = %q, want %q", resp.Snapshot.AgentID, agent.AgentID)
	}
}

func TestHTTPReviewArtifactUploadQueuesReviewEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.ReviewTargetChatID = 1001
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example/control",
		KeyFingerprint:   "child-key-fp",
		Status:           "active",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()
	reqBody := core.DurableAgentReviewArtifactUploadRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessageReviewArtifactUpload,
			MessageID:       "artifact-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
		Artifact: core.DurableReviewArtifact{
			AgentID:       agent.AgentID,
			Summary:       "Family calendar changed and may need parent visibility.",
			IntervalLabel: "msg-9",
			LocalActions:  []string{"Held a reply pending parent visibility."},
			Questions:     []string{"Should this update be retained in durable continuity?"},
			RiskFlags:     []string{"family_relevant_update"},
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, reqBody.Artifact)
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(review artifact) err = %v", err)
	}
	reqBody.Envelope.Signature = signature
	rec := performJSONRequest(t, handler, http.MethodPost, ControlPlaneArtifactUploadPath, reqBody)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Summary, "Family calendar changed") {
		t.Fatalf("Summary = %q, want uploaded artifact summary", events[0].Summary)
	}
}

func TestHTTPHandlerVerifierRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store)
	handler.Verifier = func(envelope core.DurableAgentControlEnvelope, payload any) error {
		if envelope.Signature != "expected-signature" {
			return errors.New("invalid signature")
		}
		return nil
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
			Signature:       "wrong-signature",
		},
		Payload: core.DurableAgentRemoteBootstrap{
			ReviewTargetChatID: agent.ReviewTargetChatID,
			AgentID:            agent.AgentID,
			ParentAgentID:      "house",
			ChannelKind:        agent.ChannelKind,
			ParentControlURL:   "https://house.example",
			EnrollmentToken:    "enroll-token-1",
			KeyFingerprint:     "child-key-fp",
			ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
			BootstrapLLM:       testDurableAgentBootstrapLLM(),
			BootstrapCeiling:   agent.BootstrapCeiling,
		}.EnrollmentPayload(),
	}
	rec := performJSONRequest(t, handler.Handler(), http.MethodPost, ControlPlaneEnrollPath, reqBody)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPClientSignerSatisfiesHandlerVerifier(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store)
	handler.Verifier = func(envelope core.DurableAgentControlEnvelope, payload any) error {
		if envelope.Signature != "expected-signature" {
			return errors.New("invalid signature")
		}
		return nil
	}

	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		ReviewTargetChatID: agent.ReviewTargetChatID,
		AgentID:            agent.AgentID,
		ParentAgentID:      "house",
		ChannelKind:        agent.ChannelKind,
		ParentControlURL:   "https://house.example",
		EnrollmentToken:    "enroll-token-1",
		KeyFingerprint:     "child-key-fp",
		ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:       testDurableAgentBootstrapLLM(),
		BootstrapCeiling:   agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = &http.Client{Transport: handlerRoundTripper{handler: handler.Handler()}}
	client.Signer = func(core.DurableAgentControlEnvelope, any) (string, error) {
		return "expected-signature", nil
	}

	resp, err := client.Enroll(context.Background())
	if err != nil {
		t.Fatalf("Enroll() err = %v", err)
	}
	if resp.Policy.PolicyVersion != 1 {
		t.Fatalf("policy version = %d, want 1", resp.Policy.PolicyVersion)
	}
}

func TestHTTPHandlerRejectsWrongStoreBackedSignature(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	agent.ControlPlaneSecret = "expected-control-secret"
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		ReviewTargetChatID: agent.ReviewTargetChatID,
		AgentID:            agent.AgentID,
		ParentAgentID:      "house",
		ChannelKind:        agent.ChannelKind,
		ParentControlURL:   "https://house.example",
		EnrollmentToken:    "wrong-control-secret",
		KeyFingerprint:     "child-key-fp",
		ProtocolVersion:    core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:       testDurableAgentBootstrapLLM(),
		BootstrapCeiling:   agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = &http.Client{Transport: handlerRoundTripper{handler: NewHTTPHandler(store).Handler()}}

	_, err = client.Enroll(context.Background())
	if err == nil {
		t.Fatal("Enroll() err = nil, want invalid signature failure")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("Enroll() err = %v, want invalid signature", err)
	}
}

func performJSONRequest(t *testing.T, handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal(body) err = %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
