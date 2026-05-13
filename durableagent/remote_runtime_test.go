//go:build linux

package durableagent

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestRemoteRuntimeSyncEnrollsAndAppliesInitialPolicy(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	bootstrapPath := filepath.Join(t.TempDir(), "remote-bootstrap.json")
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
		LocalStorageRoots: []string{
			filepath.Join(t.TempDir(), "work"),
			filepath.Join(t.TempDir(), "memory"),
		},
		SecretScopes:  []string{"telegram_bot"},
		NetworkPolicy: "restricted",
	}
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}

	rt := NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
		client, err := NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(parentStore).Handler())
		return client, nil
	})
	result, err := rt.Sync(context.Background(), bootstrapPath)
	if err != nil {
		t.Fatalf("Sync() err = %v", err)
	}
	if !result.Enrolled {
		t.Fatal("Sync().Enrolled = false, want true on first sync")
	}
	if !result.PolicyChanged {
		t.Fatal("Sync().PolicyChanged = false, want true on initial sync")
	}
	if result.PolicyVersion != 1 {
		t.Fatalf("Sync().PolicyVersion = %d, want 1", result.PolicyVersion)
	}

	localAgent, err := childStore.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgent() err = %v", err)
	}
	if localAgent.PolicyVersion != 1 {
		t.Fatalf("local PolicyVersion = %d, want 1", localAgent.PolicyVersion)
	}
	if localAgent.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("local BootstrapLLM.NativeProvider = %q, want openrouter", localAgent.BootstrapLLM.NativeProvider)
	}
	state, err := childStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != 1 {
		t.Fatalf("LastOfferedPolicyVersion = %d, want 1", state.LastOfferedPolicyVersion)
	}
	if state.LastAppliedPolicyVersion != 1 {
		t.Fatalf("LastAppliedPolicyVersion = %d, want 1", state.LastAppliedPolicyVersion)
	}
	if state.LastAcknowledgedPolicyVersion != 1 {
		t.Fatalf("LastAcknowledgedPolicyVersion = %d, want 1", state.LastAcknowledgedPolicyVersion)
	}
}

func TestRemoteRuntimeSyncPollsAndAppliesUpdatedPolicy(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	bootstrapPath := filepath.Join(t.TempDir(), "remote-bootstrap.json")
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}

	rt := NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
		client, err := NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(parentStore).Handler())
		return client, nil
	})
	if _, err := rt.Sync(context.Background(), bootstrapPath); err != nil {
		t.Fatalf("first Sync() err = %v", err)
	}

	updated, _, err := parentStore.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface bounded family coordination, but allow reviewed drafting.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 0, "offer remote narrowed policy")
	if err != nil {
		t.Fatalf("parent ApplyDurableAgentLivePolicy() err = %v", err)
	}

	result, err := rt.Sync(context.Background(), bootstrapPath)
	if err != nil {
		t.Fatalf("second Sync() err = %v", err)
	}
	if result.Enrolled {
		t.Fatal("Sync().Enrolled = true, want false after initial enrollment")
	}
	if !result.PolicyChanged {
		t.Fatal("Sync().PolicyChanged = false, want true after parent update")
	}
	if result.PolicyVersion != updated.PolicyVersion {
		t.Fatalf("Sync().PolicyVersion = %d, want %d", result.PolicyVersion, updated.PolicyVersion)
	}

	localAgent, err := childStore.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgent() err = %v", err)
	}
	if localAgent.PolicyVersion != updated.PolicyVersion {
		t.Fatalf("local PolicyVersion = %d, want %d", localAgent.PolicyVersion, updated.PolicyVersion)
	}
	if localAgent.LivePolicy.OutboundMode != "draft_only" {
		t.Fatalf("local OutboundMode = %q, want draft_only", localAgent.LivePolicy.OutboundMode)
	}
	state, err := childStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentState() err = %v", err)
	}
	if state.LastAppliedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAppliedPolicyVersion = %d, want %d", state.LastAppliedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastAcknowledgedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAcknowledgedPolicyVersion = %d, want %d", state.LastAcknowledgedPolicyVersion, updated.PolicyVersion)
	}
}

