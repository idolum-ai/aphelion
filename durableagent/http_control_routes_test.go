//go:build linux

package durableagent

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestHTTPControlReceiptReplayDoesNotDuplicateReviewArtifact(t *testing.T) {
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
			MessageID:       "artifact-retry-1",
			Sequence:        1,
			Timestamp:       time.Now().UTC(),
		},
		Artifact: core.DurableReviewArtifact{
			AgentID:       agent.AgentID,
			Summary:       "Retry-safe artifact.",
			IntervalLabel: "msg-10",
			LocalActions:  []string{"Prepared one artifact."},
		},
	}
	signature, err := SignEnvelopeHMAC(agent.ControlPlaneSecret, reqBody.Envelope, reqBody.Artifact)
	if err != nil {
		t.Fatalf("SignEnvelopeHMAC(review artifact) err = %v", err)
	}
	reqBody.Envelope.Signature = signature

	first := performJSONRequest(t, handler, http.MethodPost, ControlPlaneArtifactUploadPath, reqBody)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	second := performJSONRequest(t, handler, http.MethodPost, ControlPlaneArtifactUploadPath, reqBody)
	if second.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body = %q, want original %q", second.Body.String(), first.Body.String())
	}
	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want one event after replay", len(events))
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
