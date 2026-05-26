//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

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
		displayPrefix:    r.telegramPresentationForMessage(msg).Prefix,
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
