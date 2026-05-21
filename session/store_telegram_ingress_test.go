//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestTelegramIngressLedgerPersistsOffsetsAndFailures(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if next, err := store.TelegramIngressNextUpdateID("telegram:primary"); err != nil || next != 0 {
		t.Fatalf("initial TelegramIngressNextUpdateID() = %d, err=%v, want zero nil", next, err)
	}
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTelegramIngressNextUpdateID("telegram:primary", 44, now); err != nil {
		t.Fatalf("SaveTelegramIngressNextUpdateID(44) err = %v", err)
	}
	if err := store.SaveTelegramIngressNextUpdateID("telegram:primary", 43, now.Add(time.Minute)); err != nil {
		t.Fatalf("SaveTelegramIngressNextUpdateID(43) err = %v", err)
	}
	if next, err := store.TelegramIngressNextUpdateID("telegram:primary"); err != nil || next != 44 {
		t.Fatalf("TelegramIngressNextUpdateID() = %d, err=%v, want 44 nil", next, err)
	}
	if err := store.RecordTelegramIngressFailure(TelegramIngressFailureRecord{
		Surface:    "telegram:primary",
		UpdateID:   42,
		UpdateKind: "message",
		ChatID:     7001,
		SenderID:   9,
		MessageID:  100,
		ErrorText:  "handler failed",
		Payload:    `{"update_id":42}`,
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressFailure() err = %v", err)
	}
	failures, err := store.RecentTelegramIngressFailures(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressFailures() err = %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %#v, want one", failures)
	}
	if failures[0].UpdateID != 42 || failures[0].UpdateKind != "message" || failures[0].ChatID != 7001 {
		t.Fatalf("failure = %#v, want stored refs", failures[0])
	}
}

