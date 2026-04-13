//go:build linux

package durableagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestHTTPClientPolicyPollAndAckFlow(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()

	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		KeyFingerprint:   "child-key-fp",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = &http.Client{Transport: handlerRoundTripper{handler: handler}}

	enrollmentResp, err := client.Enroll(context.Background())
	if err != nil {
		t.Fatalf("Enroll() err = %v", err)
	}
	if enrollmentResp.Policy.PolicyVersion != 1 {
		t.Fatalf("enrolled policy version = %d, want 1", enrollmentResp.Policy.PolicyVersion)
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

	pollResp, err := client.PollPolicy(context.Background(), enrollmentResp.Policy.PolicyVersion, enrollmentResp.Policy.PolicyHash)
	if err != nil {
		t.Fatalf("PollPolicy() err = %v", err)
	}
	if !pollResp.Changed {
		t.Fatal("PollPolicy().Changed = false, want true after parent policy update")
	}
	if pollResp.Snapshot.PolicyVersion != updated.PolicyVersion {
		t.Fatalf("poll policy version = %d, want %d", pollResp.Snapshot.PolicyVersion, updated.PolicyVersion)
	}

	ackResp, err := client.AcknowledgePolicy(context.Background(), core.DurableAgentPolicyAcknowledgement{
		AgentID:             agent.AgentID,
		AcknowledgedVersion: pollResp.Snapshot.PolicyVersion,
		AcknowledgedHash:    pollResp.Snapshot.PolicyHash,
		AppliedVersion:      pollResp.Snapshot.PolicyVersion,
		AppliedHash:         pollResp.Snapshot.PolicyHash,
		Status:              "applied",
	})
	if err != nil {
		t.Fatalf("AcknowledgePolicy() err = %v", err)
	}
	if !ackResp.Accepted {
		t.Fatal("AcknowledgePolicy().Accepted = false, want true")
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

func TestHTTPClientUploadsReviewArtifact(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()

	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		KeyFingerprint:   "child-key-fp",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = &http.Client{Transport: handlerRoundTripper{handler: handler}}
	if _, err := client.Enroll(context.Background()); err != nil {
		t.Fatalf("Enroll() err = %v", err)
	}

	resp, err := client.UploadReviewArtifact(context.Background(), core.DurableReviewArtifact{
		AgentID:       agent.AgentID,
		Summary:       "Family calendar changed and may need parent visibility.",
		IntervalLabel: "msg-9",
		LocalActions:  []string{"Held a reply pending parent visibility."},
		Questions:     []string{"Should this update be retained in durable continuity?"},
		RiskFlags:     []string{"family_relevant_update"},
	})
	if err != nil {
		t.Fatalf("UploadReviewArtifact() err = %v", err)
	}
	if !resp.Accepted {
		t.Fatal("UploadReviewArtifact().Accepted = false, want true")
	}
	events, err := store.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events len = %d, want 1", len(events))
	}
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (t handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}
