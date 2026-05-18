//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (m *turnMonitor) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	startedAt := time.Now().UTC()
	preview := toolInputPreview(input)
	if m.audit != nil {
		m.audit.ToolStarted(name, preview)
	}
	if m.runID != 0 {
		if err := m.runtime.store.NoteTurnRunToolStart(m.runID, name, preview); err != nil {
			if m.runtime.expectedShutdownNoise(ctx, err) {
				log.Printf("INFO suppressing expected shutdown tool-start note failure id=%d tool=%s err=%v", m.runID, name, err)
			} else {
				log.Printf("WARN note turn run tool start id=%d tool=%s err=%v", m.runID, name, err)
			}
		}
	}
	if m.toolStarts != nil {
		m.toolStarts[toolDurationKey(name, input)] = startedAt
	}
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolStarted, "tool", "started", map[string]any{
		"run_id":  m.runID,
		"tool":    strings.TrimSpace(name),
		"preview": preview,
	}, startedAt)
	if m.progress != nil {
		m.progress.ToolStarted(ctx, name, input)
	}
}

func (m *turnMonitor) ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error) {
	resultPreview := truncatePreview(strings.TrimSpace(output), 220)
	errorText := ""
	if err != nil {
		errorText = trimError(err.Error())
	}
	if m.audit != nil {
		m.audit.ToolFinished(name, resultPreview, errorText)
	}
	if m.runID != 0 {
		if storeErr := m.runtime.store.NoteTurnRunToolFinish(m.runID, resultPreview, errorText); storeErr != nil {
			if m.runtime.expectedShutdownNoise(ctx, storeErr) {
				log.Printf("INFO suppressing expected shutdown tool-finish note failure id=%d tool=%s err=%v", m.runID, name, storeErr)
			} else {
				log.Printf("WARN note turn run tool finish id=%d tool=%s err=%v", m.runID, name, storeErr)
			}
		}
	}
	eventType := core.ExecutionEventToolSucceeded
	status := "succeeded"
	if err != nil {
		eventType = core.ExecutionEventToolFailed
		status = "failed"
	}
	toolDurationMS := int64(0)
	if m.toolStarts != nil {
		key := toolDurationKey(name, input)
		if startedAt, ok := m.toolStarts[key]; ok {
			toolDurationMS = elapsedMillisSince(startedAt)
			delete(m.toolStarts, key)
		}
	}
	m.runtime.recordExecutionEvent(m.key, eventType, "tool", status, map[string]any{
		"run_id":           m.runID,
		"tool":             strings.TrimSpace(name),
		"result_preview":   resultPreview,
		"error":            errorText,
		"tool_duration_ms": toolDurationMS,
	}, time.Now().UTC())
	if m.progress != nil {
		m.progress.ToolFinished(ctx, name, err)
	}
}

func toolDurationKey(name string, input json.RawMessage) string {
	return strings.TrimSpace(name) + "\n" + string(input)
}

