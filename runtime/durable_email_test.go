//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestPollDurableEmailAgentsQueuesReviewDigestFromGogInbox(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Email digest prepared."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review the inbox, summarize PDFs, and surface important threads without sending mail.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		ChannelConfig: core.DurableAgentChannelConfig{
			Email: &core.DurableAgentEmailChannelConfig{
				Address:          "idolum@example.com",
				Account:          "idolum@example.com",
				Adapter:          "gog_cli",
				Query:            "label:inbox newer_than:7d",
				PollInterval:     "1m",
				SurfaceRules:     []string{"job opportunity", "external inquiry"},
				SummarizePDFs:    true,
				SynthesisCadence: "4h",
				NeverRetain:      []string{"oauth_token", "password"},
			},
		},
		WakeupMode: "poll",
		Status:     "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	origRunner := durableEmailCommandRunner
	defer func() { durableEmailCommandRunner = origRunner }()
	durableEmailCommandRunner = func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.Contains(cmd, "gmail search"):
			return []byte(`[{"id":"thread-1"}]`), nil
		case strings.Contains(cmd, "gmail thread get"):
			return []byte(`{
				"id":"thread-1",
				"historyId":"2001",
				"messages":[
					{
						"id":"msg-1",
						"threadId":"thread-1",
						"labelIds":["INBOX","UNREAD"],
						"snippet":"We'd love to talk about a role for Idolum.",
						"internalDate":"1710000000000",
						"payload":{
							"mimeType":"multipart/mixed",
							"headers":[
								{"name":"From","value":"Jobs <jobs@example.com>"},
								{"name":"Subject","value":"Job opportunity for Idolum"}
							],
							"parts":[
								{"mimeType":"text/plain","body":{"data":"We'd love to talk to Idolum about a role."}},
								{"filename":"brief.pdf","mimeType":"application/pdf","body":{"attachmentId":"att-1"}}
							]
						}
					}
				]
			}`), nil
		case strings.Contains(cmd, "gmail attachment"):
			return []byte("%PDF-1.4 test pdf"), nil
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	if err := rt.pollDurableEmailAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableEmailAgents() err = %v", err)
	}

	sender.mu.Lock()
	if got := len(sender.sent); got != 0 {
		t.Fatalf("sent len = %d, want 0 for read-only email child", got)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want 1", len(events))
	}
	if !strings.Contains(strings.ToLower(events[0].Summary), "job opportunity") {
		t.Fatalf("review summary = %q, want job opportunity signal", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, "brief.pdf") {
		t.Fatalf("review metadata = %q, want surfaced PDF attachment", events[0].MetadataJSON)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if !strings.Contains(state.Cursor, "thread-1") {
		t.Fatalf("Cursor = %q, want latest thread id", state.Cursor)
	}
}
