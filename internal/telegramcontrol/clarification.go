//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
)

func (c CommandControl) QueueClarification(ctx context.Context, msg core.InboundMessage) error {
	updateKind := "callback_clarification"
	switch msg.IngressSurface {
	case telegramruntime.ContextClarificationIngressSurface:
		updateKind = "callback_context_clarification"
	case telegramruntime.MemoryClarificationIngressSurface:
		updateKind = "callback_memory_clarification"
	case telegramruntime.MissionClarificationIngressSurface:
		updateKind = "callback_mission_clarification"
	}
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, updateKind); err != nil {
		return err
	}
	return c.RouteAccepted(ctx, msg)
}

func (c CommandControl) QueueMissionClarification(ctx context.Context, msg core.InboundMessage, promptID string) error {
	if c.Runtime == nil {
		return fmt.Errorf("Mission Question is unavailable.")
	}
	msg.IngressSurface = telegramruntime.MissionClarificationIngressSurface
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, "callback_mission_clarification"); err != nil {
		return err
	}
	if _, err := c.Runtime.ResolveMissionAskPrompt(ctx, msg.SenderID, promptID, session.MissionAskStatusAsked, "mission clarification queued"); err != nil {
		return err
	}
	return c.RouteAccepted(ctx, msg)
}
