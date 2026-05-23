//go:build linux

package telegramcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
	"log"
	"strings"
	"time"
)

func mergeContinuationLabel(a string, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func mergeStopResults(a core.StopResult, b core.StopResult) core.StopResult {
	return core.StopResult{
		ActiveCanceled:      a.ActiveCanceled || b.ActiveCanceled,
		QueuedDropped:       a.QueuedDropped || b.QueuedDropped,
		ContinuationRevoked: a.ContinuationRevoked || b.ContinuationRevoked,
		ContinuationLabel:   mergeContinuationLabel(a.ContinuationLabel, b.ContinuationLabel),
	}
}

func (c CommandControl) Stop(chatID int64) core.StopResult {
	result := core.StopResult{}
	if c.Ingress != nil {
		result = mergeStopResults(result, c.Ingress.Stop(chatID))
	}
	if c.Router != nil {
		result = mergeStopResults(result, c.Router.Stop(chatID))
	}
	if c.RevokeContinuation != nil {
		revoke, err := c.RevokeContinuation(chatID)
		if err == nil {
			result = mergeStopResults(result, revoke)
		}
	}
	c.maybeFlushMemory(chatID, "stop")
	return result
}

func (c CommandControl) StopForMessage(msg core.InboundMessage) core.StopResult {
	result := core.StopResult{}
	if c.Ingress != nil {
		result = mergeStopResults(result, c.Ingress.StopForMessage(msg))
	}
	if c.Router != nil {
		result = mergeStopResults(result, c.Router.StopForMessage(msg))
	}
	if c.RevokeContinuationForMessage != nil {
		revoke, err := c.RevokeContinuationForMessage(msg)
		if err == nil {
			result = mergeStopResults(result, revoke)
		}
	}
	return result
}

func (c CommandControl) Route(ctx context.Context, msg core.InboundMessage) {
	if err := c.RouteAccepted(ctx, msg); err != nil {
		log.Printf("WARN telegram ingress route failed chat_id=%d message_id=%d err=%v", msg.ChatID, msg.MessageID, err)
	}
}

func (c CommandControl) RouteAccepted(ctx context.Context, msg core.InboundMessage) error {
	if dropped, err := c.DropClosedTelegramThreadIngress(msg); err != nil || dropped {
		return err
	}
	if err := c.RebindTelegramIngressForMessage(msg); err != nil {
		return err
	}
	if c.Store != nil && strings.TrimSpace(msg.IngressSurface) != "" && msg.IngressUpdateID > 0 {
		result, err := c.Store.MarkTelegramIngressQueued(msg.IngressSurface, msg.IngressUpdateID, time.Now().UTC())
		if err != nil {
			return err
		}
		if !result.Found {
			return fmt.Errorf("telegram ingress update %s/%d is not accepted", strings.TrimSpace(msg.IngressSurface), msg.IngressUpdateID)
		}
		if !result.Dispatch {
			return nil
		}
	}
	if c.Ingress != nil {
		if err := c.Ingress.Enqueue(ctx, msg); err != nil {
			return err
		}
		return nil
	}
	if c.Router != nil {
		c.Router.Route(ctx, msg)
	}
	return nil
}

func (c CommandControl) DropClosedTelegramThreadIngress(msg core.InboundMessage) (bool, error) {
	if c.Store == nil || msg.ChatID == 0 || msg.TelegramThreadID <= 0 {
		return false, nil
	}
	open, found, err := c.Store.TelegramThreadIsOpen(msg.ChatID, msg.TelegramThreadID)
	if err != nil {
		return false, err
	}
	if found && open {
		return false, nil
	}
	reason := session.TelegramIngressDropReasonTelegramThreadClosed
	if !found {
		reason = session.TelegramIngressDropReasonTelegramThreadMissing
	}
	if strings.TrimSpace(msg.IngressSurface) != "" && msg.IngressUpdateID > 0 {
		if _, err := c.Store.MarkTelegramIngressDroppedIfDispatchable(msg.IngressSurface, msg.IngressUpdateID, reason, time.Now().UTC()); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (c CommandControl) RebindTelegramIngressForMessage(msg core.InboundMessage) error {
	if c.Store == nil || msg.TelegramThreadID <= 0 || strings.TrimSpace(msg.IngressSurface) == "" || msg.IngressUpdateID <= 0 {
		return nil
	}
	open, found, err := c.Store.TelegramThreadIsOpen(msg.ChatID, msg.TelegramThreadID)
	if err != nil {
		return err
	}
	if !found || !open {
		return nil
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	return c.Store.RebindTelegramIngressSession(msg.IngressSurface, msg.IngressUpdateID, core.SessionIDForInboundMessage(msg), encoded, time.Now().UTC())
}

func (c CommandControl) MarkStreamControlStopping(streamID string, chatID int64) bool {
	if c.Runtime == nil {
		return false
	}
	return c.Runtime.MarkStreamControlStopping(streamID, chatID)
}

func (c CommandControl) StopRun(runID int64, senderID int64) (core.StopResult, bool, error) {
	if c.Store == nil || runID <= 0 {
		return core.StopResult{}, false, nil
	}
	run, err := c.Store.TurnRun(runID)
	if err != nil {
		return core.StopResult{}, false, err
	}
	if strings.TrimSpace(string(run.Status)) != string(session.TurnRunStatusRunning) {
		return core.StopResult{}, false, nil
	}
	return c.StopForMessage(telegramruntime.InboundForTurnRun(*run, senderID)), true, nil
}

func (c CommandControl) DetachRun(runID int64, senderID int64) (core.DetachResult, bool, error) {
	if c.Store == nil || runID <= 0 {
		return core.DetachResult{}, false, nil
	}
	run, err := c.Store.TurnRun(runID)
	if err != nil {
		return core.DetachResult{}, false, err
	}
	if strings.TrimSpace(string(run.Status)) != string(session.TurnRunStatusRunning) {
		return core.DetachResult{}, false, nil
	}
	result, err := c.DetachForMessage(telegramruntime.InboundForTurnRun(*run, senderID))
	if err != nil {
		return core.DetachResult{}, false, err
	}
	return result, true, nil
}

func (c CommandControl) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	stopped := c.Stop(chatID)
	result := core.NewSessionResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.DecisionDetacher != nil {
		removed, err := 0, error(nil)
		if detacher, ok := c.DecisionDetacher.(DecisionChatSenderDetacher); ok {
			removed, err = detacher.DetachByChatSender(context.Background(), chatID, senderID)
		} else {
			ownerKey := decision.OwnerKey(chatID, senderID)
			removed, err = c.DecisionDetacher.DetachByOwner(context.Background(), ownerKey)
		}
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.PendingDecisionsDetached = removed
	}
	if c.Runtime != nil {
		cleared, err := c.Runtime.ClearChatSessionContext(chatID)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.ContextCleared = cleared
	}
	return result, nil
}

func (c CommandControl) NewForMessage(msg core.InboundMessage) (core.NewSessionResult, error) {
	stopped := c.StopForMessage(msg)
	result := core.NewSessionResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.DecisionDetacher != nil {
		ownerKey := telegramruntime.SessionOwnerKey(msg)
		removed, err := c.DecisionDetacher.DetachByOwner(context.Background(), ownerKey)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.PendingDecisionsDetached = removed
	}
	if c.Runtime != nil {
		cleared, err := c.Runtime.ClearSessionContextForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramruntime.CommandMessageScope(msg)})
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.ContextCleared = cleared
	}
	return result, nil
}

func (c CommandControl) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	stopped := c.Stop(chatID)
	result := core.DetachResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.DecisionDetacher == nil {
		return result, nil
	}
	removed, err := 0, error(nil)
	if detacher, ok := c.DecisionDetacher.(DecisionChatSenderDetacher); ok {
		removed, err = detacher.DetachByChatSender(context.Background(), chatID, senderID)
	} else {
		ownerKey := decision.OwnerKey(chatID, senderID)
		removed, err = c.DecisionDetacher.DetachByOwner(context.Background(), ownerKey)
	}
	if err != nil {
		return core.DetachResult{}, err
	}
	result.PendingDecisionsDetached = removed
	return result, nil
}

func (c CommandControl) DetachForMessage(msg core.InboundMessage) (core.DetachResult, error) {
	stopped := c.StopForMessage(msg)
	result := core.DetachResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.DecisionDetacher == nil {
		return result, nil
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	removed, err := c.DecisionDetacher.DetachByOwner(context.Background(), ownerKey)
	if err != nil {
		return core.DetachResult{}, err
	}
	result.PendingDecisionsDetached = removed
	return result, nil
}

func (c CommandControl) maybeFlushMemory(chatID int64, reason string) {
	if c.Runtime == nil {
		return
	}
	if err := c.Runtime.FlushChatMemory(context.Background(), chatID, reason); err != nil {
		log.Printf("WARN memory flush skipped chat_id=%d reason=%s err=%v", chatID, strings.TrimSpace(reason), err)
		c.Runtime.ReportOperationalIssue(context.Background(), "memory_flush", fmt.Errorf("chat_id=%d reason=%s: %w", chatID, strings.TrimSpace(reason), err))
	}
}
