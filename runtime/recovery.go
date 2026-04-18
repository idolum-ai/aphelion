//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
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
	requestText := renderStartupRecoveryRequest(runs)
	prepared := pipeline.TurnPrepareContract{
		UserText:   requestText,
		LedgerText: requestText,
	}
	exec := r.executionForTurn(prepared)
	governorAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindRecovery, "system", exec)

	assembler := r.maintenanceAssembler
	if assembler == nil {
		assembler = newMaintenanceTurnAssembler(r)
	}
	turnResult, err := assembler.Run(ctx, maintenanceTurnAssemblyInput{
		Species:               maintenanceTurnRecovery,
		RunKind:               session.TurnRunKindRecovery,
		Key:                   maintenanceKey,
		Sess:                  maintenanceSession,
		Scope:                 scope,
		Prepared:              prepared,
		Exec:                  exec,
		PromptContext:         promptContext,
		RecoveryRuns:          runs,
		UseMaterialFloor:      false,
		GovernorName:          prompt.DefaultGovernorName,
		FaceName:              face.DefaultFaceName,
		Channel:               "system",
		PrincipalRole:         "admin",
		SessionUserName:       "startup-recovery",
		BaseGovernorAwareness: governorAwareness,
		RuntimeAwareness:      governorAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{Reason: "startup_recovery_maintenance"}
		},
		ErrContext: turnCommitErrorContext{
			ConvertMessages: "convert recovery messages",
			LoadPlanState:   "load recovery plan state before save",
			LoadOperation:   "load recovery operation state before save",
			SaveSession:     "save recovery maintenance session",
		},
		Inbound: core.InboundMessage{
			ChatID: maintenanceKey.ChatID,
			Text:   requestText,
		},
		Now:         now,
		UseFacePort: false,
	})
	if err != nil {
		return err
	}
	if turnResult == nil || turnResult.Turn == nil {
		return fmt.Errorf("startup recovery turn did not return a result")
	}
	if !turnResult.Commit.Persisted {
		return nil
	}
	floorText := strings.TrimSpace(turnResult.FloorText)

	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	if err := r.store.MarkTurnRunsRecovered(ids, floorText); err != nil {
		return fmt.Errorf("mark turn runs recovered: %w", err)
	}
	if err := r.deliverStartupRecoveryCatchup(ctx, maintenanceSession.SystemPrompt, runs, floorText); err != nil {
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
	notice := face.StartupRecoveryNotice{InterruptedCount: len(runs)}
	if len(runs) > 0 {
		last := runs[0]
		for _, run := range runs[1:] {
			if run.LastActivityAt.After(last.LastActivityAt) {
				last = run
			}
		}
		if request := strings.TrimSpace(last.RequestText); request != "" {
			notice.MostRecentRequest = truncatePreview(request, 160)
		}
		if tool := strings.TrimSpace(last.LastToolName); tool != "" {
			notice.LastTool = tool
		}
	}
	if summary := sanitizeStartupRecoveryCatchupSummary(floorText); summary != "" {
		notice.RecoverySummary = sentenceAwareSummary(summary, 240)
	}
	return face.RenderStartupRecovery(notice)
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
