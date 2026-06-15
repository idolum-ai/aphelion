//go:build linux

package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestTelegramMediaThreadPickerSanitizesAndRequiresPendingStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	inbound := core.InboundMessage{
		ChatID:    44,
		SenderID:  12,
		MessageID: 700,
		Text:      "caption",
		Raw:       json.RawMessage(`{"message_id":700,"private":"raw-update"}`),
		Artifacts: []core.Artifact{{
			ID:         "telegram:voice:file-id",
			Channel:    "telegram",
			RemoteID:   "file-id",
			SourceType: "voice",
			Kind:       "audio",
			Subtype:    "voice_note",
			Data:       []byte("voice bytes that should not be duplicated in picker state"),
		}},
	}
	if err := store.RecordTelegramMediaThreadPicker(44, 900, inbound, time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramMediaThreadPicker() err = %v", err)
	}

	rec, ok, err := store.TelegramMediaThreadPicker(44, 900)
	if err != nil {
		t.Fatalf("TelegramMediaThreadPicker() err = %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want pending picker")
	}
	if len(rec.Inbound.Raw) != 0 {
		t.Fatalf("raw len = %d, want sanitized raw payload", len(rec.Inbound.Raw))
	}
	if len(rec.Inbound.Artifacts) != 1 || len(rec.Inbound.Artifacts[0].Data) != 0 || rec.Inbound.Artifacts[0].RemoteID != "file-id" {
		t.Fatalf("artifact after sanitize = %#v, want metadata without data bytes", rec.Inbound.Artifacts)
	}

	if err := store.MarkTelegramMediaThreadPickerStatus(44, 900, "routed", time.Now().UTC()); err != nil {
		t.Fatalf("MarkTelegramMediaThreadPickerStatus() err = %v", err)
	}
	if _, ok, err := store.TelegramMediaThreadPicker(44, 900); err != nil {
		t.Fatalf("TelegramMediaThreadPicker() after routed err = %v", err)
	} else if ok {
		t.Fatal("ok = true after routed status, want inactive picker unavailable")
	}
}