func TestTelegramIngressLedgerTracksAcceptedQueuedAndHandledUpdates(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	if result, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    77,
		UpdateKind:  "message",
		ChatID:      7001,
		SenderID:    9,
		MessageID:   200,
		SessionID:   "telegram_dm:7001",
		Status:      TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":7001,"Text":"hello"}`,
		PayloadJSON: `{"update_id":77}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	} else if !result.Dispatch || result.Terminal {
		t.Fatalf("RecordTelegramIngressAccepted() result = %#v, want dispatchable accepted", result)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 1 || pending[0].Status != TelegramIngressUpdateAccepted || pending[0].InboundJSON == "" {
		t.Fatalf("pending accepted = %#v, want accepted update", pending)
	}
	if result, err := store.MarkTelegramIngressQueued("telegram:primary", 77, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressQueued() err = %v", err)
	} else if !result.Dispatch || !result.Queued || result.Terminal {
		t.Fatalf("MarkTelegramIngressQueued() result = %#v, want dispatchable queued", result)
	}
	pending, err = store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates(queued) err = %v", err)
	}
	if len(pending) != 1 || pending[0].Status != TelegramIngressUpdateQueued || pending[0].QueuedAt.IsZero() {
		t.Fatalf("pending queued = %#v, want queued update", pending)
	}
	if err := store.MarkTelegramIngressHandled("telegram:primary", 77, now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressHandled(queued) err = %v", err)
	}
	pending, err = store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates(after queued handled) err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("queued update was completed by handled marker; pending=%#v, want still pending", pending)
	}
	if err := store.MarkTelegramIngressCompleted("telegram:primary", 77, 12, TelegramIngressUpdateCompleted, "", now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressCompleted() err = %v", err)
	}
	pending, err = store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates(after completion) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after completion = %#v, want none", pending)
	}
	recent, err := store.RecentTelegramIngressUpdates(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	if len(recent) != 1 || recent[0].Status != TelegramIngressUpdateCompleted || recent[0].TurnRunID != 12 {
		t.Fatalf("recent = %#v, want completed turn run 12", recent)
	}
}

func TestTelegramIngressHandledCompletesOnlyAcceptedUpdates(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 10, 30, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   88,
		UpdateKind: "message",
		ChatID:     7002,
		MessageID:  201,
		SessionID:  "telegram_dm:7002",
		Status:     TelegramIngressUpdateAccepted,
		AcceptedAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if err := store.MarkTelegramIngressHandled("telegram:primary", 88, now.Add(time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressHandled() err = %v", err)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want accepted command-like update completed", pending)
	}
}

func TestTelegramIngressAcceptedKeepsFullReplayPayload(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	longText := strings.Repeat("x", 25000)
	inbound := `{"ChatID":7004,"Text":"` + longText + `"}`
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    89,
		UpdateKind:  "message",
		ChatID:      7004,
		SessionID:   "telegram_dm:7004",
		Status:      TelegramIngressUpdateAccepted,
		InboundJSON: inbound,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 1 || pending[0].InboundJSON != inbound {
		gotLen := 0
		if len(pending) > 0 {
			gotLen = len(pending[0].InboundJSON)
		}
		t.Fatalf("stored inbound len=%d, want full len=%d", gotLen, len(inbound))
	}
}

func TestBeginTurnRunForTelegramIngressMarksAcceptedUpdateRunning(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 10, 45, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   99,
		UpdateKind: "message",
		ChatID:     7003,
		MessageID:  202,
		SessionID:  "telegram_dm:7003",
		Status:     TelegramIngressUpdateQueued,
		AcceptedAt: now,
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	run, err := store.BeginTurnRunForTelegramIngress(SessionKey{ChatID: 7003}, TurnRunKindInteractive, "hello", "telegram:primary", 99)
	if err != nil {
		t.Fatalf("BeginTurnRunForTelegramIngress() err = %v", err)
	}
	recent, err := store.RecentTelegramIngressUpdates(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	if len(recent) != 1 || recent[0].Status != TelegramIngressUpdateRunning || recent[0].TurnRunID != run.ID || recent[0].StartedAt.IsZero() {
		t.Fatalf("recent = %#v, want running update tied to turn run %d", recent, run.ID)
	}
}

func TestInterruptRunningTurnRunsMarksRunningTelegramIngressInterrupted(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   100,
		UpdateKind: "message",
		ChatID:     7005,
		MessageID:  203,
		SessionID:  "telegram_dm:7005",
		Status:     TelegramIngressUpdateQueued,
		AcceptedAt: now,
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	run, err := store.BeginTurnRunForTelegramIngress(SessionKey{ChatID: 7005}, TurnRunKindInteractive, "crash during turn", "telegram:primary", 100)
	if err != nil {
		t.Fatalf("BeginTurnRunForTelegramIngress() err = %v", err)
	}
	interrupted, err := store.InterruptRunningTurnRuns()
	if err != nil {
		t.Fatalf("InterruptRunningTurnRuns() err = %v", err)
	}
	if len(interrupted) != 1 || interrupted[0].ID != run.ID {
		t.Fatalf("interrupted = %#v, want turn run %d", interrupted, run.ID)
	}
	recent, err := store.RecentTelegramIngressUpdates(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	if len(recent) != 1 || recent[0].Status != TelegramIngressUpdateInterrupted || recent[0].TurnRunID != run.ID || recent[0].CompletedAt.IsZero() {
		t.Fatalf("recent = %#v, want interrupted ingress tied to turn run %d", recent, run.ID)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want interrupted ingress excluded from replay", pending)
	}
}

func TestReconcileRunningTelegramIngressWithTerminalTurnRuns(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 15, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   101,
		UpdateKind: "message",
		ChatID:     7005,
		MessageID:  204,
		SessionID:  "telegram_dm:7005",
		Status:     TelegramIngressUpdateQueued,
		AcceptedAt: now,
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	run, err := store.BeginTurnRunForTelegramIngress(SessionKey{ChatID: 7005}, TurnRunKindInteractive, "finished before ingress terminal", "telegram:primary", 101)
	if err != nil {
		t.Fatalf("BeginTurnRunForTelegramIngress() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusFailed, "tool failed"); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	reconciled, err := store.ReconcileRunningTelegramIngressWithTerminalTurnRuns()
	if err != nil {
		t.Fatalf("ReconcileRunningTelegramIngressWithTerminalTurnRuns() err = %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled = %d, want 1", reconciled)
	}
	recent, err := store.RecentTelegramIngressUpdates(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	if len(recent) != 1 || recent[0].Status != TelegramIngressUpdateFailed || recent[0].ErrorText != "tool failed" || recent[0].CompletedAt.IsZero() {
		t.Fatalf("recent = %#v, want failed ingress reconciled from terminal turn run", recent)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want reconciled ingress excluded from replay", pending)
	}
}

func TestTelegramIngressTerminalUpsertCreatesTerminalRows(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 30, 0, 0, time.UTC)
	rows := []TelegramIngressUpdateRecord{
		{Surface: "telegram:primary", UpdateID: 201, UpdateKind: "callback_query", ChatID: 7006, SenderID: 31, MessageID: 300, Status: TelegramIngressUpdateCompleted, ErrorText: "callback_handled", PayloadJSON: `{"update_id":201}`, CompletedAt: now},
		{Surface: "telegram:primary", UpdateID: 202, UpdateKind: "callback_query", ChatID: 7006, SenderID: 32, MessageID: 301, Status: TelegramIngressUpdateFailed, ErrorText: "callback failed", PayloadJSON: `{"update_id":202}`, CompletedAt: now.Add(time.Second)},
		{Surface: "telegram:primary", UpdateID: 203, UpdateKind: "message", ChatID: 7006, SenderID: 33, MessageID: 302, Status: TelegramIngressUpdateSkipped, ErrorText: "unresolved_message_principal", PayloadJSON: `{"update_id":203}`, CompletedAt: now.Add(2 * time.Second)},
	}
	for _, row := range rows {
		if err := store.RecordTelegramIngressTerminal(row); err != nil {
			t.Fatalf("RecordTelegramIngressTerminal(%d) err = %v", row.UpdateID, err)
		}
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want no terminal rows replayed", pending)
	}
	recent, err := store.RecentTelegramIngressUpdates(10)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	byID := make(map[int64]TelegramIngressUpdateRecord, len(recent))
	for _, row := range recent {
		byID[row.UpdateID] = row
	}
	for _, want := range rows {
		got, ok := byID[want.UpdateID]
		if !ok {
			t.Fatalf("recent missing update %d in %#v", want.UpdateID, recent)
		}
		if got.Status != want.Status || got.UpdateKind != want.UpdateKind || got.ErrorText != want.ErrorText || got.PayloadJSON == "" || got.CompletedAt.IsZero() {
			t.Fatalf("terminal update %d = %#v, want status=%s kind=%s reason=%q payload and completed_at", want.UpdateID, got, want.Status, want.UpdateKind, want.ErrorText)
		}
	}
}

func TestTelegramIngressAcceptedAndQueuedTransitionsDoNotRedispatchTerminalRows(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 45, 0, 0, time.UTC)
	if err := store.RecordTelegramIngressTerminal(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    301,
		UpdateKind:  "message",
		ChatID:      7008,
		SenderID:    41,
		MessageID:   401,
		Status:      TelegramIngressUpdateCompleted,
		PayloadJSON: `{"update_id":301}`,
		CompletedAt: now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressTerminal() err = %v", err)
	}
	result, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    301,
		UpdateKind:  "message",
		ChatID:      7008,
		SenderID:    41,
		MessageID:   401,
		SessionID:   "telegram_dm:7008",
		Status:      TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":7008,"Text":"redelivered"}`,
		AcceptedAt:  now.Add(time.Second),
		UpdatedAt:   now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("RecordTelegramIngressAccepted(redelivery) err = %v", err)
	}
	if result.Dispatch || !result.Terminal || result.Record.Status != TelegramIngressUpdateCompleted {
		t.Fatalf("redelivery result = %#v, want terminal non-dispatchable completed row", result)
	}
	if strings.Contains(result.Record.InboundJSON, "redelivered") {
		t.Fatalf("terminal inbound_json = %q, want accepted redelivery payload ignored", result.Record.InboundJSON)
	}
	result, err = store.MarkTelegramIngressQueued("telegram:primary", 301, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("MarkTelegramIngressQueued(terminal) err = %v", err)
	}
	if result.Dispatch || !result.Terminal || !result.Record.QueuedAt.IsZero() {
		t.Fatalf("queued terminal result = %#v, want terminal row without queued_at mutation", result)
	}
	if !result.Record.UpdatedAt.Equal(now) {
		t.Fatalf("terminal updated_at = %s, want unchanged %s", result.Record.UpdatedAt, now)
	}
}

