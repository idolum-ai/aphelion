//go:build linux

package runtime

import (
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func (p *toolProgressReporter) prefixProgressText(text string) string {
	text = strings.TrimSpace(text)
	prefix := strings.TrimSpace(p.displayPrefix)
	if prefix == "" || text == "" {
		return text
	}
	if strings.HasPrefix(strings.ToLower(text), strings.ToLower(prefix)) {
		return text
	}
	return prefix + "\n\n" + text
}

func (p *toolProgressReporter) currentProgressDetailsMode() bool {
	if p == nil || p.runtime == nil || p.runtime.store == nil || p.runID <= 0 {
		return false
	}
	state, ok, err := p.runtime.store.TurnProgressView(p.runID)
	return err == nil && ok && state.SelectedView == session.TurnProgressViewDetails
}

func (p *toolProgressReporter) cachedProgressText(details bool) string {
	if p == nil || p.runtime == nil || p.runtime.store == nil || p.runID <= 0 {
		return ""
	}
	state, ok, err := p.runtime.store.TurnProgressView(p.runID)
	if err != nil || !ok {
		return ""
	}
	if details {
		return state.DetailsText
	}
	return state.SummaryText
}

func (p *toolProgressReporter) saveProgressRenderCache(details bool, pair progressRenderedTextPair) {
	if p == nil || p.runtime == nil || p.runtime.store == nil || p.runID <= 0 {
		return
	}
	if cached := p.cachedProgressText(false); shouldUseCachedProgressText(pair.Summary, cached, false) {
		pair.Summary = cached
	}
	if cached := p.cachedProgressText(true); shouldUseCachedProgressText(pair.Details, cached, true) {
		pair.Details = cached
	}
	if err := p.runtime.store.SaveTurnProgressRender(p.runID, p.messageID, progressViewName(details), pair.Summary, pair.Details); err != nil {
		log.Printf("WARN save turn progress render cache run_id=%d msg_id=%d err=%v", p.runID, p.messageID, err)
	}
}
