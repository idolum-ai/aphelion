//go:build linux

package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestChildTaskPacketAndResultRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7701, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-alpha", DurableAgentID: "child-alpha"}}
	now := time.Now().UTC().Round(0)
	packetInput := ChildTaskPacketInput{
		PacketID:       "grant_task:child-alpha",
		AgentID:        "child-alpha",
		Key:            key,
		TaskKind:       "capability_grant_wake",
		AuthorityKind:  "capability_grant",
		AuthorityID:    "capg-child-alpha",
		GrantID:        "capg-child-alpha",
		RequestID:      "cap-child-alpha",
		TargetResource: "codex",
		RequiredAction: "invoke",
		InputJSON:      `{"grant_id":"capg-child-alpha"}`,
		CreatedAt:      now,
	}
	packet, err := store.RecordChildTaskPacket(packetInput)
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	if packet.Status != ChildTaskPacketQueued || packet.TaskLeaseID == "" || packet.SessionID != SessionIDForKey(key) {
		t.Fatalf("packet = %#v, want queued durable packet with lease and session", packet)
	}

	replayed, err := store.RecordChildTaskPacket(packetInput)
	if err != nil {
		t.Fatalf("RecordChildTaskPacket(replay) err = %v", err)
	}
	if replayed.CreatedAt != packet.CreatedAt || replayed.GrantID != "capg-child-alpha" {
		t.Fatalf("replayed packet = %#v, want idempotent original", replayed)
	}
	changedInput := packetInput
	changedInput.RequiredAction = "read_file"
	if _, err := store.RecordChildTaskPacket(changedInput); err == nil {
		t.Fatal("RecordChildTaskPacket(changed immutable input) err = nil, want idempotency conflict")
	}

	claimed := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:roundtrip", now.Add(500*time.Millisecond))
	result, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       claimed.ActiveAttemptID,
		LeaseOwner:      claimed.LeaseOwner,
		LeaseGeneration: claimed.LeaseGeneration,
		FencingToken:    claimed.FencingToken,
		AgentID:         "child-alpha",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Grant incorporated.",
		EvidenceRefs:    []string{"capability_grant:capg-child-alpha"},
		CreatedAt:       now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest() err = %v", err)
	}
	if result.ResultID == "" || result.AttemptID == "" || result.Status != ChildTaskResultCompleted || result.NextState != NextActionTerminal {
		t.Fatalf("result = %#v, want completed terminal child result", result)
	}

	completed, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket() err = %v", err)
	}
	if !ok || completed.Status != ChildTaskPacketCompleted || completed.ResultID != result.ResultID || completed.TerminalAt.IsZero() {
		t.Fatalf("completed packet = %#v ok=%t, want terminal packet linked to result", completed, ok)
	}

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !sessionTestHasExecutionEvent(events, core.ExecutionEventDurableChildTaskQueued) || !sessionTestHasExecutionEvent(events, core.ExecutionEventDurableChildTaskResult) {
		t.Fatalf("child task events = %#v, want queued and result events", events)
	}
}

