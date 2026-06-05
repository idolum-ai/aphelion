//go:build linux

package runtime

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

const progressProjectionEventLimit = 2000

type progressRenderedTextPair struct {
	Summary           string
	Details           string
	SummaryViolations []ConstitutionViolation
	DetailsViolations []ConstitutionViolation
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
	return p.renderLockedWithDetails(done, false)
}

func (p *toolProgressReporter) renderLockedWithDetails(done bool, details bool) string {
	notice, projected := p.renderNoticeFromExecutionEventsLocked(details)
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

func (p *toolProgressReporter) renderProgressTextPairLocked(done bool) progressRenderedTextPair {
	pair := progressRenderedTextPair{
		Summary: p.renderLockedWithDetails(done, false),
		Details: p.renderLockedWithDetails(done, true),
	}
	if p.validateText != nil {
		pair.Summary, pair.SummaryViolations = p.validateText(pair.Summary)
		pair.Details, pair.DetailsViolations = p.validateText(pair.Details)
	}
	return pair
}

func (p *toolProgressReporter) selectProgressTextLocked(pair progressRenderedTextPair, details bool) string {
	text := pair.Summary
	if details {
		text = pair.Details
	}
	if cached := p.cachedProgressText(details); shouldUseCachedProgressText(text, cached, details) {
		return cached
	}
	return text
}

func shouldUseCachedProgressText(rendered string, cached string, details bool) bool {
	cached = strings.TrimSpace(cached)
	if cached == "" {
		return false
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" || progressTextOnlyHeading(rendered) {
		return true
	}
	if details && strings.Contains(rendered, "No tool detail recorded yet.") && !strings.Contains(cached, "No tool detail recorded yet.") {
		return true
	}
	return false
}

func progressTextOnlyHeading(text string) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	nonempty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonempty++
		}
	}
	return nonempty <= 1
}

