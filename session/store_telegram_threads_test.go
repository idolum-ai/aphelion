//go:build linux

package session

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestTelegramThreadCreateIsPerChatAndIdempotentByUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	first, created, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "first task", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(first) err = %v", err)
	}
	if !created || first.ThreadID != 1 || first.Status != TelegramThreadStatusOpen {
		t.Fatalf("first = %#v created=%v, want new open thread 1", first, created)
	}
	threadSessionID := SessionIDForKey(SessionKey{ChatID: 1001, Scope: TelegramThreadScopeRef(1001, first.ThreadID)})
	var threadSessionCount int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, threadSessionID).Scan(&threadSessionCount); err != nil {
		t.Fatalf("query thread session count: %v", err)
	}
	if threadSessionCount != 1 {
		t.Fatalf("thread session count = %d, want durable session row at create time", threadSessionCount)
	}
	again, created, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "first task replay", now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(replay) err = %v", err)
	}
	if created || again.ThreadID != first.ThreadID || again.CreatedText != "first task" {
		t.Fatalf("again = %#v created=%v, want idempotent original thread", again, created)
	}
	second, created, err := store.CreateTelegramThreadForUpdate(1001, 2002, 302, 402, "second task", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(second) err = %v", err)
	}
	if !created || second.ThreadID != 2 {
		t.Fatalf("second = %#v created=%v, want thread 2", second, created)
	}
	otherChat, created, err := store.CreateTelegramThreadForUpdate(1002, 2002, 301, 401, "other chat task", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(other chat) err = %v", err)
	}
	if !created || otherChat.ThreadID != 1 {
		t.Fatalf("otherChat = %#v created=%v, want per-chat thread 1", otherChat, created)
	}
}

func TestTelegramThreadClosePreservesTranscriptAndMarksClosed(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "investigate child agents", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	key := SessionKey{ChatID: 1001, UserID: 0, Scope: TelegramThreadScopeRef(1001, thread.ThreadID)}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(thread session) err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "investigate child agents", TurnIndex: 1},
		{Role: "assistant", Content: "child agent plan", TurnIndex: 1},
	}, coreTokenUsageZero()); err != nil {
		t.Fatalf("Save(thread session) err = %v", err)
	}
	closed, changed, err := store.CloseTelegramThread(1001, thread.ThreadID, "outcome summary", time.Now().UTC())
	if err != nil {
		t.Fatalf("CloseTelegramThread() err = %v", err)
	}
	if !changed || closed.Status != TelegramThreadStatusClosed || closed.AbsorbSummary != "outcome summary" {
		t.Fatalf("closed = %#v changed=%v, want closed summary", closed, changed)
	}
	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(thread session after close) err = %v", err)
	}
	if len(reloaded.Messages) != 2 || reloaded.Messages[1].Content != "child agent plan" {
		t.Fatalf("messages = %#v, want preserved thread transcript", reloaded.Messages)
	}
}

func TestRecordTelegramThreadAbsorbIsAtomicWithMainNote(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "investigate child agents", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	mainKey := SessionKey{ChatID: 1001}
	mainSession, err := store.Load(mainKey)
	if err != nil {
		t.Fatalf("Load(main) err = %v", err)
	}
	mainSession.TurnCount = 1
	messages := []Message{
		{Role: "user", Content: "/absorb 1", TurnIndex: 1},
		{Role: "assistant", Content: "Thread 1 absorbed into the main chat.", FloorContent: "Thread 1 absorbed into the main chat.", FloorMetadata: `{"source":"telegram_thread_absorb"}`, TurnIndex: 1},
	}
	closed, changed, err := store.RecordTelegramThreadAbsorb(1001, thread.ThreadID, "Thread 1 absorbed into the main chat.", mainSession, messages, time.Now().UTC())
	if err != nil {
		t.Fatalf("RecordTelegramThreadAbsorb() err = %v", err)
	}
	if !changed || closed.Status != TelegramThreadStatusClosed {
		t.Fatalf("closed = %#v changed=%v, want closed thread", closed, changed)
	}
	reloaded, err := store.Load(mainKey)
	if err != nil {
		t.Fatalf("Load(main after absorb) err = %v", err)
	}
	if len(reloaded.Messages) != 2 || !strings.Contains(reloaded.Messages[1].Content, "absorbed into the main chat") {
		t.Fatalf("main messages = %#v, want synthetic absorb turn", reloaded.Messages)
	}
}

