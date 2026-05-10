//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type ReviewDigestNotice struct {
	SourceChatID int64
	SourceUserID int64
	SourceRole   string
	SourceScope  string
	SourceAgent  string
	ParentScope  string
	TurnRange    string
	Summary      string
}

type StartupRecoveryNotice struct {
	InterruptedCount  int
	MostRecentRequest string
	LastTool          string
	RecoverySummary   string
}

type RestartAwakeNotice struct {
	StartedAtUTC      string
	InterruptedCount  int
	RecoveredCount    int
	CandidateMissions int
	ActiveMissions    int
	PendingHandoffs   int
	MemoryNote        string
}

type ToolProgressEntry struct {
	Text  string
	Count int
}

type ToolProgressNotice struct {
	Omitted int
	Entries []ToolProgressEntry
}

func RenderTelegramStart(personaEffort, governorEffort string, includeAdminCommands bool) string {
	lines := []string{
		"The assistant is ready.",
		"",
		"Commands:",
		"/help - show command help",
		"/status - show live status and controls",
		"/debug - show detailed runtime debug snapshot",
		"/doctor - run an admin runtime diagnosis",
		"/tailnet - show tailnet status and controls",
		"/agents - list durable agents and controls",
		"/memory - review memory candidates and set focus",
		"/mission - show and manage the Mission Ledger",
		"/model - show and change model slots",
		"/stop - stop current work in this chat",
		"/new - start a fresh chat session context",
		"/detach - detach this chat from pending work",
	}
	if includeAdminCommands {
		lines = append(lines,
			"/autonomy - show autonomy policy",
			"/autoapprove - temporarily auto-approve admin approval prompts",
			"/restart - force an immediate gateway restart",
		)
	}
	lines = append(lines,
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose persona model",
		"/set_governor_effort - choose system reasoning effort",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current system effort: %s", strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramHelp(personaEffort, governorEffort string, includeAdminCommands bool) string {
	lines := []string{
		"Here is the current command surface.",
		"",
		"/start - show intro and command help",
		"/help - show this help",
		"/status - show live status and controls",
		"/debug - show detailed runtime debug snapshot",
		"/doctor - run an admin runtime diagnosis",
		"/tailnet - show tailnet status and controls",
		"/agents - list durable agents and controls",
		"/memory - review memory candidates and set focus",
		"/mission - show and manage the Mission Ledger",
		"/model - show and change model slots",
		"/stop - stop current work in this chat",
		"/new - start a fresh chat session context",
		"/detach - detach this chat from pending work",
	}
	if includeAdminCommands {
		lines = append(lines,
			"/autonomy - show autonomy policy",
			"/autoapprove - temporarily auto-approve admin approval prompts",
			"/restart - force an immediate gateway restart",
		)
	}
	lines = append(lines,
		"/reinstall - queue a rebuild/reinstall/restart request",
		"/set_persona_model - choose persona model",
		"/set_governor_effort - choose system reasoning effort",
		"",
		fmt.Sprintf("Current persona effort: %s", strings.TrimSpace(personaEffort)),
		fmt.Sprintf("Current system effort: %s", strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramAutonomyStatus(snapshot core.AutonomyStatusSnapshot) string {
	liveChanges := "disabled"
	if snapshot.AllowLiveOverrides {
		liveChanges = "enabled"
	}
	activeOverride := "none"
	if mode := strings.TrimSpace(snapshot.ActiveOverrideMode); mode != "" {
		activeOverride = autonomyModeLabel(mode)
		if scope := strings.TrimSpace(snapshot.ActiveOverrideScope); scope != "" {
			activeOverride += " for " + scope
		}
		if !snapshot.ActiveOverrideExpiry.IsZero() {
			activeOverride += " until " + snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339)
		}
		if snapshot.ActiveOverrideMax > 0 {
			activeOverride += fmt.Sprintf(" (%d/%d used)", snapshot.ActiveOverrideUsed, snapshot.ActiveOverrideMax)
		}
	}
	behavior := strings.TrimSpace(snapshot.AuthorityBehavior)
	if behavior == "" {
		behavior = "existing proposal and approval flows"
	}
	return strings.Join([]string{
		"Autonomy policy",
		"",
		"Default: " + autonomyModeLabel(snapshot.DefaultMode),
		"Ceiling: " + autonomyModeLabel(snapshot.Ceiling),
		"Live changes: " + liveChanges,
		"Maximum live change: " + snapshot.MaxOverrideDuration.Truncate(time.Second).String(),
		"Active override: " + activeOverride,
		"",
		"Authority behavior: " + behavior + ".",
		"This report does not grant new authority by itself.",
	}, "\n")
}

func autonomyModeLabel(mode string) string {
	switch strings.TrimSpace(mode) {
	case "off":
		return "Off"
	case "review_only":
		return "Review only"
	case "ask_first":
		return "Ask first"
	case "leased":
		return "Leased"
	case "mission":
		return "Mission"
	default:
		if strings.TrimSpace(mode) == "" {
			return "Ask first"
		}
		return strings.TrimSpace(mode)
	}
}

func RenderTelegramStop(stopped core.StopResult) string {
	continuationClause := renderStoppedContinuationClause(stopped)
	switch {
	case stopped.ActiveCanceled && stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Stopped the current turn, cleared queued work, and " + continuationClause + "."
	case stopped.ActiveCanceled && stopped.ContinuationRevoked:
		return "Stopped the current turn and " + continuationClause + "."
	case stopped.QueuedDropped && stopped.ContinuationRevoked:
		return "Cleared queued work and " + continuationClause + "."
	case stopped.ContinuationRevoked:
		return capitalizeStopSentence(continuationClause) + "."
	case stopped.ActiveCanceled && stopped.QueuedDropped:
		return "Stopped the current turn and cleared queued work for this chat."
	case stopped.ActiveCanceled:
		return "Stopped the current turn."
	case stopped.QueuedDropped:
		return "Cleared queued work for this chat."
	default:
		return "Continuation approval was already inactive for this chat."
	}
}

func renderStoppedContinuationClause(stopped core.StopResult) string {
	label := strings.TrimSpace(stopped.ContinuationLabel)
	if label == "" {
		return "revoked continuation approval for this chat"
	}
	return "stopped " + label
}

func capitalizeStopSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func RenderTelegramNewSession(result core.NewSessionResult) string {
	parts := make([]string, 0, 5)
	if result.ActiveCanceled {
		parts = append(parts, "stopped current turn")
	}
	if result.QueuedDropped {
		parts = append(parts, "cleared queued work")
	}
	if result.ContinuationRevoked {
		parts = append(parts, "revoked continuation")
	}
	if result.PendingDecisionsDetached > 0 {
		label := "pending decision"
		if result.PendingDecisionsDetached != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("detached %d %s", result.PendingDecisionsDetached, label))
	}
	if result.ContextCleared {
		parts = append(parts, "cleared prior session context")
	} else {
		parts = append(parts, "session context was already clear")
	}
	return "Started a new session for this chat: " + strings.Join(parts, ", ") + ". Memories were not changed."
}

func RenderTelegramDetach(detached core.DetachResult) string {
	parts := make([]string, 0, 4)
	if detached.ActiveCanceled {
		parts = append(parts, "stopped current turn")
	}
	if detached.QueuedDropped {
		parts = append(parts, "cleared queued work")
	}
	if detached.ContinuationRevoked {
		parts = append(parts, "revoked continuation")
	}
	if detached.PendingDecisionsDetached > 0 {
		label := "pending decision"
		if detached.PendingDecisionsDetached != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("detached %d %s", detached.PendingDecisionsDetached, label))
	}
	if len(parts) == 0 {
		return "Nothing was pending to detach for this chat."
	}
	return "Detached this chat from pending work: " + strings.Join(parts, ", ") + "."
}

func RenderTelegramRestart() string {
	return "Restarting the gateway now. Active work and continuation leases will be parked for startup recovery."
}

func RenderTelegramRestartDenied() string {
	return "Restart denied. Only Telegram admins can run /restart."
}

func RenderTelegramQueuedReinstall() string {
	return "Queued a reinstall request as a normal turn in this chat."
}

func RenderTelegramPersonaModelSelector(current string, options []string) string {
	lines := []string{
		"Select the persona model.",
		fmt.Sprintf("Current: %s", strings.TrimSpace(current)),
	}
	if len(options) > 0 {
		lines = append(lines, "", "Available:")
		for _, option := range options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			lines = append(lines, "- "+option)
		}
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramGovernorEffortSelector(current string, options []string) string {
	lines := []string{
		"Select system reasoning effort for interactive/recovery turns.",
		fmt.Sprintf("Current: %s", strings.TrimSpace(current)),
	}
	if len(options) > 0 {
		lines = append(lines, "", "Available:")
		for _, option := range options {
			option = strings.TrimSpace(option)
			if option == "" {
				continue
			}
			lines = append(lines, "- "+option)
		}
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramSetPersonaModel(model string) string {
	model = strings.TrimSpace(model)
	return fmt.Sprintf("Persona model is now %s.", model)
}

func RenderTelegramSetGovernorEffort(effort string) string {
	effort = strings.TrimSpace(effort)
	return fmt.Sprintf("System effort is now %s.", effort)
}

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
		switch strings.ToLower(agent) {
		case "idolum-email":
			return "Email child review"
		case "idolum-daily-review":
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
	case "email":
		return "Email"
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
