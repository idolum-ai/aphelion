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

	progress := r.newDoctorProgressReporter(key, msg, sess)
	monitor := r.startTurnMonitor(key, session.TurnRunKindDoctor, "/doctor", progress, nil)
	var monitorErr error
	defer func() {
		if monitorErr != nil {
			surfaceDoctorProgress(ctx, progress, "Doctor diagnostics failed: "+trimError(monitorErr.Error()))
		}
		monitor.Finish(ctx, monitorErr)
	}()

	surfaceDoctorProgress(ctx, progress, "Loading prompt and memory context")
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		monitorErr = fmt.Errorf("load doctor prompt context: %w", err)
		return monitorErr
	}
	prepared := pipeline.TurnPrepareContract{
		UserText:   "/doctor",
		LedgerText: "/doctor",
	}
	exec := r.executionForTurn(prepared)
	surfaceDoctorProgress(ctx, progress, "Collecting session, memory, log, and runtime evidence")
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

	surfaceDoctorProgress(ctx, progress, "Asking the model to write the read-only diagnosis")
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
	surfaceDoctorProgress(ctx, progress, "Saving the doctor report into chat history")
	newMessages := appendSyntheticTurn(sess, "/doctor", report, report, "")
	if err := r.store.Save(sess, newMessages, turnResult.TokenUsage); err != nil {
		monitorErr = fmt.Errorf("save doctor report: %w", err)
		return monitorErr
	}
	surfaceDoctorProgress(ctx, progress, "Sending the doctor report to Telegram")
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

func (r *Runtime) newDoctorProgressReporter(key session.SessionKey, msg core.InboundMessage, sess *session.Session) *toolProgressReporter {
	if r == nil {
		return nil
	}
	var plan session.PlanState
	if sess != nil {
		plan = sess.PlanState
	}
	progress := r.newToolProgressReporter(key, msg, plan, nil)
	if progress == nil {
		return nil
	}
	progress.suppressControls = true
	progress.controls = nil
	progress.taskSummary = "doctor diagnostics"
	progress.currentPlanStep = ""
	return progress
}

func surfaceDoctorProgress(ctx context.Context, progress *toolProgressReporter, text string) {
	if progress == nil {
		return
	}
	progress.Surface(ctx, text)
}

func doctorReadOnlySystemNote() string {
	return strings.Join([]string{
		"You are running the /doctor command.",
		"This is a read-only diagnostic pass. Do not claim to have edited files, run commands, restarted services, changed memory, or committed code.",
		"Use the diagnostic packet and the loaded prompt/memory context to produce an operator-facing report.",
		"For every issue you report, classify it as active, likely_fixed, historical_resolved, residual_risk, or unknown by comparing old failure evidence with current-state checks.",
		"Do not present an old failure as active when the current-state checks indicate it is likely fixed; instead call out remaining verification gaps.",
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

	writeDoctorSection(&b, "Known Issue Status Checks")
	r.writeDoctorIssueStatusChecks(&b, input)

	writeDoctorSection(&b, "Execution Events")
	r.writeDoctorExecutionEvents(ctx, &b, input.Key, now)

	writeDoctorSection(&b, "Turn Runs")
	r.writeDoctorTurnRuns(ctx, &b, now)

	writeDoctorSection(&b, "Semantic Store")
	r.writeDoctorSemanticStats(&b)

	writeDoctorSection(&b, "Recent Service Log Tail")
	r.writeDoctorLogTail(&b)

	writeDoctorSection(&b, "Doctor Instructions")
	writeDoctorLine(&b, "Analyze the evidence above and the loaded prompt/memory context. Identify likely causes, residual risks, and specific follow-up work. Do not perform actions. Before reporting a failure as current, check whether the Known Issue Status Checks or newer runtime evidence indicate it has already been fixed.")

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

func (r *Runtime) writeDoctorIssueStatusChecks(b *strings.Builder, input doctorDiagnosticInput) {
	writeDoctorLine(b, "classification_contract: before reporting an issue as current, compare historical failure evidence with current runtime, prompt, memory, and source-state evidence.")
	writeDoctorLine(b, "allowed_statuses: active, likely_fixed, historical_resolved, residual_risk, unknown")
	writeDoctorLine(b, "reporting_rule: if evidence is old and the current-state check passes, report it as historical/resolved or residual risk, not as an active failure.")

	workingRoot := strings.TrimSpace(input.Scope.WorkingRoot)
	retrySourceOK := doctorSourceContainsAll(workingRoot, "agent/turn.go", []string{"completeWithRetry", "isRetryableProviderError", "maxProviderRetries"})
	transportRetries := 0
	if r != nil && r.cfg != nil {
		transportRetries = r.cfg.Governor.Codex.TransportRetries
	}
	switch {
	case retrySourceOK && transportRetries > 0:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "likely_fixed", fmt.Sprintf("codex_transport_retries=%d and provider retry loop is present in agent/turn.go", transportRetries))
	case retrySourceOK:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "residual_risk", fmt.Sprintf("agent provider retry loop is present, but codex_transport_retries=%d", transportRetries))
	default:
		writeDoctorIssueCheck(b, "codex_timeout_retries", "unknown", "could not confirm retry-loop source evidence from working_root")
	}

	skillsConfigured := r != nil && r.cfg != nil && doctorPathListContains(r.cfg.Agent.DynamicFiles, "SKILLS.md")
	skillsLoaded := doctorPromptContextHasFile(input.PromptContext, "SKILLS.md")
	if skillsConfigured && skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "likely_fixed", "SKILLS.md is configured as a dynamic file and is present in loaded prompt context")
	} else if skillsConfigured {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is configured but was not present in loaded prompt context")
	} else if skillsLoaded {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "residual_risk", "SKILLS.md loaded, but it is not explicitly listed in configured dynamic files")
	} else {
		writeDoctorIssueCheck(b, "dynamic_skills_prompt_loading", "active", "SKILLS.md is not configured or loaded as dynamic context")
	}

	memoryConfigured := r != nil && r.cfg != nil &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/knowledge.md") &&
		doctorPathListContains(r.cfg.Agent.DynamicFiles, "memory/decisions.md")
	memoryLoaded := doctorPromptContextHasFile(input.PromptContext, "memory/knowledge.md") ||
		doctorPromptContextHasFile(input.PromptContext, "memory/decisions.md")
	recoverySourceOK := doctorSourceContainsAll(workingRoot, "main.go", []string{"StartStartupRecovery"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/recovery.go", []string{"StartStartupRecovery", "PendingRecoveryTurnRuns"})
	switch {
	case memoryConfigured && memoryLoaded && recoverySourceOK:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "likely_fixed", "structured memory files load dynamically and startup recovery source evidence is present")
	case memoryConfigured && memoryLoaded:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "residual_risk", "structured memory files load dynamically, but startup recovery source evidence was not confirmed")
	case memoryConfigured:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are configured but were not present in loaded prompt context")
	default:
		writeDoctorIssueCheck(b, "memory_survives_restart_and_dynamic_files_load", "active", "structured memory files are not fully configured as dynamic context")
	}

	productionEmailMatches := doctorSourceMatches(workingRoot, []string{"runtime", "tool", "core", "session"}, []string{"email", "e-mail"}, false, 8)
	if len(productionEmailMatches) == 0 {
		writeDoctorIssueCheck(b, "feature_specific_email_subagent_code", "likely_fixed", "no production source matches for email-specific terms under runtime/tool/core/session")
	} else {
		writeDoctorIssueCheck(b, "feature_specific_email_subagent_code", "residual_risk", "production source still contains email-specific matches: "+strings.Join(productionEmailMatches, ", "))
	}

	operationalAlertSourceOK := doctorSourceContainsAll(workingRoot, "runtime/operational_alerts.go", []string{"reportOperationalIssueAsync", "sendOperationalNoticeToAdmin", "system_warning"}) &&
		doctorSourceContainsAll(workingRoot, "runtime/turn_coordinator_common.go", []string{"reportOperationalIssueAsync", "provider"})
	adminConfigured := r != nil && r.cfg != nil && len(uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)) > 0
	if operationalAlertSourceOK && adminConfigured {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "likely_fixed", "operational alert delivery source is present and at least one admin Telegram ID is configured")
	} else if operationalAlertSourceOK {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "residual_risk", "operational alert delivery source is present, but no admin Telegram ID is configured")
	} else {
		writeDoctorIssueCheck(b, "runtime_failures_surface_to_telegram", "unknown", "could not confirm operational alert delivery source evidence from working_root")
	}

	workspaceEscapeGateOK := doctorSourceContainsAll(workingRoot, "tool/exec.go", []string{"workspace_escape", "ConfirmExec", "ProposalStatusApproved"})
	if workspaceEscapeGateOK {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "likely_fixed", "exec workspace escape path is gated by capability/proposal source evidence")
	} else {
		writeDoctorIssueCheck(b, "admin_workspace_escape_requires_approval", "unknown", "could not confirm workspace escape approval gate source evidence from working_root")
	}
}

