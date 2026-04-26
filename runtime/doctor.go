//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

const (
	doctorRequestMarker      = "DOCTOR_DIAGNOSTIC_REQUEST"
	doctorReportFallbackText = "Doctor diagnostics finished, but the model returned an empty report."
	doctorRunTimeout         = 5 * time.Minute
	doctorPacketMaxChars     = 120000
	doctorLogTailBytes       = 16000
	doctorFilePreviewChars   = 700
	doctorMessageLimit       = 12
)

type doctorDiagnosticInput struct {
	Message       core.InboundMessage
	Actor         principal.Principal
	Key           session.SessionKey
	Session       *session.Session
	Scope         sandbox.Scope
	PromptContext *workspace.PromptContext
	Exec          pipeline.TurnExecutionContract
	Now           time.Time
}

func (r *Runtime) StartDoctor(ctx context.Context, msg core.InboundMessage) error {
	if r == nil {
		return fmt.Errorf("runtime is unavailable")
	}
	if _, err := r.resolveDoctorAdmin(msg); err != nil {
		return err
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
		defer cancel()
		if err := r.runDoctorOnce(runCtx, msg, time.Now().UTC()); err != nil {
			log.Printf("WARN doctor diagnostics failed chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			r.reportOperationalIssueAsync("doctor", err)
		}
	}()
	_ = ctx
	return nil
}

func (r *Runtime) runDoctorOnce(ctx context.Context, msg core.InboundMessage, now time.Time) (err error) {
	if r == nil || r.store == nil || r.provider == nil || r.outbound == nil {
		return fmt.Errorf("runtime doctor dependencies are unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	actor, err := r.resolveDoctorAdmin(msg)
	if err != nil {
		return err
	}
	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return fmt.Errorf("resolve doctor scope: %w", err)
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	unlock := r.lockSession(key)
	defer unlock()

	sess, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load doctor session: %w", err)
	}
	applySessionScope(sess, key)
	if strings.TrimSpace(sess.ChatType) == "" {
		sess.ChatType = firstNonEmpty(strings.TrimSpace(msg.ChatType), "dm")
	}
	if strings.TrimSpace(sess.UserName) == "" {
		sess.UserName = strings.TrimSpace(msg.SenderName)
	}

	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return fmt.Errorf("load doctor prompt context: %w", err)
	}
	prepared := pipeline.TurnPrepareContract{
		UserText:   "/doctor",
		LedgerText: "/doctor",
	}
	exec := r.executionForTurn(prepared)
	packet := r.buildDoctorDiagnosticPacket(ctx, doctorDiagnosticInput{
		Message:       msg,
		Actor:         actor,
		Key:           key,
		Session:       sess,
		Scope:         scope,
		PromptContext: promptContext,
		Exec:          exec,
		Now:           now,
	})

	awareness := r.governorRuntimeAwareness(scope, session.TurnRunKindDoctor, "telegram", exec)
	systemBlocks := prompt.BuildGovernorPromptBlocks(prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: exec.Backend,
		PrincipalRole:   "admin",
		WorkspaceRoot:   scope.WorkingRoot,
		Workspace:       promptContext,
		Runtime:         awareness,
	})
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	sess.SystemPrompt = systemPrompt

	monitor := r.startTurnMonitor(key, session.TurnRunKindDoctor, "/doctor", nil, nil)
	var monitorErr error
	defer func() {
		monitor.Finish(ctx, monitorErr)
	}()

	input := []agent.Message{
		{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks},
		{Role: "system", Content: doctorReadOnlySystemNote()},
		{Role: "user", Content: packet},
	}
	r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptStarted, "provider", "started", map[string]any{
		"backend":       strings.TrimSpace(exec.Backend),
		"provider":      strings.TrimSpace(exec.ProviderName),
		"model":         strings.TrimSpace(exec.ModelName),
		"provider_path": strings.Join(exec.ProviderPath, ","),
		"run_kind":      string(session.TurnRunKindDoctor),
	}, time.Now().UTC())

	turnResult, _, runErr := agent.RunTurn(ctx, exec.Provider, nil, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindDoctor), input)
	if runErr != nil {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"error":    trimError(runErr.Error()),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
		monitorErr = fmt.Errorf("run doctor diagnostics: %w", runErr)
		return monitorErr
	}
	if turnResult == nil {
		monitorErr = fmt.Errorf("doctor diagnostics returned no turn result")
		return monitorErr
	}
	if strings.TrimSpace(turnResult.ProviderFailure) != "" {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptFailed, "provider", "failed", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"error":    trimError(turnResult.ProviderFailure),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
		r.reportOperationalIssueAsync("doctor", fmt.Errorf("%s", strings.TrimSpace(turnResult.ProviderFailure)))
	} else {
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"run_kind": string(session.TurnRunKindDoctor),
		}, time.Now().UTC())
	}

	report := strings.TrimSpace(turnResult.Text)
	if report == "" {
		report = doctorReportFallbackText
	}
	report = redactDoctorText(report)
	newMessages := appendSyntheticTurn(sess, "/doctor", report, report, "")
	if err := r.store.Save(sess, newMessages, turnResult.TokenUsage); err != nil {
		monitorErr = fmt.Errorf("save doctor report: %w", err)
		return monitorErr
	}
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    report,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		monitorErr = fmt.Errorf("send doctor report: %w", err)
		return monitorErr
	}
	if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "doctor"); err != nil {
		monitorErr = fmt.Errorf("record doctor outbound: %w", err)
		return monitorErr
	}
	return nil
}

