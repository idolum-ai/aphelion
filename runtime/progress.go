//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

var turnRunActivityHeartbeatInterval = 30 * time.Second

type messageEditor interface {
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type messageKeyboardEditor interface {
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type messageDeleter interface {
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
}

type inlineKeyboardSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type toolObserver interface {
	ToolStarted(ctx context.Context, name string, input json.RawMessage)
	ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error)
}

type observedToolRegistry struct {
	base     agent.ToolRegistry
	observer toolObserver
}

func (o *observedToolRegistry) Definitions() []agent.ToolDef {
	if o.base == nil {
		return nil
	}
	return o.base.Definitions()
}

func (o *observedToolRegistry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	if o.observer != nil {
		o.observer.ToolStarted(ctx, name, input)
	}
	out, err := o.base.Execute(ctx, name, input)
	if o.observer != nil {
		o.observer.ToolFinished(ctx, name, input, out, err)
	}
	return out, err
}

type turnMonitor struct {
	runtime                  *Runtime
	key                      session.SessionKey
	runID                    int64
	progress                 *toolProgressReporter
	audit                    *turnAuditRecorder
	stopRunActivityHeartbeat context.CancelFunc
}

func (r *Runtime) startTurnMonitor(key session.SessionKey, kind session.TurnRunKind, requestText string, progress *toolProgressReporter, audit *turnAuditRecorder) *turnMonitor {
	monitor := &turnMonitor{
		runtime:  r,
		key:      key,
		progress: progress,
		audit:    audit,
	}

	run, err := r.store.BeginTurnRun(key, kind, requestText)
	if err != nil {
		log.Printf("WARN begin turn run kind=%s chat_id=%d user_id=%d err=%v", kind, key.ChatID, key.UserID, err)
		return monitor
	}
	monitor.runID = run.ID
	r.recordExecutionEvent(key, core.ExecutionEventTurnStarted, "turn", string(session.TurnRunStatusRunning), map[string]any{
		"run_id":       run.ID,
		"run_kind":     strings.TrimSpace(string(kind)),
		"request_text": truncatePreview(strings.TrimSpace(requestText), 220),
	}, time.Now().UTC())
	if progress != nil {
		progress.BindTurnRun(run.ID)
		progress.recordMessageID = func(messageID int64) {
			if err := r.store.UpdateTurnRunProgressMessage(run.ID, messageID); err != nil {
				log.Printf("WARN update turn run progress id=%d msg_id=%d err=%v", run.ID, messageID, err)
			}
		}
	}
	monitor.startRunActivityHeartbeat()
	return monitor
}

func (m *turnMonitor) observeTools(base agent.ToolRegistry) agent.ToolRegistry {
	if base == nil {
		return nil
	}
	return &observedToolRegistry{base: base, observer: m}
}

func (m *turnMonitor) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	preview := toolInputPreview(input)
	if m.audit != nil {
		m.audit.ToolStarted(name, preview)
	}
	if m.runID != 0 {
		if err := m.runtime.store.NoteTurnRunToolStart(m.runID, name, preview); err != nil {
			log.Printf("WARN note turn run tool start id=%d tool=%s err=%v", m.runID, name, err)
		}
	}
	m.runtime.recordExecutionEvent(m.key, core.ExecutionEventToolStarted, "tool", "started", map[string]any{
		"run_id":  m.runID,
		"tool":    strings.TrimSpace(name),
		"preview": preview,
	}, time.Now().UTC())
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
			log.Printf("WARN note turn run tool finish id=%d tool=%s err=%v", m.runID, name, storeErr)
		}
	}
	eventType := core.ExecutionEventToolSucceeded
	status := "succeeded"
	if err != nil {
		eventType = core.ExecutionEventToolFailed
		status = "failed"
	}
	m.runtime.recordExecutionEvent(m.key, eventType, "tool", status, map[string]any{
		"run_id":         m.runID,
		"tool":           strings.TrimSpace(name),
		"result_preview": resultPreview,
		"error":          errorText,
	}, time.Now().UTC())
	if m.progress != nil {
		m.progress.ToolFinished(ctx, name, err)
	}
}

