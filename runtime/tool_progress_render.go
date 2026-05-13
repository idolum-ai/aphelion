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
		if last.Text == entry.Text {
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
	if n := len(p.entries); n > 0 && p.entries[n-1].Text == entry.Text {
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
