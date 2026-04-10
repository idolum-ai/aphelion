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
		m.progress.ToolStarted(ctx, name, input)
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
	style           string
	window          int
	cleanup         bool
	messageID       int64
	entries         []toolProgressEntry
	seenKeys        map[string]struct{}
	recordMessageID func(messageID int64)
}

type toolProgressEntry struct {
	Key   string
	Text  string
	Count int
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
		sender:   r.outbound,
		chatID:   msg.ChatID,
		replyTo:  replyToMessageID(msg.MessageID),
		mode:     mode,
		style:    strings.ToLower(strings.TrimSpace(r.toolProgressStyle)),
		window:   r.toolProgressWindow,
		cleanup:  r.toolProgressCleanup,
		seenKeys: make(map[string]struct{}),
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
	if deleter, ok := r.outbound.(messageDeleter); ok {
		reporter.deleter = deleter
	}
	return reporter
}

func (p *toolProgressReporter) ToolStarted(ctx context.Context, name string, input json.RawMessage) {
	if p == nil {
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
	lines := []string{"Working on it..."}
	if len(p.entries) > p.window {
		lines = append(lines, fmt.Sprintf("%d earlier steps omitted.", len(p.entries)-p.window))
	}
	start := 0
	if len(p.entries) > p.window {
		start = len(p.entries) - p.window
	}
	for i, entry := range p.entries[start:] {
		_ = i
		line := "- " + entry.Text
		if entry.Count > 1 {
			line = fmt.Sprintf("- %s (%dx)", entry.Text, entry.Count)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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
	return semanticToolProgressEntry(name, input)
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

func semanticToolProgressEntry(name string, input json.RawMessage) toolProgressEntry {
	switch strings.TrimSpace(name) {
	case "exec":
		return semanticExecProgressEntry(input)
	case "memory":
		return toolProgressEntry{Key: "memory:update", Text: "Updating memory"}
	case "session_search":
		return toolProgressEntry{Key: "session:search", Text: "Searching past sessions"}
	case "semantic_search":
		return toolProgressEntry{Key: "memory:semantic", Text: "Searching semantic memory"}
	case "openai_file":
		return semanticOpenAIFileProgressEntry(input)
	case "openai_vector_store":
		return semanticOpenAIVectorStoreProgressEntry(input)
	default:
		return toolProgressEntry{Key: name, Text: fmt.Sprintf("Using %s", name)}
	}
}

type execToolInput struct {
	Command string `json:"command"`
}

type openAIFileProgressInput struct {
	Action string `json:"action"`
}

type openAIVectorStoreProgressInput struct {
	Action string `json:"action"`
}

func semanticExecProgressEntry(input json.RawMessage) toolProgressEntry {
	var parsed execToolInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return toolProgressEntry{Key: "exec", Text: "Running command"}
	}
	return classifyExecCommand(parsed.Command)
}

func semanticOpenAIFileProgressEntry(input json.RawMessage) toolProgressEntry {
	var parsed openAIFileProgressInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return toolProgressEntry{Key: "openai:file", Text: "Using OpenAI file storage"}
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Action)) {
	case "put":
		return toolProgressEntry{Key: "openai:file:put", Text: "Uploading file to OpenAI storage"}
	case "list":
		return toolProgressEntry{Key: "openai:file:list", Text: "Listing OpenAI files"}
	case "get_metadata":
		return toolProgressEntry{Key: "openai:file:meta", Text: "Inspecting OpenAI file metadata"}
	case "delete":
		return toolProgressEntry{Key: "openai:file:delete", Text: "Deleting OpenAI file"}
	default:
		return toolProgressEntry{Key: "openai:file", Text: "Using OpenAI file storage"}
	}
}

func semanticOpenAIVectorStoreProgressEntry(input json.RawMessage) toolProgressEntry {
	var parsed openAIVectorStoreProgressInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return toolProgressEntry{Key: "openai:vector", Text: "Using OpenAI vector store"}
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Action)) {
	case "create":
		return toolProgressEntry{Key: "openai:vector:create", Text: "Creating vector store"}
	case "attach":
		return toolProgressEntry{Key: "openai:vector:attach", Text: "Attaching file to vector store"}
	case "search":
		return toolProgressEntry{Key: "openai:vector:search", Text: "Searching vector store"}
	default:
		return toolProgressEntry{Key: "openai:vector", Text: "Using OpenAI vector store"}
	}
}

