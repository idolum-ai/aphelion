//go:build linux

package face

import (
	"fmt"
	"strings"
)

func RenderReviewDigest(notice ReviewDigestNotice) string {
	sections := parseReviewDigestSummary(notice.Summary)
	lines := []string{"**" + reviewDigestTitle(notice) + "**"}
	summary, inlineHighlights := reviewDigestSummaryAndHighlights(sections.Summary)
	highlights := append([]string(nil), inlineHighlights...)
	highlights = append(highlights, sections.Highlights...)

	context := reviewDigestContextLine(notice, sections.Context)
	if context != "" {
		lines = append(lines, context)
	}
	if summary := strings.TrimSpace(summary); summary != "" {
		lines = append(lines, "", "**Summary**", truncateReviewDigestText(summary, 900))
	}
	if len(highlights) > 0 {
		lines = append(lines, "", "**Highlights**")
		lines = append(lines, reviewDigestBullets(highlights, 6)...)
	}
	if len(sections.Local) > 0 {
		lines = append(lines, "", "**Checked**")
		lines = append(lines, reviewDigestBullets(sections.Local, 6)...)
	}
	if len(sections.Questions) > 0 {
		lines = append(lines, "", "**Needs attention**")
		lines = append(lines, reviewDigestBullets(sections.Questions, 4)...)
	}
	if len(sections.Risks) > 0 {
		lines = append(lines, "", "**Risks**")
		lines = append(lines, reviewDigestBullets(sections.Risks, 4)...)
	}
	if strings.TrimSpace(summary) == "" && len(highlights) == 0 && len(sections.Local) == 0 && len(sections.Questions) == 0 && len(sections.Risks) == 0 {
		if raw := strings.TrimSpace(notice.Summary); raw != "" {
			lines = append(lines, "", truncateReviewDigestText(raw, 1200))
		}
	}
	return truncateReviewDigestBlock(strings.Join(lines, "\n"), 3900)
}

type reviewDigestSections struct {
	Context    []string
	Summary    string
	Highlights []string
	Local      []string
	Questions  []string
	Risks      []string
}

func reviewDigestTitle(notice ReviewDigestNotice) string {
	if agent := strings.TrimSpace(notice.SourceAgent); agent != "" {
		switch {
		case strings.Contains(strings.ToLower(agent), "daily-review"):
			return "Daily review"
		}
		return "Review: " + agent
	}
	switch strings.TrimSpace(notice.SourceRole) {
	case "capability_request":
		return "Review: capability request"
	case "":
		return "Review"
	default:
		return "Review: " + strings.ReplaceAll(strings.TrimSpace(notice.SourceRole), "_", " ")
	}
}

func reviewDigestContextLine(notice ReviewDigestNotice, summaryContext []string) string {
	if reviewDigestIsDurable(notice) {
		return reviewDigestDurableContextLine(notice, summaryContext)
	}
	parts := make([]string, 0, 6)
	for _, context := range summaryContext {
		context = strings.TrimSpace(context)
		if context != "" {
			parts = append(parts, context)
		}
	}
	if agent := strings.TrimSpace(notice.SourceAgent); agent != "" && !reviewDigestContextContains(parts, "agent=") && !reviewDigestContextContains(parts, "durable_agent=") {
		parts = append(parts, "agent="+agent)
	}
	if scope := strings.TrimSpace(notice.SourceScope); scope != "" && strings.TrimSpace(notice.SourceAgent) == "" && !reviewDigestContextContains(parts, "scope=") {
		parts = append(parts, "scope="+scope)
	}
	if parent := strings.TrimSpace(notice.ParentScope); parent != "" && !reviewDigestContextContains(parts, "parent=") {
		parts = append(parts, "parent="+parent)
	}
	if turns := strings.TrimSpace(notice.TurnRange); turns != "" && turns != "n/a" {
		parts = append(parts, "turns="+turns)
	}
	if notice.SourceChatID != 0 && !reviewDigestContextContains(parts, "chat=") && !reviewDigestContextContains(parts, "source_chat=") {
		parts = append(parts, fmt.Sprintf("chat=%d", notice.SourceChatID))
	}
	if notice.SourceUserID != 0 && !reviewDigestContextContains(parts, "user=") && !reviewDigestContextContains(parts, "source_user=") {
		parts = append(parts, fmt.Sprintf("user=%d", notice.SourceUserID))
	}
	if role := strings.TrimSpace(notice.SourceRole); role != "" && role != "durable_agent" && !reviewDigestContextContains(parts, "role=") {
		parts = append(parts, "role="+role)
	}
	if len(parts) == 0 {
		return ""
	}
	return "`" + strings.Join(parts, " ") + "`"
}