func TestRemoteRuntimeSyncParentConversationPreservesParentMessageID(t *testing.T) {
	t.Parallel()

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	agent := testRemoteDurableAgent()
	if err := childStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("child UpsertDurableAgent() err = %v", err)
	}

	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	parentMessage := core.DurableAgentConversationMessage{
		MessageID: "parent-msg-opaque-1",
		Role:      "parent",
		Text:      "Use the exact parent message identity.",
		CreatedAt: time.Date(2026, 5, 13, 13, 30, 0, 0, time.UTC),
	}
	client := &remoteRuntimeParentConversationClient{
		pollResponse: core.DurableAgentParentConversationPollResponse{
			Messages: []core.DurableAgentConversationMessage{parentMessage},
		},
	}
	rt := NewRemoteRuntime(childStore, nil)

	messageIDs, err := rt.syncParentConversation(context.Background(), client, bootstrap)
	if err != nil {
		t.Fatalf("syncParentConversation() err = %v", err)
	}
	if len(messageIDs) != 1 || messageIDs[0] != parentMessage.MessageID {
		t.Fatalf("syncParentConversation() messageIDs = %#v, want [%q]", messageIDs, parentMessage.MessageID)
	}

	state, err := childStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) != 1 {
		t.Fatalf("Conversation = %#v, want one parent message", continuity.Conversation)
	}
	stored := continuity.Conversation.Messages[0]
	if stored.MessageID != parentMessage.MessageID {
		t.Fatalf("stored MessageID = %q, want %q", stored.MessageID, parentMessage.MessageID)
	}
	regeneratedIDs := core.DurableAgentConversationMessageIDs([]core.DurableAgentConversationMessage{{
		Role:      parentMessage.Role,
		Text:      parentMessage.Text,
		CreatedAt: parentMessage.CreatedAt,
	}})
	if len(regeneratedIDs) != 1 {
		t.Fatalf("regeneratedIDs len = %d, want 1", len(regeneratedIDs))
	}
	if stored.MessageID == regeneratedIDs[0] {
		t.Fatalf("stored MessageID = %q, want preserved opaque id instead of regenerated id", stored.MessageID)
	}
}

func TestRemoteRuntimeSyncParentConversationUpgradesStoredGeneratedID(t *testing.T) {
	t.Parallel()

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	agent := testRemoteDurableAgent()
	if err := childStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("child UpsertDurableAgent() err = %v", err)
	}

	createdAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	text := "Check the remote child health."
	initialContinuity := core.DurableAgentContinuityState{}.WithConversationMessage("parent", text, createdAt)
	if initialContinuity.Conversation == nil || len(initialContinuity.Conversation.Messages) != 1 {
		t.Fatalf("initial conversation = %#v, want one parent message", initialContinuity.Conversation)
	}
	generatedID := initialContinuity.Conversation.Messages[0].MessageID
	if generatedID == "" || !strings.HasPrefix(generatedID, "dcm_") {
		t.Fatalf("generatedID = %q, want dcm_ id", generatedID)
	}
	raw, err := initialContinuity.Marshal()
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if err := childStore.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		Status:    "active",
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("child SaveDurableAgentState() err = %v", err)
	}

	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	parentMessage := core.DurableAgentConversationMessage{
		MessageID: "parent-msg-opaque-1",
		Role:      "parent",
		Text:      text,
		CreatedAt: createdAt,
	}
	client := &remoteRuntimeParentConversationClient{
		pollResponse: core.DurableAgentParentConversationPollResponse{
			Messages: []core.DurableAgentConversationMessage{parentMessage},
		},
	}
	rt := NewRemoteRuntime(childStore, nil)

	messageIDs, err := rt.syncParentConversation(context.Background(), client, bootstrap)
	if err != nil {
		t.Fatalf("syncParentConversation() err = %v", err)
	}
	if len(messageIDs) != 1 || messageIDs[0] != parentMessage.MessageID {
		t.Fatalf("syncParentConversation() messageIDs = %#v, want [%q]", messageIDs, parentMessage.MessageID)
	}

	state, err := childStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) != 1 {
		t.Fatalf("Conversation = %#v, want one upgraded parent message", continuity.Conversation)
	}
	stored := continuity.Conversation.Messages[0]
	if stored.MessageID != parentMessage.MessageID {
		t.Fatalf("stored MessageID = %q, want %q", stored.MessageID, parentMessage.MessageID)
	}
	if stored.MessageID == generatedID {
		t.Fatalf("stored MessageID = %q, want replacement of generated id", stored.MessageID)
	}
	if stored.Text != text || !stored.CreatedAt.Equal(createdAt) || stored.Role != "parent" {
		t.Fatalf("stored message = %#v, want original parent content with canonical id", stored)
	}
}

