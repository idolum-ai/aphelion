//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestParentConversationAckSuppressedWhenChildQueuesConcreteReview(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Concrete child report from the wake."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-reporting",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests and report concrete findings.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Inspect runtime grants.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeAdapters = []durableWakeIngressAdapter{&testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}}
	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want only concrete child review", got)
	}
	if !strings.Contains(sender.inline[0].text, "Concrete child report from the wake.") {
		t.Fatalf("inline text = %q, want concrete child report", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "Processed pending parent guidance") {
		t.Fatalf("inline text = %q, want parent ack wrapper suppressed", sender.inline[0].text)
	}
}

func TestParentConversationWakePromptDoesNotExposeParentControlPlaneToolCall(t *testing.T) {
	t.Parallel()

	prompt := durableParentConversationWakePrompt(core.DurableAgent{
		AgentID:     "idolum-email",
		ChannelKind: "external_channel",
	}, []core.DurableAgentConversationMessage{{
		MessageID: "msg-parent-control",
		Role:      "parent",
		Text:      "Exact approved child_wake: tool=durable_agent action=wake_once agent_id=idolum-email. Consume only pending guidance and use child-local gog_cli if needed.",
	}})

	for _, forbidden := range []string{"tool=durable_agent", "action=wake_once", "child_wake", "durable_agent", "wake_once"} {
		if strings.Contains(strings.ToLower(prompt), forbidden) {
			t.Fatalf("prompt leaked parent-control token %q:\n%s", forbidden, prompt)
		}
	}
	for _, required := range []string{"parent control-plane approval was already consumed", "Do not call the parent durable-agent governance tool or wake action", "child-local gog_cli"} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(required)) {
			t.Fatalf("prompt missing %q:\n%s", required, prompt)
		}
	}
}

func TestRunDurableAgentChildWakeProcessesPendingParentBeforeExternalCadence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed pending parent image job.\nREVIEW_STATUS: completed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "image2",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Generate one image artifact when parent asks.",
			CapabilityEnvelope: []string{"image_brief_refinement", "codex_image_generation_probe", "artifact_return", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Generate exactly one image artifact.", time.Now().UTC().Add(-time.Minute))
	continuity.ExternalChannel = encodeGenericExternalChannelState(core.DurableAgentExternalChannelRuntimeState{
		Adapter:       "codex_image_generation",
		LastAttemptAt: time.Now().UTC(),
		LastStatus:    "wake_completed",
	}, "codex_image_generation")
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending parent messages = %d, want acked by forced parent wake", len(pending))
	}
	if len(provider.seenGovernorSystem) == 0 || !strings.Contains(strings.Join(provider.seenGovernorSystem, "\n"), "parent conversation wake") {
		t.Fatalf("governor prompts = %#v, want parent conversation wake", provider.seenGovernorSystem)
	}
}

