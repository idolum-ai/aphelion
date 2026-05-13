//go:build linux

package durableagent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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

func TestRemoteChildRunnerProcessesAndAcknowledgesParentConversation(t *testing.T) {
	t.Parallel()

	parentStore := newTestSQLiteStore(t)
	defer parentStore.Close()
	agent := testRemoteDurableAgent()
	if err := parentStore.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("parent UpsertDurableAgent() err = %v", err)
	}
	parentContinuity := core.DurableAgentContinuityState{}.WithConversationMessage(
		"parent",
		"Check the remote child health and report anything actionable.",
		time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
	)
	parentStateJSON, err := parentContinuity.Marshal()
	if err != nil {
		t.Fatalf("parent continuity Marshal() err = %v", err)
	}
	if err := parentStore.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: parentStateJSON,
	}); err != nil {
		t.Fatalf("parent SaveDurableAgentState() err = %v", err)
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
			if msg.ChatType != "durable_parent_conversation" {
				t.Fatalf("executor ChatType = %q, want durable_parent_conversation", msg.ChatType)
			}
			state, err := childStore.DurableAgentState(agent.AgentID)
			if err != nil {
				return err
			}
			continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
			if err != nil {
				return err
			}
			if pending := continuity.PendingParentConversationMessages(5); len(pending) != 1 {
				t.Fatalf("child pending parent messages len = %d, want 1", len(pending))
			}
			pending := continuity.PendingParentConversationMessages(5)
			continuity, err = continuity.AcknowledgeParentConversationMessageIDs(core.DurableAgentConversationMessageIDs(pending), time.Now().UTC())
			if err != nil {
				return err
			}
			stateJSON, err := continuity.Marshal()
			if err != nil {
				return err
			}
			state.StateJSON = stateJSON
			if err := childStore.SaveDurableAgentState(*state); err != nil {
				return err
			}
			parentState, err := parentStore.DurableAgentState(agent.AgentID)
			if err != nil {
				return err
			}
			parentContinuity, err := core.ParseDurableAgentContinuityState(parentState.StateJSON)
			if err != nil {
				return err
			}
			parentContinuity = parentContinuity.WithConversationMessage(
				"parent",
				"New parent instruction that arrived during the child wake.",
				time.Date(2026, 5, 13, 12, 0, 30, 0, time.UTC),
			)
			parentStateJSON, err := parentContinuity.Marshal()
			if err != nil {
				return err
			}
			parentState.StateJSON = parentStateJSON
			if err := parentStore.SaveDurableAgentState(*parentState); err != nil {
				return err
			}
			_, err = NewRuntime(childStore).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				Summary:       "Remote child health is stable; no operator action is required.",
				IntervalLabel: "parent conversation wake",
				LocalActions:  []string{"Checked local state after parent conversation wake."},
				Questions:     []string{"Should this child keep polling at the current cadence?"},
				RiskFlags:     []string{"none"},
			})
			return err
		}),
	)

	result, err := runner.RunParentConversation(context.Background(), bootstrapPath)
	if err != nil {
		t.Fatalf("RunParentConversation() err = %v", err)
	}
	if len(result.Sync.ParentConversationMessageIDs) != 1 {
		t.Fatalf("ParentConversationMessageIDs len = %d, want 1", len(result.Sync.ParentConversationMessageIDs))
	}
	if !result.AcknowledgedParent {
		t.Fatal("AcknowledgedParent = false, want true")
	}
	if result.UploadedReviewArtifacts != 1 {
		t.Fatalf("UploadedReviewArtifacts = %d, want 1", result.UploadedReviewArtifacts)
	}

	parentState, err := parentStore.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("parent DurableAgentState() err = %v", err)
	}
	parentAfter, err := core.ParseDurableAgentContinuityState(parentState.StateJSON)
	if err != nil {
		t.Fatalf("parent ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := parentAfter.PendingParentConversationMessages(5); len(pending) != 1 || pending[0].Text != "New parent instruction that arrived during the child wake." {
		t.Fatalf("parent pending parent messages = %#v, want only wake-race message after remote acknowledgement", pending)
	}
	parentEvents, err := parentStore.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
	if !strings.Contains(parentEvents[0].Summary, "Remote child health is stable") {
		t.Fatalf("parent Summary = %q, want uploaded wake review summary", parentEvents[0].Summary)
	}
}

func TestRemoteChildLoopRunnerProcessesQueuedMessageFiles(t *testing.T) {
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

	inboxDir := filepath.Join(t.TempDir(), "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(inbox) err = %v", err)
	}
	msg := core.InboundMessage{
		ChatID:         -100123,
		ChatType:       "group",
		SenderID:       77,
		SenderName:     "Aunt May",
		Text:           "Can you remind everyone again?",
		MessageID:      22,
		DurableAgentID: agent.AgentID,
		Timestamp:      time.Now().UTC(),
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal(msg) err = %v", err)
	}
	messagePath := filepath.Join(inboxDir, "0001.json")
	if err := os.WriteFile(messagePath, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(message) err = %v", err)
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
			})
			return err
		}),
	)
	loop := NewRemoteChildLoopRunner(runner)
	loop.Sleep = func(context.Context, time.Duration) error { return nil }

	result, err := loop.Run(context.Background(), bootstrapPath, inboxDir, time.Second, 1)
	if err != nil {
		t.Fatalf("Run(loop) err = %v", err)
	}
	if result.MessagesProcessed != 1 {
		t.Fatalf("MessagesProcessed = %d, want 1", result.MessagesProcessed)
	}
	if result.UploadedReviewArtifacts != 1 {
		t.Fatalf("UploadedReviewArtifacts = %d, want 1", result.UploadedReviewArtifacts)
	}
	if _, err := os.Stat(messagePath); !os.IsNotExist(err) {
		t.Fatalf("message file still exists, err=%v", err)
	}

	parentEvents, err := parentStore.PendingReviewEvents(agent.ReviewTargetChatID, 10)
	if err != nil {
		t.Fatalf("parent PendingReviewEvents() err = %v", err)
	}
	if len(parentEvents) != 1 {
		t.Fatalf("parent pending review events len = %d, want 1", len(parentEvents))
	}
}