func TestRecordTelegramMediaThreadPickerParksSourceIngressUntilRouted(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 14, 20, 25, 37, 0, time.UTC)
	inbound := core.InboundMessage{
		ChatID:          7001,
		SenderID:        42,
		MessageID:       501,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 9001,
		Artifacts: []core.Artifact{{
			ID:         "telegram:document:file-id",
			Channel:    "telegram",
			RemoteID:   "file-id",
			SourceType: "document",
			Kind:       "document",
		}},
	}
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:     inbound.IngressSurface,
		UpdateID:    inbound.IngressUpdateID,
		UpdateKind:  "message",
		ChatID:      inbound.ChatID,
		SenderID:    inbound.SenderID,
		MessageID:   inbound.MessageID,
		SessionID:   "telegram_dm:7001",
		Status:      TelegramIngressUpdateAccepted,
		InboundJSON: `{"MessageID":501}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if err := store.RecordTelegramMediaThreadPicker(inbound.ChatID, 502, inbound, now.Add(time.Second)); err != nil {
		t.Fatalf("RecordTelegramMediaThreadPicker() err = %v", err)
	}
	record, ok, err := store.TelegramIngressUpdate(inbound.IngressSurface, inbound.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateParked || record.ErrorText != TelegramIngressParkReasonMediaThreadPicker || !record.CompletedAt.IsZero() {
		t.Fatalf("parked ingress = %#v, want parked non-terminal source", record)
	}
	pending, err := store.PendingTelegramIngressUpdates(inbound.IngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending parked ingress = %#v, want parked source excluded from replay", pending)
	}
	if err := store.MarkTelegramIngressHandled(inbound.IngressSurface, inbound.IngressUpdateID, now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressHandled() err = %v", err)
	}
	record, ok, err = store.TelegramIngressUpdate(inbound.IngressSurface, inbound.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(after handled) ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateParked || !record.CompletedAt.IsZero() {
		t.Fatalf("handled parked ingress = %#v, want still parked", record)
	}
	if result, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    inbound.IngressSurface,
		UpdateID:   inbound.IngressUpdateID,
		UpdateKind: "message",
		ChatID:     inbound.ChatID,
		SenderID:   inbound.SenderID,
		MessageID:  inbound.MessageID,
		SessionID:  "telegram_dm:7001",
		Status:     TelegramIngressUpdateAccepted,
		AcceptedAt: now.Add(3 * time.Second),
		UpdatedAt:  now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted(redelivery) err = %v", err)
	} else if result.Dispatch || result.Record.Status != TelegramIngressUpdateParked {
		t.Fatalf("redelivery result = %#v, want parked non-dispatchable", result)
	}
	if result, err := store.MarkTelegramIngressQueued(inbound.IngressSurface, inbound.IngressUpdateID, now.Add(4*time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressQueued() err = %v", err)
	} else if !result.Dispatch || !result.Queued || result.Record.Status != TelegramIngressUpdateQueued {
		t.Fatalf("MarkTelegramIngressQueued() result = %#v, want queued dispatchable source", result)
	}
	pending, err = store.PendingTelegramIngressUpdates(inbound.IngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates(after queue) err = %v", err)
	}
	if len(pending) != 1 || pending[0].UpdateID != inbound.IngressUpdateID || pending[0].Status != TelegramIngressUpdateQueued {
		t.Fatalf("pending routed ingress = %#v, want queued source", pending)
	}
}

func TestExpireTelegramMediaThreadPickersDropsStillParkedSourceIngress(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	created := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	inbound := core.InboundMessage{
		ChatID:          44,
		MessageID:       700,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 990,
		Artifacts:       []core.Artifact{{ID: "telegram:document:file-id"}},
	}
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    inbound.IngressSurface,
		UpdateID:   inbound.IngressUpdateID,
		UpdateKind: "message",
		ChatID:     inbound.ChatID,
		MessageID:  inbound.MessageID,
		SessionID:  "telegram_dm:44",
		Status:     TelegramIngressUpdateAccepted,
		AcceptedAt: created,
		UpdatedAt:  created,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if err := store.RecordTelegramMediaThreadPicker(44, 901, inbound, created); err != nil {
		t.Fatalf("RecordTelegramMediaThreadPicker() err = %v", err)
	}
	now := created.Add(TelegramMediaThreadPickerPendingTTL + time.Minute)
	if n, err := store.ExpireTelegramMediaThreadPickers(now.Add(-TelegramMediaThreadPickerPendingTTL), now); err != nil {
		t.Fatalf("ExpireTelegramMediaThreadPickers() err = %v", err)
	} else if n != 1 {
		t.Fatalf("expired rows = %d, want 1", n)
	}
	record, ok, err := store.TelegramIngressUpdate(inbound.IngressSurface, inbound.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateDropped || record.ErrorText != TelegramIngressDropReasonMediaThreadPickerTTL || record.CompletedAt.IsZero() {
		t.Fatalf("expired source ingress = %#v, want dropped parked source", record)
	}
}

func TestExpireTelegramMediaThreadPickersDropsParkedSourceWithCorruptLegacyInbound(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`DROP TABLE telegram_media_thread_pickers`); err != nil {
		t.Fatalf("drop media picker table err = %v", err)
	}
	if _, err := store.db.Exec(`CREATE TABLE telegram_media_thread_pickers (
		chat_id INTEGER NOT NULL,
		picker_message_id INTEGER NOT NULL,
		source_message_id INTEGER NOT NULL DEFAULT 0,
		inbound_json TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'routed', 'cleared')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY(chat_id, picker_message_id)
	)`); err != nil {
		t.Fatalf("create legacy media picker table err = %v", err)
	}

	created := time.Date(2026, 6, 14, 8, 0, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(TelegramIngressUpdateRecord{
		Surface:    "telegram:primary",
		UpdateID:   991,
		UpdateKind: "message",
		ChatID:     44,
		MessageID:  701,
		SessionID:  "telegram_dm:44",
		Status:     TelegramIngressUpdateAccepted,
		AcceptedAt: created,
		UpdatedAt:  created,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if _, err := store.MarkTelegramIngressParked("telegram:primary", 991, TelegramIngressParkReasonMediaThreadPicker, created.Add(time.Second)); err != nil {
		t.Fatalf("MarkTelegramIngressParked() err = %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO telegram_media_thread_pickers(chat_id, picker_message_id, source_message_id, inbound_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
		int64(44), int64(902), int64(701), `{"not valid json"`, created.UTC().Format(time.RFC3339Nano), created.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert corrupt legacy picker err = %v", err)
	}
	now := created.Add(TelegramMediaThreadPickerPendingTTL + time.Minute)
	if n, err := store.ExpireTelegramMediaThreadPickers(now.Add(-TelegramMediaThreadPickerPendingTTL), now); err != nil {
		t.Fatalf("ExpireTelegramMediaThreadPickers() err = %v", err)
	} else if n != 1 {
		t.Fatalf("expired rows = %d, want 1", n)
	}
	record, ok, err := store.TelegramIngressUpdate("telegram:primary", 991)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%v err=%v", ok, err)
	}
	if record.Status != TelegramIngressUpdateDropped || record.ErrorText != TelegramIngressDropReasonMediaThreadPickerTTL || record.CompletedAt.IsZero() {
		t.Fatalf("expired corrupt legacy source ingress = %#v, want dropped parked source", record)
	}
}

func TestExpireTelegramMediaThreadPickersOnlyExpiresStalePendingRows(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	old := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	fresh := old.Add(2 * time.Hour)
	inbound := core.InboundMessage{ChatID: 44, MessageID: 700}
	if err := store.RecordTelegramMediaThreadPicker(44, 901, inbound, old); err != nil {
		t.Fatalf("record old pending: %v", err)
	}
	if err := store.RecordTelegramMediaThreadPicker(44, 902, inbound, fresh); err != nil {
		t.Fatalf("record fresh pending: %v", err)
	}
	if err := store.RecordTelegramMediaThreadPicker(44, 903, inbound, old); err != nil {
		t.Fatalf("record old routed: %v", err)
	}
	if err := store.MarkTelegramMediaThreadPickerStatus(44, 903, "routed", old.Add(time.Minute)); err != nil {
		t.Fatalf("mark routed: %v", err)
	}

	expired, err := store.ExpireTelegramMediaThreadPickers(fresh.Add(-time.Minute), fresh.Add(time.Minute))
	if err != nil {
		t.Fatalf("ExpireTelegramMediaThreadPickers() err = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	if _, ok, err := store.TelegramMediaThreadPicker(44, 901); err != nil {
		t.Fatalf("old picker err = %v", err)
	} else if ok {
		t.Fatal("old picker still available after expiry")
	}
	if _, ok, err := store.TelegramMediaThreadPicker(44, 902); err != nil {
		t.Fatalf("fresh picker err = %v", err)
	} else if !ok {
		t.Fatal("fresh picker unavailable, want still pending")
	}
}

func TestExpireTelegramMediaThreadPickersUsesLegacyCompatibleStatus(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	created := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC).Add(-TelegramMediaThreadPickerPendingTTL - time.Minute)
	inbound := core.InboundMessage{ChatID: 44, MessageID: 700}
	if err := store.RecordTelegramMediaThreadPicker(44, 901, inbound, created); err != nil {
		t.Fatalf("RecordTelegramMediaThreadPicker() err = %v", err)
	}
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if n, err := store.ExpireTelegramMediaThreadPickers(now.Add(-TelegramMediaThreadPickerPendingTTL), now); err != nil {
		t.Fatalf("ExpireTelegramMediaThreadPickers() err = %v", err)
	} else if n != 1 {
		t.Fatalf("expired rows = %d, want 1", n)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM telegram_media_thread_pickers WHERE chat_id = ? AND picker_message_id = ?`, int64(44), int64(901)).Scan(&status); err != nil {
		t.Fatalf("select picker status err = %v", err)
	}
	if status != "cleared" {
		t.Fatalf("expired picker status = %q, want legacy-compatible cleared", status)
	}
}

