//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestClearChatSessionContextRemovesSessionRow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	chatID := int64(7331)
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-1",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, exists, err := store.StatusStateIfExists(key); err != nil {
		t.Fatalf("StatusStateIfExists(before) err = %v", err)
	} else if !exists {
		t.Fatal("session exists(before) = false, want true")
	}

	cleared, err := rt.ClearChatSessionContext(chatID)
	if err != nil {
		t.Fatalf("ClearChatSessionContext() err = %v", err)
	}
	if !cleared {
		t.Fatal("cleared = false, want true")
	}

	if _, exists, err := store.StatusStateIfExists(key); err != nil {
		t.Fatalf("StatusStateIfExists(after) err = %v", err)
	} else if exists {
		t.Fatal("session exists(after) = true, want false")
	}

	cleared, err = rt.ClearChatSessionContext(chatID)
	if err != nil {
		t.Fatalf("ClearChatSessionContext(second) err = %v", err)
	}
	if cleared {
		t.Fatal("cleared(second) = true, want false")
	}
}