func (m *turnMonitor) Finish(ctx context.Context, turnErr error) {
	if m.progress != nil {
		m.progress.Finish(ctx)
	}
	if m.stopRunActivityHeartbeat != nil {
		m.stopRunActivityHeartbeat()
		m.stopRunActivityHeartbeat = nil
	}
	if m.runID == 0 {
		return
	}
	m.runtime.unregisterActiveTurn(m.runID)
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}

	status := session.TurnRunStatusCompleted
	errorText := ""
	if turnErr != nil {
		status = session.TurnRunStatusFailed
		if errors.Is(turnErr, context.Canceled) {
			status = session.TurnRunStatusInterrupted
		}
		errorText = trimError(turnErr.Error())
	}
	if err := m.runtime.store.CompleteTurnRun(m.runID, status, errorText); err != nil {
		if m.runtime.expectedShutdownNoise(ctx, err) {
			log.Printf("INFO suppressing expected shutdown turn completion failure id=%d status=%s err=%v", m.runID, status, err)
		} else {
			log.Printf("WARN complete turn run id=%d status=%s err=%v", m.runID, status, err)
		}
	}
	if m.ingressSurface != "" && m.ingressUpdateID > 0 {
		ingressStatus := session.TelegramIngressUpdateCompleted
		if turnErr != nil {
			if errors.Is(turnErr, context.Canceled) {
				ingressStatus = session.TelegramIngressUpdateDropped
			} else {
				ingressStatus = session.TelegramIngressUpdateFailed
			}
		}
		if err := m.runtime.store.MarkTelegramIngressCompleted(m.ingressSurface, m.ingressUpdateID, m.runID, ingressStatus, errorText, time.Now().UTC()); err != nil {
			if m.runtime.expectedShutdownNoise(ctx, err) {
				log.Printf("INFO suppressing expected shutdown telegram ingress completion failure update_id=%d status=%s err=%v", m.ingressUpdateID, ingressStatus, err)
			} else {
				log.Printf("WARN complete telegram ingress update_id=%d status=%s err=%v", m.ingressUpdateID, ingressStatus, err)
			}
		}
	}
	eventType := core.ExecutionEventTurnCompleted
	eventStatus := "completed"
	if turnErr != nil {
		if errors.Is(turnErr, context.Canceled) {
			eventType = core.ExecutionEventTurnInterrupted
			eventStatus = "interrupted"
		} else {
			eventType = core.ExecutionEventTurnFailed
			eventStatus = "failed"
		}
	}
	m.runtime.recordExecutionEvent(m.key, eventType, "turn", eventStatus, map[string]any{
		"run_id":           m.runID,
		"error":            errorText,
		"turn_duration_ms": elapsedMillisSince(m.startedAt),
	}, time.Now().UTC())
}

type toolProgressReporter struct {
	runtime          *Runtime
	executionKey     session.SessionKey
	mu               sync.Mutex
	sender           OutboundSender
	inlineSender     inlineKeyboardSender
	editor           messageEditor
	keyboardEditor   messageKeyboardEditor
	deleter          messageDeleter
	reportIssue      func(ctx context.Context, err error)
	chatID           int64
	replyTo          *int64
	suppressControls bool
	mode             string
	style            string
	window           int
	cleanup          bool
	messageID        int64
	entries          []toolProgressEntry
	seenKeys         map[string]struct{}
	recordMessageID  func(messageID int64)
	validateText     func(string) (string, []ConstitutionViolation)
	audit            *turnAuditRecorder
	taskSummary      string
	displayPrefix    string
	currentPlanStep  string
	runID            int64
	controls         [][]telegram.InlineButton
	startedAt        time.Time
	finished         bool
	lastRendered     string
	lastWithControls bool
}

type toolProgressEntry struct {
	Key   string
	Text  string
	Count int
}

func (r *Runtime) newToolProgressReporter(key session.SessionKey, msg core.InboundMessage, audit *turnAuditRecorder) *toolProgressReporter {
	mode := strings.ToLower(strings.TrimSpace(r.toolProgressMode))
	if mode == "" {
		mode = "all"
	}
	if mode == "off" || r.outbound == nil {
		return nil
	}
	target := r.resolveToolProgressTarget(msg)
	if target.ChatID == 0 {
		return nil
	}

	reporter := &toolProgressReporter{
		runtime:          r,
		executionKey:     key,
		sender:           r.outbound,
		reportIssue:      nil,
		chatID:           target.ChatID,
		replyTo:          target.ReplyTo,
		suppressControls: target.SuppressControls,
		mode:             mode,
		style:            strings.ToLower(strings.TrimSpace(r.toolProgressStyle)),
		window:           r.toolProgressWindow,
		cleanup:          r.toolProgressCleanup,
		seenKeys:         make(map[string]struct{}),
		audit:            audit,
		taskSummary:      summarizeProgressTask(msg.Text),
		displayPrefix:    telegramThreadDisplayPrefix(msg.TelegramThreadID),
	}
	if target.SuppressControls {
		reporter.reportIssue = r.reportToolProgressIssue
	}
	if reporter.style == "" {
		reporter.style = "semantic"
	}
	if reporter.window <= 0 {
		reporter.window = 4
	}
	if editor, ok := r.outbound.(messageEditor); ok {
		reporter.editor = editor
	}
	if sender, ok := r.outbound.(inlineKeyboardSender); ok {
		reporter.inlineSender = sender
	}
	if keyboardEditor, ok := r.outbound.(messageKeyboardEditor); ok {
		reporter.keyboardEditor = keyboardEditor
	}
	if deleter, ok := r.outbound.(messageDeleter); ok {
		reporter.deleter = deleter
	}
	reporter.validateText = r.filterProgressText
	return reporter
}

