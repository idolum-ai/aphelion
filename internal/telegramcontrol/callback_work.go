//go:build linux

package telegramcontrol

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func recordTelegramCallbackWorkAccepted(store *session.SQLiteStore, msg core.InboundMessage, updateKind string) error {
	if store == nil || strings.TrimSpace(msg.IngressSurface) == "" || msg.IngressUpdateID <= 0 {
		return nil
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	now := time.Now().UTC()
	_, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     msg.IngressSurface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  strings.TrimSpace(updateKind),
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: encoded,
		AcceptedAt:  now,
		UpdatedAt:   now,
	})
	return err
}

func ensureTelegramCallbackWorkQueued(store *session.SQLiteStore, msg core.InboundMessage, updateKind string) (bool, error) {
	surface := strings.TrimSpace(msg.IngressSurface)
	if store == nil || surface == "" || msg.IngressUpdateID <= 0 {
		return true, nil
	}
	now := time.Now().UTC()
	result, err := store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, now)
	if err != nil {
		return false, err
	}
	if result.Found {
		return result.Dispatch, nil
	}
	if err := recordTelegramCallbackWorkAccepted(store, msg, updateKind); err != nil {
		return false, err
	}
	result, err = store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, now)
	if err != nil {
		return false, err
	}
	return result.Dispatch, nil
}