func reviewDigestIsDurable(notice ReviewDigestNotice) bool {
	return strings.TrimSpace(notice.SourceAgent) != "" || strings.TrimSpace(notice.SourceRole) == "durable_agent"
}

func reviewDigestDurableContextLine(notice ReviewDigestNotice, summaryContext []string) string {
	meta := reviewDigestContextMetadata(summaryContext)
	parts := make([]string, 0, 3)
	if channel := strings.TrimSpace(meta["channel"]); channel != "" {
		parts = append(parts, reviewDigestHumanChannel(channel))
	}
	if interval := strings.TrimSpace(meta["interval"]); interval != "" {
		parts = append(parts, interval)
	}
	if len(parts) == 0 {
		if scope := strings.TrimSpace(notice.SourceScope); scope != "" {
			parts = append(parts, reviewDigestHumanScope(scope))
		}
	}
	return strings.Join(parts, " • ")
}

func reviewDigestContextMetadata(lines []string) map[string]string {
	meta := make(map[string]string)
	for _, line := range lines {
		for _, field := range strings.Fields(strings.TrimSpace(line)) {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				meta[key] = value
			}
		}
	}
	return meta
}

func reviewDigestHumanChannel(channel string) string {
	switch strings.TrimSpace(strings.ToLower(channel)) {
	case "daily_review":
		return "Daily review"
	default:
		return strings.ReplaceAll(strings.TrimSpace(channel), "_", " ")
	}
}

func reviewDigestHumanScope(scope string) string {
	scope = strings.TrimSpace(scope)
	switch {
	case strings.HasPrefix(scope, "durable_agent:"):
		return strings.TrimPrefix(scope, "durable_agent:")
	case strings.HasPrefix(scope, "telegram_dm:"):
		return "Telegram DM"
	case strings.HasPrefix(scope, "heartbeat:"):
		return "Heartbeat"
	default:
		return scope
	}
}

func reviewDigestContextContains(parts []string, prefix string) bool {
	for _, part := range parts {
		for _, field := range strings.Fields(strings.TrimSpace(part)) {
			if strings.HasPrefix(field, prefix) {
				return true
			}
		}
		if strings.HasPrefix(strings.TrimSpace(part), prefix) {
			return true
		}
	}
	return false
}

func parseReviewDigestSummary(raw string) reviewDigestSections {
	var out reviewDigestSections
	current := ""
	appendSection := func(key string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		switch key {
		case "summary":
			value = cleanReviewDigestSummary(value)
			if value == "" {
				return
			}
			if strings.HasPrefix(value, "-") {
				out.Highlights = append(out.Highlights, splitReviewDigestItems(value)...)
			} else if out.Summary == "" {
				out.Summary = value
			} else {
				out.Summary += "\n" + value
			}
		case "highlights":
			out.Highlights = append(out.Highlights, splitReviewDigestItems(value)...)
		case "local":
			out.Local = append(out.Local, splitReviewDigestItems(value)...)
		case "questions":
			out.Questions = append(out.Questions, splitReviewDigestItems(value)...)
		case "risks":
			out.Risks = append(out.Risks, splitReviewDigestItems(value)...)
		case "context":
			out.Context = append(out.Context, value)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if key, value, ok := splitReviewDigestSection(line); ok {
			current = key
			appendSection(key, value)
			continue
		}
		if current != "" {
			appendSection(current, line)
			continue
		}
		if strings.Contains(line, "=") && !strings.Contains(line, ": ") {
			appendSection("context", line)
			continue
		}
		appendSection("summary", line)
	}
	return out
}

func splitReviewDigestSection(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(line[:idx]))
	switch key {
	case "summary", "highlights", "highlight", "local", "questions", "risks":
		if key == "highlight" {
			key = "highlights"
		}
		return key, strings.TrimSpace(line[idx+1:]), true
	default:
		return "", "", false
	}
}

