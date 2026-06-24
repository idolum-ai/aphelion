//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestDurableAgentWakeClaimIsOneTimePerLease(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	now := time.Now().UTC()
	input := DurableAgentWakeClaimInput{
		LeaseID:          "lease-child-wake-claim",
		AgentID:          "child-alpha",
		TurnRunID:        42,
		MessageBatchHash: DurableAgentWakeMessageBatchHash("child-alpha", []string{"parent-msg-1"}),
		MessageIDs:       []string{"parent-msg-1"},
		CreatedAt:        now,
	}
	claim, err := store.ClaimDurableAgentWakeOnce(input)
	if err != nil {
		t.Fatalf("ClaimDurableAgentWakeOnce() err = %v", err)
	}
	if claim.LeaseID != input.LeaseID || claim.AgentID != input.AgentID || claim.TurnRunID != input.TurnRunID {
		t.Fatalf("claim = %#v, want input identity", claim)
	}

	_, err = store.ClaimDurableAgentWakeOnce(DurableAgentWakeClaimInput{
		LeaseID:          input.LeaseID,
		AgentID:          "child-beta",
		TurnRunID:        43,
		MessageBatchHash: DurableAgentWakeMessageBatchHash("child-beta", []string{"parent-msg-2"}),
		MessageIDs:       []string{"parent-msg-2"},
		CreatedAt:        now.Add(time.Second),
	})
	if err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("ClaimDurableAgentWakeOnce(second lease use) err = %v, want already claimed", err)
	}
}
