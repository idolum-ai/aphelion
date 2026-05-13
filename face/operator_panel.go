//go:build linux

package face

import (
	"strconv"
	"strings"
)

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

type OperatorPanelCompactOptions struct {
	DetailLimit   int
	EvidenceLimit int
}

func RenderOperatorPanel(panel OperatorPanel) string {
	return renderOperatorPanel(panel, OperatorPanelCompactOptions{DetailLimit: -1, EvidenceLimit: -1})
}

func RenderCompactOperatorPanel(panel OperatorPanel, opts OperatorPanelCompactOptions) string {
	if opts.DetailLimit < 0 {
		opts.DetailLimit = 0
	}
	if opts.EvidenceLimit < 0 {
		opts.EvidenceLimit = 0
	}
	return renderOperatorPanel(panel, opts)
}

func renderOperatorPanel(panel OperatorPanel, opts OperatorPanelCompactOptions) string {
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
		block := limitOperatorPanelLines(compactOperatorPanelLines(values), operatorPanelLimitForLabel(label, opts), label)
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

func operatorPanelLimitForLabel(label string, opts OperatorPanelCompactOptions) int {
	switch label {
	case "Details":
		return opts.DetailLimit
	case "Evidence":
		return opts.EvidenceLimit
	default:
		return -1
	}
}

func limitOperatorPanelLines(values []string, limit int, label string) []string {
	if limit < 0 || len(values) <= limit {
		return values
	}
	if limit == 0 {
		return nil
	}
	out := append([]string(nil), values[:limit]...)
	omitted := len(values) - limit
	if omitted > 0 {
		out = append(out, strings.TrimSpace(pluralOperatorPanelOmitted(label, omitted)))
	}
	return out
}

func pluralOperatorPanelOmitted(label string, count int) string {
	item := "item"
	if label == "Details" {
		item = "detail"
	}
	if label == "Evidence" {
		item = "evidence item"
	}
	if count == 1 {
		return "1 more " + item + " available."
	}
	return strconv.Itoa(count) + " more " + item + "s available."
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
