//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func (p *toolProgressReporter) ToolFinished(_ context.Context, _ string, _ error) {
}

func (p *toolProgressReporter) Finish(ctx context.Context) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.messageID == 0 || p.finished {
		return
	}
	p.finished = true
	if p.cleanup && p.deleter != nil {
		if err := p.deleter.DeleteMessage(ctx, p.chatID, p.messageID); err != nil {
			if p.shouldSuppressDeliveryError(err) {
				log.Printf("INFO suppressing expected tool progress delete failure chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
				return
			}
			log.Printf("WARN delete tool progress chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
			if p.reportIssue != nil {
				p.reportIssue(ctx, fmt.Errorf("delete tool progress chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
			}
		}
		return
	}
	p.sendOrEditLocked(ctx, true, false)
}

func (p *toolProgressReporter) recordProgressEvent(eventType string, status string, payload map[string]any) {
	if p == nil || p.runtime == nil {
		return
	}
	p.runtime.recordExecutionEvent(
		p.executionKey,
		eventType,
		"progress",
		status,
		payload,
		time.Now().UTC(),
	)
}

func deliberationControlRows(runID int64, details bool) [][]telegram.InlineButton {
	if runID <= 0 {
		return nil
	}
	detachData := core.EncodeDeliberationControlCallbackData(runID, core.DeliberationControlActionDetach)
	toggleAction := core.DeliberationControlActionDetails
	toggleLabel := "Details"
	if details {
		toggleAction = core.DeliberationControlActionSummary
		toggleLabel = "Summary"
	}
	toggleData := core.EncodeDeliberationControlCallbackData(runID, toggleAction)
	stopData := core.EncodeDeliberationControlCallbackData(runID, core.DeliberationControlActionStop)
	if detachData == "" || toggleData == "" || stopData == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Reassess", CallbackData: detachData},
		{Text: toggleLabel, CallbackData: toggleData},
		{Text: "Stop", CallbackData: stopData},
	}}
}
