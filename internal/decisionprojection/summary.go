//go:build linux

package decisionprojection

import (
	"fmt"
	"strings"
)

func DecisionSummary(kind string, prompt string, details string) string {
	kind = strings.TrimSpace(kind)
	prompt = strings.TrimSpace(prompt)
	details = strings.TrimSpace(details)
	if details != "" {
		switch kind {
		case "proposal_approval":
			if summary := ProposalApprovalSummary(details); summary != "" {
				return summary
			}
		default:
			if summary := compactSentence(details); summary != "" {
				return summary
			}
		}
	}
	if kind == "" {
		return "prompt=" + truncateStatusText(prompt, 80)
	}
	if prompt == "" {
		return "kind=" + kind
	}
	return fmt.Sprintf("kind=%s prompt=%s", kind, truncateStatusText(prompt, 80))
}

func ProposalApprovalSummary(details string) string {
	sections := splitDecisionSections(details)
	summary := compactSentence(cleanProposalApprovalSummary(firstNonEmpty(sections["summary"])))
	kind := firstNonEmpty(sections["kind"], metadataLineValue(details, "Kind"))
	command := firstNonEmpty(sections["command"])
	commandClass := firstNonEmpty(sections["command class"], ExecCommandClass(command))
	intent := firstNonEmpty(sections["intent"])
	workdir := firstNonEmpty(sections["workdir"])

	if message := commitMessageFromProposalCommand(command); message != "" {
		return "I’d like to commit: `" + message + "`."
	}
	if proposalSummaryLooksHighRisk(kind, summary) {
		return "High-risk approval: " + ensureDecisionSentence(summary)
	}
	if proposalSummaryLooksLikePossibleMatch(kind, summary) {
		return "Command needs confirmation: " + lowercaseDecisionStart(ensureDecisionSentence(summary))
	}
	if kind == "workspace_escape" {
		if text := workspaceEscapeSummary(intent, commandClass, workdir, summary); text != "" {
			return text
		}
	}
	if summary != "" {
		return "I’d like to " + lowercaseDecisionStart(ensureDecisionSentence(summary))
	}
	if effect := compactSentence(firstNonEmpty(sections["if approved"])); effect != "" {
		return "I’d like to " + lowercaseDecisionStart(ensureDecisionSentence(effect))
	}
	return compactSentence(details)
}

func workspaceEscapeSummary(intent string, commandClass string, workdir string, fallback string) string {
	intent = firstNonEmpty(intent, execApprovalIntent("workspace_escape", commandClass, fallback))
	if intent == "" {
		return ""
	}
	lines := []string{"I’d like to " + lowercaseDecisionStart(ensureDecisionSentence(intent))}
	if commandClass != "" && commandClass != ExecCommandClassUnknown {
		lines = append(lines, "Command class: "+commandClass)
	}
	if workdir != "" {
		lines = append(lines, "Workdir: "+compactWorkdir(workdir))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func compactWorkdir(workdir string) string {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" || len(workdir) <= 76 {
		return workdir
	}
	parts := strings.Split(workdir, "/")
	kept := make([]string, 0, 3)
	for i := len(parts) - 1; i >= 0 && len(kept) < 3; i-- {
		part := strings.TrimSpace(parts[i])
		if part != "" {
			kept = append([]string{part}, kept...)
		}
	}
	if len(kept) == 0 {
		return workdir
	}
	return ".../" + strings.Join(kept, "/")
}

func commitMessageFromProposalCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	needle := "git" + " commit"
	if !strings.Contains(command, needle) {
		return ""
	}
	idx := strings.Index(command, " -m ")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(command[idx+4:])
	if rest == "" {
		return ""
	}
	quote := rest[0]
	if quote == '\'' || quote == '"' {
		for i := 1; i < len(rest); i++ {
			if rest[i] == quote && rest[i-1] != '\\' {
				return strings.TrimSpace(rest[1:i])
			}
		}
	}
	return compactSentence(rest)
}

func proposalSummaryLooksHighRisk(kind string, summary string) bool {
	joined := strings.ToLower(strings.Join([]string{kind, summary}, " "))
	return strings.Contains(joined, "remote_shell") ||
		strings.Contains(joined, "high_impact") ||
		strings.Contains(joined, "service_interruption") ||
		strings.Contains(joined, "process_interruption")
}

func proposalSummaryLooksLikePossibleMatch(kind string, summary string) bool {
	joined := strings.ToLower(strings.Join([]string{kind, summary}, " "))
	return strings.Contains(joined, "possible") ||
		strings.Contains(joined, "may delete") ||
		strings.Contains(joined, "delete pattern")
}

func cleanProposalApprovalSummary(summary string) string {
	lines := make([]string, 0)
	for _, raw := range strings.Split(strings.TrimSpace(summary), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "kind:") || strings.HasPrefix(lower, "trigger:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func splitDecisionSections(details string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.TrimSpace(details), "\n")
	current := "summary"
	buf := []string{}
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" && strings.TrimSpace(out[current]) == "" {
			out[current] = text
		}
		buf = buf[:0]
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			if len(buf) > 0 {
				buf = append(buf, "")
			}
			continue
		}
		if strings.HasSuffix(line, ":") {
			flush()
			current = strings.ToLower(strings.TrimSuffix(line, ":"))
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func metadataLineValue(details string, label string) string {
	prefix := strings.ToLower(strings.TrimSpace(label)) + ":"
	for _, raw := range strings.Split(strings.TrimSpace(details), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func compactSentence(text string) string {
	return truncateStatusText(text, 220)
}

func truncateStatusText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	cut := text[:limit-3]
	if idx := strings.LastIndex(cut, " "); idx > 40 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "..."
}

func lowercaseDecisionStart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = []rune(strings.ToLower(string(r[0])))[0]
	return string(r)
}

func ensureDecisionSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s[len(s)-1] {
	case '.', '!', '?':
		return s
	default:
		return s + "."
	}
}