func TestChildTaskAdmissionAtomicallyQueuesContinuityPacketEventAndNextAction(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7710, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-admit", DurableAgentID: "child-admit"}}
	now := time.Now().UTC().Round(0)
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "child-admit",
		ChannelKind: "manual_channel",
		Status:      "active",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "test-api-key",
			Model:          "test-model",
			MaxTokens:      64,
		},
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	payloadRaw, _ := json.Marshal(map[string]any{"grant_id": "capg-child-admit", "allowed_actions": []string{"invoke"}})
	packetInput := ChildTaskPacketInput{
		PacketID:       "grant_task:child-admit",
		AgentID:        "child-admit",
		Key:            key,
		TaskKind:       "capability_grant_wake",
		AuthorityKind:  "capability_grant",
		AuthorityID:    "capg-child-admit",
		GrantID:        "capg-child-admit",
		RequestID:      "cap-child-admit",
		TargetResource: "codex",
		RequiredAction: "invoke",
		InputJSON:      string(payloadRaw),
		CreatedAt:      now,
	}
	packet, err := store.RecordChildTaskAdmission(ChildTaskAdmissionInput{
		AgentID: "child-admit",
		ContinuityMessage: core.DurableAgentConversationMessage{
			MessageID: packetInput.PacketID,
			Role:      "parent",
			Text:      "Capability grant activated.",
			CreatedAt: now,
		},
		Packet: packetInput,
		QueuedEvents: []ExecutionEventInput{{
			EventType:   core.ExecutionEventCapabilityGrantWakeQueued,
			Stage:       "capability",
			Status:      "wake_queued",
			PayloadJSON: `{"grant_id":"capg-child-admit"}`,
			CreatedAt:   now,
		}},
		NextAction: &NextActionInput{
			Owner:              "capability_grant_wake",
			State:              NextActionWaitingForChild,
			CausalRefs:         []string{"capability_grant:capg-child-admit", "task_packet:" + packetInput.PacketID},
			NextAction:         "wake the child with the compact grant task packet",
			RequiredAuthority:  "tool",
			OperatorProjection: "The grant was activated.",
			CreatedAt:          now,
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskAdmission() err = %v", err)
	}
	if packet.PacketID != packetInput.PacketID || packet.InputFingerprint == "" {
		t.Fatalf("packet = %#v, want admitted packet with immutable fingerprint", packet)
	}
	state, err := store.DurableAgentState("child-admit")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	pending := continuity.PendingParentConversationMessages(10)
	if len(pending) != 1 || pending[0].MessageID != packetInput.PacketID {
		t.Fatalf("pending continuity = %#v, want one packet message", pending)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != packetInput.PacketID {
		t.Fatalf("open next actions = %#v, want packet-anchored waiting action", open)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !sessionTestHasExecutionEvent(events, core.ExecutionEventCapabilityGrantWakeQueued) || !sessionTestHasExecutionEvent(events, core.ExecutionEventDurableChildTaskQueued) {
		t.Fatalf("events = %#v, want admission and packet events", events)
	}

	if _, err := store.RecordChildTaskAdmission(ChildTaskAdmissionInput{
		AgentID: "child-admit",
		ContinuityMessage: core.DurableAgentConversationMessage{
			MessageID: packetInput.PacketID,
			Role:      "parent",
			Text:      "Capability grant activated.",
			CreatedAt: now,
		},
		Packet:    packetInput,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("RecordChildTaskAdmission(replay) err = %v", err)
	}
	state, err = store.DurableAgentState("child-admit")
	if err != nil {
		t.Fatalf("DurableAgentState(replay) err = %v", err)
	}
	continuity, err = core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState(replay) err = %v", err)
	}
	if got := len(continuity.PendingParentConversationMessages(10)); got != 1 {
		t.Fatalf("pending continuity after replay = %d, want no duplicate message", got)
	}
	changed := packetInput
	changed.RequiredAction = "read_file"
	if _, err := store.RecordChildTaskAdmission(ChildTaskAdmissionInput{AgentID: "child-admit", Packet: changed, CreatedAt: now}); err == nil {
		t.Fatal("RecordChildTaskAdmission(changed input) err = nil, want idempotency conflict")
	}
}

func TestChildTaskResultAttemptsDoNotCollapse(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7702, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-retry", DurableAgentID: "child-retry"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:retry",
		AgentID:   "child-retry",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{"reason":"retry"}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}

	firstAttemptID := ChildTaskAttemptID(packet.PacketID, "attempt-1")
	firstClaim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, firstAttemptID, now.Add(time.Second))
	update, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       firstAttemptID,
		LeaseOwner:      firstClaim.LeaseOwner,
		LeaseGeneration: firstClaim.LeaseGeneration,
		FencingToken:    firstClaim.FencingToken,
		AgentID:         "child-retry",
		Key:             key,
		Status:          ChildTaskResultUpdate,
		Summary:         "Still working through the bounded task.",
		CreatedAt:       now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(update) err = %v", err)
	}
	if update.AttemptID != firstAttemptID || update.Status != ChildTaskResultUpdate || update.NextState != NextActionWaitingForChild {
		t.Fatalf("update result = %#v, want nonterminal first attempt", update)
	}
	inProgress, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(in progress) err = %v", err)
	}
	if !ok || inProgress.Status != ChildTaskPacketInProgress || inProgress.ResultID != update.ResultID || !inProgress.TerminalAt.IsZero() {
		t.Fatalf("in-progress packet = %#v ok=%t, want nonterminal update state", inProgress, ok)
	}

	secondAttemptID := ChildTaskAttemptID(packet.PacketID, "attempt-2")
	secondClaim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, secondAttemptID, now.Add(2*time.Second))
	completed, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       secondAttemptID,
		LeaseOwner:      secondClaim.LeaseOwner,
		LeaseGeneration: secondClaim.LeaseGeneration,
		FencingToken:    secondClaim.FencingToken,
		AgentID:         "child-retry",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Completed on retry.",
		EvidenceRefs:    []string{"child_task_result:" + update.ResultID},
		CreatedAt:       now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(completed) err = %v", err)
	}
	if completed.ResultID == update.ResultID || completed.AttemptID == update.AttemptID {
		t.Fatalf("completed result = %#v update = %#v, want distinct attempt identity", completed, update)
	}
	if _, ok, err := store.ChildTaskResult(update.ResultID); err != nil || !ok {
		t.Fatalf("ChildTaskResult(update) ok=%t err=%v, want first attempt retained", ok, err)
	}
	terminal, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(terminal) err = %v", err)
	}
	if !ok || terminal.Status != ChildTaskPacketCompleted || terminal.ResultID != completed.ResultID || terminal.TerminalAt.IsZero() {
		t.Fatalf("terminal packet = %#v ok=%t, want completed second attempt linked", terminal, ok)
	}
}

