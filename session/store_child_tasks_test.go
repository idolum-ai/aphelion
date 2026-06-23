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

	result, err := store.RecordChildTaskResult(ChildTaskResultInput{
		PacketID:     packet.PacketID,
		AgentID:      "child-alpha",
		Key:          key,
		Status:       ChildTaskResultCompleted,
		Summary:      "Grant incorporated.",
		EvidenceRefs: []string{"capability_grant:capg-child-alpha"},
		CreatedAt:    now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("RecordChildTaskResult() err = %v", err)
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
	update, err := store.RecordChildTaskResult(ChildTaskResultInput{
		PacketID:  packet.PacketID,
		AttemptID: firstAttemptID,
		AgentID:   "child-retry",
		Key:       key,
		Status:    ChildTaskResultUpdate,
		Summary:   "Still working through the bounded task.",
		CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("RecordChildTaskResult(update) err = %v", err)
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
	completed, err := store.RecordChildTaskResult(ChildTaskResultInput{
		PacketID:     packet.PacketID,
		AttemptID:    secondAttemptID,
		AgentID:      "child-retry",
		Key:          key,
		Status:       ChildTaskResultCompleted,
		Summary:      "Completed on retry.",
		EvidenceRefs: []string{"child_task_result:" + update.ResultID},
		CreatedAt:    now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("RecordChildTaskResult(completed) err = %v", err)
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

func sessionTestHasExecutionEvent(events []ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