func TestRunDurableAgentChildWakeDoesNotRebindParentAuthorityAdmission(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed pending parent guidance.\nREVIEW_STATUS: completed"
	inspector := &contextInspectingProvider{fakeProvider: provider}
	parentKey := session.SessionKey{ChatID: 9306, UserID: 1001, Scope: telegramDMScopeRef(9306)}
	parentSessionID := session.SessionIDForKey(parentKey)
	parentLeaseID := "lease-parent-child-wake-context"
	var sawParentAdmission bool
	var sawChildTaskAdmission bool
	var sawParentAuthorityUseRef bool
	var sawToolInvocationRef bool
	inspector.inspect = func(ctx context.Context) {
		if admission, ok := toolpkg.ExecutionAuthorityAdmissionFromContext(ctx); ok {
			switch admission.LeaseKind {
			case session.ExecutionAuthorityLeaseKindChildTask:
				sawChildTaskAdmission = true
			case session.ExecutionAuthorityLeaseKindContinuation:
				if admission.ContinuationLeaseID == parentLeaseID {
					sawParentAdmission = true
				}
			default:
				sawParentAdmission = true
			}
		}
		if ref, ok := toolpkg.AuthorityUseRefFromContext(ctx); ok {
			if ref.ContinuationLeaseID == parentLeaseID || ref.SessionID == parentSessionID {
				sawParentAuthorityUseRef = true
			}
		}
		if _, ok := toolpkg.ToolInvocationRefFromContext(ctx); ok {
			sawToolInvocationRef = true
		}
	}
	rt, err := New(cfg, store, inspector, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-authority-isolated",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process one pending parent guidance item.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Run one no-content readiness check.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	now := time.Now().UTC()
	parentState := approvedReadOnlyContinuationStateForScopeTest("parent-child-wake-context", now)
	parentState.ContinuationLease.ID = parentLeaseID
	parentState.ContinuationLease.LeaseClass = session.ContinuationLeaseClassChildWake
	parentState.ContinuationLease.AllowedActions = []string{"wake_named_child"}
	parentState.ContinuationLease.Constraints = map[string]string{"agent_id": agent.AgentID}
	if err := store.UpdateContinuationState(parentKey, parentState); err != nil {
		t.Fatalf("UpdateContinuationState(parent) err = %v", err)
	}
	rawAdmission := session.ExecutionRunAuthority{
		SessionID:           session.SessionIDForKey(parentKey),
		ChatID:              parentKey.ChatID,
		UserID:              parentKey.UserID,
		Scope:               parentKey.Scope,
		Principal:           "telegram:1001",
		PrincipalRole:       "admin",
		ExecutionSpecies:    "direct_continuation",
		LeaseKind:           session.ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: parentState.ContinuationLease.ID,
		LeaseStatus:         string(session.ContinuationLeaseStatusActive),
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		LeaseAllowedActions: []string{"wake_named_child"},
		LeaseConstraints:    map[string]string{"agent_id": agent.AgentID},
		LeaseRemainingTurns: 1,
		LeaseExpiresAt:      now.Add(time.Hour),
		AdmittedAt:          now,
	}
	ctx := toolpkg.WithToolInvocationRef(
		toolpkg.WithAuthorityUseRef(
			toolpkg.WithExecutionAuthorityAdmission(context.Background(), rawAdmission),
			session.AuthorityUseRef{
				SessionID:           rawAdmission.SessionID,
				TurnRunID:           930600,
				ContinuationLeaseID: rawAdmission.ContinuationLeaseID,
				AuthoritySource:     "context",
			},
		),
		toolpkg.ToolInvocationRef{TurnRunID: 930600, InvocationID: "parent-durable-agent-wake-once"},
	)

	if err := rt.RunDurableAgentChildWake(ctx, agent.AgentID, now); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v, want parent authority admission isolated from child turn", err)
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("provider saw no governor prompts, want child wake turn to run")
	}
	if sawParentAdmission {
		t.Fatal("child provider context inherited parent raw execution authority admission")
	}
	if !sawChildTaskAdmission {
		t.Fatal("child provider context did not receive child-task execution authority admission")
	}
	if sawParentAuthorityUseRef {
		t.Fatal("child provider context inherited parent authority-use ref")
	}
	if sawToolInvocationRef {
		t.Fatal("child provider context inherited parent tool invocation ref")
	}
}

type contextInspectingProvider struct {
	*fakeProvider
	inspect func(context.Context)
}

func (p *contextInspectingProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if p.inspect != nil {
		p.inspect(ctx)
	}
	return p.fakeProvider.Complete(ctx, messages, tools)
}

func (p *contextInspectingProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, opts agent.CompleteOptions) (*agent.Response, error) {
	if p.inspect != nil {
		p.inspect(ctx)
	}
	return p.fakeProvider.CompleteWithOptions(ctx, messages, tools, opts)
}

func TestRunDurableAgentChildWakeSkipsWithoutPendingParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Unsupported channel should not run"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-unsupported-channel",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "unsupported_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Attempt unsupported wake channel.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	err = rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v, want nil for empty parent queue", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no child turn without pending parent conversation", provider.seenGovernorSystem)
	}
}

func TestRunDurableAgentParentConversationWakeDoesNotFallbackAfterQueueRace(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "fallback should not run"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-race",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent guidance only when it is still pending.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Wake if this guidance is still pending.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	messageIDs := core.DurableAgentConversationMessageIDs(pending)
	if len(messageIDs) != 1 {
		t.Fatalf("pending message IDs = %#v, want one", messageIDs)
	}
	if err := rt.acknowledgeDurableAgentParentConversation(agent.AgentID, pending, time.Now().UTC()); err != nil {
		t.Fatalf("acknowledgeDurableAgentParentConversation() err = %v", err)
	}
	fallback := &testDurableWakeAdapter{channelKind: "external_channel"}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{fallback}

	err = rt.RunDurableAgentParentConversationWake(context.Background(), agent.AgentID, messageIDs, "", time.Now().UTC())
	var claimedBatchErr core.DurableAgentWakeFailureError
	if !errors.As(err, &claimedBatchErr) || claimedBatchErr.Class != core.DurableAgentWakeFailureClaimedParentBatchMissing {
		t.Fatalf("RunDurableAgentParentConversationWake() err = %T %[1]v, want typed claimed-batch failure", err)
	}
	if fallback.prepareCalls != 0 {
		t.Fatalf("fallback prepare calls = %d, want none after parent queue race", fallback.prepareCalls)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no fallback turn", provider.seenGovernorSystem)
	}
	if packet, ok, err := store.ChildTaskPacket(messageIDs[0]); err != nil || ok {
		t.Fatalf("ChildTaskPacket(%q) = %#v ok=%t err=%v, want no child task for vanished claimed batch", messageIDs[0], packet, ok, err)
	}
}