func (p *toolProgressReporter) renderNoticeFromExecutionEventsLocked(details bool) (face.ToolProgressNotice, bool) {
	if p == nil || p.runtime == nil || p.runtime.store == nil || p.runID <= 0 {
		return face.ToolProgressNotice{}, false
	}
	events, err := p.runtime.store.ExecutionEventsByTurnRun(p.executionKey, p.runID, progressProjectionEventLimit)
	if err != nil {
		return face.ToolProgressNotice{}, false
	}
	if len(events) == 0 {
		if details {
			return face.ToolProgressNotice{
				Entries: []face.ToolProgressEntry{{Text: "No tool detail recorded yet."}},
			}, true
		}
		return face.ToolProgressNotice{}, false
	}

	projected := make([]toolProgressEntry, 0, 8)
	detailToolEntries := 0
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
			if !details && p.style != "raw" && isProgressMetadataTool(toolName) {
				continue
			}
			input := json.RawMessage(nil)
			if preview != "" && json.Valid([]byte(preview)) {
				input = json.RawMessage(preview)
			}
			entry := summaryToolProgressEntry(toolName, input, p.currentPlanStep, p.taskSummary)
			if details || p.style == "raw" {
				entry.Text = safeRawToolProgressEventText(toolName, preview)
				detailToolEntries++
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
	if details && detailToolEntries == 0 {
		addProjectedProgressEntry(&projected, toolProgressEntry{
			Key:  "details:none",
			Text: "No tool detail recorded yet.",
		})
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

func safeRawToolProgressEventText(name string, preview string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	preview = safeRawProgressPreview(name, preview)
	if preview == "" {
		return name
	}
	return name + " " + preview
}

func safeRawProgressPreview(toolName string, preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return ""
	}
	var payload map[string]any
	if json.Unmarshal([]byte(preview), &payload) == nil {
		redacted := make(map[string]any, len(payload))
		for key, value := range payload {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if progressPreviewSensitiveKey(key) {
				redacted[key] = "[redacted]"
				continue
			}
			redacted[key] = safeRawProgressValue(toolName, key, value)
		}
		if encoded, err := json.Marshal(redacted); err == nil {
			return truncatePreview(string(encoded), 220)
		}
	}
	return truncatePreview(redactProgressPreviewText(preview), 220)
}

func progressPreviewSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	sensitiveNeedles := []string{"token", "secret", "password", "passwd", "api_key", "apikey", "auth", "credential", "cookie", "bearer"}
	for _, needle := range sensitiveNeedles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func safeRawProgressValue(toolName string, key string, value any) any {
	switch typed := value.(type) {
	case string:
		return safeRawProgressString(toolName, key, typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, safeRawProgressValue(toolName, key, item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			if progressPreviewSensitiveKey(nestedKey) {
				out[nestedKey] = "[redacted]"
				continue
			}
			out[nestedKey] = safeRawProgressValue(toolName, nestedKey, nestedValue)
		}
		return out
	default:
		return value
	}
}

func safeRawProgressString(toolName string, key string, value string) string {
	value = redactProgressPreviewText(strings.TrimSpace(value))
	if value == "" {
		return value
	}
	if progressPreviewSensitiveKey(key) {
		return "[redacted]"
	}
	switch strings.TrimSpace(toolName) {
	case "exec":
		if strings.EqualFold(strings.TrimSpace(key), "command") || strings.EqualFold(strings.TrimSpace(key), "cmd") {
			return safeRawExecCommandPreview(value)
		}
	case "search", "read_file", "list_dir", "session_search", "semantic_search":
		if key == "query" || key == "path" {
			return "[redacted]"
		}
	}
	return truncatePreview(value, 96)
}

func safeRawExecCommandPreview(command string) string {
	return truncatePreview(redactProgressPreviewText(command), 160)
}

func redactProgressPreviewText(text string) string {
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	for i, field := range fields {
		if looksSensitiveProgressToken(field) {
			fields[i] = "[redacted]"
		}
	}
	return strings.Join(fields, " ")
}

func looksSensitiveProgressToken(token string) bool {
	lower := strings.ToLower(strings.Trim(token, `"'`))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "xox") || strings.HasPrefix(lower, "ghp_") || strings.HasPrefix(lower, "github_pat_") {
		return true
	}
	for _, word := range []string{"secret", "token", "password", "credential", "api-key", "api_key", "apikey", "bearer"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	for _, marker := range []string{"token=", "token:", "password=", "password:", "secret=", "secret:", "api_key=", "apikey=", "authorization:"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func rawToolProgressEventText(name string, preview string) string {
	return safeRawToolProgressEventText(name, preview)
}

func addProjectedProgressEntry(entries *[]toolProgressEntry, entry toolProgressEntry) {
	appendOrAggregateProgressEntry(entries, entry)
}

func appendOrAggregateProgressEntry(entries *[]toolProgressEntry, entry toolProgressEntry) bool {
	if entries == nil {
		return false
	}
	entry.Key = strings.TrimSpace(entry.Key)
	entry.Text = strings.TrimSpace(entry.Text)
	if entry.Key == "" {
		entry.Key = "tool"
	}
	if entry.Text == "" {
		entry.Text = "Using tool"
	}
	if entry.Count <= 0 {
		entry.Count = 1
	}

	for i := range *entries {
		if (*entries)[i].Text != entry.Text {
			continue
		}
		(*entries)[i].Count += entry.Count
		if i != len(*entries)-1 {
			updated := (*entries)[i]
			copy((*entries)[i:], (*entries)[i+1:])
			(*entries)[len(*entries)-1] = updated
		}
		return true
	}

	*entries = append(*entries, entry)
	return true
}

func (p *toolProgressReporter) progressHeading(done bool) string {
	if done {
		return "Done."
	}
	return "Working..."
}

func (p *toolProgressReporter) addEntry(entry toolProgressEntry) bool {
	if p == nil {
		return false
	}
	return appendOrAggregateProgressEntry(&p.entries, entry)
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

func (p *toolProgressReporter) observePlanToolInput(name string, input json.RawMessage) {
	if p == nil || strings.TrimSpace(name) != "update_plan" {
		return
	}
	step, ok := currentProgressPlanStepFromUpdatePlanInput(input)
	if !ok {
		return
	}
	p.currentPlanStep = step
}

func rawToolProgressEntry(name string, input json.RawMessage) toolProgressEntry {
	return toolProgressEntry{
		Key:  name,
		Text: safeRawToolProgressEventText(name, toolInputPreview(input)),
	}
}

func semanticToolProgressEntry(name string, input json.RawMessage, currentStep string, taskSummary string) toolProgressEntry {
	// Layer A intentionally ignores taskSummary here: it is derived from inbound
	// user text and remains only as a reserved Layer C headline compatibility hook.
	_ = taskSummary
	name = strings.TrimSpace(name)
	switch name {
	case "update_plan":
		return toolProgressEntry{Key: "metadata:plan", Text: "Updating plan metadata"}
	case "update_operation":
		return toolProgressEntry{Key: "metadata:operation", Text: "Updating operation metadata"}
	}
	if label := progressToolEvidenceLabel(name, input); label != "" {
		return toolProgressEntry{Key: "task:" + name, Text: label}
	}
	// TODO(progress-headline): emit a presentation-source execution event for the
	// selected branch (typed tool, metadata tool, plan step, generic fallback)
	// before using fallback distribution to decide whether Layer C is warranted.
	contextLabel := strings.TrimSpace(currentStep)
	if contextLabel != "" {
		return toolProgressEntry{Key: "task:" + name, Text: "Working on " + contextLabel}
	}
	return toolProgressEntry{Key: "task:" + name, Text: "Working through the request"}
}

func summaryToolProgressEntry(name string, input json.RawMessage, currentStep string, taskSummary string) toolProgressEntry {
	entry := semanticToolProgressEntry(name, input, currentStep, taskSummary)
	switch strings.TrimSpace(entry.Text) {
	case "Searching files", "Reading file", "Reading file evidence", "Listing directory":
		return toolProgressEntry{Key: "task:file_exploration", Text: "Exploring files", Count: entry.Count}
	default:
		return entry
	}
}

func isProgressMetadataTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "update_plan", "update_operation":
		return true
	default:
		return false
	}
}

func progressToolEvidenceLabel(name string, input json.RawMessage) string {
	switch strings.TrimSpace(name) {
	case "exec":
		return progressExecEvidenceLabel(toolProgressInputString(input, "command", "cmd"))
	case "search":
		return "Searching files"
	case "read_file":
		return "Reading file"
	case "list_dir":
		return "Listing directory"
	case "session_search":
		return "Searching transcript history"
	case "semantic_search":
		return "Searching memory"
	case "fetch_url":
		return "Fetching URL"
	case "write_file":
		return "Writing file"
	case "operation_artifact":
		return "Resolving operation artifact"
	case "request_approval":
		return "Requesting approval"
	case "mission_ledger":
		return "Recording mission ledger"
	case "capability_request":
		return "Requesting capability"
	case "capability_authority":
		return "Checking capability authority"
	case "tool_authority":
		return "Checking tool authority"
	case "memory":
		return "Updating memory"
	case "durable_agent":
		return "Coordinating durable agent"
	}
	return ""
}

func progressExecEvidenceLabel(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	if lower == "" {
		return "Running command"
	}
	if commandContainsAny(lower, "git diff --check") {
		return "Checking diff hygiene"
	}
	if commandContainsAny(lower, "git status") {
		return "Checking git status"
	}
	if commandContainsAny(lower, "git diff") {
		return "Reviewing git diff"
	}
	if commandContainsAny(lower, "go test", "pytest", "npm test", "pnpm test", "yarn test") {
		return "Running tests"
	}
	if commandHasToken(lower, "rg", "ripgrep", "grep", "find") {
		return "Searching files"
	}
	if commandHasToken(lower, "sed", "nl", "cat", "head", "tail", "less", "awk") {
		return "Reading file evidence"
	}
	if commandHasToken(lower, "sqlite3") {
		return "Inspecting database"
	}
	if commandHasToken(lower, "journalctl") {
		return "Reading service logs"
	}
	if commandHasToken(lower, "systemctl") && commandContainsAny(lower, " status", " show", " list-units", " is-active") {
		return "Checking service status"
	}
	if commandHasToken(lower, "ls", "tree") {
		return "Listing directory"
	}
	return "Running command"
}

func commandContainsAny(command string, needles ...string) bool {
	command = strings.ReplaceAll(command, "\n", " ")
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle == "" {
			continue
		}
		if strings.Contains(command, needle) {
			return true
		}
	}
	return false
}

func commandHasToken(command string, tokens ...string) bool {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')':
			return true
		default:
			return false
		}
	})
	if len(fields) == 0 {
		return false
	}
	want := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		token = strings.ToLower(strings.TrimSpace(token))
		if token != "" {
			want[token] = struct{}{}
		}
	}
	for _, field := range fields {
		field = strings.Trim(strings.TrimSpace(field), `"'`)
		if _, ok := want[field]; ok {
			return true
		}
	}
	return false
}

