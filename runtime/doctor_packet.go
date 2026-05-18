//go:build linux

package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

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

	writeDoctorSection(&b, "Provider Health")
	r.writeDoctorProviderHealth(&b, now)

	writeDoctorSection(&b, "Autonomy")
	r.writeDoctorAutonomyStatus(&b, input.Key, input.Message.SenderID, now)

	writeDoctorSection(&b, "Authority Projection")
	r.writeDoctorAuthorityProjection(&b, now)

	writeDoctorSection(&b, "Sandbox Readiness")
	r.writeDoctorSandboxReadiness(&b, now)

	writeDoctorSection(&b, "Current Session")
	writeDoctorSessionSummary(&b, input.Session)
	writeDoctorRecentMessages(&b, input.Session, doctorMessageLimit)

	writeDoctorSection(&b, "Telegram Threads")
	r.writeDoctorTelegramThreads(&b, input.Key)

	writeDoctorSection(&b, "Mission Ledger")
	r.writeDoctorMissionLedger(&b, input.Key, now)

	writeDoctorSection(&b, "Prompt Context Inventory")
	writeDoctorPromptInventory(&b, input.PromptContext)

	writeDoctorSection(&b, "Memory Footprint")
	r.writeDoctorMemoryFootprint(&b, input.Scope, now)

	writeDoctorSection(&b, "Maintainer Delegate")
	writeDoctorMaintainerDelegate(&b, input.Maintainer)

	writeDoctorSection(&b, "Known Issue Status Checks")
	r.writeDoctorIssueStatusChecks(&b, input)

	writeDoctorSection(&b, "Design Principle Health")
	r.writeDoctorDesignPrincipleHealth(&b, input)

	writeDoctorSection(&b, "External Tool Invocation Readiness")
	r.writeDoctorExternalToolInvocationReadiness(&b, input)

	writeDoctorSection(&b, "External Channel Adapter Readiness")
	r.writeDoctorExternalChannelAdapterReadiness(&b, input)

	writeDoctorSection(&b, "Execution Events")
	r.writeDoctorExecutionEvents(ctx, &b, input.Key, now)

	writeDoctorSection(&b, "Runtime Adjudications")
	r.writeDoctorRuntimeAdjudications(ctx, &b, input.Key, now)

	writeDoctorSection(&b, "Turn Runs")
	r.writeDoctorTurnRuns(ctx, &b, now)

	writeDoctorSection(&b, "Semantic Store")
	r.writeDoctorSemanticStats(&b)

	writeDoctorSection(&b, "Tailnet")
	r.writeDoctorTailnetDiagnostics(ctx, &b)

	writeDoctorSection(&b, "Recent Service Log Tail")
	r.writeDoctorLogTail(&b)

	writeDoctorSection(&b, "Codex Work Evidence Review")
	r.writeDoctorCodexWorkEvidenceReview(ctx, &b, input)

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
	warnings := r.cfg.Warnings()
	writeDoctorKV(b, "config_ignored_key_count", strconv.Itoa(len(warnings)))
	for _, warning := range warnings {
		writeDoctorLine(b, fmt.Sprintf("config_ignored_key=%s message=%s", strconv.Quote(strings.TrimSpace(warning.Path)), strconv.Quote(strings.TrimSpace(warning.Message))))
	}
	writeDoctorKV(b, "identity_anonymous_profile", strconv.FormatBool(r.cfg.Identity.AnonymousProfile))
	writeDoctorKV(b, "identity_governor_name_effective", r.governorName())
	writeDoctorKV(b, "identity_face_name_effective", r.faceName())
	writeDoctorKV(b, "codex_context_window", strconv.Itoa(r.cfg.Governor.Codex.ContextWindow))
	writeDoctorKV(b, "codex_transport_retries", strconv.Itoa(r.cfg.Governor.Codex.TransportRetries))
	workStatus := WorkExecutorStatus{}
	if r.workExecutor != nil {
		workStatus = r.workExecutor.Status()
	}
	writeDoctorKV(b, "work_executor_configured", firstNonEmpty(strings.TrimSpace(workStatus.Configured), strings.TrimSpace(r.cfg.Work.Executor)))
	writeDoctorKV(b, "work_executor_preferred", firstNonEmpty(strings.TrimSpace(workStatus.Preferred), firstRuntimeWorkExecutor(r.cfg.Work)))
	writeDoctorKV(b, "work_executor_active", strings.TrimSpace(workStatus.Active))
	writeDoctorKV(b, "work_executor_last_attempted", strings.TrimSpace(workStatus.LastAttempted))
	writeDoctorKV(b, "work_executor_fallback_reason", strings.TrimSpace(workStatus.FallbackReason))
	writeDoctorKV(b, "work_executor_last_error", strings.TrimSpace(workStatus.LastError))
	writeDoctorKV(b, "codex_work_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	autonomy := r.AutonomyStatusSnapshot()
	writeDoctorKV(b, "autonomy_default_mode", strings.TrimSpace(autonomy.DefaultMode))
	writeDoctorKV(b, "autonomy_ceiling", strings.TrimSpace(autonomy.Ceiling))
	writeDoctorKV(b, "autonomy_live_overrides", strconv.FormatBool(autonomy.AllowLiveOverrides))
	writeDoctorKV(b, "autonomy_max_override_duration", autonomy.MaxOverrideDuration.Truncate(time.Second).String())
	writeDoctorKV(b, "autonomy_authority_behavior", strings.TrimSpace(autonomy.AuthorityBehavior))
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

func (r *Runtime) writeDoctorAutonomyStatus(b *strings.Builder, key session.SessionKey, senderID int64, now time.Time) {
	if r == nil || r.cfg == nil {
		writeDoctorLine(b, "autonomy_status: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	snapshot := r.autonomyStatusSnapshot(key.ChatID, senderID, now)
	writeDoctorKV(b, "autonomy_effective_default_mode", strings.TrimSpace(snapshot.DefaultMode))
	writeDoctorKV(b, "autonomy_effective_ceiling", strings.TrimSpace(snapshot.Ceiling))
	writeDoctorKV(b, "autonomy_effective_live_overrides", strconv.FormatBool(snapshot.AllowLiveOverrides))
	writeDoctorKV(b, "autonomy_effective_max_override_duration", snapshot.MaxOverrideDuration.Truncate(time.Second).String())

	rawActiveCount := 0
	if r.store != nil && key.ChatID != 0 {
		rawModes, rawErr := r.store.ActiveOperatorAutonomyOverrides(key.ChatID, now)
		if rawErr != nil {
			writeDoctorLine(b, "autonomy_raw_active_mode_error="+strconv.Quote(rawErr.Error()))
		} else {
			rawActiveCount = len(rawModes)
		}
	}
	writeDoctorKV(b, "autonomy_raw_active_mode_count", strconv.Itoa(rawActiveCount))
	active := strings.TrimSpace(snapshot.ActiveOverrideMode) != ""
	writeDoctorKV(b, "autonomy_effective_active_override", strconv.FormatBool(active))
	if active {
		writeDoctorKV(b, "autonomy_active_override_mode", strings.TrimSpace(snapshot.ActiveOverrideMode))
		writeDoctorKV(b, "autonomy_active_override_scope", strings.TrimSpace(snapshot.ActiveOverrideScope))
		writeDoctorKV(b, "autonomy_active_override_actor", strings.TrimSpace(snapshot.ActiveOverrideActor))
		if !snapshot.ActiveOverrideExpiry.IsZero() {
			writeDoctorKV(b, "autonomy_active_override_expires_at", snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339))
			writeDoctorKV(b, "autonomy_active_override_remaining", roundDuration(snapshot.ActiveOverrideExpiry.Sub(now)))
		}
	}

	precedenceStatus := "inactive"
	precedenceReason := "no active override"
	if err := r.validateAutonomyLiveOverride("leased", 0); err != nil {
		precedenceStatus = "blocked_by_config"
		precedenceReason = err.Error()
	} else if active {
		precedenceStatus = "active_within_ceiling"
		precedenceReason = "leased override is within configured ceiling"
	} else if rawActiveCount > 0 {
		precedenceStatus = "blocked_or_filtered"
		precedenceReason = "raw active mode exists but no effective override was selected"
	}
	writeDoctorKV(b, "autonomy_precedence_status", precedenceStatus)
	writeDoctorKV(b, "autonomy_precedence_reason", precedenceReason)

	expiryStatus := "none"
	if active {
		if snapshot.ActiveOverrideExpiry.After(now) {
			expiryStatus = "active_until_expiry"
		} else {
			expiryStatus = "expired"
		}
	}
	writeDoctorKV(b, "autonomy_expiry_status", expiryStatus)
}

func (r *Runtime) writeDoctorSandboxReadiness(b *strings.Builder, now time.Time) {
	if r == nil || r.cfg == nil {
		writeDoctorLine(b, "sandbox_readiness: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	snapshot := r.sandboxReadinessSnapshot(now)
	writeDoctorKV(b, "sandbox_readiness_issue_count", strconv.Itoa(len(snapshot.Issues)))
	for _, issue := range snapshot.Issues {
		writeDoctorLine(b, fmt.Sprintf(
			"sandbox_readiness_issue role=%s mode=%s network=%s code=%s severity=%s summary=%s next_repair=%s",
			strconv.Quote(strings.TrimSpace(issue.Role)),
			strconv.Quote(strings.TrimSpace(issue.Mode)),
			strconv.Quote(strings.TrimSpace(issue.Network)),
			strconv.Quote(strings.TrimSpace(issue.Code)),
			strconv.Quote(strings.TrimSpace(issue.Severity)),
			strconv.Quote(strings.TrimSpace(issue.Summary)),
			strconv.Quote(strings.TrimSpace(issue.NextRepairAction)),
		))
	}
}

func (r *Runtime) writeDoctorCodexWorkEvidenceReview(ctx context.Context, b *strings.Builder, input doctorDiagnosticInput) {
	if r == nil || r.store == nil {
		writeDoctorLine(b, "codex_work_evidence_review: unavailable")
		return
	}
	opState, err := r.store.OperationState(input.Key)
	if err != nil {
		writeDoctorLine(b, "codex_work_operation_error="+strconv.Quote(err.Error()))
		return
	}
	opState = session.NormalizeOperationState(opState)
	work := opState.Work
	writeDoctorKV(b, "codex_work_executor", strings.TrimSpace(work.Executor))
	writeDoctorKV(b, "codex_work_configured_executor", strings.TrimSpace(work.ConfiguredExecutor))
	writeDoctorKV(b, "codex_work_preferred_executor", strings.TrimSpace(work.PreferredExecutor))
	writeDoctorKV(b, "codex_work_lane_mode", strings.TrimSpace(work.CodexLaneMode))
	writeDoctorKV(b, "codex_work_thread_id", strings.TrimSpace(work.CodexThreadID))
	writeDoctorKV(b, "codex_work_turn_id", strings.TrimSpace(work.CodexLastTurnID))
	writeDoctorKV(b, "codex_work_commit_lane_status", strings.TrimSpace(work.CommitLaneStatus))
	writeDoctorKV(b, "codex_work_changed_files", strconv.Itoa(len(work.ChangedFiles)))
	writeDoctorKV(b, "codex_work_commands", strconv.Itoa(len(work.Commands)))
	counts := codexWorkEventCounts(work.CodexEvents)
	writeDoctorKV(b, "codex_work_event_count", strconv.Itoa(len(work.CodexEvents)))
	writeDoctorKV(b, "codex_work_file_change_events", strconv.Itoa(counts["file_change"]))
	writeDoctorKV(b, "codex_work_command_events", strconv.Itoa(counts["command"]))
	writeDoctorKV(b, "codex_work_user_input_events", strconv.Itoa(counts["user_input"]))
	writeDoctorKV(b, "codex_work_subagent_events", strconv.Itoa(counts["subagent"]))
	writeDoctorKV(b, "codex_work_mcp_events", strconv.Itoa(counts["mcp"]))
	writeDoctorKV(b, "codex_work_auto_review_events", strconv.Itoa(counts["auto_review"]))
	writeDoctorKV(b, "codex_work_rollout_history_events", strconv.Itoa(counts["rollout_history"]))
	if len(work.CodexEvents) > 0 {
		writeDoctorLine(b, "codex_work_recent_events:")
		start := len(work.CodexEvents) - 8
		if start < 0 {
			start = 0
		}
		for _, event := range work.CodexEvents[start:] {
			writeDoctorLine(b, fmt.Sprintf("- kind=%s method=%s status=%s subject=%q path=%q command=%q",
				strings.TrimSpace(event.Kind),
				strings.TrimSpace(event.Method),
				strings.TrimSpace(event.Status),
				truncatePreview(event.Subject, 180),
				truncatePreview(event.Path, 180),
				truncatePreview(event.Command, 220),
			))
		}
	}
	if preview := strings.TrimSpace(work.PatchPreview); preview != "" {
		writeDoctorKV(b, "codex_work_patch_preview_chars", strconv.Itoa(len(preview)))
	}
	continuation, continuationErr := r.store.ContinuationState(input.Key)
	if continuationErr != nil {
		writeDoctorLine(b, "codex_work_continuation_error="+strconv.Quote(continuationErr.Error()))
	} else {
		continuation = session.NormalizeContinuationState(continuation)
		writeDoctorKV(b, "codex_work_continuation_status", string(continuation.Status))
		writeDoctorKV(b, "codex_work_lease_status", string(continuation.ContinuationLease.Status))
		writeDoctorKV(b, "codex_work_lease_id", strings.TrimSpace(continuation.ContinuationLease.ID))
		writeDoctorKV(b, "codex_work_lease_expires_at", continuation.ContinuationLease.ExpiresAt.UTC().Format(time.RFC3339))
		writeDoctorKV(b, "codex_work_continuation_mode", string(continuationWorkMode(continuation)))
		writeDoctorKV(b, "codex_work_continuation_eligible", strconv.FormatBool(r.shouldRouteContinuationThroughWorkExecutor(continuation)))
	}
	if r.cfg != nil {
		writeDoctorKV(b, "codex_work_config_executor", strings.TrimSpace(r.cfg.Work.Executor))
		writeDoctorKV(b, "codex_work_config_auto_order", strings.Join(r.cfg.Work.AutoOrder, " -> "))
		writeDoctorKV(b, "codex_work_config_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	}
	recentWorkEvents := 0
	if events, eventErr := r.store.ExecutionEventsRecent(120); eventErr == nil {
		for _, event := range events {
			if strings.HasPrefix(strings.TrimSpace(event.EventType), "work.executor.") {
				recentWorkEvents++
			}
		}
		writeDoctorKV(b, "codex_work_recent_executor_events", strconv.Itoa(recentWorkEvents))
	} else {
		writeDoctorLine(b, "codex_work_recent_events_error="+strconv.Quote(eventErr.Error()))
	}
	status := "not_started"
	switch {
	case len(work.CodexEvents) > 0:
		status = "evidence_present"
	case strings.EqualFold(strings.TrimSpace(work.Executor), "codex") || strings.TrimSpace(work.CodexThreadID) != "":
		status = "needs_event_evidence_review"
	case r.cfg != nil && strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress) == "":
		status = "codex_app_server_unconfigured"
	}
	writeDoctorKV(b, "codex_work_evidence_status", status)
	writeDoctorLine(b, "codex_work_evidence_next=\"Before expanding Codex runtime features, confirm live operation_state.work carries codex_events, patch_preview, commit_lane_status, thread ids, and recent work.executor execution events.\"")
	_ = ctx
}

func codexWorkEventCounts(events []session.WorkCodexEvent) map[string]int {
	counts := map[string]int{}
	for _, event := range events {
		if kind := strings.TrimSpace(event.Kind); kind != "" {
			counts[kind]++
		}
	}
	return counts
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

func (r *Runtime) writeDoctorMissionLedger(b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		writeDoctorLine(b, "mission_ledger: unavailable")
		return
	}
	health, err := r.store.MissionLedgerHealth(now)
	if err != nil {
		writeDoctorLine(b, "mission_ledger_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorKV(b, "mission_active", strconv.Itoa(health.ActiveCount))
	writeDoctorKV(b, "mission_pinned", strconv.Itoa(health.PinnedCount))
	writeDoctorKV(b, "mission_recurring", strconv.Itoa(health.RecurringCount))
	writeDoctorKV(b, "mission_blocked", strconv.Itoa(health.BlockedCount))
	writeDoctorKV(b, "mission_self_continuation_enabled", strconv.Itoa(health.SelfContinuationEnabledCount))
	writeDoctorKV(b, "mission_stale_candidates", strconv.Itoa(health.StaleCandidateCount))
	writeDoctorKV(b, "mission_pending_handoffs", strconv.Itoa(health.PendingHandoffCount))
	if working, err := r.store.WorkingObjective(key); err == nil && strings.TrimSpace(working.Objective) != "" {
		writeDoctorKV(b, "working_objective", truncatePreview(working.Objective, 400))
	}
	handoffs, err := r.store.MissionHandoffs(session.MissionHandoffFilter{Status: "pending", Limit: 5})
	if err != nil {
		writeDoctorLine(b, "mission_handoff_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "pending_mission_handoffs:")
		if len(handoffs) == 0 {
			writeDoctorLine(b, "- none")
		}
		for _, handoff := range handoffs {
			writeDoctorLine(b, fmt.Sprintf("- id=%s mission_id=%s operation_id=%s action=%q question=%q", handoff.ID, handoff.MissionID, handoff.OperationID, truncatePreview(handoff.PlannedAction, 120), truncatePreview(handoff.RecoveryQuestion, 120)))
		}
	}
	results, err := r.store.MissionResults(5)
	if err != nil {
		writeDoctorLine(b, "mission_result_error="+strconv.Quote(err.Error()))
	} else {
		writeDoctorLine(b, "recent_mission_results:")
		if len(results) == 0 {
			writeDoctorLine(b, "- none")
		}
		for _, result := range results {
			writeDoctorLine(b, fmt.Sprintf("- id=%s handoff_id=%s mission_id=%s operation_id=%s status=%s summary=%q", result.ID, result.HandoffID, result.MissionID, result.OperationID, result.Status, truncatePreview(result.Summary, 120)))
		}
	}
	missions, err := r.store.Missions(session.MissionFilter{Limit: 12})
	if err != nil {
		writeDoctorLine(b, "mission_list_error="+strconv.Quote(err.Error()))
		return
	}
	writeDoctorLine(b, "recent_missions:")
	if len(missions) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	for _, mission := range missions {
		writeDoctorLine(b, fmt.Sprintf("- id=%s status=%s pinned=%t owner=%s title=%q self_continue=%t", mission.ID, mission.Status, mission.Pinned, mission.Owner, truncatePreview(mission.Title, 120), mission.Authority.CanSelfContinue))
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