func (r *Runtime) resolveDoctorAdmin(msg core.InboundMessage) (principal.Principal, error) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, fmt.Errorf("principal resolver is unavailable")
	}
	if chatType := strings.TrimSpace(msg.ChatType); chatType != "" && chatType != "private" && chatType != "dm" {
		return principal.Principal{}, fmt.Errorf("doctor diagnostics must be run from an admin private chat")
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok || actor.Role != principal.RoleAdmin {
		return principal.Principal{}, ErrPrincipalDenied
	}
	return actor, nil
}

func doctorReadOnlySystemNote() string {
	return strings.Join([]string{
		"You are running the /doctor command.",
		"This is a read-only diagnostic pass. Do not claim to have edited files, run commands, restarted services, changed memory, or committed code.",
		"Use the diagnostic packet and the loaded prompt/memory context to produce an operator-facing report.",
		"Include concrete code recommendations when the evidence points to code changes, but frame them as recommendations only.",
		"Required sections: State of Things, Recent Failures or Risks, Memory and Prompt Health, Runtime and Session Health, Recommendations, Code Recommendations, Confidence and Unknowns.",
	}, "\n")
}

func (r *Runtime) buildDoctorDiagnosticPacket(ctx context.Context, input doctorDiagnosticInput) string {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var b strings.Builder
	writeDoctorLine(&b, doctorRequestMarker)
	writeDoctorKV(&b, "generated_at_utc", now.Format(time.RFC3339))
	writeDoctorKV(&b, "chat_id", strconv.FormatInt(input.Key.ChatID, 10))
	writeDoctorKV(&b, "sender_id", strconv.FormatInt(input.Message.SenderID, 10))
	writeDoctorKV(&b, "run_kind", string(session.TurnRunKindDoctor))
	writeDoctorKV(&b, "mode", "read_only")

	writeDoctorSection(&b, "Effective Runtime")
	r.writeDoctorRuntimeConfig(&b, input.Exec, input.Scope)

	writeDoctorSection(&b, "Current Session")
	writeDoctorSessionSummary(&b, input.Session)
	writeDoctorRecentMessages(&b, input.Session, doctorMessageLimit)

	writeDoctorSection(&b, "Prompt Context Inventory")
	writeDoctorPromptInventory(&b, input.PromptContext)

	writeDoctorSection(&b, "Memory Footprint")
	r.writeDoctorMemoryFootprint(&b, input.Scope, now)

	writeDoctorSection(&b, "Execution Events")
	r.writeDoctorExecutionEvents(ctx, &b, input.Key, now)

	writeDoctorSection(&b, "Turn Runs")
	r.writeDoctorTurnRuns(ctx, &b, now)

	writeDoctorSection(&b, "Semantic Store")
	r.writeDoctorSemanticStats(&b)

	writeDoctorSection(&b, "Recent Service Log Tail")
	r.writeDoctorLogTail(&b)

	writeDoctorSection(&b, "Doctor Instructions")
	writeDoctorLine(&b, "Analyze the evidence above and the loaded prompt/memory context. Identify likely causes, residual risks, and specific follow-up work. Do not perform actions.")

	packet := redactDoctorText(b.String())
	if len(packet) > doctorPacketMaxChars {
		packet = strings.TrimSpace(packet[:doctorPacketMaxChars]) + "\n\n[doctor diagnostic packet truncated]"
	}
	return packet
}