func TestRemoteRuntimeUploadReviewArtifactQueuesParentReviewAndUpdatesLocalContinuity(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	bootstrapPath := filepath.Join(t.TempDir(), "remote-bootstrap.json")
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
		LocalStorageRoots: []string{
			filepath.Join(t.TempDir(), "work"),
			filepath.Join(t.TempDir(), "memory"),
		},
		SecretScopes:  []string{"telegram_bot"},
		NetworkPolicy: "restricted",
	}
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}

	rt := NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
		client, err := NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(parentStore).Handler())
		return client, nil
	})
	if _, err := rt.Sync(context.Background(), bootstrapPath); err != nil {
		t.Fatalf("Sync() err = %v", err)
	}

	artifact := core.DurableReviewArtifact{
		Summary:       "Calendar drift is building around the family dinner plan.",
		IntervalLabel: "messages 12-18",
		LocalActions:  []string{"Held reply pending parent visibility."},
		Questions:     []string{"Should this become a standing family reminder?"},
		RiskFlags:     []string{"family_relevant_update"},
	}
	result, err := rt.UploadReviewArtifact(context.Background(), bootstrapPath, artifact)
	if err != nil {
		t.Fatalf("UploadReviewArtifact() err = %v", err)
	}
	if result.ReviewEventID == 0 {
		t.Fatalf("UploadReviewArtifact().ReviewEventID = %d, want non-zero", result.ReviewEventID)
	}

	events, err := parentStore.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("parent pending len = %d, want 1", len(events))
	}
	if events[0].ID != result.ReviewEventID {
		t.Fatalf("parent review event id = %d, want %d", events[0].ID, result.ReviewEventID)
	}
	if !strings.Contains(events[0].Summary, "Calendar drift is building") {
		t.Fatalf("parent Summary = %q, want uploaded review summary", events[0].Summary)
	}

	state, err := childStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentState() err = %v", err)
	}
	if state.LastReviewAt.IsZero() {
		t.Fatal("LastReviewAt is zero, want remote upload to update local review state")
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.ReviewRefs) != 1 {
		t.Fatalf("ReviewRefs len = %d, want 1", len(continuity.ReviewRefs))
	}
	if continuity.ReviewRefs[0].ReviewEventID != result.ReviewEventID {
		t.Fatalf("ReviewRefs[0].ReviewEventID = %d, want %d", continuity.ReviewRefs[0].ReviewEventID, result.ReviewEventID)
	}
	if len(continuity.PendingQuestions) != 1 {
		t.Fatalf("PendingQuestions len = %d, want 1", len(continuity.PendingQuestions))
	}
	if !strings.Contains(continuity.PendingQuestions[0].Question, "standing family reminder") {
		t.Fatalf("PendingQuestions[0].Question = %q, want uploaded question", continuity.PendingQuestions[0].Question)
	}
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) != 1 {
		t.Fatalf("Conversation = %#v, want one child message", continuity.Conversation)
	}
	if continuity.Conversation.Messages[0].Role != "child" {
		t.Fatalf("Conversation.Messages[0].Role = %q, want child", continuity.Conversation.Messages[0].Role)
	}
	if !strings.Contains(continuity.Conversation.Messages[0].Text, "Calendar drift is building") {
		t.Fatalf("Conversation.Messages[0].Text = %q, want artifact summary", continuity.Conversation.Messages[0].Text)
	}

	enrollment, err := childStore.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentRemoteEnrollment() err = %v", err)
	}
	if enrollment.LastSequence <= 2 {
		t.Fatalf("LastSequence = %d, want > 2 after sync and upload", enrollment.LastSequence)
	}
}

func TestRemoteRuntimeSyncReattestsWhenParentControlURLChanges(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	bootstrapPath := filepath.Join(t.TempDir(), "remote-bootstrap.json")
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}

	rt := NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
		client, err := NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(parentStore).Handler())
		return client, nil
	})
	if _, err := rt.Sync(context.Background(), bootstrapPath); err != nil {
		t.Fatalf("first Sync() err = %v", err)
	}

	bootstrap.ParentControlURL = "https://house-alt.example"
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap(updated) err = %v", err)
	}

	result, err := rt.Sync(context.Background(), bootstrapPath)
	if err != nil {
		t.Fatalf("second Sync() err = %v", err)
	}
	if result.Enrolled {
		t.Fatal("Sync().Enrolled = true, want false on re-attestation")
	}

	parentEnrollment, err := parentStore.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("parent DurableAgentRemoteEnrollment() err = %v", err)
	}
	if parentEnrollment.ParentControlURL != "https://house-alt.example" {
		t.Fatalf("parent ParentControlURL = %q, want https://house-alt.example", parentEnrollment.ParentControlURL)
	}
	childEnrollment, err := childStore.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("child DurableAgentRemoteEnrollment() err = %v", err)
	}
	if childEnrollment.ParentControlURL != "https://house-alt.example" {
		t.Fatalf("child ParentControlURL = %q, want https://house-alt.example", childEnrollment.ParentControlURL)
	}
}

