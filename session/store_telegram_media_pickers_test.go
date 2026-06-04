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
