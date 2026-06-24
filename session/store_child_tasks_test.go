//go:build linux

package session

import (
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
	packet, err := store.RecordChildTaskPacket(ChildTaskPacketInput{
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
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	if packet.Status != ChildTaskPacketQueued || packet.TaskLeaseID == "" || packet.SessionID != SessionIDForKey(key) {
		t.Fatalf("packet = %#v, want queued durable packet with lease and session", packet)
	}

	replayed, err := store.RecordChildTaskPacket(ChildTaskPacketInput{PacketID: packet.PacketID, AgentID: "child-alpha", Key: key})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket(replay) err = %v", err)
	}
	if replayed.CreatedAt != packet.CreatedAt || replayed.GrantID != "capg-child-alpha" {
		t.Fatalf("replayed packet = %#v, want idempotent original", replayed)
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