func TestChildTaskTerminalPacketAbsorbsStaleResults(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7703, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-terminal", DurableAgentID: "child-terminal"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:terminal",
		AgentID:   "child-terminal",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:terminal-1", now.Add(time.Second))
	completed, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       claim.ActiveAttemptID,
		LeaseOwner:      claim.LeaseOwner,
		LeaseGeneration: claim.LeaseGeneration,
		FencingToken:    claim.FencingToken,
		AgentID:         "child-terminal",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Complete.",
		CreatedAt:       now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(completed) err = %v", err)
	}

	if _, err := store.ClaimChildTaskAttempt(ChildTaskAttemptClaimInput{
		PacketID:  packet.PacketID,
		AttemptID: "child_attempt:terminal-2",
		AgentID:   "child-terminal",
		Key:       key,
		ClaimedAt: now.Add(3 * time.Second),
	}); err == nil {
		t.Fatal("ClaimChildTaskAttempt(terminal) err = nil, want terminal packet to reject claim")
	}
	staleAttemptID := "child_attempt:terminal-stale"
	if _, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       staleAttemptID,
		LeaseOwner:      "stale-owner",
		LeaseGeneration: claim.LeaseGeneration + 1,
		FencingToken:    ChildTaskFencingToken(packet.PacketID, staleAttemptID, claim.LeaseGeneration+1),
		AgentID:         "child-terminal",
		Key:             key,
		Status:          ChildTaskResultUpdate,
		Summary:         "Late update.",
		CreatedAt:       now.Add(4 * time.Second),
	}); err == nil {
		t.Fatal("recordChildTaskResultForTest(stale update) err = nil, want stale result rejected")
	}
	after, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(after stale) err = %v", err)
	}
	if !ok || after.Status != ChildTaskPacketCompleted || after.ResultID != completed.ResultID || after.TerminalAt.IsZero() {
		t.Fatalf("after stale packet = %#v ok=%t, want completed packet unchanged", after, ok)
	}
	if _, ok, err := store.ChildTaskResult(ChildTaskResultID("child-terminal", packet.PacketID, staleAttemptID)); err != nil || ok {
		t.Fatalf("ChildTaskResult(stale) ok=%t err=%v, want no stale result stored", ok, err)
	}
}

func TestChildTaskLiveLeaseRejectsSecondClaimAndAllowsOwnerResult(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7704, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-live-lease", DurableAgentID: "child-live-lease"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:live_lease",
		AgentID:   "child-live-lease",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	first := claimChildTaskAttemptWithLeaseForTest(t, store, key, packet.PacketID, "child_attempt:live-a", "worker-a", now.Add(time.Second), now.Add(10*time.Minute))
	if _, err := store.ClaimChildTaskAttempt(ChildTaskAttemptClaimInput{
		PacketID:       packet.PacketID,
		AttemptID:      "child_attempt:live-b",
		LeaseOwner:     "worker-b",
		Key:            key,
		ClaimedAt:      now.Add(2 * time.Second),
		LeaseExpiresAt: now.Add(10 * time.Minute),
	}); err == nil {
		t.Fatal("ClaimChildTaskAttempt(live competing owner) err = nil, want live lease to reject second claim")
	}

	completed, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       first.ActiveAttemptID,
		LeaseOwner:      first.LeaseOwner,
		LeaseGeneration: first.LeaseGeneration,
		FencingToken:    first.FencingToken,
		AgentID:         "child-live-lease",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "First owner completed while lease remained live.",
		CreatedAt:       now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(first owner) err = %v", err)
	}
	after, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(after live lease) err = %v", err)
	}
	if !ok || after.Status != ChildTaskPacketCompleted || after.ResultID != completed.ResultID || after.ActiveAttemptID != first.ActiveAttemptID {
		t.Fatalf("after live lease packet = %#v ok=%t, want first owner completion", after, ok)
	}
}

