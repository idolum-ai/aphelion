//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (p *toolProgressReporter) sendOrEditLocked(ctx context.Context, done bool, withControls bool) {
	if p == nil {
		return
	}
	deliveryStarted := time.Now()
	details := p.currentProgressDetailsMode()
	if withControls && !p.suppressControls && p.runID > 0 {
		p.controls = deliberationControlRows(p.runID, details)
	}
	pair := p.renderProgressTextPairLocked(done)
	text := p.selectProgressTextLocked(pair, details)
	text = p.prefixProgressText(text)
	if p.audit != nil {
		if details {
			p.audit.RecordViolations(pair.DetailsViolations)
		} else {
			p.audit.RecordViolations(pair.SummaryViolations)
		}
		p.audit.RecordProgress(text)
	}
	if p.messageID != 0 && text == p.lastRendered && withControls == p.lastWithControls && !done {
		return
	}
	if p.messageID == 0 {
		msgID := int64(0)
		var err error
		if withControls && len(p.controls) > 0 && p.inlineSender != nil {
			msgID, err = p.inlineSender.SendInlineKeyboard(ctx, p.chatID, text, p.controls, p.replyTo)
		} else {
			msgID, err = p.sender.SendMessage(ctx, core.OutboundMessage{
				ChatID:  p.chatID,
				Text:    text,
				ReplyTo: p.replyTo,
			})
		}
		if err != nil {
			if p.shouldSuppressDeliveryError(err) {
				log.Printf("INFO suppressing expected tool progress delivery failure chat_id=%d err=%v", p.chatID, err)
				return
			}
			log.Printf("WARN send tool progress chat_id=%d err=%v", p.chatID, err)
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
				"method":         "send",
				"error":          trimError(err.Error()),
				"source_class":   "canonical",
				"source_surface": "outbound_transport_ledger",
				"visibility":     "human_render_unknown",
			})
			if p.reportIssue != nil {
				p.reportIssue(ctx, fmt.Errorf("send tool progress chat_id=%d: %w", p.chatID, err))
			}
			return
		}
		p.messageID = msgID
		p.lastRendered = text
		p.lastWithControls = withControls
		p.saveProgressRenderCache(details, pair)
		p.recordProgressEvent(core.ExecutionEventDeliveryProgressSent, "sent", map[string]any{
			"message_id":                    msgID,
			"run_id":                        p.runID,
			"view":                          progressViewName(details),
			"progress_delivery_duration_ms": durationMillis(time.Since(deliveryStarted)),
			"with_controls":                 withControls && len(p.controls) > 0,
			"source_class":                  "canonical",
			"source_surface":                "outbound_transport_ledger",
			"visibility":                    "human_render_unknown",
			"transport_status":              "acknowledged",
		})
		if p.recordMessageID != nil {
			p.recordMessageID(msgID)
		}
		return
	}

	if withControls && len(p.controls) > 0 && p.keyboardEditor != nil {
		if err := p.keyboardEditor.EditMessageTextWithInlineKeyboard(ctx, p.chatID, p.messageID, text, "", p.controls); err != nil {
			if p.shouldSuppressDeliveryError(err) {
				log.Printf("INFO suppressing expected tool progress inline edit failure chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
				return
			}
			log.Printf("WARN edit tool progress inline chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
				"method":         "edit_inline",
				"message_id":     p.messageID,
				"error":          trimError(err.Error()),
				"source_class":   "canonical",
				"source_surface": "outbound_transport_ledger",
				"visibility":     "human_render_unknown",
			})
			if p.reportIssue != nil {
				p.reportIssue(ctx, fmt.Errorf("edit tool progress inline chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
			}
		} else {
			p.lastRendered = text
			p.lastWithControls = true
			p.saveProgressRenderCache(details, pair)
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressEdited, "edited", map[string]any{
				"method":                        "edit_inline",
				"message_id":                    p.messageID,
				"run_id":                        p.runID,
				"view":                          progressViewName(details),
				"progress_delivery_duration_ms": durationMillis(time.Since(deliveryStarted)),
				"source_class":                  "canonical",
				"source_surface":                "outbound_transport_ledger",
				"visibility":                    "human_render_unknown",
				"transport_status":              "acknowledged",
			})
			return
		}
	}
	if !withControls && len(p.controls) > 0 {
		if clearer, ok := p.sender.(messageKeyboardClearer); ok {
			if err := clearer.EditMessageTextWithoutInlineKeyboard(ctx, p.chatID, p.messageID, text, ""); err != nil {
				if p.shouldSuppressDeliveryError(err) {
					log.Printf("INFO suppressing expected tool progress keyboard clear failure chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
					return
				}
				log.Printf("WARN edit tool progress clear keyboard chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
				p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
					"method":         "edit_clear_keyboard",
					"message_id":     p.messageID,
					"error":          trimError(err.Error()),
					"source_class":   "canonical",
					"source_surface": "outbound_transport_ledger",
					"visibility":     "human_render_unknown",
				})
				if p.reportIssue != nil {
					p.reportIssue(ctx, fmt.Errorf("edit tool progress clear keyboard chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
				}
			} else {
				p.lastRendered = text
				p.lastWithControls = false
				p.saveProgressRenderCache(details, pair)
				p.recordProgressEvent(core.ExecutionEventDeliveryProgressEdited, "edited", map[string]any{
					"method":                        "edit_clear_keyboard",
					"message_id":                    p.messageID,
					"progress_delivery_duration_ms": durationMillis(time.Since(deliveryStarted)),
					"run_id":                        p.runID,
					"view":                          progressViewName(details),
					"source_class":                  "canonical",
					"source_surface":                "outbound_transport_ledger",
					"visibility":                    "human_render_unknown",
					"transport_status":              "acknowledged",
				})
				return
			}
		}
	}
	if p.editor == nil {
		return
	}
	if err := p.editor.EditMessageText(ctx, p.chatID, p.messageID, text, ""); err != nil {
		if p.shouldSuppressDeliveryError(err) {
			log.Printf("INFO suppressing expected tool progress edit failure chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
			return
		}
		log.Printf("WARN edit tool progress chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
		p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
			"method":         "edit_text",
			"message_id":     p.messageID,
			"error":          trimError(err.Error()),
			"source_class":   "canonical",
			"source_surface": "outbound_transport_ledger",
			"visibility":     "human_render_unknown",
		})
		if p.reportIssue != nil {
			p.reportIssue(ctx, fmt.Errorf("edit tool progress chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
		}
		return
	}
	p.lastRendered = text
	p.lastWithControls = withControls
	p.saveProgressRenderCache(details, pair)
	p.recordProgressEvent(core.ExecutionEventDeliveryProgressEdited, "edited", map[string]any{
		"method":                        "edit_text",
		"message_id":                    p.messageID,
		"progress_delivery_duration_ms": durationMillis(time.Since(deliveryStarted)),
		"run_id":                        p.runID,
		"view":                          progressViewName(details),
		"source_class":                  "canonical",
		"source_surface":                "outbound_transport_ledger",
		"visibility":                    "human_render_unknown",
		"transport_status":              "acknowledged",
	})
}

func (p *toolProgressReporter) shouldSuppressDeliveryError(err error) bool {
	return isExpectedDurableChildOutboundUnavailable(err) || (p != nil && p.runtime != nil && p.runtime.expectedShutdownNoise(context.Background(), err))
}

func isExpectedDurableChildOutboundUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "outbound delivery is unavailable in durable child mode")
}
