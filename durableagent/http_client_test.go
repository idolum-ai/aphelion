//go:build linux

package durableagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestHTTPClientSupportsControlPlaneBasePath(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store).HandlerWithBasePath("/control")
	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example/control",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = &http.Client{Transport: handlerRoundTripper{handler: handler}}

	resp, err := client.Enroll(context.Background())
	if err != nil {
		t.Fatalf("Enroll() err = %v", err)
	}
	if resp.Enrollment.AgentID != agent.AgentID {
		t.Fatalf("Enrollment.AgentID = %q, want %q", resp.Enrollment.AgentID, agent.AgentID)
	}
}

func TestHTTPClientReattestsAndUpdatesParentControlURL(t *testing.T) {
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

	client.Bootstrap.ParentControlURL = "https://house-alt.example"
	resp, err := client.Reattest(context.Background())
	if err != nil {
		t.Fatalf("Reattest() err = %v", err)
	}
	if resp.Enrollment.ParentControlURL != "https://house-alt.example" {
		t.Fatalf("Enrollment.ParentControlURL = %q, want https://house-alt.example", resp.Enrollment.ParentControlURL)
	}

	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.ParentControlURL != "https://house-alt.example" {
		t.Fatalf("stored ParentControlURL = %q, want https://house-alt.example", enrollment.ParentControlURL)
	}
}

func TestHTTPClientRejectsOldControlPlaneSecretAfterRotation(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	handler := NewHTTPHandler(store).Handler()
	oldClient, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient(old) err = %v", err)
	}
	oldClient.Client = &http.Client{Transport: handlerRoundTripper{handler: handler}}
	if _, err := oldClient.Enroll(context.Background()); err != nil {
		t.Fatalf("Enroll() err = %v", err)
	}

	agent.ControlPlaneSecret = "enroll-token-2"
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent(rotated) err = %v", err)
	}

	if _, err := oldClient.PollPolicy(context.Background(), 0, ""); err == nil {
		t.Fatal("PollPolicy(old token) err = nil, want invalid signature")
	} else if err.Error() != "invalid signature" {
		t.Fatalf("PollPolicy(old token) err = %v, want invalid signature", err)
	}

	newClient, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-2",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient(new) err = %v", err)
	}
	newClient.Client = &http.Client{Transport: handlerRoundTripper{handler: handler}}
	enrollment, err := store.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() err = %v", err)
	}
	seedRemoteClientSequence(newClient, enrollment)
	if _, err := newClient.PollPolicy(context.Background(), 0, ""); err != nil {
		t.Fatalf("PollPolicy(new token) err = %v", err)
	}
}

func TestHTTPClientPollsAndAcknowledgesParentConversation(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	agent := testRemoteDurableAgent()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.UpsertDurableAgentRemoteEnrollment(core.DurableAgentRemoteEnrollment{
		AgentID:          agent.AgentID,
		ParentControlURL: "https://house.example",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		Status:           "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}.WithConversationMessage("parent", "Check the remote child health.", time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	client, err := NewHTTPClient(core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	})
	if err != nil {
		t.Fatalf("NewHTTPClient() err = %v", err)
	}
	client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(store).Handler())

	resp, err := client.PollParentConversation(context.Background(), 5)
	if err != nil {
		t.Fatalf("PollParentConversation() err = %v", err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].Text != "Check the remote child health." {
		t.Fatalf("PollParentConversation() = %#v, want pending parent message", resp)
	}
	if resp.Messages[0].MessageID == "" {
		t.Fatal("PollParentConversation() message_id is empty")
	}
	if _, err := client.AcknowledgeParentConversation(context.Background(), core.DurableAgentParentConversationAcknowledgement{
		AgentID:        agent.AgentID,
		AcknowledgedAt: time.Date(2026, 5, 13, 12, 0, 15, 0, time.UTC),
	}); err == nil {
		t.Fatal("AcknowledgeParentConversation() without message_ids err = nil, want rejection")
	}
	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(before race append) err = %v", err)
	}
	continuity, err = core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState(before race append) err = %v", err)
	}
	if pending := continuity.PendingParentConversationMessages(5); len(pending) != 1 || pending[0].MessageID != resp.Messages[0].MessageID {
		t.Fatalf("pending after rejected ack = %#v, want original polled message", pending)
	}
	continuity = continuity.WithConversationMessage("parent", "New instruction after poll.", time.Date(2026, 5, 13, 12, 0, 30, 0, time.UTC))
	raw, err = continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal(after race append) err = %v", err)
	}
	state.StateJSON = raw
	if err := store.SaveDurableAgentState(*state); err != nil {
		t.Fatalf("SaveDurableAgentState(after race append) err = %v", err)
	}
	ackResp, err := client.AcknowledgeParentConversation(context.Background(), core.DurableAgentParentConversationAcknowledgement{
		AgentID:        agent.AgentID,
		MessageIDs:     []string{resp.Messages[0].MessageID},
		AcknowledgedAt: time.Date(2026, 5, 13, 12, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AcknowledgeParentConversation() err = %v", err)
	}
	if !ackResp.Accepted {
		t.Fatal("AcknowledgeParentConversation().Accepted = false, want true")
	}
	state, err = store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updated, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updated.PendingParentConversationMessages(5); len(pending) != 1 || pending[0].Text != "New instruction after poll." {
		t.Fatalf("pending parent messages = %#v, want only race-appended message after ack", pending)
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