func classifyExecCommand(command string) toolProgressEntry {
	lower := strings.ToLower(strings.TrimSpace(command))
	switch {
	case lower == "":
		return toolProgressEntry{Key: "exec", Text: "Running command"}
	case isServiceRestart(lower):
		return toolProgressEntry{Key: "service:restart", Text: "Restarting service"}
	case isServiceStatus(lower):
		return toolProgressEntry{Key: "service:status", Text: "Checking service status"}
	case targetsConfig(lower) && commandLooksLikeWrite(lower):
		return toolProgressEntry{Key: "config:write", Text: "Updating config"}
	case targetsConfig(lower) && commandLooksLikeRead(lower):
		return toolProgressEntry{Key: "config:read", Text: "Inspecting config"}
	case targetsMemory(lower) && commandLooksLikeWrite(lower):
		return toolProgressEntry{Key: "memory:write", Text: "Writing memory files"}
	case targetsMemory(lower) && commandLooksLikeRead(lower):
		return toolProgressEntry{Key: "memory:read", Text: "Inspecting memory files"}
	case commandLooksLikeTest(lower):
		return toolProgressEntry{Key: "tests", Text: "Running tests"}
	case commandLooksLikeBuild(lower):
		return toolProgressEntry{Key: "build", Text: "Building project"}
	case commandLooksLikeGitInspect(lower):
		return toolProgressEntry{Key: "git:inspect", Text: "Inspecting git state"}
	case commandLooksLikeExternal(lower):
		return toolProgressEntry{Key: "network", Text: "Calling external service"}
	case commandLooksLikeRead(lower):
		return toolProgressEntry{Key: "files:read", Text: "Inspecting files"}
	case commandLooksLikeWrite(lower):
		return toolProgressEntry{Key: "files:write", Text: "Updating files"}
	default:
		return toolProgressEntry{Key: "exec", Text: "Running command"}
	}
}

func isServiceRestart(command string) bool {
	return strings.Contains(command, "systemctl") && strings.Contains(command, "restart") && strings.Contains(command, "aphelion")
}

func isServiceStatus(command string) bool {
	return strings.Contains(command, "systemctl") && strings.Contains(command, "status") && strings.Contains(command, "aphelion")
}

func targetsConfig(command string) bool {
	return strings.Contains(command, "aphelion.toml") || strings.Contains(command, "config.toml")
}

func targetsMemory(command string) bool {
	return strings.Contains(command, "/memory/") ||
		strings.Contains(command, "memory/") ||
		strings.Contains(command, "memory.md") ||
		strings.Contains(command, "heartbeat.md") ||
		strings.Contains(command, "knowledge.md") ||
		strings.Contains(command, "decisions.md") ||
		strings.Contains(command, "questions.md") ||
		strings.Contains(command, "rhizome.md") ||
		strings.Contains(command, "dreams.md")
}

func commandLooksLikeRead(command string) bool {
	head := commandHead(command)
	switch head {
	case "rg", "grep", "cat", "head", "tail", "ls", "find", "tree", "wc", "stat":
		return !strings.Contains(command, ">")
	case "sed":
		return strings.Contains(command, "-n")
	default:
		return false
	}
}

func commandLooksLikeWrite(command string) bool {
	head := commandHead(command)
	if strings.Contains(command, "cat >") || strings.Contains(command, ">>") || strings.Contains(command, ">") && strings.Contains(command, "cat ") {
		return true
	}
	switch head {
	case "tee", "mv", "cp", "mkdir", "touch", "chmod", "chown":
		return true
	case "sed":
		return strings.Contains(command, "-i")
	case "perl":
		return strings.Contains(command, "-pi")
	default:
		return false
	}
}

func commandLooksLikeTest(command string) bool {
	return strings.HasPrefix(command, "go test") ||
		strings.HasPrefix(command, "pytest") ||
		strings.HasPrefix(command, "cargo test") ||
		strings.HasPrefix(command, "npm test") ||
		strings.HasPrefix(command, "pnpm test") ||
		strings.HasPrefix(command, "yarn test") ||
		strings.HasPrefix(command, "make test")
}

func commandLooksLikeBuild(command string) bool {
	return strings.HasPrefix(command, "go build") ||
		strings.HasPrefix(command, "cargo build") ||
		strings.HasPrefix(command, "npm run build") ||
		strings.HasPrefix(command, "pnpm build") ||
		strings.HasPrefix(command, "yarn build") ||
		strings.HasPrefix(command, "make build")
}

func commandLooksLikeGitInspect(command string) bool {
	return strings.HasPrefix(command, "git status") ||
		strings.HasPrefix(command, "git diff") ||
		strings.HasPrefix(command, "git show") ||
		strings.HasPrefix(command, "git log")
}

func commandLooksLikeExternal(command string) bool {
	return strings.HasPrefix(command, "curl ") || strings.HasPrefix(command, "wget ")
}

func commandHead(command string) string {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
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
