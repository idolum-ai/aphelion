//go:build linux

package telegramcommands

import (
	"strings"

	"github.com/idolum-ai/aphelion/face"
)

var telegramCompactPanelOptions = face.OperatorPanelCompactOptions{
	DetailLimit:   4,
	EvidenceLimit: 2,
}

func renderTelegramCompactPanel(panel face.OperatorPanel, detailed bool) string {
	if detailed {
		return face.RenderOperatorPanel(panel)
	}
	return face.RenderCompactOperatorPanel(panel, telegramCompactPanelOptions)
}

func renderTelegramCompactPanelWithLimits(panel face.OperatorPanel, detailLimit int, evidenceLimit int) string {
	options := telegramCompactPanelOptions
	if detailLimit > 0 {
		options.DetailLimit = detailLimit
	}
	if evidenceLimit > 0 {
		options.EvidenceLimit = evidenceLimit
	}
	return face.RenderCompactOperatorPanel(panel, options)
}

func truncateOperatorLine(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}

func operatorBoolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