func TestExpireTelegramMediaThreadPickersWorksWithLegacyCheckConstraint(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`DROP TABLE telegram_media_thread_pickers`); err != nil {
		t.Fatalf("drop media picker table err = %v", err)
	}
	if _, err := store.db.Exec(`CREATE TABLE telegram_media_thread_pickers (
		chat_id INTEGER NOT NULL,
		picker_message_id INTEGER NOT NULL,
		source_message_id INTEGER NOT NULL DEFAULT 0,
		inbound_json TEXT NOT NULL DEFAULT '{}',
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'routed', 'cleared')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY(chat_id, picker_message_id)
	)`); err != nil {
		t.Fatalf("create legacy media picker table err = %v", err)
	}

	created := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	inbound := sanitizeTelegramMediaPickerInbound(core.InboundMessage{ChatID: 44, MessageID: 700})
	raw, err := json.Marshal(inbound)
	if err != nil {
		t.Fatalf("marshal inbound err = %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO telegram_media_thread_pickers(chat_id, picker_message_id, source_message_id, inbound_json, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`, int64(44), int64(901), int64(700), string(raw), created.UTC().Format(time.RFC3339Nano), created.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert legacy picker err = %v", err)
	}
	now := created.Add(TelegramMediaThreadPickerPendingTTL + time.Minute)
	if n, err := store.ExpireTelegramMediaThreadPickers(now.Add(-TelegramMediaThreadPickerPendingTTL), now); err != nil {
		t.Fatalf("ExpireTelegramMediaThreadPickers() err = %v", err)
	} else if n != 1 {
		t.Fatalf("expired rows = %d, want 1", n)
	}
	var status string
	if err := store.db.QueryRow(`SELECT status FROM telegram_media_thread_pickers WHERE chat_id = ? AND picker_message_id = ?`, int64(44), int64(901)).Scan(&status); err != nil {
		t.Fatalf("select picker status err = %v", err)
	}
	if status != "cleared" {
		t.Fatalf("legacy picker status = %q, want cleared", status)
	}
}

func TestRecordTelegramMediaThreadPickerExpiresRowsPastTTL(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-TelegramMediaThreadPickerPendingTTL - time.Minute)
	inbound := core.InboundMessage{ChatID: 44, MessageID: 700}
	if err := store.RecordTelegramMediaThreadPicker(44, 901, inbound, stale); err != nil {
		t.Fatalf("record stale pending: %v", err)
	}
	if err := store.RecordTelegramMediaThreadPicker(44, 902, inbound, now); err != nil {
		t.Fatalf("record fresh pending: %v", err)
	}
	if _, ok, err := store.TelegramMediaThreadPicker(44, 901); err != nil {
		t.Fatalf("stale picker err = %v", err)
	} else if ok {
		t.Fatal("stale picker still available after record-time TTL cleanup")
	}
	if _, ok, err := store.TelegramMediaThreadPicker(44, 902); err != nil {
		t.Fatalf("fresh picker err = %v", err)
	} else if !ok {
		t.Fatal("fresh picker unavailable after record-time TTL cleanup")
	}
}