func TestChildTaskExpiredLeaseAllowsTakeoverAndRejectsOldResult(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7706, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-expired-lease", DurableAgentID: "child-expired-lease"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:expired_lease",
		AgentID:   "child-expired-lease",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	first := claimChildTaskAttemptWithLeaseForTest(t, store, key, packet.PacketID, "child_attempt:expired-a", "worker-a", now.Add(time.Second), now.Add(2*time.Second))
	second := claimChildTaskAttemptWithLeaseForTest(t, store, key, packet.PacketID, "child_attempt:expired-b", "worker-b", now.Add(3*time.Second), now.Add(10*time.Minute))
	if second.LeaseGeneration <= first.LeaseGeneration || second.LeaseOwner != "worker-b" {
		t.Fatalf("second claim = %#v first = %#v, want takeover after expiry", second, first)
	}
	if _, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       first.ActiveAttemptID,
		LeaseOwner:      first.LeaseOwner,
		LeaseGeneration: first.LeaseGeneration,
		FencingToken:    first.FencingToken,
		AgentID:         "child-expired-lease",
		Key:             key,
		Status:          ChildTaskResultFailed,
		Summary:         "Late result from expired first lease.",
		CreatedAt:       now.Add(4 * time.Second),
	}); err == nil {
		t.Fatal("recordChildTaskResultForTest(expired first lease) err = nil, want old owner rejected")
	}
	completed, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       second.ActiveAttemptID,
		LeaseOwner:      second.LeaseOwner,
		LeaseGeneration: second.LeaseGeneration,
		FencingToken:    second.FencingToken,
		AgentID:         "child-expired-lease",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Second owner completed after takeover.",
		CreatedAt:       now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(second owner) err = %v", err)
	}
	after, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(after expiry takeover) err = %v", err)
	}
	if !ok || after.Status != ChildTaskPacketCompleted || after.ResultID != completed.ResultID {
		t.Fatalf("after expiry takeover packet = %#v ok=%t, want second owner completion", after, ok)
	}
}

func TestChildTaskReleasedLeaseAllowsTakeover(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7707, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-released-lease", DurableAgentID: "child-released-lease"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:released_lease",
		AgentID:   "child-released-lease",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	first := claimChildTaskAttemptWithLeaseForTest(t, store, key, packet.PacketID, "child_attempt:released-a", "worker-a", now.Add(time.Second), now.Add(10*time.Minute))
	released, err := store.ReleaseChildTaskAttempt(ChildTaskAttemptReleaseInput{
		PacketID:        packet.PacketID,
		AttemptID:       first.ActiveAttemptID,
		LeaseOwner:      first.LeaseOwner,
		LeaseGeneration: first.LeaseGeneration,
		FencingToken:    first.FencingToken,
		ReleasedAt:      now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("ReleaseChildTaskAttempt() err = %v", err)
	}
	if released.LeaseReleasedAt.IsZero() {
		t.Fatalf("released packet = %#v, want release timestamp", released)
	}
	second := claimChildTaskAttemptWithLeaseForTest(t, store, key, packet.PacketID, "child_attempt:released-b", "worker-b", now.Add(3*time.Second), now.Add(10*time.Minute))
	if second.LeaseGeneration <= first.LeaseGeneration || second.LeaseOwner != "worker-b" || !second.LeaseReleasedAt.IsZero() {
		t.Fatalf("second claim = %#v first = %#v, want fresh lease after explicit release", second, first)
	}
	if _, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       first.ActiveAttemptID,
		LeaseOwner:      first.LeaseOwner,
		LeaseGeneration: first.LeaseGeneration,
		FencingToken:    first.FencingToken,
		AgentID:         "child-released-lease",
		Key:             key,
		Status:          ChildTaskResultFailed,
		Summary:         "Late result from released first lease.",
		CreatedAt:       now.Add(4 * time.Second),
	}); err == nil {
		t.Fatal("recordChildTaskResultForTest(released first lease) err = nil, want released owner rejected")
	}
	completed, err := store.recordChildTaskResultForTest(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       second.ActiveAttemptID,
		LeaseOwner:      second.LeaseOwner,
		LeaseGeneration: second.LeaseGeneration,
		FencingToken:    second.FencingToken,
		AgentID:         "child-released-lease",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Second owner completed after release.",
		CreatedAt:       now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(second owner) err = %v", err)
	}
	after, ok, err := store.ChildTaskPacket(packet.PacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(after release takeover) err = %v", err)
	}
	if !ok || after.Status != ChildTaskPacketCompleted || after.ResultID != completed.ResultID {
		t.Fatalf("after release takeover packet = %#v ok=%t, want second owner completion", after, ok)
	}
}

