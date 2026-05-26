//go:build linux

package telegramcontrol

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
)

func (c CommandControl) QueueClarification(ctx context.Context, msg core.InboundMessage) error {
	updateKind := "callback_clarification"
	switch msg.IngressSurface {
	case telegramruntime.ContextClarificationIngressSurface:
		updateKind = "callback_context_clarification"
	case telegramruntime.MemoryClarificationIngressSurface:
		updateKind = "callback_memory_clarification"
	}
	if err := recordTelegramCallbackWorkAccepted(c.Store, msg, updateKind); err != nil {
		return err
	}
	return c.RouteAccepted(ctx, msg)
}
