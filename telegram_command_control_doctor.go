//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"time"
)

func (c telegramCommandControl) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	if c.router == nil {
		return fmt.Errorf("router is not configured")
	}
	queued := msg
	queued.Text = reinstallTemplateMessage
	queued.Raw = nil
	return c.RouteAccepted(ctx, queued)
}

func (c telegramCommandControl) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	if c.rt == nil {
		return fmt.Errorf("runtime is not configured")
	}
	dispatch, err := c.ensureDoctorIngressQueued(msg)
	if err != nil {
		return err
	}
	if !dispatch {
		return nil
	}
	return c.rt.StartDoctor(ctx, msg)
}

func (c telegramCommandControl) ensureDoctorIngressQueued(msg core.InboundMessage) (bool, error) {
	surface := strings.TrimSpace(msg.IngressSurface)
	if c.store == nil || surface == "" || msg.IngressUpdateID <= 0 {
		return true, nil
	}
	result, err := c.store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if result.Found {
		return result.Dispatch, nil
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	now := time.Now().UTC()
	updateKind := "callback_doctor"
	if surface == telegramPrimaryIngressSurface {
		updateKind = "message"
	}
	result, err = c.store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     surface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  updateKind,
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: encoded,
		AcceptedAt:  now,
		UpdatedAt:   now,
	})
	if err != nil {
		return false, err
	}
	if !result.Dispatch {
		return false, nil
	}
	result, err = c.store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, now)
	if err != nil {
		return false, err
	}
	return result.Dispatch, nil
}

func (c telegramCommandControl) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	if c.rt == nil {
		return session.DoctorReportRecord{}, false, fmt.Errorf("Health diagnosis report storage is unavailable.")
	}
	return c.rt.LatestDoctorReport(ctx, chatID, senderID)
}