func TestChildTaskExactReplayIsIdempotent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7705, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-replay", DurableAgentID: "child-replay"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:replay",
		AgentID:   "child-replay",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:replay-1", now.Add(time.Second))
	input := ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       claim.ActiveAttemptID,
		LeaseOwner:      claim.LeaseOwner,
		LeaseGeneration: claim.LeaseGeneration,
		FencingToken:    claim.FencingToken,
		AgentID:         "child-replay",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Replay-safe completion.",
		CreatedAt:       now.Add(2 * time.Second),
	}
	first, err := store.recordChildTaskResultForTest(input)
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(first) err = %v", err)
	}
	replayed, err := store.recordChildTaskResultForTest(input)
	if err != nil {
		t.Fatalf("recordChildTaskResultForTest(replay) err = %v", err)
	}
	if replayed.ResultID != first.ResultID || replayed.AttemptID != first.AttemptID {
		t.Fatalf("replayed result = %#v first = %#v, want exact idempotent replay", replayed, first)
	}
}

func TestChildTaskOutcomeCommitRecordsIntentWithResultAndNextAction(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7708, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-intent", DurableAgentID: "child-intent"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:intent",
		AgentID:   "child-intent",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:intent-1", now.Add(time.Second))
	resultInput := NormalizeChildTaskResultInput(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       claim.ActiveAttemptID,
		LeaseOwner:      claim.LeaseOwner,
		LeaseGeneration: claim.LeaseGeneration,
		FencingToken:    claim.FencingToken,
		AgentID:         "child-intent",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "Completed with post-outcome work.",
		CreatedAt:       now.Add(2 * time.Second),
	})
	result, err := store.CommitChildTaskOutcome(ChildTaskOutcomeCommitInput{
		Result: resultInput,
		OutcomeIntents: []ChildTaskOutcomeIntentInput{{
			Kind:        ChildTaskOutcomeIntentGenericFinalize,
			PayloadJSON: `{"callback":"test"}`,
			ResultRef:   "test://finalize",
		}},
		ResolvedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome() err = %v", err)
	}
	if result.ResultID != resultInput.ResultID {
		t.Fatalf("result = %#v input = %#v, want normalized result id", result, resultInput)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions = %#v, want terminal outcome to resolve packet actions", open)
	}
	intents, err := store.PendingChildTaskOutcomeIntents(10)
	if err != nil {
		t.Fatalf("PendingChildTaskOutcomeIntents() err = %v", err)
	}
	if len(intents) != 1 || intents[0].PacketID != packet.PacketID || intents[0].ResultID != result.ResultID || intents[0].Kind != ChildTaskOutcomeIntentGenericFinalize {
		t.Fatalf("pending intents = %#v, want result-linked generic finalizer", intents)
	}
	if err := store.MarkChildTaskOutcomeIntentApplied(intents[0].IntentID, now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkChildTaskOutcomeIntentApplied() err = %v", err)
	}
	intents, err = store.PendingChildTaskOutcomeIntents(10)
	if err != nil {
		t.Fatalf("PendingChildTaskOutcomeIntents(after applied) err = %v", err)
	}
	if len(intents) != 0 {
		t.Fatalf("pending intents after applied = %#v, want none", intents)
	}
}