func (m *turnMonitor) startRunActivityHeartbeat() {
	if m == nil || m.runtime == nil || m.runID == 0 {
		return
	}
	interval := turnRunActivityHeartbeatInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	heartbeatCtx, cancel := context.WithCancel(context.Background())
	m.stopRunActivityHeartbeat = cancel
	go runPeriodic(heartbeatCtx, interval, func(runCtx context.Context) {
		select {
		case <-runCtx.Done():
			return
		default:
		}
		if err := m.runtime.store.TouchTurnRunActivity(m.runID); err != nil {
			log.Printf("WARN touch turn run activity id=%d err=%v", m.runID, err)
		}
		if m.progress != nil {
			m.progress.Heartbeat(runCtx)
		}
	})
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

	status := session.TurnRunStatusCompleted
	errorText := ""
	if turnErr != nil {
		status = session.TurnRunStatusFailed
		errorText = trimError(turnErr.Error())
	}
	if err := m.runtime.store.CompleteTurnRun(m.runID, status, errorText); err != nil {
		log.Printf("WARN complete turn run id=%d status=%s err=%v", m.runID, status, err)
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
		"run_id": m.runID,
		"error":  errorText,
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
	currentPlanStep  string
	runID            int64
	controls         [][]telegram.InlineButton
	startedAt        time.Time
	finished         bool
}

type toolProgressEntry struct {
	Key   string
	Text  string
	Count int
}

func (r *Runtime) newToolProgressReporter(key session.SessionKey, msg core.InboundMessage, planState session.PlanState, audit *turnAuditRecorder) *toolProgressReporter {
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
		currentPlanStep:  currentProgressPlanStep(planState),
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
	if p == nil || runID <= 0 || p.suppressControls {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runID = runID
	p.controls = deliberationControlRows(runID)
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
	text := p.renderLocked(done)
	if p.validateText != nil {
		filtered, violations := p.validateText(text)
		if p.audit != nil {
			p.audit.RecordViolations(violations)
		}
		text = filtered
	}
	if p.audit != nil {
		p.audit.RecordProgress(text)
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
			log.Printf("WARN send tool progress chat_id=%d err=%v", p.chatID, err)
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
				"method": "send",
				"error":  trimError(err.Error()),
			})
			if p.reportIssue != nil {
				p.reportIssue(ctx, fmt.Errorf("send tool progress chat_id=%d: %w", p.chatID, err))
			}
			return
		}
		p.messageID = msgID
		p.recordProgressEvent(core.ExecutionEventDeliveryProgressSent, "sent", map[string]any{
			"message_id":    msgID,
			"with_controls": withControls && len(p.controls) > 0,
		})
		if p.recordMessageID != nil {
			p.recordMessageID(msgID)
		}
		return
	}

	if withControls && len(p.controls) > 0 && p.keyboardEditor != nil {
		if err := p.keyboardEditor.EditMessageTextWithInlineKeyboard(ctx, p.chatID, p.messageID, text, "", p.controls); err != nil {
			log.Printf("WARN edit tool progress inline chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
				"method":     "edit_inline",
				"message_id": p.messageID,
				"error":      trimError(err.Error()),
			})
			if p.reportIssue != nil {
				p.reportIssue(ctx, fmt.Errorf("edit tool progress inline chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
			}
		} else {
			p.recordProgressEvent(core.ExecutionEventDeliveryProgressEdited, "edited", map[string]any{
				"method":     "edit_inline",
				"message_id": p.messageID,
			})
			return
		}
	}
	if p.editor == nil {
		return
	}
	if err := p.editor.EditMessageText(ctx, p.chatID, p.messageID, text, ""); err != nil {
		log.Printf("WARN edit tool progress chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
		p.recordProgressEvent(core.ExecutionEventDeliveryProgressFailed, "failed", map[string]any{
			"method":     "edit_text",
			"message_id": p.messageID,
			"error":      trimError(err.Error()),
		})
		if p.reportIssue != nil {
			p.reportIssue(ctx, fmt.Errorf("edit tool progress chat_id=%d msg_id=%d: %w", p.chatID, p.messageID, err))
		}
		return
	}
	p.recordProgressEvent(core.ExecutionEventDeliveryProgressEdited, "edited", map[string]any{
		"method":     "edit_text",
		"message_id": p.messageID,
	})
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

func deliberationControlRows(runID int64) [][]telegram.InlineButton {
	if runID <= 0 {
		return nil
	}
	detachData := core.EncodeDeliberationControlCallbackData(runID, core.DeliberationControlActionDetach)
	stopData := core.EncodeDeliberationControlCallbackData(runID, core.DeliberationControlActionStop)
	if detachData == "" || stopData == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Reassess", CallbackData: detachData},
		{Text: "Stop", CallbackData: stopData},
	}}
}

