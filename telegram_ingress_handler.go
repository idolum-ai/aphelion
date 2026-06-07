//go:build linux

package main

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
)

type telegramIngressRouter interface {
	telegramcommands.Router
	telegramcommands.ThreadRouter
	RouteAccepted(ctx context.Context, msg core.InboundMessage) error
}

type telegramIngressDecisionHandler interface {
	HandleBusyMessage(ctx context.Context, msg core.InboundMessage) (bool, error)
	HandleArtifactRetentionMessage(ctx context.Context, msg core.InboundMessage) (bool, error)
}

func handleTelegramIngressMessage(ctx context.Context, sender telegramcommands.Sender, router telegramIngressRouter, decisions telegramIngressDecisionHandler, msg core.InboundMessage) error {
	msg = telegramruntime.RewriteDurableWizardIntent(msg, router)
	msg = telegramruntime.RewriteDurableRelayIntent(msg)
	if routed, handled, err := telegramcommands.ResolveTelegramThreadPrefix(ctx, sender, router, msg); err != nil {
		return err
	} else if handled {
		return nil
	} else {
		msg = routed
	}
	threadCommandPayload := false
	if routed, retargeted, handled, err := telegramcommands.ResolveTelegramThreadStartCommand(ctx, sender, router, msg); err != nil {
		return err
	} else if handled {
		return nil
	} else if retargeted {
		msg = routed
		threadCommandPayload = true
	}
	if !threadCommandPayload {
		handled, err := telegramcommands.HandleTelegramCommand(ctx, sender, router, msg)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	if routed, handled, err := telegramcommands.ResolveTelegramThreadReply(ctx, sender, router, msg); err != nil {
		return err
	} else if handled {
		return nil
	} else {
		msg = routed
	}
	if handled, err := telegramcommands.ResolveTelegramAgentReply(ctx, sender, router, msg); err != nil {
		return err
	} else if handled {
		return nil
	}
	if mediaPickerHandled, mediaPickerErr := telegramcommands.MaybeAskTelegramMediaThreadPicker(ctx, sender, router, msg); mediaPickerErr != nil {
		return mediaPickerErr
	} else if mediaPickerHandled {
		return nil
	}
	if decisions != nil {
		if busyHandled, busyErr := decisions.HandleBusyMessage(ctx, msg); busyErr != nil {
			return busyErr
		} else if busyHandled {
			return nil
		}
		if retentionHandled, retentionErr := decisions.HandleArtifactRetentionMessage(ctx, msg); retentionErr != nil {
			return retentionErr
		} else if retentionHandled {
			return nil
		}
	}
	return router.RouteAccepted(ctx, msg)
}
