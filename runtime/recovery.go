//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const maxStartupRecoveryRuns = 20

func (r *Runtime) StartStartupRecovery(ctx context.Context, logger func(string, ...any)) {
	if r == nil {
		return
	}
	if logger == nil {
		logger = log.Printf
	}

	go func() {
		if err := r.runStartupRecoveryOnce(ctx, time.Now()); err != nil {
			logger("WARN startup recovery failed: %v", err)
		}
	}()
}

func (r *Runtime) runStartupRecoveryOnce(ctx context.Context, now time.Time) (err error) {
	if _, err := r.store.InterruptRunningTurnRuns(); err != nil {
		return fmt.Errorf("interrupt running turn runs: %w", err)
	}

	runs, err := r.store.PendingRecoveryTurnRuns(maxStartupRecoveryRuns)
	if err != nil {
		return fmt.Errorf("load pending recovery turn runs: %w", err)
	}
	if len(runs) == 0 {
		return nil
	}

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return fmt.Errorf("resolve recovery scope: %w", err)
	}

	maintenanceKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	unlock := r.lockSession(maintenanceKey)
	defer unlock()

	maintenanceSession, err := r.store.Load(maintenanceKey)
	if err != nil {
		return fmt.Errorf("load recovery maintenance session: %w", err)
	}
	applySessionScope(maintenanceSession, maintenanceKey)

	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return fmt.Errorf("load recovery prompt context: %w", err)
	}
	governorAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindRecovery, "system", governorExecution{})
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: r.governorBackend,
		PrincipalRole:   "admin",
		WorkspaceRoot:   scope.WorkingRoot,
		Workspace:       promptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)

	history, err := session.ToAgentHistory(maintenanceSession.Messages)
	if err != nil {
		return fmt.Errorf("assemble recovery history: %w", err)
	}

	requestText := renderStartupRecoveryRequest(runs)
	monitor := r.startTurnMonitor(maintenanceKey, session.TurnRunKindRecovery, requestText, nil)
	defer monitor.Finish(ctx, err)

	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: requestText})

	result, outHistory, err := agent.RunTurn(ctx, r.provider, nil, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindRecovery), input)
	if err != nil {
		return fmt.Errorf("run startup recovery turn: %w", err)
	}
	if len(outHistory) < len(input) {
		return fmt.Errorf("invalid recovery output: history shrank from %d to %d", len(input), len(outHistory))
	}

	floorText := strings.TrimSpace(result.Text)
	if floorText == "" {
		floorText = fallbackRecoverySummary(runs)
	}

	maintenanceSession.ChatType = "system"
	maintenanceSession.UserName = "startup-recovery"
	maintenanceSession.SystemPrompt = systemPrompt
	maintenanceSession.TurnCount++
	maintenanceSession.LastFloorText = floorText

	newMessages, err := session.NewMessagesForTurn(requestText, outHistory[len(input):], maintenanceSession.TurnCount)
	if err != nil {
		return fmt.Errorf("convert recovery messages: %w", err)
	}
	newMessages = replaceLastAssistantWithSceneText(newMessages, floorText)
	newMessages = setLastAssistantFloor(newMessages, floorText)
	if err := r.store.Save(maintenanceSession, newMessages, result.TokenUsage); err != nil {
		return fmt.Errorf("save recovery maintenance session: %w", err)
	}

	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	if err := r.store.MarkTurnRunsRecovered(ids, floorText); err != nil {
		return fmt.Errorf("mark turn runs recovered: %w", err)
	}
	if err := r.deliverStartupRecoveryCatchup(ctx, systemPrompt, runs, floorText); err != nil {
		return fmt.Errorf("deliver startup recovery catch-up: %w", err)
	}
	return nil
}

func renderStartupRecoveryRequest(runs []session.TurnRun) string {
	lines := []string{
		"Startup recovery analysis.",
		"The previous Aphelion process ended while the following turns were still running.",
		"Analyze where execution likely stopped and suggest safe recovery options.",
		"",
	}

	for _, run := range runs {
		lines = append(lines, fmt.Sprintf("- run_id=%d kind=%s chat_id=%d user_id=%d", run.ID, run.Kind, run.ChatID, run.UserID))
		lines = append(lines, "  started_at="+run.StartedAt.UTC().Format(time.RFC3339))
		if !run.LastActivityAt.IsZero() {
			lines = append(lines, "  last_activity_at="+run.LastActivityAt.UTC().Format(time.RFC3339))
		}
		lines = append(lines, "  request="+strconv.Quote(truncatePreview(run.RequestText, 220)))
		if run.ToolCallsStarted > 0 {
			lines = append(lines, fmt.Sprintf("  tool_calls_started=%d", run.ToolCallsStarted))
		}
		if run.ToolCallsFinished > 0 {
			lines = append(lines, fmt.Sprintf("  tool_calls_finished=%d", run.ToolCallsFinished))
		}
		if strings.TrimSpace(run.LastToolName) != "" {
			lines = append(lines, "  last_tool="+run.LastToolName)
		}
		if strings.TrimSpace(run.LastToolPreview) != "" {
			lines = append(lines, "  last_tool_preview="+truncatePreview(run.LastToolPreview, 220))
		}
		if strings.TrimSpace(run.LastToolResultPreview) != "" {
			lines = append(lines, "  last_tool_result_preview="+truncatePreview(run.LastToolResultPreview, 220))
		}
		if strings.TrimSpace(run.LastToolError) != "" {
			lines = append(lines, "  last_tool_error="+strconv.Quote(truncatePreview(run.LastToolError, 220)))
		}
		if run.ProgressMessageID != 0 {
			lines = append(lines, fmt.Sprintf("  progress_message_id=%d", run.ProgressMessageID))
		}
		if strings.TrimSpace(run.ErrorText) != "" {
			lines = append(lines, "  machine_error="+strconv.Quote(truncatePreview(run.ErrorText, 220)))
		}
		lines = append(lines, "")
	}

	lines = append(lines, "Log a concise recovery note into the maintenance ledger. Do not send a user-facing message unless explicitly requested elsewhere.")
	return strings.Join(lines, "\n")
}

