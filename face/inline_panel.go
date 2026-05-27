//go:build linux

package face

import "strings"

// InlinePanel renders a thin presentation surface for home/list views that
// do not need the five-element operator-presentation contract
// (title/status/why/next/details/evidence). Use OperatorPanel for surfaces
// where each of those elements is load-bearing — approval flows, authority
// status, recovery notices, deep evidence views.
//
// InlinePanel deliberately omits Why and Evidence:
//
//   - Why is the most over-used label in operator panels; most surfaces have
//     no meaningful "why this matters" beyond what title and state convey.
//   - Evidence belongs in /health trace, --format=kv outputs, and audit lines;
//     home/list views either have no evidence or carry it in a deeper view.
//
// Surfaces that genuinely need either field should stay on OperatorPanel.
type InlinePanel struct {
	Title   string   // optional one-line header
	State   string   // optional one-line state; rendered without a "Status:" label
	Next    string   // optional one-sentence action hint; rendered without a "Next:" label
	Details []string // optional short list rendered as bullets without a "Details:" header
}

// RenderInlinePanel produces a compact Telegram-friendly string from the
// panel. All fields are optional. Blank fields are skipped. Adjacent blank
// lines are collapsed.
func RenderInlinePanel(panel InlinePanel) string {
	lines := make([]string, 0, 4+len(panel.Details))
	if title := strings.TrimSpace(panel.Title); title != "" {
		lines = append(lines, title)
	}
	if state := strings.TrimSpace(panel.State); state != "" {
		lines = append(lines, state)
	}
	if next := strings.TrimSpace(panel.Next); next != "" {
		lines = append(lines, next)
	}
	for _, detail := range panel.Details {
		detail = strings.TrimSpace(detail)
		if detail == "" {
			continue
		}
		lines = append(lines, "- "+detail)
	}
	return strings.Join(compactOperatorPanelLines(lines), "\n")
}