func (r *Runtime) filterProgressText(text string) (string, []ConstitutionViolation) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || r == nil || r.constitutionGate == nil {
		return trimmed, nil
	}
	violations := r.constitutionGate.ValidateProgressText(trimmed)
	if len(violations) == 0 {
		return trimmed, nil
	}
	return face.RenderToolProgress(face.ToolProgressNotice{
		Entries: []face.ToolProgressEntry{{Text: "Working"}},
	}), violations
}

func (p *toolProgressReporter) renderLocked(done bool) string {
	notice, projected := p.renderNoticeFromExecutionEventsLocked()
	if !projected {
		notice = face.ToolProgressNotice{}
		if len(p.entries) > p.window {
			notice.Omitted = len(p.entries) - p.window
		}
		start := 0
		if len(p.entries) > p.window {
			start = len(p.entries) - p.window
		}
		for _, entry := range p.entries[start:] {
			notice.Entries = append(notice.Entries, face.ToolProgressEntry{
				Text:  entry.Text,
				Count: entry.Count,
			})
		}
	}
	rendered := face.RenderToolProgress(notice)
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	lines[0] = p.progressHeading(done)
	return strings.Join(lines, "\n")
}

func (p *toolProgressReporter) renderNoticeFromExecutionEventsLocked() (face.ToolProgressNotice, bool) {
	if p == nil || p.runtime == nil || p.runtime.store == nil || p.runID <= 0 {
		return face.ToolProgressNotice{}, false
	}
	events, err := p.runtime.store.ExecutionEventsBySession(p.executionKey, 0, 600)
	if err != nil || len(events) == 0 {
		return face.ToolProgressNotice{}, false
	}

	projected := make([]toolProgressEntry, 0, 8)
	for _, event := range events {
		payload := executionEventPayload(event.PayloadJSON)
		runID, ok := payloadInt64(payload, "run_id")
		if !ok || runID != p.runID {
			continue
		}

		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventToolStarted:
			toolName := firstNonEmpty(payloadString(payload, "tool"), "tool")
			preview := strings.TrimSpace(payloadString(payload, "preview"))
			entry := toolProgressEntry{
				Key:  "tool:" + toolName,
				Text: semanticToolProgressEntry(toolName, nil, p.currentPlanStep, p.taskSummary).Text,
			}
			if p.style == "raw" {
				entry.Text = rawToolProgressEventText(toolName, preview)
			}
			addProjectedProgressEntry(&projected, entry)
		case core.ExecutionEventProgressSurface:
			text := normalizeProgressSurfaceText(payloadString(payload, "text"))
			if text == "" {
				continue
			}
			addProjectedProgressEntry(&projected, toolProgressEntry{
				Key:  "surface:" + text,
				Text: text,
			})
		}
	}
	if len(projected) == 0 {
		return face.ToolProgressNotice{}, false
	}
	notice := face.ToolProgressNotice{}
	if len(projected) > p.window {
		notice.Omitted = len(projected) - p.window
	}
	start := 0
	if len(projected) > p.window {
		start = len(projected) - p.window
	}
	for _, entry := range projected[start:] {
		notice.Entries = append(notice.Entries, face.ToolProgressEntry{
			Text:  entry.Text,
			Count: entry.Count,
		})
	}
	return notice, true
}

func rawToolProgressEventText(name string, preview string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return name
	}
	return name + " " + preview
}