func writeDoctorIssueCheck(b *strings.Builder, issue string, status string, evidence string) {
	writeDoctorLine(b, fmt.Sprintf("- issue=%s status=%s evidence=%q",
		strings.TrimSpace(issue),
		strings.TrimSpace(status),
		truncatePreview(evidence, 600),
	))
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

func doctorPathListContains(paths []string, want string) bool {
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, path := range paths {
		if filepath.ToSlash(strings.TrimSpace(path)) == want {
			return true
		}
	}
	return false
}

func doctorPromptContextHasFile(ctx *workspace.PromptContext, want string) bool {
	if ctx == nil {
		return false
	}
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, file := range append(append([]workspace.LoadedFile{}, ctx.Stable...), ctx.Dynamic...) {
		path := filepath.ToSlash(strings.TrimSpace(file.Path))
		if path == want || strings.HasSuffix(path, "/"+want) {
			return true
		}
	}
	return false
}

func doctorSourceContainsAll(root string, rel string, needles []string) bool {
	root = strings.TrimSpace(root)
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if root == "" || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	text := string(data)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}

func doctorSourceMatches(root string, dirs []string, needles []string, includeTests bool, limit int) []string {
	root = strings.TrimSpace(root)
	if root == "" || limit == 0 {
		return nil
	}
	if limit < 0 {
		limit = 8
	}
	lowerNeedles := make([]string, 0, len(needles))
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" {
			lowerNeedles = append(lowerNeedles, needle)
		}
	}
	if len(lowerNeedles) == 0 {
		return nil
	}

	var matches []string
	for _, dir := range dirs {
		if len(matches) >= limit {
			break
		}
		relDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(dir)))
		if relDir == "" || relDir == "." || strings.HasPrefix(relDir, "..") {
			continue
		}
		base := filepath.Join(root, relDir)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil || len(matches) >= limit {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == ".git" || name == "vendor" || name == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || (!includeTests && strings.HasSuffix(name, "_test.go")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := strings.ToLower(string(data))
			for _, needle := range lowerNeedles {
				if strings.Contains(text, needle) {
					if rel, relErr := filepath.Rel(root, path); relErr == nil {
						matches = append(matches, filepath.ToSlash(rel))
					} else {
						matches = append(matches, filepath.ToSlash(path))
					}
					break
				}
			}
			return nil
		}); err != nil {
			continue
		}
	}
	sort.Strings(matches)
	return matches
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
