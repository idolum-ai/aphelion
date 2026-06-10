//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestReplyAnchorForTelegramMessageUsesThreadLastMessage(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	thread, _, err := store.CreateTelegramThreadForUpdate(7001, 42, 100, 501, "thread opener", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if err := store.RecordTelegramThreadLastMessage(7001, thread.ThreadID, 9090, "test_anchor", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramThreadLastMessage() err = %v", err)
	}
	rt := &Runtime{store: store}
	anchor := rt.replyAnchorForTelegramMessage(core.InboundMessage{ChatID: 7001, MessageID: 502, TelegramThreadID: thread.ThreadID})
	if anchor == nil || *anchor != 9090 {
		t.Fatalf("reply anchor = %v, want last thread message 9090", anchor)
	}
}

func TestReplyAnchorForTelegramMessageSkipsDurableAgent(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	thread, _, err := store.CreateTelegramThreadForUpdate(7002, 42, 100, 501, "thread opener", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if err := store.RecordTelegramThreadLastMessage(7002, thread.ThreadID, 9090, "test_anchor", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramThreadLastMessage() err = %v", err)
	}
	rt := &Runtime{store: store}
	anchor := rt.replyAnchorForTelegramMessage(core.InboundMessage{ChatID: 7002, MessageID: 502, TelegramThreadID: thread.ThreadID, DurableAgentID: "agent-alpha"})
	if anchor == nil || *anchor != 502 {
		t.Fatalf("durable agent reply anchor = %v, want inbound message 502", anchor)
	}
}

func TestTurnDeliveryRecordsAllThreadReplyChunksAsAnchors(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	thread, _, err := store.CreateTelegramThreadForUpdate(7003, 42, 100, 501, "thread opener", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	rt := &Runtime{store: store}
	key := session.SessionKey{ChatID: 7003, Scope: session.TelegramThreadScopeRef(7003, thread.ThreadID)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(thread session) err = %v", err)
	}
	port := &turnDeliveryPort{
		runtime:        rt,
		msg:            core.InboundMessage{ChatID: 7003, MessageID: 502, TelegramThreadID: thread.ThreadID},
		recordErrCtx:   "record outbound reply",
		deliveryMsgIDs: []int64{6101, 6102, 6103},
	}
	if err := port.recordOutboundWithContext(context.Background(), sess, key, 6101, "text"); err != nil {
		t.Fatalf("recordOutboundWithContext() err = %v", err)
	}
	for _, messageID := range []int64{6101, 6102, 6103} {
		if got, ok, err := store.TelegramThreadIDForReplyMessage(7003, messageID); err != nil || !ok || got != thread.ThreadID {
			t.Fatalf("TelegramThreadIDForReplyMessage(%d) = %d ok=%v err=%v, want thread %d", messageID, got, ok, err, thread.ThreadID)
		}
	}
	anchor, ok, err := store.TelegramThreadLastMessage(7003, thread.ThreadID)
	if err != nil || !ok || anchor.MessageID != 6103 {
		t.Fatalf("TelegramThreadLastMessage() = %#v ok=%v err=%v, want final chunk 6103", anchor, ok, err)
	}
}