type toolProgressTarget struct {
	ChatID           int64
	ReplyTo          *int64
	SuppressControls bool
}

func (r *Runtime) resolveToolProgressTarget(msg core.InboundMessage) toolProgressTarget {
	target := toolProgressTarget{
		ChatID:  msg.ChatID,
		ReplyTo: replyToMessageID(msg.MessageID),
	}
	if r == nil {
		return target
	}
	if toolProgressUsesInboundTelegramChat(msg) {
		return target
	}
	relayChatID := r.resolveInternalProgressRelayChat(msg)
	if relayChatID == 0 {
		return target
	}
	target.ChatID = relayChatID
	target.ReplyTo = nil
	target.SuppressControls = true
	return target
}

func toolProgressUsesInboundTelegramChat(msg core.InboundMessage) bool {
	chatType := strings.ToLower(strings.TrimSpace(msg.ChatType))
	if chatType == "" {
		return msg.ChatID > 0
	}
	switch chatType {
	case "private", "group", "supergroup", "channel", "dm", "telegram_dm", "telegram_group":
		return msg.ChatID != 0
	default:
		return false
	}
}

func (r *Runtime) resolveInternalProgressRelayChat(msg core.InboundMessage) int64 {
	if r == nil || r.cfg == nil {
		return 0
	}
	if r.store != nil {
		agentID := strings.TrimSpace(msg.DurableAgentID)
		if agentID != "" {
			if agent, err := r.store.DurableAgent(agentID); err == nil && agent != nil && agent.ReviewTargetChatID > 0 {
				return agent.ReviewTargetChatID
			}
		}
	}
	adminIDs := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(adminIDs) == 0 {
		return 0
	}
	if targetChatID := r.lastActiveAdminChat(adminIDs); targetChatID != 0 {
		return targetChatID
	}
	return adminIDs[0]
}

func (r *Runtime) reportToolProgressIssue(ctx context.Context, err error) {
	if r == nil || err == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.reportOperationalIssue(ctx, "tool_progress", err)
}

func (p *toolProgressReporter) BindTurnRun(runID int64) {
	if p == nil || runID <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runID = runID
	if p.suppressControls {
		return
	}
	p.controls = deliberationControlRows(runID, false)
}

func (p *toolProgressReporter) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	if p.startedAt.IsZero() {
		p.startedAt = time.Now().UTC()
	}
	p.observePlanToolInput(name, input)
	if p.style != "raw" && isProgressMetadataTool(name) {
		return
	}
	entry := p.makeEntry(name, input)

	update := false
	switch p.mode {
	case "all":
		update = p.addEntry(entry)
	case "new":
		if _, ok := p.seenKeys[entry.Key]; !ok {
			update = p.addEntry(entry)
		}
	default:
		return
	}
	p.seenKeys[entry.Key] = struct{}{}
	if !update {
		return
	}
	p.sendOrEditLocked(ctx, false, true)
}

func (p *toolProgressReporter) Heartbeat(ctx context.Context) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	if p.startedAt.IsZero() {
		p.startedAt = time.Now().UTC()
	}
	p.sendOrEditLocked(ctx, false, true)
}

func (p *toolProgressReporter) Surface(ctx context.Context, text string) {
	if p == nil {
		return
	}
	normalized := normalizeProgressSurfaceText(text)
	if normalized == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	if p.startedAt.IsZero() {
		p.startedAt = time.Now().UTC()
	}
	p.recordProgressEvent(core.ExecutionEventProgressSurface, "active", map[string]any{
		"run_id": p.runID,
		"text":   normalized,
	})
	entry := toolProgressEntry{
		Key:  "surface:" + normalized,
		Text: normalized,
	}
	if !p.addEntry(entry) {
		return
	}
	p.sendOrEditLocked(ctx, false, true)
}

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

func (p *toolProgressReporter) shouldSuppressDeliveryError(err error) bool {
	return isExpectedDurableChildOutboundUnavailable(err) || (p != nil && p.runtime != nil && p.runtime.expectedShutdownNoise(context.Background(), err))
}

func isExpectedDurableChildOutboundUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "outbound delivery is unavailable in durable child mode")
}

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