func TestCommitChildTaskOutcomeReplayIsValueIdempotent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7718, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-replay", DurableAgentID: "child-replay"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:replay",
		AgentID:   "child-replay",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:replay", now.Add(time.Second))
	resultInput := NormalizeChildTaskResultInput(ChildTaskResultInput{
		PacketID:        packet.PacketID,
		AttemptID:       claim.ActiveAttemptID,
		LeaseOwner:      claim.LeaseOwner,
		LeaseGeneration: claim.LeaseGeneration,
		FencingToken:    claim.FencingToken,
		AgentID:         "child-replay",
		Key:             key,
		Status:          ChildTaskResultCompleted,
		Summary:         "same value",
		CreatedAt:       now.Add(2 * time.Second),
	})
	input := ChildTaskOutcomeCommitInput{
		Result: resultInput,
		OutcomeIntents: []ChildTaskOutcomeIntentInput{{
			Kind:        ChildTaskOutcomeIntentPolicyApplied,
			PayloadJSON: `{"agent_id":"child-replay"}`,
			ResultRef:   "durable_policy_applied:child-replay",
		}},
		ResolvedAt: now.Add(2 * time.Second),
	}
	first, err := store.CommitChildTaskOutcome(input)
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome(first) err = %v", err)
	}
	second, err := store.CommitChildTaskOutcome(input)
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome(replay) err = %v", err)
	}
	if second.ResultID != first.ResultID || second.ResultFingerprint != first.ResultFingerprint || second.IntentSetFingerprint != first.IntentSetFingerprint {
		t.Fatalf("replay result = %#v first = %#v, want exact idempotent value", second, first)
	}
	divergent := input
	divergent.Result.Summary = "changed value"
	if _, err := store.CommitChildTaskOutcome(divergent); err == nil || !strings.Contains(err.Error(), "idempotency conflict") {
		t.Fatalf("CommitChildTaskOutcome(changed summary) err = %v, want idempotency conflict", err)
	}
	divergent = input
	divergent.OutcomeIntents = append([]ChildTaskOutcomeIntentInput(nil), input.OutcomeIntents...)
	divergent.OutcomeIntents[0].PayloadJSON = `{"agent_id":"different"}`
	if _, err := store.CommitChildTaskOutcome(divergent); err == nil || !strings.Contains(err.Error(), "idempotency conflict") {
		t.Fatalf("CommitChildTaskOutcome(changed intents) err = %v, want idempotency conflict", err)
	}
}