func TestTelegramIngressDropTerminalizesOnlyDispatchableRows(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	dispatchable := []struct {
		updateID int64
		status   TelegramIngressUpdateStatus
	}{
		{401, TelegramIngressUpdateAccepted},
		{402, TelegramIngressUpdateQueued},
	}
	for _, row := range dispatchable {
		if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
			Surface:    "telegram:primary",
			UpdateID:   row.updateID,
			UpdateKind: "message",
			ChatID:     7010,
			MessageID:  row.updateID,
			SessionID:  "telegram_dm:7010",
			Status:     row.status,
			AcceptedAt: now,
			QueuedAt:   now,
			UpdatedAt:  now,
		}); err != nil {
			t.Fatalf("RecordTelegramIngressAccepted(%d) err = %v", row.updateID, err)
		}
	}
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   403,
		UpdateKind: "message",
		ChatID:     7010,
		MessageID:  403,
		SessionID:  "telegram_dm:7010",
		Status:     TelegramIngressUpdateQueued,
		AcceptedAt: now,
		QueuedAt:   now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted(running seed) err = %v", err)
	}
	run, err := store.BeginTurnRunForTelegramIngress(SessionKey{ChatID: 7010}, TurnRunKindInteractive, "running", "telegram:primary", 403)
	if err != nil {
		t.Fatalf("BeginTurnRunForTelegramIngress() err = %v", err)
	}
	terminalRows := []struct {
		updateID int64
		status   TelegramIngressUpdateStatus
	}{
		{404, TelegramIngressUpdateCompleted},
		{405, TelegramIngressUpdateFailed},
		{406, TelegramIngressUpdateInterrupted},
		{407, TelegramIngressUpdateSkipped},
	}
	for _, row := range terminalRows {
		if err := store.RecordTelegramIngressTerminal(TelegramIngressUpdateRecord{
			Surface:     "telegram:primary",
			UpdateID:    row.updateID,
			UpdateKind:  "message",
			ChatID:      7010,
			MessageID:   row.updateID,
			Status:      row.status,
			ErrorText:   "terminal",
			CompletedAt: now,
		}); err != nil {
			t.Fatalf("RecordTelegramIngressTerminal(%d) err = %v", row.updateID, err)
		}
	}

	for updateID := int64(401); updateID <= 407; updateID++ {
		if _, err := store.MarkTelegramIngressDroppedIfDispatchable("telegram:primary", updateID, "operator_session_stop", now.Add(time.Minute)); err != nil {
			t.Fatalf("MarkTelegramIngressDroppedIfDispatchable(%d) err = %v", updateID, err)
		}
	}

	wants := map[int64]TelegramIngressUpdateStatus{
		401: TelegramIngressUpdateDropped,
		402: TelegramIngressUpdateDropped,
		403: TelegramIngressUpdateRunning,
		404: TelegramIngressUpdateCompleted,
		405: TelegramIngressUpdateFailed,
		406: TelegramIngressUpdateInterrupted,
		407: TelegramIngressUpdateSkipped,
	}
	for updateID, wantStatus := range wants {
		record, ok, err := store.TelegramIngressUpdate("telegram:primary", updateID)
		if err != nil || !ok {
			t.Fatalf("TelegramIngressUpdate(%d) ok=%v err=%v", updateID, ok, err)
		}
		if record.Status != wantStatus {
			t.Fatalf("update %d status = %s, want %s", updateID, record.Status, wantStatus)
		}
		if wantStatus == TelegramIngressUpdateDropped && (record.CompletedAt.IsZero() || record.ErrorText != "operator_session_stop") {
			t.Fatalf("dropped update %d = %#v, want completed_at and reason", updateID, record)
		}
		if updateID == 403 && record.TurnRunID != run.ID {
			t.Fatalf("running update turn id = %d, want %d", record.TurnRunID, run.ID)
		}
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want dropped/running/terminal rows excluded from replay", pending)
	}
}

