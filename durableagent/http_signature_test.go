//go:build linux

package durableagent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

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

func TestHTTPHandlerRejectsNilVerifier(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store)
	handler.Verifier = nil

	reqBody := core.DurableAgentEnrollmentRequest{
		Envelope: core.DurableAgentControlEnvelope{
			ProtocolVersion: core.DefaultDurableAgentControlProtocolVersion,
			AgentID:         agent.AgentID,
			ParentAgentID:   "house",
			MessageKind:     core.DurableAgentControlMessageEnrollment,
			MessageID:       "enroll-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
			Signature:       "signature",
		},
		Payload: core.DurableAgentRemoteBootstrap{
			ReviewTargetChatID: agent.ReviewTargetChatID,
			AgentID:            agent.AgentID,
			ParentAgentID:      "house",
			ChannelKind:        agent.ChannelKind,
			ParentControlURL:   "https://house.example",
			EnrollmentToken:    "enroll-token-1",
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
