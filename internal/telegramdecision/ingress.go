//go:build linux

package telegramdecision

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type AcceptedRouter interface {
	RouteAccepted(ctx context.Context, msg core.InboundMessage) error
}

func (h *Handler) routeDecisionMessage(ctx context.Context, msg core.InboundMessage) error {
	if h == nil || h.router == nil {
		return nil
	}
	if accepted, ok := h.router.(AcceptedRouter); ok {
		return accepted.RouteAccepted(ctx, msg)
	}
	h.router.Route(ctx, msg)
	return nil
}

func (h *Handler) routeDeferredDecisionMessage(ctx context.Context, msg core.InboundMessage, surface string, updateKind string) error {
	if h == nil || h.router == nil {
		return nil
	}
	accepted, ok := h.router.(AcceptedRouter)
	if h.store == nil || !ok {
		return h.routeDecisionMessage(ctx, msg)
	}
	msg.IngressSurface = strings.TrimSpace(surface)
	msg.IngressUpdateID = DecisionResumeUpdateID(msg, msg.IngressSurface)
	result, err := h.recordDecisionResumeAccepted(msg, updateKind)
	if err != nil {
		return err
	}
	if !result.Dispatch {
		return nil
	}
	return accepted.RouteAccepted(ctx, msg)
}

func (h *Handler) recordDecisionResumeAccepted(msg core.InboundMessage, updateKind string) (session.TelegramIngressTransitionResult, error) {
	if h == nil || h.store == nil {
		return session.TelegramIngressTransitionResult{}, nil
	}
	surface := strings.TrimSpace(msg.IngressSurface)
	if surface == "" || msg.IngressUpdateID <= 0 {
		return session.TelegramIngressTransitionResult{}, fmt.Errorf("decision resume ingress identity is required")
	}
	encoded := ""
	if raw, err := json.Marshal(msg); err == nil {
		encoded = string(raw)
	}
	now := time.Now().UTC()
	return h.store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     surface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  strings.TrimSpace(updateKind),
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: encoded,
		AcceptedAt:  now,
		UpdatedAt:   now,
	})
}

func DecisionResumeUpdateID(msg core.InboundMessage, surface string) int64 {
	if msg.IngressUpdateID > 0 {
		return msg.IngressUpdateID
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(surface)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(fmt.Sprintf("%d:%d:%d:%d:%s", msg.ChatID, msg.SenderID, msg.MessageID, msg.TelegramThreadID, strings.TrimSpace(msg.Text))))
	id := int64(h.Sum64() & uint64(math.MaxInt64))
	if id <= 0 {
		return 1
	}
	return id
}

func logTelegramDecisionResumeError(kind string, ownerKey string, err error) {
	if err == nil {
		return
	}
	log.Printf("WARN telegram decision resume failed kind=%s owner_key=%s err=%v", strings.TrimSpace(kind), strings.TrimSpace(ownerKey), err)
}
