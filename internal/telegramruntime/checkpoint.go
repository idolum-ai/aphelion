//go:build linux

package telegramruntime

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type telegramIngressCheckpoint struct {
	store   *session.SQLiteStore
	surface string
}

func newTelegramIngressCheckpoint(store *session.SQLiteStore, surface string) telegramIngressCheckpoint {
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = telegramPrimaryIngressSurface
	}
	return telegramIngressCheckpoint{store: store, surface: surface}
}

func (c telegramIngressCheckpoint) NextUpdateID(ctx context.Context) (int64, error) {
	if c.store == nil {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return c.store.TelegramIngressNextUpdateID(c.surface)
}

func (c telegramIngressCheckpoint) SaveNextUpdateID(ctx context.Context, nextUpdateID int64) error {
	if c.store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.SaveTelegramIngressNextUpdateID(c.surface, nextUpdateID, time.Now().UTC())
}

func (c telegramIngressCheckpoint) UpdateState(ctx context.Context, updateID int64) (telegram.PollerUpdateState, error) {
	if c.store == nil || updateID <= 0 {
		return telegram.PollerUpdateState{}, nil
	}
	if err := ctx.Err(); err != nil {
		return telegram.PollerUpdateState{}, err
	}
	record, ok, err := c.store.TelegramIngressUpdate(c.surface, updateID)
	if err != nil || !ok {
		return telegram.PollerUpdateState{}, err
	}
	return telegram.PollerUpdateState{
		Found:    true,
		Terminal: session.TelegramIngressUpdateStatusTerminal(record.Status),
		Status:   string(record.Status),
	}, nil
}

func (c telegramIngressCheckpoint) RecordAccepted(ctx context.Context, accepted telegram.PollerAccepted) (telegram.PollerAcceptResult, error) {
	if c.store == nil {
		return telegram.PollerAcceptResult{Dispatch: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return telegram.PollerAcceptResult{}, err
	}
	result, err := c.store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     c.surface,
		UpdateID:    accepted.UpdateID,
		UpdateKind:  accepted.UpdateKind,
		ChatID:      accepted.ChatID,
		SenderID:    accepted.SenderID,
		MessageID:   accepted.MessageID,
		SessionID:   accepted.SessionID,
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: accepted.InboundJSON,
		PayloadJSON: accepted.Payload,
		AcceptedAt:  accepted.CreatedAt,
		UpdatedAt:   accepted.CreatedAt,
	})
	if err != nil {
		return telegram.PollerAcceptResult{}, err
	}
	return telegram.PollerAcceptResult{
		Dispatch: result.Dispatch,
		Terminal: result.Terminal,
		Status:   string(result.Record.Status),
	}, nil
}

func (c telegramIngressCheckpoint) RecordHandled(ctx context.Context, updateID int64) error {
	if c.store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.store.MarkTelegramIngressHandled(c.surface, updateID, time.Now().UTC())
}

func (c telegramIngressCheckpoint) RecordFailure(ctx context.Context, failure telegram.PollerFailure) error {
	if c.store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.store.RecordTelegramIngressTerminal(session.TelegramIngressUpdateRecord{
		Surface:     c.surface,
		UpdateID:    failure.UpdateID,
		UpdateKind:  failure.UpdateKind,
		ChatID:      failure.ChatID,
		SenderID:    failure.SenderID,
		MessageID:   failure.MessageID,
		Status:      session.TelegramIngressUpdateFailed,
		ErrorText:   failure.ErrorText,
		PayloadJSON: failure.Payload,
		CompletedAt: failure.CreatedAt,
		UpdatedAt:   failure.CreatedAt,
	}); err != nil {
		return err
	}
	return c.store.RecordTelegramIngressFailure(session.TelegramIngressFailureRecord{
		Surface:    c.surface,
		UpdateID:   failure.UpdateID,
		UpdateKind: failure.UpdateKind,
		ChatID:     failure.ChatID,
		SenderID:   failure.SenderID,
		MessageID:  failure.MessageID,
		ErrorText:  failure.ErrorText,
		Payload:    failure.Payload,
		CreatedAt:  failure.CreatedAt,
	})
}

func (c telegramIngressCheckpoint) RecordTerminal(ctx context.Context, terminal telegram.PollerTerminal) error {
	if c.store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	status := session.TelegramIngressUpdateStatus(strings.TrimSpace(terminal.Status))
	switch status {
	case session.TelegramIngressUpdateCompleted, session.TelegramIngressUpdateSkipped:
	default:
		status = session.TelegramIngressUpdateCompleted
	}
	return c.store.RecordTelegramIngressTerminal(session.TelegramIngressUpdateRecord{
		Surface:     c.surface,
		UpdateID:    terminal.UpdateID,
		UpdateKind:  terminal.UpdateKind,
		ChatID:      terminal.ChatID,
		SenderID:    terminal.SenderID,
		MessageID:   terminal.MessageID,
		Status:      status,
		ErrorText:   terminal.Reason,
		PayloadJSON: terminal.Payload,
		CompletedAt: terminal.CreatedAt,
		UpdatedAt:   terminal.CreatedAt,
	})
}