func toolProgressInputString(input json.RawMessage, keys ...string) string {
	if len(input) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(input, &payload); err != nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(payloadString(payload, key)); value != "" {
			return value
		}
	}
	return ""
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

func currentProgressPlanStepFromUpdatePlanInput(input json.RawMessage) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	var payload struct {
		Plan []session.PlanStep `json:"plan"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return "", false
	}
	if payload.Plan == nil {
		return "", false
	}
	return currentProgressPlanStep(session.PlanState{Steps: payload.Plan}), true
}

// summarizeProgressTask prepares an inbound-message-derived headline candidate
// for the planned Layer C compatibility path. Layer A must not use this value as
// a semantic progress fallback because that would echo the user's request.
func summarizeProgressTask(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", " ")
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	fields := progressTaskFields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	if isLowSignalProgressTask(fields) {
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

func progressTaskFields(text string) []string {
	raw := strings.Fields(text)
	fields := make([]string, 0, len(raw))
	for _, field := range raw {
		field = strings.Trim(field, " \t\r\n\"'`.,:;!?()[]{}<>")
		if !hasASCIIAlnum(field) {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func hasASCIIAlnum(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

func isLowSignalProgressTask(fields []string) bool {
	if len(fields) == 0 || len(fields) > 5 {
		return false
	}
	lower := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.Trim(field, " \t\r\n\"'`.,:;!?()[]{}<>"))
		if field != "" {
			lower = append(lower, field)
		}
	}
	phrase := strings.Join(lower, " ")
	switch phrase {
	case "hi", "hello", "hey", "howdy", "thanks", "thank you", "ok", "okay", "lol", "lmao",
		"go on", "continue", "keep going", "tell me more", "what next", "now what",
		"what happened", "what happened next", "then what", "then what happened", "and then":
		return true
	}
	return strings.HasPrefix(phrase, "then what ") || strings.HasPrefix(phrase, "and then ")
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
	text := truncatePreview(strings.Join(parts, " "), 220)
	if isInternalDeliberationSurface(text) {
		return ""
	}
	return text
}

func isInternalDeliberationSurface(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "continuation_") ||
		strings.HasPrefix(lower, "inspect:") ||
		strings.HasPrefix(lower, "question:") ||
		strings.HasPrefix(lower, "answer:") ||
		strings.HasPrefix(lower, "ratification:") {
		return true
	}
	if strings.HasPrefix(lower, "center the next turn") {
		return true
	}
	if strings.Contains(lower, "internal deliberation") ||
		strings.Contains(lower, "hidden input") ||
		strings.Contains(lower, "execution contract") ||
		strings.Contains(lower, "governor ratification") {
		return true
	}
	if strings.Contains(lower, "next turn") && (strings.Contains(lower, "answer ") || strings.Contains(lower, "overbuild") || strings.Contains(lower, "dramatic timing")) {
		return true
	}
	return false
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
