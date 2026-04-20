//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestPollDurableEmailAgentsQueuesReviewDigestFromGogInbox(t *testing.T) {
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

func TestPollDurableEmailAgentsRespectsSynthesisCadenceAndBuffersPendingThreads(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Email digest prepared."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email-cadence",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox and synthesize periodically.",
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
				SurfaceRules:     []string{"job opportunity"},
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

	base := time.Now().UTC().Add(-2 * time.Minute)
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:      agent.AgentID,
		LastWakeAt:   base,
		LastReviewAt: base,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	origRunner := durableEmailCommandRunner
	defer func() { durableEmailCommandRunner = origRunner }()
	searchCalls := 0
	durableEmailCommandRunner = func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.Contains(cmd, "gmail search"):
			searchCalls++
			if searchCalls == 1 {
				return []byte(`[{"id":"thread-cadence-1"}]`), nil
			}
			return []byte(`[]`), nil
		case strings.Contains(cmd, "gmail thread get thread-cadence-1"):
			return []byte(`{
				"id":"thread-cadence-1",
				"messages":[{"id":"msg-cadence-1","threadId":"thread-cadence-1","snippet":"Job opportunity","internalDate":"1710000000000","payload":{"headers":[{"name":"From","value":"Jobs <jobs@example.com>"},{"name":"Subject","value":"Job opportunity for Idolum"}],"parts":[{"mimeType":"text/plain","body":{"data":"Please review this role."}}]}}]
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	firstRun := base.Add(2 * time.Minute)
	if err := rt.pollDurableEmailAgents(context.Background(), firstRun); err != nil {
		t.Fatalf("pollDurableEmailAgents(first run) err = %v", err)
	}
	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(first run) err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents(first run) len = %d, want 0 before synthesis cadence", len(events))
	}
	stateAfterFirst, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(first run) err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(stateAfterFirst.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState(first run) err = %v", err)
	}
	if continuity.EmailPending == nil || len(continuity.EmailPending.Threads) != 1 {
		t.Fatalf("EmailPending(first run) = %#v, want one buffered thread", continuity.EmailPending)
	}

	secondRun := base.Add(5 * time.Hour)
	if err := rt.pollDurableEmailAgents(context.Background(), secondRun); err != nil {
		t.Fatalf("pollDurableEmailAgents(second run) err = %v", err)
	}
	events, err = store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(second run) err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents(second run) len = %d, want 1 after cadence window", len(events))
	}
	stateAfterSecond, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(second run) err = %v", err)
	}
	continuity, err = core.ParseDurableAgentContinuityState(stateAfterSecond.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState(second run) err = %v", err)
	}
	if continuity.EmailPending != nil && len(continuity.EmailPending.Threads) != 0 {
		t.Fatalf("EmailPending(second run) = %#v, want drained pending buffer", continuity.EmailPending)
	}
}

func TestPollDurableEmailAgentsNeverRetainScrubsSensitiveMaterial(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Email digest prepared."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email-retention",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox with strict retention guardrails.",
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
				SurfaceRules:     []string{"credentials"},
				SummarizePDFs:    true,
				SynthesisCadence: "1m",
				NeverRetain:      []string{"password"},
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
			return []byte(`[{"id":"thread-retain-1"}]`), nil
		case strings.Contains(cmd, "gmail thread get thread-retain-1"):
			return []byte(`{
				"id":"thread-retain-1",
				"messages":[{"id":"msg-retain-1","threadId":"thread-retain-1","snippet":"Password reset: hunter2","internalDate":"1710000000000","payload":{"headers":[{"name":"From","value":"Ops <ops@example.com>"},{"name":"Subject","value":"Password reset in plain text"}],"parts":[{"mimeType":"text/plain","body":{"data":"The password is hunter2"}}]}}]
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	if err := rt.pollDurableEmailAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableEmailAgents() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want 1", len(events))
	}
	if strings.Contains(events[0].Summary, "hunter2") || strings.Contains(events[0].MetadataJSON, "hunter2") {
		t.Fatalf("review event leaked never-retain secret: summary=%q metadata=%q", events[0].Summary, events[0].MetadataJSON)
	}
	if !strings.Contains(events[0].MetadataJSON, "never_retain_redactions") {
		t.Fatalf("review metadata = %q, want never_retain_redactions marker", events[0].MetadataJSON)
	}
}

func TestPollDurableEmailAgentsPushWakeProcessesInboxEventFiles(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Email digest prepared."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email-push",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox via push events.",
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
				PollInterval:     "1h",
				SurfaceRules:     []string{"job opportunity"},
				SummarizePDFs:    false,
				SynthesisCadence: "1m",
				NeverRetain:      []string{"oauth_token", "password"},
			},
		},
		WakeupMode: "push",
		Status:     "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		t.Fatalf("scopeForDurableAgent() err = %v", err)
	}
	pushDir := filepath.Join(scope.WorkingRoot, ".aphelion", "email-push", agent.AgentID, "inbox")
	if err := os.MkdirAll(pushDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(pushDir) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pushDir, "evt-1.json"), []byte(`{"thread_ids":["thread-push-1"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile(push event) err = %v", err)
	}

	origRunner := durableEmailCommandRunner
	defer func() { durableEmailCommandRunner = origRunner }()
	durableEmailCommandRunner = func(_ context.Context, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case strings.Contains(cmd, "gmail thread get thread-push-1"):
			return []byte(`{
				"id":"thread-push-1",
				"messages":[{"id":"msg-push-1","threadId":"thread-push-1","snippet":"Job opportunity","internalDate":"1710000000000","payload":{"headers":[{"name":"From","value":"Jobs <jobs@example.com>"},{"name":"Subject","value":"Job opportunity via push"}],"parts":[{"mimeType":"text/plain","body":{"data":"Push-triggered email review."}}]}}]
			}`), nil
		case strings.Contains(cmd, "gmail search"):
			return nil, fmt.Errorf("unexpected gmail search call during push wake")
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	if err := rt.pollDurableEmailAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableEmailAgents() err = %v", err)
	}
	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want 1", len(events))
	}
	entries, err := os.ReadDir(pushDir)
	if err != nil {
		t.Fatalf("ReadDir(pushDir) err = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("push inbox entries = %d, want consumed event files", len(entries))
	}
}

func TestPollDurableEmailAgentsUsesChildExecutionWhenBootstrapConfigured(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Email digest prepared."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox via isolated child execution.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
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
				SynthesisCadence: "5m",
			},
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	childRuns := 0
	rt.durableWakeChild = inlineDurableWakeChildExecutor{
		run: func(_ context.Context, scope sandbox.Scope, got core.DurableAgent, _ time.Time) error {
			childRuns++
			if strings.TrimSpace(got.AgentID) != "idolum-email-child" {
				t.Fatalf("child agent id = %q, want idolum-email-child", got.AgentID)
			}
			if strings.TrimSpace(scope.Principal.DurableAgentID) != "idolum-email-child" {
				t.Fatalf("child scope durable agent id = %q, want idolum-email-child", scope.Principal.DurableAgentID)
			}
			return nil
		},
	}

	origRunner := durableEmailCommandRunner
	defer func() { durableEmailCommandRunner = origRunner }()
	durableEmailCommandRunner = func(_ context.Context, _ ...string) ([]byte, error) {
		return nil, fmt.Errorf("legacy durable email command runner should not execute when child runtime is configured")
	}

	if err := rt.pollDurableEmailAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableEmailAgents() err = %v", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns = %d, want 1", childRuns)
	}
}

func TestRunDurableAgentChildWakePersistsTurnAndQueuesReviewArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Child-reviewed inbox summary with actionable priorities."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-email-turn",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox through child turn synthesis.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
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
				SynthesisCadence: "1m",
				SurfaceRules:     []string{"job opportunity"},
			},
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
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
			return []byte(`[{"id":"thread-turn-1"}]`), nil
		case strings.Contains(cmd, "gmail thread get thread-turn-1"):
			return []byte(`{
				"id":"thread-turn-1",
				"messages":[{"id":"msg-turn-1","threadId":"thread-turn-1","snippet":"Job opportunity for Idolum","internalDate":"1710000000000","payload":{"headers":[{"name":"From","value":"Jobs <jobs@example.com>"},{"name":"Subject","value":"Job opportunity for Idolum"}],"parts":[{"mimeType":"text/plain","body":{"data":"We want Idolum to review this role."}}]}}]
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	now := time.Now().UTC()
	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, now); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}

	key := session.SessionKey{
		ChatID: durableEmailSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(email child session) err = %v", err)
	}
	if sess.TurnCount == 0 {
		t.Fatalf("email child session turn_count = %d, want > 0", sess.TurnCount)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Summary, "Child-reviewed inbox summary") {
		t.Fatalf("review summary = %q, want child turn synthesis summary", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, "child_turn_summary") {
		t.Fatalf("review metadata = %q, want child_turn_summary marker", events[0].MetadataJSON)
	}
}

