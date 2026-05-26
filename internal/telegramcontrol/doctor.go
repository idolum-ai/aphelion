//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
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
	updateKind := "callback_doctor"
	if msg.IngressSurface == telegramruntime.PrimaryIngressSurface {
		updateKind = "message"
	}
	return ensureTelegramCallbackWorkQueued(c.Store, msg, updateKind)
}

func (c CommandControl) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	if c.Runtime == nil {
		return session.DoctorReportRecord{}, false, fmt.Errorf("Health diagnosis report storage is unavailable.")
	}
	return c.Runtime.LatestDoctorReport(ctx, chatID, senderID)
}
