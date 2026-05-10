//go:build linux

package face

import "strings"

// OperatorPanel is presentation-only. It renders typed/runtime facts for a human
// operator, but it must not be used as authority, consent, or evidence storage.
type OperatorPanel struct {
	Title    string
	State    string
	Why      string
	Next     string
	Details  []string
	Evidence []string
}

func RenderOperatorPanel(panel OperatorPanel) string {
	lines := make([]string, 0, 8+len(panel.Details)+len(panel.Evidence))
	title := strings.TrimSpace(panel.Title)
	if title != "" {
		lines = append(lines, title)
	}
	if state := strings.TrimSpace(panel.State); state != "" {
		lines = append(lines, "Status: "+state)
	}
	if why := strings.TrimSpace(panel.Why); why != "" {
		lines = append(lines, "Why: "+why)
	}
	if next := strings.TrimSpace(panel.Next); next != "" {
		lines = append(lines, "Next: "+next)
	}
	appendBlock := func(label string, values []string) {
		block := compactOperatorPanelLines(values)
		if len(block) == 0 {
			return
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, label+":")
		for _, value := range block {
			lines = append(lines, "- "+value)
		}
	}
	appendBlock("Details", panel.Details)
	appendBlock("Evidence", panel.Evidence)
	return strings.Join(compactOperatorPanelLines(lines), "\n")
}

func compactOperatorPanelLines(values []string) []string {
	out := make([]string, 0, len(values))
	blank := false
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			if len(out) > 0 {
				blank = true
			}
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, value)
	}
	return out
}
