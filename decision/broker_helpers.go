//go:build linux

package decision

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"
)

func (b *Broker) nextDecision() (uint64, string) {
	next := atomic.AddUint64(&b.nextID, 1)
	return next, strconvBase36(next)
}

func decisionOwnerKey(req Request) string {
	if ownerKey := strings.TrimSpace(req.OwnerKey); ownerKey != "" {
		return ownerKey
	}
	if req.ChatID != 0 && req.SenderID != 0 {
		return fmt.Sprintf("chat:%d:sender:%d", req.ChatID, req.SenderID)
	}
	if req.ChatID != 0 {
		return fmt.Sprintf("chat:%d", req.ChatID)
	}
	if req.SenderID != 0 {
		return fmt.Sprintf("sender:%d", req.SenderID)
	}
	return ""
}

func decisionExclusiveKey(req Request, ownerKey string) string {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return ""
	}
	kind := strings.TrimSpace(string(req.Kind))
	if kind == "" {
		kind = "generic"
	}
	return kind + ":" + ownerKey
}

func normalizeRequest(req Request) Request {
	req.OwnerKey = strings.TrimSpace(req.OwnerKey)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ScopeKind = strings.TrimSpace(req.ScopeKind)
	req.ScopeID = strings.TrimSpace(req.ScopeID)
	req.DurableAgentID = strings.TrimSpace(req.DurableAgentID)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Details = strings.TrimSpace(req.Details)
	req.Metadata = normalizeRequestMetadata(req.Metadata)
	if req.Timeout == 0 {
		req.Timeout = 30 * time.Second
	}
	return req
}

func normalizeRequestMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsChoice(choices []Choice, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, choice := range choices {
		if strings.TrimSpace(choice.ID) == id {
			return true
		}
	}
	return false
}

func OwnerKey(chatID int64, senderID int64) string {
	return decisionOwnerKey(Request{ChatID: chatID, SenderID: senderID})
}

func (b *Broker) emitEvent(ctx context.Context, pending *pendingDecision, eventType EventType, choice string, timedOut bool, reason string) {
	if b == nil || pending == nil {
		return
	}
	b.mu.Lock()
	observer := b.observer
	b.mu.Unlock()
	if observer == nil {
		return
	}
	event := Event{
		Type:      eventType,
		Decision:  pending.request,
		OwnerKey:  strings.TrimSpace(pending.ownerKey),
		Seq:       pending.seq,
		Choice:    strings.TrimSpace(choice),
		TimedOut:  timedOut,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: time.Now().UTC(),
	}
	observer(ctx, event)
}

func resolveDefaultChoice(pending *pendingDecision) {
	if pending == nil {
		return
	}
	defaultChoice := strings.TrimSpace(pending.request.DefaultChoice)
	if defaultChoice == "" {
		return
	}
	select {
	case pending.resultCh <- defaultChoice:
	default:
	}
}

func EncodeCallbackData(id string, choice string) string {
	return "decision:" + strings.TrimSpace(id) + ":" + strings.TrimSpace(choice)
}

func DecodeCallbackData(data string) (id string, choice string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, "decision:") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), true
}

func strconvBase36(v uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	var buf [13]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[int(v%36)]
		v /= 36
	}
	return string(buf[i:])
}

func parseBase36(raw string) (uint64, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false
	}
	var out uint64
	for _, ch := range raw {
		var digit uint64
		switch {
		case ch >= '0' && ch <= '9':
			digit = uint64(ch - '0')
		case ch >= 'a' && ch <= 'z':
			digit = uint64(ch-'a') + 10
		default:
			return 0, false
		}
		if out > (math.MaxUint64-digit)/36 {
			return 0, false
		}
		out = out*36 + digit
	}
	return out, true
}