func TestRunDurableAgentChildWakeUsesDurablePrincipalScopedTools(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: true,
	}
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	agent := core.DurableAgent{
		AgentID:            "idolum-email-tools",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "email",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review inbox with tool access.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
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
				SynthesisCadence: "1m",
			},
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
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
			return []byte(`[{"id":"thread-tools-1"}]`), nil
		case strings.Contains(cmd, "gmail thread get thread-tools-1"):
			return []byte(`{
				"id":"thread-tools-1",
				"messages":[{"id":"msg-tools-1","threadId":"thread-tools-1","snippet":"Inbox update","internalDate":"1710000000000","payload":{"headers":[{"name":"From","value":"Ops <ops@example.com>"},{"name":"Subject","value":"Inbox update"}],"parts":[{"mimeType":"text/plain","body":{"data":"Run a quick check."}}]}}]
			}`), nil
		default:
			return nil, fmt.Errorf("unexpected durable email command: %s", cmd)
		}
	}

	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}

	if provider.firstToolCount != 1 {
		t.Fatalf("first tool count = %d, want 1", provider.firstToolCount)
	}
	if tools.executeForPrincipalCalls != 1 {
		t.Fatalf("executeForPrincipal calls = %d, want 1", tools.executeForPrincipalCalls)
	}
	if tools.lastPrincipal.Role != principal.RoleDurableAgent {
		t.Fatalf("last principal role = %q, want durable_agent", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.DurableAgentID != agent.AgentID {
		t.Fatalf("last durable agent id = %q, want %q", tools.lastPrincipal.DurableAgentID, agent.AgentID)
	}
}

func TestRunDurableAgentChildWakeRejectsUnsupportedChannel(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-telegram-agent",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_dm",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle direct Telegram messages.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "telegram_update",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	err = rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC())
	if err == nil {
		t.Fatal("RunDurableAgentChildWake() err = nil, want unsupported channel error")
	}
	if !strings.Contains(err.Error(), "does not support child wake execution") {
		t.Fatalf("RunDurableAgentChildWake() err = %v, want unsupported channel detail", err)
	}
}