func TestChildTaskOutcomeIntentClaimRetryAndResultScopedLookup(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7719, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-intents", DurableAgentID: "child-intents"}}
	now := time.Now().UTC().Round(0)
	var targetResult ChildTaskResult
	for i := 0; i < 102; i++ {
		packetID := "child_task:intent_scope_" + strconv.Itoa(i)
		agentID := "child-intents-" + strconv.Itoa(i)
		packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
			PacketID:  packetID,
			AgentID:   agentID,
			Key:       key,
			TaskKind:  "durable_wake",
			InputJSON: `{}`,
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		})
		if err != nil {
			t.Fatalf("RecordChildTaskPacket(%d) err = %v", i, err)
		}
		claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:intent_scope_"+strconv.Itoa(i), now.Add(time.Second+time.Duration(i)*time.Millisecond))
		result, err := store.CommitChildTaskOutcome(ChildTaskOutcomeCommitInput{
			Result: NormalizeChildTaskResultInput(ChildTaskResultInput{
				PacketID:        packet.PacketID,
				AttemptID:       claim.ActiveAttemptID,
				LeaseOwner:      claim.LeaseOwner,
				LeaseGeneration: claim.LeaseGeneration,
				FencingToken:    claim.FencingToken,
				AgentID:         agentID,
				Key:             key,
				Status:          ChildTaskResultCompleted,
				Summary:         "done",
				CreatedAt:       now.Add(2*time.Second + time.Duration(i)*time.Millisecond),
			}),
			OutcomeIntents: []ChildTaskOutcomeIntentInput{{
				Kind:        ChildTaskOutcomeIntentPolicyApplied,
				PayloadJSON: `{"agent_id":"` + agentID + `"}`,
				ResultRef:   "durable_policy_applied:" + agentID,
			}},
			ResolvedAt: now.Add(2*time.Second + time.Duration(i)*time.Millisecond),
		})
		if err != nil {
			t.Fatalf("CommitChildTaskOutcome(%d) err = %v", i, err)
		}
		if i == 101 {
			targetResult = result
		}
	}
	scoped, err := store.ChildTaskOutcomeIntentsForResult(targetResult.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult() err = %v", err)
	}
	if len(scoped) != 1 || scoped[0].ResultID != targetResult.ResultID {
		t.Fatalf("scoped intents = %#v, want target result intent past first 100 pending rows", scoped)
	}
	claimed, ok, err := store.ClaimChildTaskOutcomeIntent(ChildTaskOutcomeIntentClaimInput{
		IntentID:       scoped[0].IntentID,
		LeaseOwner:     "worker:test",
		ClaimedAt:      now.Add(3 * time.Second),
		LeaseExpiresAt: now.Add(8 * time.Second),
	})
	if err != nil || !ok {
		t.Fatalf("ClaimChildTaskOutcomeIntent() intent=%#v ok=%t err=%v", claimed, ok, err)
	}
	if err := store.RetryChildTaskOutcomeIntent(ChildTaskOutcomeIntentRetryInput{
		IntentID:        claimed.IntentID,
		LeaseOwner:      claimed.LeaseOwner,
		LeaseGeneration: claimed.LeaseGeneration,
		FencingToken:    claimed.FencingToken,
		LastError:       "temporary",
		AttemptedAt:     now.Add(4 * time.Second),
		NextAttemptAt:   now.Add(10 * time.Second),
	}); err != nil {
		t.Fatalf("RetryChildTaskOutcomeIntent() err = %v", err)
	}
	pending, err := store.PendingChildTaskOutcomeIntents(200)
	if err != nil {
		t.Fatalf("PendingChildTaskOutcomeIntents() err = %v", err)
	}
	for _, intent := range pending {
		if intent.IntentID == claimed.IntentID {
			t.Fatalf("retryable future intent appeared in pending list: %#v", intent)
		}
	}
	claimed, ok, err = store.ClaimChildTaskOutcomeIntent(ChildTaskOutcomeIntentClaimInput{
		IntentID:       scoped[0].IntentID,
		LeaseOwner:     "worker:test-2",
		ClaimedAt:      now.Add(11 * time.Second),
		LeaseExpiresAt: now.Add(16 * time.Second),
	})
	if err != nil || !ok {
		t.Fatalf("ClaimChildTaskOutcomeIntent(retryable) intent=%#v ok=%t err=%v", claimed, ok, err)
	}
	if err := store.CompleteChildTaskOutcomeIntent(ChildTaskOutcomeIntentCompletionInput{
		IntentID:        claimed.IntentID,
		LeaseOwner:      claimed.LeaseOwner,
		LeaseGeneration: claimed.LeaseGeneration,
		FencingToken:    claimed.FencingToken,
		CompletedAt:     now.Add(12 * time.Second),
	}); err != nil {
		t.Fatalf("CompleteChildTaskOutcomeIntent() err = %v", err)
	}
	applied, ok, err := store.ChildTaskOutcomeIntent(claimed.IntentID)
	if err != nil || !ok || applied.Status != ChildTaskOutcomeIntentApplied {
		t.Fatalf("ChildTaskOutcomeIntent() intent=%#v ok=%t err=%v, want applied", applied, ok, err)
	}
}