func addProjectedProgressEntry(entries *[]toolProgressEntry, entry toolProgressEntry) {
	if entries == nil {
		return
	}
	entry.Key = strings.TrimSpace(entry.Key)
	entry.Text = strings.TrimSpace(entry.Text)
	if entry.Key == "" {
		entry.Key = "tool"
	}
	if entry.Text == "" {
		entry.Text = "Using tool"
	}
	entry.Count = 1
	n := len(*entries)
	if n > 0 {
		last := &(*entries)[n-1]
		if last.Key == entry.Key && last.Text == entry.Text {
			last.Count++
			return
		}
	}
	*entries = append(*entries, entry)
}

func (p *toolProgressReporter) progressHeading(done bool) string {
	if done {
		return "Done."
	}
	return "Thinking..."
}

func (p *toolProgressReporter) addEntry(entry toolProgressEntry) bool {
	if entry.Key == "" {
		entry.Key = "tool"
	}
	if entry.Text == "" {
		entry.Text = "Using tool"
	}
	entry.Count = 1
	if n := len(p.entries); n > 0 && p.entries[n-1].Key == entry.Key && p.entries[n-1].Text == entry.Text {
		p.entries[n-1].Count++
		return true
	}
	p.entries = append(p.entries, entry)
	return true
}

func (p *toolProgressReporter) makeEntry(name string, input json.RawMessage) toolProgressEntry {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	if p.style == "raw" {
		return rawToolProgressEntry(name, input)
	}
	return semanticToolProgressEntry(name, input, p.currentPlanStep, p.taskSummary)
}

func rawToolProgressEntry(name string, input json.RawMessage) toolProgressEntry {
	text := name
	if preview := toolInputPreview(input); preview != "" {
		text += " " + preview
	}
	return toolProgressEntry{
		Key:  name,
		Text: text,
	}
}

func semanticToolProgressEntry(name string, input json.RawMessage, currentStep string, taskSummary string) toolProgressEntry {
	contextLabel := strings.TrimSpace(currentStep)
	if contextLabel == "" {
		contextLabel = strings.TrimSpace(taskSummary)
	}
	switch strings.TrimSpace(name) {
	case "update_plan":
		if contextLabel != "" {
			return toolProgressEntry{Key: "plan:update", Text: "Refining the plan for " + contextLabel}
		}
		return toolProgressEntry{Key: "plan:update", Text: "Refining the plan"}
	case "update_operation":
		if contextLabel != "" {
			return toolProgressEntry{Key: "operation:update", Text: "Updating the operation for " + contextLabel}
		}
		return toolProgressEntry{Key: "operation:update", Text: "Updating the operation"}
	default:
		if contextLabel != "" {
			return toolProgressEntry{Key: "task:" + name, Text: "Working on " + contextLabel}
		}
		return toolProgressEntry{Key: "task:" + name, Text: "Working through the request"}
	}
}

func currentProgressPlanStep(planState session.PlanState) string {
	normalized := session.NormalizePlanState(planState)
	for _, step := range normalized.Steps {
		if step.Status == session.PlanStatusInProgress {
			return strings.TrimSpace(step.Step)
		}
	}
	for _, step := range normalized.Steps {
		if step.Status == session.PlanStatusPending {
			return strings.TrimSpace(step.Step)
		}
	}
	return ""
}

func summarizeProgressTask(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", " ")
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 10 {
		fields = fields[:10]
	}
	summary := strings.Join(fields, " ")
	if len(summary) > 80 {
		summary = strings.TrimSpace(summary[:80])
	}
	return strings.TrimRight(summary, ".,:;!?")
}

func normalizeProgressSurfaceText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return ""
	}
	return truncatePreview(strings.Join(parts, " "), 220)
}

func toolInputPreview(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		return ""
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err == nil {
		trimmed = compact.String()
	}
	return truncatePreview(trimmed, 96)
}

func truncatePreview(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	if limit <= 3 {
		return raw[:limit]
	}
	return raw[:limit-3] + "..."
}

func trimError(raw string) string {
	return truncatePreview(strings.TrimSpace(raw), 400)
}
