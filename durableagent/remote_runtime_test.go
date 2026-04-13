//go:build linux

package durableagent

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

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
		KeyFingerprint:   "child-key-fp",
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
		KeyFingerprint:   "child-key-fp",
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

func remoteRuntimeHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: handlerRoundTripper{handler: handler}}
}
