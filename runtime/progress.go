//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type messageEditor interface {
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string) error
}

type messageDeleter interface {
	DeleteMessage(ctx context.Context, chatID int64, messageID int64) error
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
	runtime  *Runtime
	key      session.SessionKey
	runID    int64
	progress *toolProgressReporter
}

func (r *Runtime) startTurnMonitor(key session.SessionKey, kind session.TurnRunKind, requestText string, progress *toolProgressReporter) *turnMonitor {
	monitor := &turnMonitor{
		runtime:  r,
		key:      key,
		progress: progress,
	}

	run, err := r.store.BeginTurnRun(key, kind, requestText)
	if err != nil {
		log.Printf("WARN begin turn run kind=%s chat_id=%d user_id=%d err=%v", kind, key.ChatID, key.UserID, err)
		return monitor
	}
	monitor.runID = run.ID
	if progress != nil {
		progress.recordMessageID = func(messageID int64) {
			if err := r.store.UpdateTurnRunProgressMessage(run.ID, messageID); err != nil {
				log.Printf("WARN update turn run progress id=%d msg_id=%d err=%v", run.ID, messageID, err)
			}
		}
	}
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
	if m.runID != 0 {
		if err := m.runtime.store.NoteTurnRunToolStart(m.runID, name, preview); err != nil {
			log.Printf("WARN note turn run tool start id=%d tool=%s err=%v", m.runID, name, err)
		}
	}
	if m.progress != nil {
		m.progress.ToolStarted(ctx, name, preview)
	}
}

func (m *turnMonitor) ToolFinished(ctx context.Context, name string, input json.RawMessage, output string, err error) {
	if m.progress != nil {
		m.progress.ToolFinished(ctx, name, err)
	}
}

func (m *turnMonitor) Finish(ctx context.Context, turnErr error) {
	if m.progress != nil {
		m.progress.Finish(ctx)
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
}

type toolProgressReporter struct {
	sender          OutboundSender
	editor          messageEditor
	deleter         messageDeleter
	chatID          int64
	replyTo         *int64
	mode            string
	cleanup         bool
	messageID       int64
	entries         []toolProgressEntry
	seenNames       map[string]struct{}
	recordMessageID func(messageID int64)
}

type toolProgressEntry struct {
	Name    string
	Preview string
}

func (r *Runtime) newToolProgressReporter(msg core.InboundMessage) *toolProgressReporter {
	mode := strings.ToLower(strings.TrimSpace(r.toolProgressMode))
	if mode == "" {
		mode = "all"
	}
	if mode == "off" || r.outbound == nil {
		return nil
	}

	reporter := &toolProgressReporter{
		sender:    r.outbound,
		chatID:    msg.ChatID,
		replyTo:   replyToMessageID(msg.MessageID),
		mode:      mode,
		cleanup:   r.toolProgressCleanup,
		seenNames: make(map[string]struct{}),
	}
	if editor, ok := r.outbound.(messageEditor); ok {
		reporter.editor = editor
	}
	if deleter, ok := r.outbound.(messageDeleter); ok {
		reporter.deleter = deleter
	}
	return reporter
}

func (p *toolProgressReporter) ToolStarted(ctx context.Context, name string, preview string) {
	if p == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}

	update := false
	switch p.mode {
	case "all":
		p.entries = append(p.entries, toolProgressEntry{Name: name, Preview: preview})
		update = true
	case "new":
		if _, ok := p.seenNames[name]; !ok {
			p.entries = append(p.entries, toolProgressEntry{Name: name, Preview: preview})
			update = true
		}
	default:
		return
	}
	p.seenNames[name] = struct{}{}
	if !update {
		return
	}

	text := p.render()
	if p.messageID == 0 {
		msgID, err := p.sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  p.chatID,
			Text:    text,
			ReplyTo: p.replyTo,
		})
		if err != nil {
			log.Printf("WARN send tool progress chat_id=%d err=%v", p.chatID, err)
			return
		}
		p.messageID = msgID
		if p.recordMessageID != nil {
			p.recordMessageID(msgID)
		}
		return
	}

	if p.editor == nil {
		return
	}
	if err := p.editor.EditMessageText(ctx, p.chatID, p.messageID, text); err != nil {
		log.Printf("WARN edit tool progress chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
	}
}

func (p *toolProgressReporter) ToolFinished(_ context.Context, _ string, _ error) {
}

func (p *toolProgressReporter) Finish(ctx context.Context) {
	if p == nil || p.messageID == 0 || !p.cleanup || p.deleter == nil {
		return
	}
	if err := p.deleter.DeleteMessage(ctx, p.chatID, p.messageID); err != nil {
		log.Printf("WARN delete tool progress chat_id=%d msg_id=%d err=%v", p.chatID, p.messageID, err)
	}
}

func (p *toolProgressReporter) render() string {
	lines := []string{"Working with tools..."}
	if len(p.entries) > 8 {
		lines = append(lines, fmt.Sprintf("%d earlier tool starts omitted.", len(p.entries)-8))
	}
	start := 0
	if len(p.entries) > 8 {
		start = len(p.entries) - 8
	}
	for i, entry := range p.entries[start:] {
		line := fmt.Sprintf("%d. %s", start+i+1, entry.Name)
		if entry.Preview != "" {
			line += " " + entry.Preview
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