func TestRunDurableAgentParentConversationWakeRecordsTaskForScopeFailure(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "child turn should not start when scope setup fails"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	badRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(badRoot) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-scope-fails",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LocalStorageRoots:  []string{badRoot, filepath.Join(t.TempDir(), "memory")},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent guidance only when the runtime can create child scope.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Run one readiness wake and report the first blocker.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	messageIDs := core.DurableAgentConversationMessageIDs(pending)
	if len(messageIDs) != 1 {
		t.Fatalf("pending message IDs = %#v, want one", messageIDs)
	}

	now := time.Date(2026, 6, 27, 20, 45, 0, 0, time.UTC)
	err = rt.RunDurableAgentParentConversationWake(context.Background(), agent.AgentID, messageIDs, "", now)
	var wakeFailure core.DurableAgentWakeFailureError
	if !errors.As(err, &wakeFailure) || wakeFailure.Class != core.DurableAgentWakeFailureScopeSetup {
		t.Fatalf("RunDurableAgentParentConversationWake() err = %T %[1]v, want typed scope setup failure", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no child turn after pre-start scope failure", provider.seenGovernorSystem)
	}
	packet, ok, err := store.ChildTaskPacket(messageIDs[0])
	if err != nil {
		t.Fatalf("ChildTaskPacket(%q) err = %v", messageIDs[0], err)
	}
	if !ok || packet.Status != session.ChildTaskPacketFailed || packet.ResultID == "" || packet.TerminalAt.IsZero() {
		t.Fatalf("ChildTaskPacket(%q) = %#v ok=%t, want terminal failed packet despite pre-start failure", messageIDs[0], packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(%q) err = %v", packet.ResultID, err)
	}
	if !ok || result.PacketID != packet.PacketID || result.Status != session.ChildTaskResultFailed || result.NextState != session.NextActionBlockedNeedsResourceRepair {
		t.Fatalf("ChildTaskResult(%q) = %#v ok=%t, want failed repair-directed result", packet.ResultID, result, ok)
	}
	if !strings.Contains(result.ErrorText, "resolve durable wake scope") {
		t.Fatalf("result error = %q, want scope failure evidence", result.ErrorText)
	}
	open, err := store.OpenNextActionsBySession(session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsResourceRepair || open[0].SubjectRef != packet.PacketID {
		t.Fatalf("open next actions = %#v, want one packet-scoped resource repair action", open)
	}
}

func TestRunDurableAgentParentConversationWakeUsesClaimScopedPacketAfterTerminalBatchPacket(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I handled the approved retry.\nREVIEW_STATUS: completed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-approved-retry",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process one approved parent wake retry.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	queuedAt := time.Date(2026, 6, 27, 23, 0, 0, 0, time.UTC)
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Run one approved readiness wake.", queuedAt)
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	messageIDs := core.DurableAgentConversationMessageIDs(pending)
	if len(messageIDs) != 1 {
		t.Fatalf("pending message IDs = %#v, want one", messageIDs)
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	oldPacketID := durableWakeTaskPacketIDForPending(agent.AgentID, pending, queuedAt)
	oldPacket, err := store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:  oldPacketID,
		AgentID:   agent.AgentID,
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{"channel":"durable_parent_conversation","wake_claim_id":""}`,
		CreatedAt: queuedAt,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket(old terminal batch packet) err = %v", err)
	}
	oldClaim, err := store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       oldPacket.PacketID,
		AttemptID:      "child_attempt:old-terminal",
		LeaseOwner:     "durable_wake:" + agent.AgentID + ":old-terminal",
		AgentID:        agent.AgentID,
		Key:            key,
		ClaimedAt:      queuedAt.Add(time.Second),
		LeaseExpiresAt: queuedAt.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt(old terminal batch packet) err = %v", err)
	}
	if _, err := store.CommitChildTaskOutcome(session.ChildTaskOutcomeCommitInput{
		Result: session.ChildTaskResultInput{
			PacketID:        oldClaim.PacketID,
			AttemptID:       oldClaim.ActiveAttemptID,
			LeaseOwner:      oldClaim.LeaseOwner,
			LeaseGeneration: oldClaim.LeaseGeneration,
			FencingToken:    oldClaim.FencingToken,
			AgentID:         agent.AgentID,
			Key:             key,
			Status:          session.ChildTaskResultFailed,
			ErrorText:       "prior approved wake failed before child pickup",
			CreatedAt:       queuedAt.Add(2 * time.Second),
		},
		ResolvedAt: queuedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("CommitChildTaskOutcome(old terminal batch packet) err = %v", err)
	}

	wakeClaimID := "wake_claim:fresh-approved-child-wake"
	now := queuedAt.Add(time.Minute)
	err = rt.RunDurableAgentParentConversationWake(context.Background(), agent.AgentID, messageIDs, wakeClaimID, now)
	if err != nil {
		t.Fatalf("RunDurableAgentParentConversationWake(claim-scoped retry) err = %v", err)
	}

	claimPacketID := durableWakeTaskPacketIDForWakeClaim(agent.AgentID, messageIDs, wakeClaimID)
	if claimPacketID == oldPacketID {
		t.Fatalf("claim-scoped packet id = old packet id %q, want fresh packet for approved retry", oldPacketID)
	}
	claimPacket, ok, err := store.ChildTaskPacket(claimPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(%q) err = %v", claimPacketID, err)
	}
	if !ok || claimPacket.ResultID == "" {
		t.Fatalf("ChildTaskPacket(%q) = %#v ok=%t, want claim-scoped packet with child result", claimPacketID, claimPacket, ok)
	}
	claimResult, ok, err := store.ChildTaskResult(claimPacket.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(%q) err = %v", claimPacket.ResultID, err)
	}
	if !ok || claimResult.Status == session.ChildTaskResultFailed {
		t.Fatalf("ChildTaskResult(%q) = %#v ok=%t, want non-failed child result", claimPacket.ResultID, claimResult, ok)
	}
	oldPacket, ok, err = store.ChildTaskPacket(oldPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(old %q) err = %v", oldPacketID, err)
	}
	if !ok || oldPacket.Status != session.ChildTaskPacketFailed {
		t.Fatalf("old ChildTaskPacket(%q) = %#v ok=%t, want prior terminal failure preserved", oldPacketID, oldPacket, ok)
	}
	if len(provider.seenGovernorSystem) == 0 {
		t.Fatal("provider saw no child turn; approved retry should reach child runtime")
	}
}

func TestPollDurableWakeAgentsBacksOffExpiredGrantChildRuntimeBlock(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = "unused because child runtime blocks before inference"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "image2",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "local://image2",
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Generate images only when a concrete parent request and active grant exist.",
			CapabilityEnvelope: []string{"image_brief_refinement", "codex_image_generation_probe", "artifact_return", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "codex_image_generation")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	childRuns := 0
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return fmt.Errorf("child_runtime_blocked: grant_expired grant_id=capg-image2")
	}}

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents(first) err = %v, want suppressed blocked wake", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns = %d, want first blocked child attempt", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, "image2")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want blocked wake state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" || !strings.Contains(cont.ExternalChannel.LastError, "grant_expired") {
		t.Fatalf("external channel state = %#v, want grant_expired wake_blocked", cont.ExternalChannel)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29 * time.Minute)) {
		t.Fatalf("backoff_until = %v, want recorded backoff", cont.ExternalChannel.BackoffUntil)
	}
	sender.mu.Lock()
	compact := ""
	if len(sender.inline) > 0 {
		compact = sender.inline[len(sender.inline)-1].text
	}
	sender.mu.Unlock()
	if !strings.Contains(compact, "PAUSED") || strings.Contains(compact, "capg-image2") || strings.Contains(compact, "child_runtime_blocked") || strings.Contains(compact, "risk: adapter_dispatch") {
		t.Fatalf("compact review = %q, want paused operator summary without raw runtime details", compact)
	}

	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("pollDurableWakeAgents(backoff) err = %v, want quiet skip", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns after suppressed retry = %d, want 1", childRuns)
	}
}

func TestPollDurableWakeAgentsBacksOffExternalChildExecutorFailureWithoutAckingParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = "unused because child executor fails before parent conversation is processed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := genericExternalChannelTestAgent("mail-executor-failure")
	agent.BootstrapLLM = durableGroupTestBootstrapLLM()
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Process the pending mailbox instruction exactly once.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	childRuns := 0
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return fmt.Errorf("network is unreachable")
	}}

	now := time.Date(2026, 5, 18, 10, 30, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v, want external failure recorded and suppressed", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns = %d, want one failed child wake", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, agent.AgentID)
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want wake_failed state")
	}
	if cont.ExternalChannel.LastStatus != "wake_failed" || !strings.Contains(cont.ExternalChannel.LastError, "network_unreachable") {
		t.Fatalf("external channel state = %#v, want wake_failed network backoff", cont.ExternalChannel)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29*time.Minute)) || cont.ExternalChannel.FailureCount != 1 {
		t.Fatalf("backoff/failures = %v/%d, want first failure backoff", cont.ExternalChannel.BackoffUntil, cont.ExternalChannel.FailureCount)
	}
	if pending := cont.PendingParentConversationMessages(10); len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want original message preserved after failed child wake", len(pending))
	}
	sender.mu.Lock()
	compact := ""
	if len(sender.inline) > 0 {
		compact = sender.inline[len(sender.inline)-1].text
	}
	sender.mu.Unlock()
	if !strings.Contains(compact, "External-channel wake failed") || strings.Contains(compact, "Process the pending mailbox instruction") {
		t.Fatalf("review text = %q, want failure review without parent instruction leak", compact)
	}

	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("pollDurableWakeAgents(backoff) err = %v, want quiet skip while parent message remains pending", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns after suppressed retry = %d, want original failed child wake only", childRuns)
	}
}

func TestPollDurableWakeAgentsBacksOffUnconfiguredExternalParentConversationFailure(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = "unused because child executor fails before parent conversation is processed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "legacy-external-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent conversation guidance truthfully.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Process this parent guidance exactly once.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	childRuns := 0
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newDurableParentConversationWakeAdapter()}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return fmt.Errorf("network is unreachable")
	}}

	now := time.Date(2026, 5, 18, 16, 30, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents(first) err = %v, want generic external failure recorded", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns = %d, want one failed child wake", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, agent.AgentID)
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want generic wake_failed state")
	}
	if cont.ExternalChannel.Adapter != "external_channel" || cont.ExternalChannel.LastStatus != "wake_failed" || !strings.Contains(cont.ExternalChannel.LastError, "network_unreachable") {
		t.Fatalf("external channel state = %#v, want generic wake_failed network backoff", cont.ExternalChannel)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29*time.Minute)) || cont.ExternalChannel.FailureCount != 1 {
		t.Fatalf("backoff/failures = %v/%d, want first failure backoff", cont.ExternalChannel.BackoffUntil, cont.ExternalChannel.FailureCount)
	}
	if pending := cont.PendingParentConversationMessages(10); len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want original message preserved after failed child wake", len(pending))
	}

	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("pollDurableWakeAgents(backoff) err = %v, want quiet skip for legacy external child", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns after suppressed retry = %d, want backoff to hold despite pending parent message", childRuns)
	}
}

func TestPollDurableWakeAgentsPreflightsExternalChannelMaterialBeforeChildWake(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused because preflight blocks before child wake"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "local://mailbox",
			Adapter:      "mailbox_adapter",
			Query:        "label:inbox",
			PollInterval: "30m",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Poll the external channel only when grants and material are ready.",
			CapabilityEnvelope: []string{"external_channel_poll", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	childRuns := 0
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return nil
	}}

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v, want preflight block recorded without hard failure", err)
	}
	if childRuns != 0 {
		t.Fatalf("childRuns = %d, want preflight to block before child wake", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, "mail-child")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want preflight wake_blocked state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" || !strings.Contains(cont.ExternalChannel.LastError, "child_runtime_blocked") || !strings.Contains(cont.ExternalChannel.LastError, "mailbox_adapter") {
		t.Fatalf("external channel state = %#v, want generic adapter preflight blocker", cont.ExternalChannel)
	}
	sender.mu.Lock()
	compact := ""
	if len(sender.inline) > 0 {
		compact = sender.inline[len(sender.inline)-1].text
	}
	sender.mu.Unlock()
	if !strings.Contains(compact, "BLOCKED") || strings.Contains(compact, "label:inbox") {
		t.Fatalf("compact review = %q, want blocked operator summary without query leak", compact)
	}
}

func TestParentConversationWakeAdapterSkipsScheduledReviewChannel(t *testing.T) {
	t.Parallel()

	adapter := newDurableParentConversationWakeAdapter()
	if adapter.Supports(core.DurableAgent{AgentID: "scheduled", ChannelKind: scheduledReviewChannelKind}) {
		t.Fatal("parent conversation adapter supports scheduled_review; want skipped so the scheduled adapter owns its wake")
	}
	if !adapter.Supports(core.DurableAgent{AgentID: "external", ChannelKind: "external_channel"}) {
		t.Fatal("parent conversation adapter does not support ordinary external_channel child")
	}
}
