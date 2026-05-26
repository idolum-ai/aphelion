//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTelegramAgentIDForReplyMessageUsesAgentMessageLedger(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "agent-messages.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	if err := store.RecordTelegramAgentMessage(1001, 9001, "ops-child", "agent_detail", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramAgentMessage() err = %v", err)
	}
	got, ok, err := store.TelegramAgentIDForReplyMessage(1001, 9001)
	if err != nil || !ok || got != "ops-child" {
		t.Fatalf("TelegramAgentIDForReplyMessage() = %q ok=%t err=%v, want ops-child", got, ok, err)
	}
	if got, ok, err := store.TelegramAgentIDForReplyMessage(1002, 9001); err != nil || ok || got != "" {
		t.Fatalf("TelegramAgentIDForReplyMessage(other chat) = %q ok=%t err=%v, want no match", got, ok, err)
	}
}
