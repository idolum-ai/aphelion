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

func TestRemoteChildRunnerRunOnceSyncsExecutesAndUploadsPendingReviewArtifacts(t *testing.T) {
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

	runner := NewRemoteChildRunner(
		childStore,
		NewRemoteRuntime(childStore, func(b core.DurableAgentRemoteBootstrap) (RemoteControlClient, error) {
			client, err := NewHTTPClient(b)
			if err != nil {
				return nil, err
			}
			client.Client = &http.Client{Transport: handlerRoundTripper{handler: NewHTTPHandler(parentStore).Handler()}}
			return client, nil
		}),
		RemoteChildExecutorFunc(func(ctx context.Context, bootstrap core.DurableAgentRemoteBootstrap, agent core.DurableAgent, msg core.InboundMessage) error {
			_, err := NewRuntime(childStore).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				Summary:       "Family schedule drift keeps resurfacing around the dinner plan.",
				IntervalLabel: "messages 20-25",
				LocalActions:  []string{"Held reply pending parent visibility."},
				Questions:     []string{"Should this become a standing family reminder?"},
				RiskFlags:     []string{"family_relevant_update"},
				Metadata: map[string]string{
					"sender_name": "Aunt May",
				},
			})
			return err
		}),
	)

	result, err := runner.RunOnce(context.Background(), bootstrapPath, core.InboundMessage{
		ChatID:         -100123,
		ChatType:       "group",
		SenderID:       77,
		SenderName:     "Aunt May",
		Text:           "Can you remind everyone again?",
		MessageID:      22,
		DurableAgentID: agent.AgentID,
		Timestamp:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("RunOnce() err = %v", err)
	}
	if !result.Sync.Enrolled {
		t.Fatal("RunOnce().Sync.Enrolled = false, want true on first run")
	}
	if result.UploadedReviewArtifacts != 1 {
		t.Fatalf("RunOnce().UploadedReviewArtifacts = %d, want 1", result.UploadedReviewArtifacts)
	}

	parentEvents, err := parentStore.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
	if !strings.Contains(parentEvents[0].Summary, "Family schedule drift keeps resurfacing") {
		t.Fatalf("parent Summary = %q, want uploaded durable review summary", parentEvents[0].Summary)
	}

	childPending, err := childStore.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("child PendingReviewEvents() err = %v", err)
	}
	if len(childPending) != 0 {
		t.Fatalf("child pending review events len = %d, want 0 after upload", len(childPending))
	}
}
