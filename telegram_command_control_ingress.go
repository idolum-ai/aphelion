//go:build linux

package main

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
)

func (c telegramCommandControl) Stop(chatID int64) core.StopResult {
	return c.controlFacade().Stop(chatID)
}

func (c telegramCommandControl) StopForMessage(msg core.InboundMessage) core.StopResult {
	return c.controlFacade().StopForMessage(msg)
}

func (c telegramCommandControl) MarkDroppedIngress(messages []core.InboundMessage) {
	if c.store == nil || len(messages) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(messages))
	now := time.Now().UTC()
	for _, msg := range messages {
		surface, updateID, ok := telegramcontrol.IngressIdentityForMessage(msg)
		if !ok {
			continue
		}
		key := surface + ":" + strconv.FormatInt(updateID, 10)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, err := c.store.MarkTelegramIngressDroppedIfDispatchable(surface, updateID, "operator_session_stop", now); err != nil {
			log.Printf("WARN mark dropped telegram ingress failed surface=%s update_id=%d err=%v", surface, updateID, err)
		}
	}
}

func (c telegramCommandControl) Route(ctx context.Context, msg core.InboundMessage) {
	c.controlFacade().Route(ctx, msg)
}

func (c telegramCommandControl) RouteAccepted(ctx context.Context, msg core.InboundMessage) error {
	return c.controlFacade().RouteAccepted(ctx, msg)
}

func (c telegramCommandControl) rebindTelegramIngressForMessage(msg core.InboundMessage) error {
	return c.controlFacade().RebindTelegramIngressForMessage(msg)
}

func (c telegramCommandControl) MarkStreamControlStopping(streamID string, chatID int64) bool {
	return c.controlFacade().MarkStreamControlStopping(streamID, chatID)
}

func (c telegramCommandControl) StopRun(runID int64, senderID int64) (core.StopResult, bool, error) {
	return c.controlFacade().StopRun(runID, senderID)
}

func (c telegramCommandControl) DetachRun(runID int64, senderID int64) (core.DetachResult, bool, error) {
	return c.controlFacade().DetachRun(runID, senderID)
}

func (c telegramCommandControl) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	return c.controlFacade().New(chatID, senderID)
}

func (c telegramCommandControl) NewForMessage(msg core.InboundMessage) (core.NewSessionResult, error) {
	return c.controlFacade().NewForMessage(msg)
}

func (c telegramCommandControl) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	return c.controlFacade().Detach(chatID, senderID)
}

func (c telegramCommandControl) DetachForMessage(msg core.InboundMessage) (core.DetachResult, error) {
	return c.controlFacade().DetachForMessage(msg)
}

func (c telegramCommandControl) Restart(chatID int64) error {
	if c.detachPendingOnRestart && c.decisionDetacher != nil {
		removed, err := c.decisionDetacher.DetachAll(context.Background())
		if err != nil {
			log.Printf("WARN restart detach pending decisions failed err=%v", err)
		} else if removed > 0 {
			log.Printf("WARN restart detached %d pending decision(s) before exit", removed)
		}
	}
	log.Printf("WARN restart requested via telegram chat_id=%d", chatID)
	if c.rt != nil {
		c.rt.BeginShutdown()
	}
	go func() {
		time.Sleep(restartExitWait)
		processExit(exitCodeFailure)
	}()
	return nil
}