func (r *Runtime) writeDoctorRuntimeConfig(b *strings.Builder, exec pipeline.TurnExecutionContract, scope sandbox.Scope) {
	if r == nil || r.cfg == nil {
		writeDoctorLine(b, "runtime_config: unavailable")
		return
	}
	writeDoctorKV(b, "governor_backend", strings.TrimSpace(exec.Backend))
	writeDoctorKV(b, "governor_provider", strings.TrimSpace(exec.ProviderName))
	writeDoctorKV(b, "governor_model", strings.TrimSpace(exec.ModelName))
	writeDoctorKV(b, "provider_path", strings.Join(exec.ProviderPath, " -> "))
	writeDoctorKV(b, "configured_provider_chain", strings.Join(config.EffectiveProviderChain(r.cfg), " -> "))
	writeDoctorKV(b, "codex_context_window", strconv.Itoa(r.cfg.Governor.Codex.ContextWindow))
	writeDoctorKV(b, "codex_transport_retries", strconv.Itoa(r.cfg.Governor.Codex.TransportRetries))
	writeDoctorKV(b, "session_max_context_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.MaxContextRatio))
	writeDoctorKV(b, "session_compaction_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.CompactionRatio))
	writeDoctorKV(b, "bootstrap_total_max_chars", strconv.Itoa(r.cfg.Agent.BootstrapTotalMaxChars))
	writeDoctorKV(b, "memory_semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	writeDoctorKV(b, "memory_aggressive_enabled", strconv.FormatBool(r.cfg.Memory.Aggressive.Enabled))
	writeDoctorKV(b, "memory_aggressive_flush_on_boundary", strconv.FormatBool(r.cfg.Memory.Aggressive.FlushOnSessionBoundary))
	writeDoctorKV(b, "heartbeat_enabled", strconv.FormatBool(r.cfg.Heartbeat.Enabled))
	writeDoctorKV(b, "cron_enabled", strconv.FormatBool(r.cfg.Cron.Enabled))
	writeDoctorKV(b, "prompt_root", r.cfg.Agent.PromptRoot)
	writeDoctorKV(b, "exec_root", r.cfg.Agent.ExecRoot)
	writeDoctorKV(b, "shared_memory_root", strings.TrimSpace(scope.SharedMemoryRoot))
	writeDoctorKV(b, "working_root", strings.TrimSpace(scope.WorkingRoot))
}

func writeDoctorSessionSummary(b *strings.Builder, sess *session.Session) {
	if sess == nil {
		writeDoctorLine(b, "session: unavailable")
		return
	}
	writeDoctorKV(b, "session_id", sess.SessionID)
	writeDoctorKV(b, "turn_count", strconv.Itoa(sess.TurnCount))
	writeDoctorKV(b, "message_count", strconv.Itoa(len(sess.Messages)))
	writeDoctorKV(b, "last_provider", sess.LastProvider)
	writeDoctorKV(b, "last_model", sess.LastModel)
	writeDoctorKV(b, "last_error", truncatePreview(sess.LastError, 500))
	writeDoctorKV(b, "last_floor_preview", truncatePreview(sess.LastFloorText, 800))
	writeDoctorKV(b, "total_input_tokens", strconv.FormatInt(sess.TotalInputTokens, 10))
	writeDoctorKV(b, "total_output_tokens", strconv.FormatInt(sess.TotalOutputTokens, 10))
	writeDoctorKV(b, "total_cache_read", strconv.FormatInt(sess.TotalCacheRead, 10))
	writeDoctorKV(b, "total_cache_write", strconv.FormatInt(sess.TotalCacheWrite, 10))
	writeDoctorKV(b, "continuation_status", string(sess.ContinuationState.Status))
	writeDoctorKV(b, "active_tool_calls", strconv.Itoa(sess.ActiveToolCalls))
}

func writeDoctorRecentMessages(b *strings.Builder, sess *session.Session, limit int) {
	if sess == nil || len(sess.Messages) == 0 || limit == 0 {
		return
	}
	if limit < 0 || limit > len(sess.Messages) {
		limit = len(sess.Messages)
	}
	start := len(sess.Messages) - limit
	if start < 0 {
		start = 0
	}
	writeDoctorLine(b, "recent_messages:")
	for _, msg := range sess.Messages[start:] {
		writeDoctorLine(b, fmt.Sprintf("- turn=%d role=%s compacted=%t chars=%d preview=%q",
			msg.TurnIndex,
			strings.TrimSpace(msg.Role),
			msg.Compacted,
			msg.ContentChars,
			truncatePreview(msg.Content, 300),
		))
	}
}

func writeDoctorPromptInventory(b *strings.Builder, ctx *workspace.PromptContext) {
	if ctx == nil {
		writeDoctorLine(b, "prompt_context: unavailable")
		return
	}
	writeDoctorKV(b, "prompt_workspace", ctx.Workspace)
	writeDoctorLine(b, "stable_files:")
	for _, file := range ctx.Stable {
		writeDoctorLoadedFile(b, file)
	}
	writeDoctorLine(b, "dynamic_files:")
	for _, file := range ctx.Dynamic {
		writeDoctorLoadedFile(b, file)
	}
}

func writeDoctorLoadedFile(b *strings.Builder, file workspace.LoadedFile) {
	writeDoctorLine(b, fmt.Sprintf("- path=%s chars=%d truncated=%t preview=%q",
		strings.TrimSpace(file.Path),
		len(file.Content),
		file.Truncated,
		truncatePreview(file.Content, doctorFilePreviewChars),
	))
}

func (r *Runtime) writeDoctorMemoryFootprint(b *strings.Builder, scope sandbox.Scope, now time.Time) {
	root := dynamicPromptRoot(scope)
	writeDoctorKV(b, "memory_root", root)
	if strings.TrimSpace(root) == "" {
		return
	}
	paths := uniqueDoctorPaths(append([]string{
		"MEMORY.md",
		"HEARTBEAT.md",
		"SKILLS.md",
		"memory/knowledge.md",
		"memory/decisions.md",
		"memory/questions.md",
		"memory/rhizome.md",
		"memory/dreams.md",
	}, r.cfg.Agent.DynamicFiles...))
	for _, rel := range paths {
		writeDoctorFileStat(b, root, rel)
	}
	writeDoctorDirStat(b, filepath.Join(root, "memory", "inbox"), "memory/inbox")
	writeDoctorDirStat(b, filepath.Join(root, "memory", "daily"), "memory/daily")
	if r.cfg.Agent.DailyNotes {
		today := filepath.ToSlash(filepath.Join("memory", "daily", now.Format("2006-01-02")+".md"))
		yesterday := filepath.ToSlash(filepath.Join("memory", "daily", now.AddDate(0, 0, -1).Format("2006-01-02")+".md"))
		writeDoctorFileStat(b, root, today)
		writeDoctorFileStat(b, root, yesterday)
	}
}

func writeDoctorFileStat(b *strings.Builder, root string, rel string) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, fmt.Sprintf("- file=%s missing=true", rel))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- file=%s error=%q", rel, err.Error()))
		return
	}
	if info.IsDir() {
		writeDoctorLine(b, fmt.Sprintf("- file=%s directory=true", rel))
		return
	}
	writeDoctorLine(b, fmt.Sprintf("- file=%s bytes=%d modified=%s", rel, info.Size(), info.ModTime().UTC().Format(time.RFC3339)))
}