func TestRemoteRuntimeSyncFailsWhenParentEnrollmentRevoked(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}

	childStore, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "child.db"))
	if err != nil {
		t.Fatalf("child NewSQLiteStore() err = %v", err)
	}
	defer childStore.Close()

	bootstrapPath := filepath.Join(t.TempDir(), "remote-bootstrap.json")
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID:          agent.AgentID,
		ParentAgentID:    "house",
		ChannelKind:      agent.ChannelKind,
		ParentControlURL: "https://house.example",
		EnrollmentToken:  "enroll-token-1",
		ProtocolVersion:  core.DefaultDurableAgentControlProtocolVersion,
		BootstrapLLM:     testDurableAgentBootstrapLLM(),
		BootstrapCeiling: agent.BootstrapCeiling,
	}
	if err := WriteRemoteBootstrap(bootstrapPath, bootstrap); err != nil {
		t.Fatalf("WriteRemoteBootstrap() err = %v", err)
	}

	rt := NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
		client, err := NewHTTPClient(b)
		if err != nil {
			return nil, err
		}
		client.Client = remoteRuntimeHTTPClient(NewHTTPHandler(parentStore).Handler())
		return client, nil
	})
	if _, err := rt.Sync(context.Background(), bootstrapPath); err != nil {
		t.Fatalf("first Sync() err = %v", err)
	}

	parentEnrollment, err := parentStore.DurableAgentRemoteEnrollment(agent.AgentID)
	if err != nil {
		t.Fatalf("parent DurableAgentRemoteEnrollment() err = %v", err)
	}
	parentEnrollment.Status = "revoked"
	parentEnrollment.RevokedAt = rt.now()
	if err := parentStore.UpsertDurableAgentRemoteEnrollment(*parentEnrollment); err != nil {
		t.Fatalf("UpsertDurableAgentRemoteEnrollment(revoked) err = %v", err)
	}

	if _, err := rt.Sync(context.Background(), bootstrapPath); err == nil {
		t.Fatal("Sync() err = nil, want revoked enrollment failure")
	} else if !strings.Contains(err.Error(), "not active") {
		t.Fatalf("Sync() err = %v, want not active", err)
	}
}

func remoteRuntimeHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: handlerRoundTripper{handler: handler}}
}

type remoteRuntimeParentConversationClient struct {
	pollResponse core.DurableAgentParentConversationPollResponse
}

func (c *remoteRuntimeParentConversationClient) Enroll(context.Context) (core.DurableAgentEnrollmentResponse, error) {
	panic("unexpected Enroll call")
}

func (c *remoteRuntimeParentConversationClient) Reattest(context.Context) (core.DurableAgentEnrollmentResponse, error) {
	panic("unexpected Reattest call")
}

func (c *remoteRuntimeParentConversationClient) PollPolicy(context.Context, int64, string) (core.DurableAgentPolicyPollResponse, error) {
	panic("unexpected PollPolicy call")
}

func (c *remoteRuntimeParentConversationClient) UploadReviewArtifact(context.Context, core.DurableReviewArtifact) (core.DurableAgentReviewArtifactUploadResponse, error) {
	panic("unexpected UploadReviewArtifact call")
}

func (c *remoteRuntimeParentConversationClient) AcknowledgePolicy(context.Context, core.DurableAgentPolicyAcknowledgement) (core.DurableAgentPolicyAcknowledgementResponse, error) {
	panic("unexpected AcknowledgePolicy call")
}

func (c *remoteRuntimeParentConversationClient) PollParentConversation(context.Context, int) (core.DurableAgentParentConversationPollResponse, error) {
	return c.pollResponse, nil
}

func (c *remoteRuntimeParentConversationClient) AcknowledgeParentConversation(context.Context, core.DurableAgentParentConversationAcknowledgement) (core.DurableAgentParentConversationAckResponse, error) {
	panic("unexpected AcknowledgeParentConversation call")
}
