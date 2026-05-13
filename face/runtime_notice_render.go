//go:build linux

package face

import (
	"fmt"
	"strings"
)

func RenderStartupRecovery(notice StartupRecoveryNotice) string {
	parts := []string{"Restart catch-up.", "Awake signal: startup recovery ran."}
	if notice.InterruptedCount == 1 {
		parts = append(parts, "I recovered 1 interrupted turn.")
	} else {
		parts = append(parts, fmt.Sprintf("I recovered %d interrupted turns.", notice.InterruptedCount))
	}
	if request := strings.TrimSpace(notice.MostRecentRequest); request != "" {
		parts = append(parts, "Most recent interrupted request: "+fmt.Sprintf("%q", request)+".")
	}
	if tool := strings.TrimSpace(notice.LastTool); tool != "" {
		parts = append(parts, "Last tool in flight: "+tool+".")
	}
	if summary := strings.TrimSpace(notice.RecoverySummary); summary != "" {
		parts = append(parts, "Recovery note: "+summary)
	}
	parts = append(parts, "Next: investigate the interruption before returning to deferred work.")
	return strings.Join(parts, " ")
}

func RenderToolProgress(notice ToolProgressNotice) string {
	lines := []string{"Working on it."}
	if notice.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("%d earlier steps omitted.", notice.Omitted))
	}
	for _, entry := range notice.Entries {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		if entry.Count > 1 {
			lines = append(lines, fmt.Sprintf("- %s (%dx)", text, entry.Count))
		} else {
			lines = append(lines, "- "+text)
		}
	}
	return strings.Join(lines, "\n")
}
