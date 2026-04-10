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

	maintenanceKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0}
	unlock := r.lockSession(maintenanceKey)
	defer unlock()

	maintenanceSession, err := r.store.Load(maintenanceKey)
	if err != nil {
		return fmt.Errorf("load recovery maintenance session: %w", err)
	}

	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return fmt.Errorf("load recovery prompt context: %w", err)
	}
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: r.governorBackend,
		PrincipalRole:   "admin",
		WorkspaceRoot:   scope.WorkingRoot,
		Workspace:       promptContext,
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
	}, input)
	if err != nil {
		return fmt.Errorf("run startup recovery turn: %w", err)
	}
	if len(outHistory) < len(input) {
		return fmt.Errorf("invalid recovery output: history shrank from %d to %d", len(input), len(outHistory))
	}

	canonicalReply := strings.TrimSpace(result.Text)
	if canonicalReply == "" {
		canonicalReply = fallbackRecoverySummary(runs)
	}

	maintenanceSession.ChatType = "system"
	maintenanceSession.UserName = "startup-recovery"
	maintenanceSession.SystemPrompt = systemPrompt
	maintenanceSession.TurnCount++
	maintenanceSession.LastCanonicalReply = canonicalReply

	newMessages, err := session.NewMessagesForTurn(requestText, outHistory[len(input):], maintenanceSession.TurnCount)
	if err != nil {
		return fmt.Errorf("convert recovery messages: %w", err)
	}
	newMessages = replaceLastAssistantWithRenderedReply(newMessages, canonicalReply)
	newMessages = setLastAssistantCanonical(newMessages, canonicalReply)
	if err := r.store.Save(maintenanceSession, newMessages, result.TokenUsage); err != nil {
		return fmt.Errorf("save recovery maintenance session: %w", err)
	}

	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	if err := r.store.MarkTurnRunsRecovered(ids, canonicalReply); err != nil {
		return fmt.Errorf("mark turn runs recovered: %w", err)
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
		if strings.TrimSpace(run.LastToolName) != "" {
			lines = append(lines, "  last_tool="+run.LastToolName)
		}
		if strings.TrimSpace(run.LastToolPreview) != "" {
			lines = append(lines, "  last_tool_preview="+truncatePreview(run.LastToolPreview, 220))
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
