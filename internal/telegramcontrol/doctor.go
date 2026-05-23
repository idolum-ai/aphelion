//go:build linux

package telegramcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"time"
)

func (c CommandControl) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	if c.Router == nil {
		return fmt.Errorf("router is not configured")
	}
	queued := msg
	queued.Text = c.ReinstallTemplate
	queued.Raw = nil
	return c.RouteAccepted(ctx, queued)
}

func (c CommandControl) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	if c.Runtime == nil {
		return fmt.Errorf("runtime is not configured")
	}
	dispatch, err := c.EnsureDoctorIngressQueued(msg)
	if err != nil {
		return err
	}
	if !dispatch {
		return nil
	}
	return c.Runtime.StartDoctor(ctx, msg)
}

func (c CommandControl) EnsureDoctorIngressQueued(msg core.InboundMessage) (bool, error) {
	surface := strings.TrimSpace(msg.IngressSurface)
	if c.Store == nil || surface == "" || msg.IngressUpdateID <= 0 {
		return true, nil
	}
	result, err := c.Store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, time.Now().UTC())
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
	if surface == telegramruntime.PrimaryIngressSurface {
		updateKind = "message"
	}
	result, err = c.Store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
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
	result, err = c.Store.MarkTelegramIngressQueued(surface, msg.IngressUpdateID, now)
	if err != nil {
		return false, err
	}
	return result.Dispatch, nil
}

func (c CommandControl) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	if c.Runtime == nil {
		return session.DoctorReportRecord{}, false, fmt.Errorf("Health diagnosis report storage is unavailable.")
	}
	return c.Runtime.LatestDoctorReport(ctx, chatID, senderID)
}