func TestRecordTelegramThreadAbsorbRollsBackCloseWhenMainNoteFails(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "investigate child agents", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_absorb_message_insert
		BEFORE INSERT ON messages
		WHEN NEW.floor_metadata = '{"source":"telegram_thread_absorb"}'
		BEGIN
			SELECT RAISE(FAIL, 'forced absorb insert failure');
		END
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	mainKey := SessionKey{ChatID: 1001}
	mainSession, err := store.Load(mainKey)
	if err != nil {
		t.Fatalf("Load(main) err = %v", err)
	}
	mainSession.TurnCount = 1
	messages := []Message{
		{Role: "user", Content: "/absorb 1", TurnIndex: 1},
		{Role: "assistant", Content: "Thread 1 absorbed into the main chat.", FloorContent: "Thread 1 absorbed into the main chat.", FloorMetadata: `{"source":"telegram_thread_absorb"}`, TurnIndex: 1},
	}
	if _, _, err := store.RecordTelegramThreadAbsorb(1001, thread.ThreadID, "Thread 1 absorbed into the main chat.", mainSession, messages, time.Now().UTC()); err == nil {
		t.Fatal("RecordTelegramThreadAbsorb() err = nil, want insert failure after close attempt")
	}
	reloadedThread, ok, err := store.TelegramThread(1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("TelegramThread() err = %v", err)
	}
	if !ok || !reloadedThread.Open() {
		t.Fatalf("thread after failed absorb = %#v ok=%v, want still open", reloadedThread, ok)
	}
	reloadedMain, err := store.Load(mainKey)
	if err != nil {
		t.Fatalf("Load(main after failed absorb) err = %v", err)
	}
	if len(reloadedMain.Messages) != 0 {
		t.Fatalf("main messages = %#v, want no partial absorb note", reloadedMain.Messages)
	}
}