func cleanReviewDigestSummary(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "Local response:"); idx >= 0 {
		response := strings.TrimSpace(value[idx+len("Local response:"):])
		if response != "" {
			return response
		}
		return "Parent guidance processed."
	}
	return value
}

func splitReviewDigestItems(raw string) []string {
	chunks := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ';' || r == '\n'
	})
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(chunk), "-"))
		if chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}

func reviewDigestSummaryAndHighlights(raw string) (string, []string) {
	summary := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if summary == "" {
		return "", nil
	}
	const marker = " - "
	idx := strings.Index(summary, marker)
	if idx < 0 {
		return summary, nil
	}
	lead := trimReviewDigestInlineBulletLead(summary[:idx])
	items := reviewDigestInlineBulletItems(strings.Split(summary[idx+len(marker):], marker))
	if len(items) == 0 {
		return summary, nil
	}
	return lead, items
}

func trimReviewDigestInlineBulletLead(raw string) string {
	lead := strings.TrimSpace(raw)
	lower := strings.ToLower(lead)
	for _, label := range []string{"what matters:", "highlights:", "key points:"} {
		if strings.HasSuffix(lower, label) {
			return strings.TrimSpace(lead[:len(lead)-len(label)])
		}
	}
	return lead
}

func reviewDigestInlineBulletItems(parts []string) []string {
	raw := make([]string, 0, len(parts))
	for _, part := range parts {
		item := cleanReviewDigestBulletItem(part)
		if item != "" {
			raw = append(raw, item)
		}
	}

	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for i := 0; i < len(raw); i++ {
		item := raw[i]
		if reviewDigestLowSignalBullet(item) {
			continue
		}
		if reviewDigestHeadingFragment(item) {
			heading := strings.TrimSpace(strings.TrimSuffix(item, ":"))
			if strings.Contains(strings.ToLower(heading), "profile") {
				appendReviewDigestBullet(&out, seen, heading)
				for i+1 < len(raw) && reviewDigestLowSignalBullet(raw[i+1]) {
					i++
				}
				continue
			}
			j := i + 1
			for j < len(raw) && reviewDigestLowSignalBullet(raw[j]) {
				j++
			}
			if j < len(raw) && !reviewDigestHeadingFragment(raw[j]) {
				appendReviewDigestBullet(&out, seen, heading+": "+raw[j])
				i = j
				continue
			}
			appendReviewDigestBullet(&out, seen, heading)
			continue
		}
		appendReviewDigestBullet(&out, seen, item)
	}
	return out
}

func cleanReviewDigestBulletItem(raw string) string {
	item := strings.TrimSpace(raw)
	item = strings.TrimSpace(strings.TrimPrefix(item, "-"))
	return strings.Join(strings.Fields(item), " ")
}

func reviewDigestHeadingFragment(item string) bool {
	item = strings.TrimSpace(item)
	return strings.HasSuffix(item, ":") && len([]rune(item)) <= 120
}

func reviewDigestLowSignalBullet(item string) bool {
	item = strings.TrimSpace(item)
	if item == "" {
		return true
	}
	fields := strings.Fields(item)
	if len(fields) != 1 {
		return false
	}
	lower := strings.ToLower(item)
	return strings.HasPrefix(lower, "profile/") ||
		strings.HasSuffix(lower, ".md") ||
		strings.HasSuffix(lower, ".json") ||
		strings.Contains(lower, "/")
}

func appendReviewDigestBullet(out *[]string, seen map[string]struct{}, item string) {
	item = strings.TrimSpace(item)
	if item == "" {
		return
	}
	key := strings.ToLower(item)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, item)
}

func reviewDigestBullets(items []string, limit int) []string {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	lines := make([]string, 0, limit+1)
	for i, item := range items {
		if i >= limit {
			lines = append(lines, fmt.Sprintf("- %d more", len(items)-limit))
			break
		}
		lines = append(lines, "- "+truncateReviewDigestText(item, 260))
	}
	return lines
}

func truncateReviewDigestText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func truncateReviewDigestBlock(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}