func writeDoctorDirStat(b *strings.Builder, dir string, label string) {
	var count int
	var bytes int64
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		bytes += info.Size()
		_ = path
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, fmt.Sprintf("- dir=%s missing=true", label))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- dir=%s error=%q", label, err.Error()))
		return
	}
	writeDoctorLine(b, fmt.Sprintf("- dir=%s files=%d bytes=%d", label, count, bytes))
}

func (r *Runtime) writeDoctorExecutionEvents(ctx context.Context, b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	chatEvents, err := r.store.ExecutionEventsByChat(key.ChatID, now.Add(-24*time.Hour), 60)
	if err != nil {
		writeDoctorLine(b, "chat_events_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "chat_events_last_24h:")
		writeDoctorEvents(b, chatEvents, 20)
	}
	recentEvents, err := r.store.ExecutionEventsRecent(80)
	if err != nil {
		writeDoctorLine(b, "recent_events_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "recent_system_events:")
		writeDoctorEvents(b, recentEvents, 25)
	}
	_ = ctx
}

func writeDoctorEvents(b *strings.Builder, events []session.ExecutionEvent, limit int) {
	if len(events) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(events) {
		limit = len(events)
	}
	for i := 0; i < limit; i++ {
		event := events[i]
		writeDoctorLine(b, fmt.Sprintf("- time=%s chat_id=%d seq=%d type=%s stage=%s status=%s payload=%s",
			event.CreatedAt.UTC().Format(time.RFC3339),
			event.ChatID,
			event.Seq,
			strings.TrimSpace(event.EventType),
			strings.TrimSpace(event.Stage),
			strings.TrimSpace(event.Status),
			strconv.Quote(truncatePreview(event.PayloadJSON, 500)),
		))
	}
}

func (r *Runtime) writeDoctorTurnRuns(ctx context.Context, b *strings.Builder, now time.Time) {
	if r == nil || r.store == nil {
		return
	}
	latest, err := r.store.LatestTurnRunsByChat(40)
	if err != nil {
		writeDoctorLine(b, "latest_turn_runs_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "latest_turn_runs_by_chat:")
		writeDoctorRuns(b, latest, 20)
	}
	pending, err := r.store.PendingRecoveryTurnRuns(40)
	if err != nil {
		writeDoctorLine(b, "pending_recovery_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "pending_recovery_runs:")
		writeDoctorRuns(b, pending, 12)
	}
	stale, err := r.staleRunningTurnRuns(now)
	if err != nil {
		writeDoctorLine(b, "stale_turn_runs_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "stale_running_turns:")
		writeDoctorRuns(b, stale, 12)
	}
	_ = ctx
}

func writeDoctorRuns(b *strings.Builder, runs []session.TurnRun, limit int) {
	if len(runs) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	if limit <= 0 || limit > len(runs) {
		limit = len(runs)
	}
	for i := 0; i < limit; i++ {
		run := runs[i]
		writeDoctorLine(b, fmt.Sprintf("- id=%d chat_id=%d kind=%s status=%s started=%s last_activity=%s tools=%d/%d request=%q last_tool=%q last_error=%q",
			run.ID,
			run.ChatID,
			run.Kind,
			run.Status,
			run.StartedAt.UTC().Format(time.RFC3339),
			run.LastActivityAt.UTC().Format(time.RFC3339),
			run.ToolCallsFinished,
			run.ToolCallsStarted,
			truncatePreview(run.RequestText, 260),
			truncatePreview(run.LastToolName, 120),
			truncatePreview(run.ErrorText, 220),
		))
	}
}

func (r *Runtime) writeDoctorSemanticStats(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	dbPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "semantic.db")
	writeDoctorKV(b, "semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	writeDoctorKV(b, "semantic_db_path", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, "semantic_db_missing=true")
			return
		}
		writeDoctorLine(b, "semantic_db_stat_error="+strconv.Quote(err.Error()))
		return
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		writeDoctorLine(b, "semantic_db_open_error="+strconv.Quote(err.Error()))
		return
	}
	defer db.Close()
	var docs, chunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_documents`).Scan(&docs); err != nil {
		writeDoctorLine(b, "semantic_documents_error="+strconv.Quote(err.Error()))
		return
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM semantic_chunks`).Scan(&chunks); err != nil {
		writeDoctorLine(b, "semantic_chunks_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorKV(b, "semantic_documents", strconv.Itoa(docs))
	writeDoctorKV(b, "semantic_chunks", strconv.Itoa(chunks))
	rows, err := db.Query(`SELECT import_state, COUNT(*) FROM semantic_documents GROUP BY import_state ORDER BY import_state`)
	if err != nil {
		writeDoctorLine(b, "semantic_import_state_error="+strconv.Quote(err.Error()))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			writeDoctorLine(b, "semantic_import_state_scan_error="+strconv.Quote(err.Error()))
			return
		}
		writeDoctorLine(b, fmt.Sprintf("- import_state=%s documents=%d", state, count))
	}
}

func (r *Runtime) writeDoctorLogTail(b *strings.Builder) {
	if r == nil || r.cfg == nil {
		return
	}
	logPath := filepath.Join(filepath.Dir(r.cfg.Sessions.DBPath), "aphelion.log")
	writeDoctorKV(b, "log_path", logPath)
	data, err := readDoctorTail(logPath, doctorLogTailBytes)
	if err != nil {
		if os.IsNotExist(err) {
			writeDoctorLine(b, "log_missing=true")
			return
		}
		writeDoctorLine(b, "log_tail_error="+strconv.Quote(err.Error()))
		return
	}
	text := strings.TrimSpace(redactDoctorText(string(data)))
	if text == "" {
		writeDoctorLine(b, "log_tail_empty=true")
		return
	}
	writeDoctorLine(b, text)
}

func readDoctorTail(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = doctorLogTailBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	offset := int64(0)
	if size > limit {
		offset = size - limit
	}
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

func uniqueDoctorPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func writeDoctorSection(b *strings.Builder, title string) {
	writeDoctorLine(b, "")
	writeDoctorLine(b, "## "+strings.TrimSpace(title))
}

func writeDoctorKV(b *strings.Builder, key string, value string) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return
	}
	writeDoctorLine(b, key+"="+strconv.Quote(value))
}

func writeDoctorLine(b *strings.Builder, line string) {
	if b == nil {
		return
	}
	b.WriteString(strings.TrimRight(line, "\n"))
	b.WriteByte('\n')
}

var doctorSecretRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)((?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)\s*=\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)("(?:bot_token|telegram_bot_token|api_key|openai_api_key|elevenlabs_api_key|access_token|refresh_token|secret|password)"\s*:\s*")[^"]*(")`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z0-9_]*=)[^\s]+`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;"}]+`),
	regexp.MustCompile(`(?i)("(?:authorization)"\s*:\s*"bearer\s+)[^"]*(")`),
	regexp.MustCompile(`(?i)((?:x-api-key|api-key)\s*[:=]\s*)[^\s,;"}]+`),
}

func redactDoctorText(text string) string {
	out := text
	for _, re := range doctorSecretRedactions {
		out = re.ReplaceAllString(out, `${1}<redacted>${2}`)
	}
	return out
}
