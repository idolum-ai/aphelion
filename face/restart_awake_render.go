//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"
)

func RenderRestartAwake(notice RestartAwakeNotice) string {
	parts := []string{"Awake after restart"}
	if started := restartAwakeStartedLabel(notice.StartedAtUTC); started != "" {
		parts = append(parts, started)
	}
	parts = append(parts, "")
	if notice.InterruptedCount <= 0 {
		parts = append(parts, "No interrupted work needed recovery.")
	} else if notice.RecoveredCount == notice.InterruptedCount {
		parts = append(parts, fmt.Sprintf("Recovered %s.", restartAwakeCountNoun(notice.RecoveredCount, "interrupted turn", "interrupted turns")))
	} else {
		parts = append(parts, fmt.Sprintf("Recovered %d of %s.", notice.RecoveredCount, restartAwakeCountNoun(notice.InterruptedCount, "interrupted turn", "interrupted turns")))
	}
	memoryLines, memoryAttention := restartAwakeMemoryLines(notice.MemoryNote)
	parts = append(parts, memoryLines...)
	parts = append(parts, restartAwakeMissionControlLine(notice))
	parts = append(parts, "")
	parts = append(parts, restartAwakeActionLine(notice, memoryAttention))
	return strings.Join(parts, "\n")
}

type restartAwakeMemoryAttention struct {
	ReofferedApprovals int
	FailedApprovals    int
	Warning            bool
}

func restartAwakeStartedLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	started, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return started.UTC().Format("15:04 UTC")
}

func restartAwakeMemoryLines(note string) ([]string, restartAwakeMemoryAttention) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, restartAwakeMemoryAttention{}
	}
	var attention restartAwakeMemoryAttention
	lines := make([]string, 0, 3)
	continuityLoaded := false
	for _, rawPart := range strings.Split(note, ";") {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		switch {
		case lower == "continuity loaded":
			continuityLoaded = true
		case lower == "no recovery rows pending":
			continue
		case strings.HasPrefix(lower, "invalid pending approvals repaired="):
			count := restartAwakeParseCounterValue(part)
			if count > 0 {
				lines = append(lines, fmt.Sprintf("Repaired %s.", restartAwakeCountNoun(count, "stale approval", "stale approvals")))
			}
		case strings.HasPrefix(lower, "parked_continuations:"):
			parkedLines, parkedAttention := restartAwakeParkedContinuationLines(part)
			lines = append(lines, parkedLines...)
			attention.ReofferedApprovals += parkedAttention.ReofferedApprovals
			attention.FailedApprovals += parkedAttention.FailedApprovals
			attention.Warning = attention.Warning || parkedAttention.Warning
		case strings.Contains(lower, "warning="):
			attention.Warning = true
			lines = append(lines, "Startup warning recorded.")
		}
	}
	if continuityLoaded {
		lines = append([]string{"Continuity is loaded."}, lines...)
	}
	return restartAwakeDedupeLines(lines), attention
}

func restartAwakeParkedContinuationLines(part string) ([]string, restartAwakeMemoryAttention) {
	idx := strings.Index(part, ":")
	if idx < 0 || idx+1 >= len(part) {
		return nil, restartAwakeMemoryAttention{}
	}
	var attention restartAwakeMemoryAttention
	reoffered := 0
	failed := 0
	for _, field := range strings.Fields(part[idx+1:]) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		count := restartAwakeAtoi(strings.TrimSpace(value))
		if count <= 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "reoffered", "approved_reoffered", "expired_reoffered":
			reoffered += count
		case "failed":
			failed += count
		}
	}
	lines := make([]string, 0, 2)
	if reoffered > 0 {
		lines = append(lines, fmt.Sprintf("Re-offered %s.", restartAwakeCountNoun(reoffered, "parked approval", "parked approvals")))
		attention.ReofferedApprovals = reoffered
	}
	if failed > 0 {
		lines = append(lines, fmt.Sprintf("Could not re-offer %s.", restartAwakeCountNoun(failed, "parked approval", "parked approvals")))
		attention.FailedApprovals = failed
	}
	return lines, attention
}

func restartAwakeMissionControlLine(notice RestartAwakeNotice) string {
	candidates := restartAwakeCountNoun(notice.CandidateMissions, "candidate", "candidates")
	active := "none active"
	if notice.ActiveMissions == 1 {
		active = "1 active"
	} else if notice.ActiveMissions > 1 {
		active = fmt.Sprintf("%d active", notice.ActiveMissions)
	}
	parts := []string{candidates, active}
	if notice.PendingHandoffs > 0 {
		parts = append(parts, restartAwakeCountNoun(notice.PendingHandoffs, "handoff", "handoffs")+" pending")
	}
	return "Mission control: " + strings.Join(parts, ", ") + "."
}

func restartAwakeActionLine(notice RestartAwakeNotice, attention restartAwakeMemoryAttention) string {
	if attention.FailedApprovals > 0 {
		return "Needs attention: parked approval resume had failures."
	}
	if notice.InterruptedCount > 0 && notice.RecoveredCount < notice.InterruptedCount {
		return "Needs attention: startup recovery was incomplete."
	}
	if notice.PendingHandoffs > 0 {
		return fmt.Sprintf("Needs attention: review %s.", restartAwakeCountNoun(notice.PendingHandoffs, "pending handoff", "pending handoffs"))
	}
	if attention.ReofferedApprovals > 0 {
		return "Needs attention: review re-offered approval buttons."
	}
	if attention.Warning {
		return "Needs attention: startup warning recorded."
	}
	return "No action needed."
}

func restartAwakeParseCounterValue(part string) int {
	_, value, ok := strings.Cut(part, "=")
	if !ok {
		return 0
	}
	return restartAwakeAtoi(strings.TrimSpace(value))
}

func restartAwakeAtoi(value string) int {
	total := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			break
		}
		total = total*10 + int(ch-'0')
	}
	return total
}

func restartAwakeCountNoun(count int, singular string, plural string) string {
	if count == 0 {
		return "no " + plural
	}
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func restartAwakeDedupeLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(lines))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}