func TestPendingChildTaskOutcomeIntentsIncludesExpiredApplyingAndBlocksSuccessors(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 7720, UserID: 1001, Scope: ScopeRef{Kind: ScopeKindDurableAgent, ID: "child-sequence", DurableAgentID: "child-sequence"}}
	now := time.Now().UTC().Round(0)
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
		PacketID:  "child_task:sequence",
		AgentID:   "child-sequence",
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim := claimChildTaskAttemptForTest(t, store, key, packet.PacketID, "child_attempt:sequence", now.Add(time.Second))
	result, err := store.CommitChildTaskOutcome(ChildTaskOutcomeCommitInput{
		Result: NormalizeChildTaskResultInput(ChildTaskResultInput{
			PacketID:        packet.PacketID,
			AttemptID:       claim.ActiveAttemptID,
			LeaseOwner:      claim.LeaseOwner,
			LeaseGeneration: claim.LeaseGeneration,
			FencingToken:    claim.FencingToken,
			AgentID:         "child-sequence",
			Key:             key,
			Status:          ChildTaskResultCompleted,
			Summary:         "done",
			CreatedAt:       now.Add(2 * time.Second),
		}),
		OutcomeIntents: []ChildTaskOutcomeIntentInput{
			{
				Kind:        ChildTaskOutcomeIntentScheduledReview,
				Sequence:    10,
				PayloadJSON: `{"agent_id":"child-sequence"}`,
				ResultRef:   "scheduled_review:child-sequence",
			},
			{
				Kind:        ChildTaskOutcomeIntentPolicyApplied,
				Sequence:    20,
				PayloadJSON: `{"agent_id":"child-sequence"}`,
				ResultRef:   "durable_policy_applied:child-sequence",
			},
		},
		ResolvedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome() err = %v", err)
	}
	intents, err := store.ChildTaskOutcomeIntentsForResult(result.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult() err = %v", err)
	}
	if len(intents) != 2 {
		t.Fatalf("intents = %#v, want two ordered intents", intents)
	}
	first := intents[0]
	claimed, ok, err := store.ClaimChildTaskOutcomeIntent(ChildTaskOutcomeIntentClaimInput{
		IntentID:       first.IntentID,
		LeaseOwner:     "worker:crash",
		ClaimedAt:      time.Now().UTC().Add(-2 * time.Second),
		LeaseExpiresAt: time.Now().UTC().Add(-time.Second),
	})
	if err != nil || !ok {
		t.Fatalf("ClaimChildTaskOutcomeIntent(first) intent=%#v ok=%t err=%v", claimed, ok, err)
	}
	pending, err := store.PendingChildTaskOutcomeIntents(10)
	if err != nil {
		t.Fatalf("PendingChildTaskOutcomeIntents(expired applying) err = %v", err)
	}
	if len(pending) != 1 || pending[0].IntentID != first.IntentID {
		t.Fatalf("pending expired applying = %#v, want only expired first intent", pending)
	}
	claimed, ok, err = store.ClaimChildTaskOutcomeIntent(ChildTaskOutcomeIntentClaimInput{
		IntentID:       first.IntentID,
		LeaseOwner:     "worker:reclaim",
		ClaimedAt:      time.Now().UTC(),
		LeaseExpiresAt: time.Now().UTC().Add(10 * time.Second),
	})
	if err != nil || !ok || claimed.LeaseOwner != "worker:reclaim" {
		t.Fatalf("ClaimChildTaskOutcomeIntent(expired applying) intent=%#v ok=%t err=%v, want reclaimed", claimed, ok, err)
	}
	if err := store.RetryChildTaskOutcomeIntent(ChildTaskOutcomeIntentRetryInput{
		IntentID:        claimed.IntentID,
		LeaseOwner:      claimed.LeaseOwner,
		LeaseGeneration: claimed.LeaseGeneration,
		FencingToken:    claimed.FencingToken,
		LastError:       "transient",
		AttemptedAt:     now.Add(6 * time.Second),
		NextAttemptAt:   now.Add(30 * time.Second),
	}); err != nil {
		t.Fatalf("RetryChildTaskOutcomeIntent() err = %v", err)
	}
	pending, err = store.PendingChildTaskOutcomeIntents(10)
	if err != nil {
		t.Fatalf("PendingChildTaskOutcomeIntents(after retry) err = %v", err)
	}
	for _, intent := range pending {
		if intent.ResultID == result.ResultID {
			t.Fatalf("pending after sequence-10 retry includes blocked successor: %#v", pending)
		}
	}
}

func claimChildTaskAttemptForTest(t *testing.T, store *SQLiteStore, key SessionKey, packetID string, attemptID string, at time.Time) ChildTaskPacket {
	t.Helper()
	return claimChildTaskAttemptWithLeaseForTest(t, store, key, packetID, attemptID, "test_worker:"+attemptID, at, at.Add(10*time.Minute))
}

func claimChildTaskAttemptWithLeaseForTest(t *testing.T, store *SQLiteStore, key SessionKey, packetID string, attemptID string, owner string, claimedAt time.Time, leaseExpiresAt time.Time) ChildTaskPacket {
	t.Helper()
	claimed, err := store.ClaimChildTaskAttempt(ChildTaskAttemptClaimInput{
		PacketID:       packetID,
		AttemptID:      attemptID,
		LeaseOwner:     owner,
		Key:            key,
		ClaimedAt:      claimedAt,
		LeaseExpiresAt: leaseExpiresAt,
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt(%s/%s) err = %v", packetID, attemptID, err)
	}
	if claimed.ActiveAttemptID != attemptID || claimed.LeaseOwner != owner || claimed.LeaseGeneration <= 0 || claimed.FencingToken == "" || !claimed.LeaseExpiresAt.Equal(leaseExpiresAt) || claimed.LeaseHeartbeatAt.IsZero() {
		t.Fatalf("claimed packet = %#v, want active attempt fence", claimed)
	}
	return claimed
}

func sessionTestHasExecutionEvent(events []ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