func fallbackRecoverySummary(runs []session.TurnRun) string {
	lines := []string{
		"Startup recovery note.",
		fmt.Sprintf("Interrupted turns detected: %d.", len(runs)),
	}
	for _, run := range runs {
		line := fmt.Sprintf("run %d (%s, chat %d)", run.ID, run.Kind, run.ChatID)
		if strings.TrimSpace(run.LastToolName) != "" {
			line += " last tool " + run.LastToolName
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (r *Runtime) deliverStartupRecoveryCatchup(ctx context.Context, systemPrompt string, runs []session.TurnRun, floorText string) error {
	adminIDs := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(adminIDs) == 0 {
		return nil
	}
	targetChatID := r.lastActiveAdminChat(adminIDs)
	if targetChatID == 0 {
		targetChatID = adminIDs[0]
	}
	text := renderStartupRecoveryCatchup(runs, floorText)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: targetChatID, Text: text})
	if err != nil {
		return err
	}
	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()
	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load startup recovery target session: %w", err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	adminSession.SystemPrompt = systemPrompt
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, text, floorText, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save startup recovery admin session: %w", err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, "startup_recovery"); err != nil {
		return fmt.Errorf("record startup recovery outbound: %w", err)
	}
	return nil
}

func renderStartupRecoveryCatchup(runs []session.TurnRun, floorText string) string {
	parts := []string{"Restart catch-up."}
	if len(runs) == 1 {
		parts = append(parts, "I recovered 1 interrupted turn.")
	} else {
		parts = append(parts, fmt.Sprintf("I recovered %d interrupted turns.", len(runs)))
	}
	if len(runs) > 0 {
		last := runs[0]
		for _, run := range runs[1:] {
			if run.LastActivityAt.After(last.LastActivityAt) {
				last = run
			}
		}
		if request := strings.TrimSpace(last.RequestText); request != "" {
			parts = append(parts, "Most recent interrupted request: "+strconv.Quote(truncatePreview(request, 160))+".")
		}
		if tool := strings.TrimSpace(last.LastToolName); tool != "" {
			parts = append(parts, "Last tool in flight: "+tool+".")
		}
	}
	if summary := sanitizeStartupRecoveryCatchupSummary(floorText); summary != "" {
		parts = append(parts, "Recovery note: "+sentenceAwareSummary(summary, 240))
	}
	parts = append(parts, "Next: investigate the interruption before returning to deferred work.")
	return strings.Join(parts, " ")
}

func sanitizeStartupRecoveryCatchupSummary(raw string) string {
	summary := strings.TrimSpace(raw)
	if summary == "" {
		return ""
	}
	summary = strings.TrimSpace(strings.TrimPrefix(summary, "Cannot write the maintenance ledger from this session. Append:"))
	summary = strings.ReplaceAll(summary, "```text", "")
	summary = strings.ReplaceAll(summary, "```", "")
	summary = strings.ReplaceAll(summary, "[MEMORY]", "")
	summary = strings.ReplaceAll(summary, "[/MEMORY]", "")
	summary = strings.ReplaceAll(summary, "[KNOWLEDGE]", "")
	summary = strings.ReplaceAll(summary, "[/KNOWLEDGE]", "")
	summary = strings.ReplaceAll(summary, "[DECISIONS]", "")
	summary = strings.ReplaceAll(summary, "[/DECISIONS]", "")
	summary = strings.ReplaceAll(summary, "[QUESTIONS]", "")
	summary = strings.ReplaceAll(summary, "[/QUESTIONS]", "")
	summary = strings.ReplaceAll(summary, "[RHIZOME]", "")
	summary = strings.ReplaceAll(summary, "[/RHIZOME]", "")
	summary = strings.TrimSpace(summary)
	if idx := strings.Index(summary, "\n"); idx >= 0 {
		first := strings.TrimSpace(summary[:idx])
		rest := strings.TrimSpace(summary[idx+1:])
		if first == "" || strings.HasPrefix(first, "[") || strings.HasPrefix(first, "run_id=") {
			summary = rest
		}
	}
	summary = strings.Join(strings.Fields(summary), " ")
	return strings.TrimSpace(summary)
}

func sentenceAwareSummary(text string, max int) string {
	text = strings.TrimSpace(text)
	if text == "" || max <= 0 {
		return text
	}
	if len(text) <= max {
		return text
	}
	window := text
	if len(window) > max {
		window = window[:max]
	}
	lastStop := -1
	for i, r := range window {
		if r == '.' || r == '!' || r == '?' {
			lastStop = i
		}
	}
	if lastStop >= 0 {
		trimmed := strings.TrimSpace(window[:lastStop+1])
		if len(trimmed) >= max/2 {
			return trimmed
		}
	}
	lastSpace := strings.LastIndex(window, " ")
	if lastSpace > 0 {
		return strings.TrimSpace(window[:lastSpace]) + " ..."
	}
	return strings.TrimSpace(window) + " ..."
}
