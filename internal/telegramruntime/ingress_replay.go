//go:build linux

package telegramruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type telegramIngressReplayLogger func(format string, args ...any)

func replayPendingTelegramIngress(
	ctx context.Context,
	store *session.SQLiteStore,
	checkpoint telegram.PollerCheckpoint,
	handler telegram.UpdateHandler,
	surface string,
	limit int,
	logger telegramIngressReplayLogger,
) error {
	if store == nil || checkpoint == nil || handler == nil {
		return nil
	}
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = telegramPrimaryIngressSurface
	}
	pending, err := store.PendingTelegramIngressUpdates(surface, limit)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	if logger != nil {
		logger("INFO replaying %d pending telegram ingress update(s) surface=%s", len(pending), surface)
	}
	for _, record := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg, err := telegramIngressReplayMessage(record)
		if err != nil {
			if checkpointErr := checkpoint.RecordFailure(ctx, telegramIngressFailureFromUpdateRecord(record, err)); checkpointErr != nil {
				return checkpointErr
			}
			if err := checkpoint.SaveNextUpdateID(ctx, record.UpdateID+1); err != nil {
				return err
			}
			continue
		}
		if err := handler(ctx, msg); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if checkpointErr := checkpoint.RecordFailure(ctx, telegramIngressFailureFromUpdateRecord(record, err)); checkpointErr != nil {
				return checkpointErr
			}
			if err := checkpoint.SaveNextUpdateID(ctx, record.UpdateID+1); err != nil {
				return err
			}
			continue
		}
		if err := checkpoint.RecordHandled(ctx, record.UpdateID); err != nil {
			return err
		}
		if err := checkpoint.SaveNextUpdateID(ctx, record.UpdateID+1); err != nil {
			return err
		}
	}
	return nil
}

func telegramIngressReplayMessage(record session.TelegramIngressUpdateRecord) (core.InboundMessage, error) {
	var msg core.InboundMessage
	if strings.TrimSpace(record.InboundJSON) == "" {
		return core.InboundMessage{}, fmt.Errorf("pending telegram ingress update has no inbound payload")
	}
	if err := json.Unmarshal([]byte(record.InboundJSON), &msg); err != nil {
		return core.InboundMessage{}, fmt.Errorf("decode pending telegram ingress inbound payload: %w", err)
	}
	msg.IngressSurface = strings.TrimSpace(record.Surface)
	msg.IngressUpdateID = record.UpdateID
	if msg.ChatID == 0 {
		msg.ChatID = record.ChatID
	}
	if msg.SenderID == 0 {
		msg.SenderID = record.SenderID
	}
	if msg.MessageID == 0 {
		msg.MessageID = record.MessageID
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = record.AcceptedAt
	}
	return msg, nil
}

func telegramIngressFailureFromUpdateRecord(record session.TelegramIngressUpdateRecord, err error) telegram.PollerFailure {
	failure := telegram.PollerFailure{
		UpdateID:   record.UpdateID,
		UpdateKind: strings.TrimSpace(record.UpdateKind),
		ChatID:     record.ChatID,
		SenderID:   record.SenderID,
		MessageID:  record.MessageID,
		CreatedAt:  time.Now().UTC(),
	}
	if err != nil {
		failure.ErrorText = err.Error()
	}
	failure.Payload = record.PayloadJSON
	return failure
}
