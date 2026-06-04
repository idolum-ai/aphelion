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