func TestTelegramThreadIDForReplyMessageUsesThreadLedgers(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "investigate child agents", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := SessionKey{ChatID: 1001, UserID: 0, Scope: TelegramThreadScopeRef(1001, thread.ThreadID)}
	if err := store.RecordOutbound(threadKey, 1, 9001, "message"); err != nil {
		t.Fatalf("RecordOutbound() err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9001); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(outbound) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}

	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    777,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   9002,
		SessionID:   SessionIDForKey(threadKey),
		InboundJSON: `{"Text":"side reply"}`,
		AcceptedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9002); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(ingress) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
	run, err := store.BeginTurnRun(threadKey, TurnRunKindInteractive, "thread progress")
	if err != nil {
		t.Fatalf("BeginTurnRun(thread) err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 9003); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage(thread) err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9003); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(progress) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
	if err := store.RecordTelegramCallbackMessageThread(1001, 9004, thread.ThreadID, "memory", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramCallbackMessageThread() err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9004); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(callback) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
	if err := store.ClearTelegramCallbackMessageThread(1001, 9004, "threads_list", time.Now().UTC()); err != nil {
		t.Fatalf("ClearTelegramCallbackMessageThread() err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9004); err != nil || ok || got != 0 {
		t.Fatalf("TelegramThreadIDForReplyMessage(cleared callback) = %d ok=%v err=%v, want no thread", got, ok, err)
	}
	if err := store.UpsertPendingDecision(PendingDecisionRecord{
		ID:                "thread-decision",
		Sequence:          10,
		SessionID:         SessionIDForKey(threadKey),
		ScopeKind:         string(ScopeKindTelegramThread),
		ScopeID:           "1001/1",
		Kind:              "interrupt",
		ChatID:            1001,
		SenderID:          2002,
		MessageID:         7001,
		ChoicesJSON:       "[]",
		DeliveryMessageID: 9005,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision(thread delivery) err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9005); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(pending decision) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    778,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   401,
		SessionID:   "telegram_dm:1001",
		InboundJSON: `{"Text":"/thread"}`,
		AcceptedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted(original thread command) err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 401); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(created) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1002, 9001); err != nil || ok || got != 0 {
		t.Fatalf("TelegramThreadIDForReplyMessage(other chat) = %d ok=%v err=%v, want no match", got, ok, err)
	}
}

func TestRecordTelegramThreadMessageEnsuresSessionAndReplyLedger(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	sessionID := SessionIDForKey(SessionKey{ChatID: 1001, Scope: TelegramThreadScopeRef(1001, thread.ThreadID)})
	if _, err := store.db.Exec(`DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		t.Fatalf("delete thread session fixture: %v", err)
	}

	if err := store.RecordTelegramThreadMessage(1001, thread.ThreadID, 9901, "thread_guide", "thread_guide", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramThreadMessage() err = %v", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatalf("query thread session count: %v", err)
	}
	if count != 1 {
		t.Fatalf("thread session count = %d, want repaired session", count)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9901); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(guide) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
}

func TestRebindTelegramIngressSessionPreservesRecoverableThreadInbound(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    77,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   3003,
		SessionID:   "telegram_dm:1001",
		InboundJSON: `{"Text":"/thread first"}`,
		AcceptedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if err := store.RebindTelegramIngressSession("telegram:primary", 77, "telegram_thread:1001:1", `{"Text":"first","TelegramThreadID":1}`, time.Now().UTC()); err != nil {
		t.Fatalf("RebindTelegramIngressSession() err = %v", err)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 1 || pending[0].SessionID != "telegram_thread:1001:1" || !strings.Contains(pending[0].InboundJSON, `"TelegramThreadID":1`) {
		t.Fatalf("pending = %#v, want rebound thread session and inbound JSON", pending)
	}
}

func coreTokenUsageZero() core.TokenUsage {
	return core.TokenUsage{}
}

func TestTelegramThreadDisplaySlotReusesClosedSlotAndArchivesName(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.Local)
	first, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "first task", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(first) err = %v", err)
	}
	second, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 302, 402, "second task", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(second) err = %v", err)
	}
	if first.DisplaySlot != 1 || second.DisplaySlot != 2 {
		t.Fatalf("display slots first=%d second=%d, want 1/2", first.DisplaySlot, second.DisplaySlot)
	}
	closed, changed, err := store.CloseTelegramThread(1001, second.ThreadID, "done", now)
	if err != nil || !changed {
		t.Fatalf("CloseTelegramThread() changed=%t err=%v", changed, err)
	}
	if closed.DisplaySlot != 0 || closed.ArchivedDisplayName != "2-2026-05-17" {
		t.Fatalf("closed = %#v, want slot cleared and archived display name", closed)
	}
	third, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 303, 403, "third task", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(third) err = %v", err)
	}
	if third.DisplaySlot != 2 {
		t.Fatalf("third display slot = %d, want reused slot 2", third.DisplaySlot)
	}
	closedAgain, changed, err := store.CloseTelegramThread(1001, third.ThreadID, "done again", now)
	if err != nil || !changed {
		t.Fatalf("CloseTelegramThread(third) changed=%t err=%v", changed, err)
	}
	if closedAgain.ArchivedDisplayName != "2-2026-05-17-1" {
		t.Fatalf("archived name = %q, want collision suffix", closedAgain.ArchivedDisplayName)
	}
}

func TestCloseTelegramThreadDropsPendingThreadIngress(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "haunted thread", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadSessionID := SessionIDForKey(SessionKey{ChatID: 1001, Scope: TelegramThreadScopeRef(1001, thread.ThreadID)})
	rows := []TelegramIngressUpdateRecord{
		{Surface: "telegram:primary", UpdateID: 701, UpdateKind: "message", ChatID: 1001, SenderID: 2002, MessageID: 501, SessionID: threadSessionID, Status: TelegramIngressUpdateAccepted, InboundJSON: `{"Text":"first","TelegramThreadID":1}`, AcceptedAt: now, UpdatedAt: now},
		{Surface: "telegram:primary", UpdateID: 702, UpdateKind: "message", ChatID: 1001, SenderID: 2002, MessageID: 502, SessionID: "telegram_dm:1001", Status: TelegramIngressUpdateQueued, InboundJSON: `{"Text":"second","TelegramThreadID": 1}`, AcceptedAt: now, QueuedAt: now, UpdatedAt: now},
		{Surface: "telegram:primary", UpdateID: 703, UpdateKind: "message", ChatID: 1001, SenderID: 2002, MessageID: 503, SessionID: "telegram_dm:1001", Status: TelegramIngressUpdateQueued, InboundJSON: `{"Text":"main"}`, AcceptedAt: now, QueuedAt: now, UpdatedAt: now},
	}
	for _, row := range rows {
		if _, err := store.RecordTelegramIngressAccepted(row); err != nil {
			t.Fatalf("RecordTelegramIngressAccepted(%d) err = %v", row.UpdateID, err)
		}
	}

	closed, changed, err := store.CloseTelegramThread(1001, thread.ThreadID, "done", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CloseTelegramThread() err = %v", err)
	}
	if !changed || !closed.ClosedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("closed=%#v changed=%v, want changed closed thread", closed, changed)
	}

	for _, updateID := range []int64{701, 702} {
		record, ok, err := store.TelegramIngressUpdate("telegram:primary", updateID)
		if err != nil || !ok {
			t.Fatalf("TelegramIngressUpdate(%d) ok=%v err=%v", updateID, ok, err)
		}
		if record.Status != TelegramIngressUpdateDropped || record.ErrorText != TelegramIngressDropReasonTelegramThreadClosed || record.CompletedAt.IsZero() {
			t.Fatalf("thread update %d = %#v, want dropped closed-thread ingress", updateID, record)
		}
	}
	mainRecord, ok, err := store.TelegramIngressUpdate("telegram:primary", 703)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(703) ok=%v err=%v", ok, err)
	}
	if mainRecord.Status != TelegramIngressUpdateQueued {
		t.Fatalf("main update status = %s, want still queued", mainRecord.Status)
	}
}

func TestRecordTelegramThreadAbsorbDropsPendingThreadIngress(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 21, 13, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "haunted thread", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadSessionID := SessionIDForKey(SessionKey{ChatID: 1001, Scope: TelegramThreadScopeRef(1001, thread.ThreadID)})
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    801,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   501,
		SessionID:   threadSessionID,
		Status:      TelegramIngressUpdateQueued,
		InboundJSON: `{"Text":"first","TelegramThreadID":1}`,
		AcceptedAt:  now,
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	mainKey := SessionKey{ChatID: 1001}
	mainSession, err := store.Load(mainKey)
	if err != nil {
		t.Fatalf("Load(main) err = %v", err)
	}
	messages := []Message{
		{Role: "user", Content: "/absorb 1", TurnIndex: 1},
		{Role: "assistant", Content: "Thread 1 absorbed into the main chat.", FloorContent: "Thread 1 absorbed into the main chat.", FloorMetadata: `{"source":"telegram_thread_absorb"}`, TurnIndex: 1},
	}
	if _, changed, err := store.RecordTelegramThreadAbsorb(1001, thread.ThreadID, "Thread 1 absorbed into the main chat.", mainSession, messages, now.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("RecordTelegramThreadAbsorb() changed=%v err=%v", changed, err)
	}
	record, ok, err := store.TelegramIngressUpdate("telegram:primary", 801)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(801) ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateDropped || record.ErrorText != TelegramIngressDropReasonTelegramThreadClosed || record.CompletedAt.IsZero() {
		t.Fatalf("absorbed-thread update = %#v, want dropped", record)
	}
}