func TestBeginTurnRunForTelegramIngressDropsClosedThreadIngress(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 21, 14, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(8001, 9002, 301, 401, "queued stale work", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, _, err := store.CloseTelegramThread(8001, thread.ThreadID, "done", now.Add(time.Minute)); err != nil {
		t.Fatalf("CloseTelegramThread() err = %v", err)
	}
	threadKey := SessionKey{ChatID: 8001, Scope: TelegramThreadScopeRef(8001, thread.ThreadID)}
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    901,
		UpdateKind:  "message",
		ChatID:      8001,
		SenderID:    9002,
		MessageID:   501,
		SessionID:   SessionIDForKey(threadKey),
		Status:      TelegramIngressUpdateQueued,
		InboundJSON: `{"Text":"stale","TelegramThreadID":1}`,
		AcceptedAt:  now,
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	if _, err := store.BeginTurnRunForTelegramIngress(threadKey, TurnRunKindInteractive, "stale", "telegram:primary", 901); err == nil || !strings.Contains(err.Error(), TelegramIngressDropReasonTelegramThreadClosed) {
		t.Fatalf("BeginTurnRunForTelegramIngress() err = %v, want closed-thread drop", err)
	}
	record, ok, err := store.TelegramIngressUpdate("telegram:primary", 901)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(901) ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateDropped || record.ErrorText != TelegramIngressDropReasonTelegramThreadClosed {
		t.Fatalf("record = %#v, want dropped closed-thread ingress", record)
	}
}
