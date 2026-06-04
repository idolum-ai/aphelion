//go:build linux

package doctor

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/releaseinfo"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

func (r *Runtime) BuildDiagnosticPacket(ctx context.Context, input DiagnosticInput) string {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var b strings.Builder
	WriteLine(&b, RequestMarker)
	WriteKV(&b, "generated_at_utc", now.Format(time.RFC3339))
	WriteKV(&b, "chat_id", strconv.FormatInt(input.Key.ChatID, 10))
	WriteKV(&b, "sender_id", strconv.FormatInt(input.Message.SenderID, 10))
	WriteKV(&b, "run_kind", string(session.TurnRunKindDoctor))
	WriteKV(&b, "mode", "read_only")

	WriteSection(&b, "Effective Runtime")
	r.writeDoctorRuntimeConfig(&b, input.Exec, input.Scope)

	WriteSection(&b, "Release Metadata")
	writeDoctorReleaseMetadata(&b)

	WriteSection(&b, "Provider Health")
	if r.writeDoctorProviderHealth != nil {
		r.writeDoctorProviderHealth(&b, now)
	} else {
		WriteLine(&b, "provider_health: unavailable")
	}

	WriteSection(&b, "Perception Budget")
	if r.writeDoctorPerceptionBudget != nil {
		r.writeDoctorPerceptionBudget(&b, input.Key, now)
	} else {
		WriteLine(&b, "perception_budget: unavailable")
	}

	WriteSection(&b, "Autonomy")
	r.writeDoctorAutonomyStatus(&b, input.Key, input.Message.SenderID, now)

	WriteSection(&b, "Authority Projection")
	if r.writeDoctorAuthorityProjection != nil {
		r.writeDoctorAuthorityProjection(&b, now)
	} else {
		WriteLine(&b, "authority_projection: unavailable")
	}

	WriteSection(&b, "Web Search")
	r.writeDoctorWebSearchStatus(&b)

	WriteSection(&b, "Sandbox Readiness")
	r.writeDoctorSandboxReadiness(&b, now)

	WriteSection(&b, "Current Session")
	writeDoctorSessionSummary(&b, input.Session)
	writeDoctorRecentMessages(&b, input.Session, MessageLimit)

	WriteSection(&b, "Telegram Threads")
	r.writeDoctorTelegramThreads(&b, input.Key)

	WriteSection(&b, "Mission Ledger")
	r.writeDoctorMissionLedger(&b, input.Key, now)

	WriteSection(&b, "Prompt Context Inventory")
	writeDoctorPromptInventory(&b, input.PromptContext)

	WriteSection(&b, "Memory Footprint")
	r.writeDoctorMemoryFootprint(&b, input.Scope, now)

	WriteSection(&b, "Maintainer Delegate")
	writeDoctorMaintainerDelegate(&b, input.Maintainer)

	WriteSection(&b, "Known Issue Status Checks")
	r.writeDoctorIssueStatusChecks(&b, input)

	WriteSection(&b, "Design Principle Health")
	r.writeDoctorDesignPrincipleHealth(&b, input)

	WriteSection(&b, "External Tool Invocation Readiness")
	r.writeDoctorExternalToolInvocationReadiness(&b, input)

	WriteSection(&b, "External Channel Adapter Readiness")
	if r.writeDoctorExternalChannelAdapterReadiness != nil {
		r.writeDoctorExternalChannelAdapterReadiness(&b, input)
	} else {
		WriteLine(&b, "external_channel_adapter_readiness: unavailable")
	}

	WriteSection(&b, "Execution Events")
	r.writeDoctorExecutionEvents(ctx, &b, input.Key, now)

	WriteSection(&b, "Runtime Adjudications")
	r.writeDoctorRuntimeAdjudications(ctx, &b, input.Key, now)

	WriteSection(&b, "Turn Runs")
	r.writeDoctorTurnRuns(ctx, &b, now)

	WriteSection(&b, "Semantic Store")
	r.writeDoctorSemanticStats(&b)

	WriteSection(&b, "Tailnet")
	r.writeDoctorTailnetDiagnostics(ctx, &b)

	WriteSection(&b, "Recent Service Log Tail")
	r.writeDoctorLogTail(&b)

	WriteSection(&b, "Codex Work Evidence Review")
	r.WriteCodexWorkEvidenceReview(ctx, &b, input)

	WriteSection(&b, "Doctor Instructions")
	WriteLine(&b, "Analyze the evidence above and the loaded prompt/memory context. Identify likely causes, residual risks, and specific follow-up work. Do not perform actions. Before reporting a failure as current, check whether the Known Issue Status Checks or newer runtime evidence indicate it has already been fixed.")

	packet := RedactText(b.String())
	if len(packet) > PacketMaxChars {
		packet = strings.TrimSpace(packet[:PacketMaxChars]) + "\n\n[doctor diagnostic packet truncated]"
	}
	return packet
}

func (r *Runtime) writeDoctorRuntimeConfig(b *strings.Builder, exec pipeline.TurnExecutionContract, scope sandbox.Scope) {
	if r == nil || r.cfg == nil {
		WriteLine(b, "runtime_config: unavailable")
		return
	}
	WriteKV(b, "governor_backend", strings.TrimSpace(exec.Backend))
	WriteKV(b, "governor_provider", strings.TrimSpace(exec.ProviderName))
	WriteKV(b, "governor_model", strings.TrimSpace(exec.ModelName))
	WriteKV(b, "provider_path", strings.Join(exec.ProviderPath, " -> "))
	WriteKV(b, "configured_provider_chain", strings.Join(config.EffectiveProviderChain(r.cfg), " -> "))
	warnings := r.cfg.Warnings()
	WriteKV(b, "config_ignored_key_count", strconv.Itoa(len(warnings)))
	for _, warning := range warnings {
		WriteLine(b, fmt.Sprintf("config_ignored_key=%s message=%s", strconv.Quote(strings.TrimSpace(warning.Path)), strconv.Quote(strings.TrimSpace(warning.Message))))
	}
	WriteKV(b, "identity_anonymous_profile", strconv.FormatBool(r.cfg.Identity.AnonymousProfile))
	WriteKV(b, "identity_governor_name_effective", callString(r.governorName))
	WriteKV(b, "identity_face_name_effective", callString(r.faceName))
	WriteKV(b, "codex_context_window", strconv.Itoa(r.cfg.Governor.Codex.ContextWindow))
	WriteKV(b, "codex_transport_retries", strconv.Itoa(r.cfg.Governor.Codex.TransportRetries))
	WriteKV(b, "codex_response_header_timeout", r.cfg.Governor.Codex.ResponseHeaderTimeout)
	workStatus := WorkExecutorStatus{}
	if r.workExecutorStatus != nil {
		workStatus = r.workExecutorStatus()
	}
	WriteKV(b, "work_executor_configured", firstNonEmpty(strings.TrimSpace(workStatus.Configured), strings.TrimSpace(r.cfg.Work.Executor)))
	WriteKV(b, "work_executor_preferred", firstNonEmpty(strings.TrimSpace(workStatus.Preferred), firstConfiguredWorkExecutor(r.cfg.Work)))
	WriteKV(b, "work_executor_active", strings.TrimSpace(workStatus.Active))
	WriteKV(b, "work_executor_last_attempted", strings.TrimSpace(workStatus.LastAttempted))
	WriteKV(b, "work_executor_fallback_reason", strings.TrimSpace(workStatus.FallbackReason))
	WriteKV(b, "work_executor_last_error", strings.TrimSpace(workStatus.LastError))
	WriteKV(b, "codex_work_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	autonomy := core.AutonomyStatusSnapshot{}
	if r.AutonomyStatusSnapshot != nil {
		autonomy = r.AutonomyStatusSnapshot()
	}
	WriteKV(b, "autonomy_default_mode", strings.TrimSpace(autonomy.DefaultMode))
	WriteKV(b, "autonomy_ceiling", strings.TrimSpace(autonomy.Ceiling))
	WriteKV(b, "autonomy_live_overrides", strconv.FormatBool(autonomy.AllowLiveOverrides))
	WriteKV(b, "autonomy_max_override_duration", autonomy.MaxOverrideDuration.Truncate(time.Second).String())
	WriteKV(b, "autonomy_authority_behavior", strings.TrimSpace(autonomy.AuthorityBehavior))
	WriteKV(b, "session_max_context_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.MaxContextRatio))
	WriteKV(b, "session_compaction_ratio", fmt.Sprintf("%.2f", r.cfg.Sessions.CompactionRatio))
	WriteKV(b, "bootstrap_total_max_chars", strconv.Itoa(r.cfg.Agent.BootstrapTotalMaxChars))
	WriteKV(b, "memory_semantic_enabled", strconv.FormatBool(r.cfg.Memory.Semantic.Enabled))
	WriteKV(b, "memory_aggressive_enabled", strconv.FormatBool(r.cfg.Memory.Aggressive.Enabled))
	WriteKV(b, "memory_aggressive_flush_on_boundary", strconv.FormatBool(r.cfg.Memory.Aggressive.FlushOnSessionBoundary))
	WriteKV(b, "heartbeat_enabled", strconv.FormatBool(r.cfg.Heartbeat.Enabled))
	WriteKV(b, "cron_enabled", strconv.FormatBool(r.cfg.Cron.Enabled))
	WriteKV(b, "prompt_root", r.cfg.Agent.PromptRoot)
	WriteKV(b, "exec_root", r.cfg.Agent.ExecRoot)
	WriteKV(b, "shared_memory_root", strings.TrimSpace(scope.SharedMemoryRoot))
	WriteKV(b, "working_root", strings.TrimSpace(scope.WorkingRoot))
}

func (r *Runtime) writeDoctorAutonomyStatus(b *strings.Builder, key session.SessionKey, senderID int64, now time.Time) {
	if r == nil || r.cfg == nil {
		WriteLine(b, "autonomy_status: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	snapshot := core.AutonomyStatusSnapshot{}
	if r.autonomyStatusSnapshot != nil {
		snapshot = r.autonomyStatusSnapshot(key.ChatID, senderID, now)
	}
	WriteKV(b, "autonomy_effective_default_mode", strings.TrimSpace(snapshot.DefaultMode))
	WriteKV(b, "autonomy_effective_ceiling", strings.TrimSpace(snapshot.Ceiling))
	WriteKV(b, "autonomy_effective_live_overrides", strconv.FormatBool(snapshot.AllowLiveOverrides))
	WriteKV(b, "autonomy_effective_max_override_duration", snapshot.MaxOverrideDuration.Truncate(time.Second).String())

	rawActiveCount := 0
	if r.store != nil && key.ChatID != 0 {
		rawModes, rawErr := r.store.ActiveOperatorAutonomyOverrides(key.ChatID, now)
		if rawErr != nil {
			WriteLine(b, "autonomy_raw_active_mode_error="+strconv.Quote(rawErr.Error()))
		} else {
			rawActiveCount = len(rawModes)
		}
	}
	WriteKV(b, "autonomy_raw_active_mode_count", strconv.Itoa(rawActiveCount))
	active := strings.TrimSpace(snapshot.ActiveOverrideMode) != ""
	WriteKV(b, "autonomy_effective_active_override", strconv.FormatBool(active))
	if active {
		WriteKV(b, "autonomy_active_override_mode", strings.TrimSpace(snapshot.ActiveOverrideMode))
		WriteKV(b, "autonomy_active_override_scope", strings.TrimSpace(snapshot.ActiveOverrideScope))
		WriteKV(b, "autonomy_active_override_actor", strings.TrimSpace(snapshot.ActiveOverrideActor))
		if !snapshot.ActiveOverrideExpiry.IsZero() {
			WriteKV(b, "autonomy_active_override_expires_at", snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339))
			WriteKV(b, "autonomy_active_override_remaining", roundDuration(snapshot.ActiveOverrideExpiry.Sub(now)))
		}
	}

	precedenceStatus := "inactive"
	precedenceReason := "no active override"
	var precedenceErr error
	if r.validateAutonomyLiveOverride != nil {
		precedenceErr = r.validateAutonomyLiveOverride("leased", 0)
	}
	if precedenceErr != nil {
		precedenceStatus = "blocked_by_config"
		precedenceReason = precedenceErr.Error()
	} else if active {
		precedenceStatus = "active_within_ceiling"
		precedenceReason = "leased override is within configured ceiling"
	} else if rawActiveCount > 0 {
		precedenceStatus = "blocked_or_filtered"
		precedenceReason = "raw active mode exists but no effective override was selected"
	}
	WriteKV(b, "autonomy_precedence_status", precedenceStatus)
	WriteKV(b, "autonomy_precedence_reason", precedenceReason)

	expiryStatus := "none"
	if active {
		if snapshot.ActiveOverrideExpiry.After(now) {
			expiryStatus = "active_until_expiry"
		} else {
			expiryStatus = "expired"
		}
	}
	WriteKV(b, "autonomy_expiry_status", expiryStatus)
}

func (r *Runtime) writeDoctorSandboxReadiness(b *strings.Builder, now time.Time) {
	if r == nil || r.cfg == nil {
		WriteLine(b, "sandbox_readiness: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	snapshot := core.SandboxReadinessSnapshot{}
	if r.sandboxReadinessSnapshot != nil {
		snapshot = r.sandboxReadinessSnapshot(now)
	}
	WriteKV(b, "sandbox_readiness_issue_count", strconv.Itoa(len(snapshot.Issues)))
	for _, issue := range snapshot.Issues {
		WriteLine(b, fmt.Sprintf(
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

func (r *Runtime) WriteCodexWorkEvidenceReview(ctx context.Context, b *strings.Builder, input DiagnosticInput) {
	if r == nil || r.store == nil {
		WriteLine(b, "codex_work_evidence_review: unavailable")
		return
	}
	opState, err := r.store.OperationState(input.Key)
	if err != nil {
		WriteLine(b, "codex_work_operation_error="+strconv.Quote(err.Error()))
		return
	}
	opState = session.NormalizeOperationState(opState)
	work := opState.Work
	WriteKV(b, "codex_work_executor", strings.TrimSpace(work.Executor))
	WriteKV(b, "codex_work_configured_executor", strings.TrimSpace(work.ConfiguredExecutor))
	WriteKV(b, "codex_work_preferred_executor", strings.TrimSpace(work.PreferredExecutor))
	WriteKV(b, "codex_work_lane_mode", strings.TrimSpace(work.CodexLaneMode))
	WriteKV(b, "codex_work_thread_id", strings.TrimSpace(work.CodexThreadID))
	WriteKV(b, "codex_work_turn_id", strings.TrimSpace(work.CodexLastTurnID))
	WriteKV(b, "codex_work_commit_lane_status", strings.TrimSpace(work.CommitLaneStatus))
	WriteKV(b, "codex_work_changed_files", strconv.Itoa(len(work.ChangedFiles)))
	WriteKV(b, "codex_work_commands", strconv.Itoa(len(work.Commands)))
	counts := codexWorkEventCounts(work.CodexEvents)
	WriteKV(b, "codex_work_event_count", strconv.Itoa(len(work.CodexEvents)))
	WriteKV(b, "codex_work_file_change_events", strconv.Itoa(counts["file_change"]))
	WriteKV(b, "codex_work_command_events", strconv.Itoa(counts["command"]))
	WriteKV(b, "codex_work_user_input_events", strconv.Itoa(counts["user_input"]))
	WriteKV(b, "codex_work_subagent_events", strconv.Itoa(counts["subagent"]))
	WriteKV(b, "codex_work_mcp_events", strconv.Itoa(counts["mcp"]))
	WriteKV(b, "codex_work_auto_review_events", strconv.Itoa(counts["auto_review"]))
	WriteKV(b, "codex_work_rollout_history_events", strconv.Itoa(counts["rollout_history"]))
	if len(work.CodexEvents) > 0 {
		WriteLine(b, "codex_work_recent_events:")
		start := len(work.CodexEvents) - 8
		if start < 0 {
			start = 0
		}
		for _, event := range work.CodexEvents[start:] {
			WriteLine(b, fmt.Sprintf("- kind=%s method=%s status=%s subject=%q path=%q command=%q",
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
		WriteKV(b, "codex_work_patch_preview_chars", strconv.Itoa(len(preview)))
	}
	continuation, continuationErr := r.store.ContinuationState(input.Key)
	if continuationErr != nil {
		WriteLine(b, "codex_work_continuation_error="+strconv.Quote(continuationErr.Error()))
	} else {
		continuation = session.NormalizeContinuationState(continuation)
		WriteKV(b, "codex_work_continuation_status", string(continuation.Status))
		WriteKV(b, "codex_work_lease_status", string(continuation.ContinuationLease.Status))
		WriteKV(b, "codex_work_lease_id", strings.TrimSpace(continuation.ContinuationLease.ID))
		WriteKV(b, "codex_work_lease_expires_at", continuation.ContinuationLease.ExpiresAt.UTC().Format(time.RFC3339))
		WriteKV(b, "codex_work_continuation_mode", string(ContinuationWorkMode(continuation)))
		WriteKV(b, "codex_work_continuation_eligible", strconv.FormatBool(r.shouldRouteContinuationThroughWorkExecutor != nil && r.shouldRouteContinuationThroughWorkExecutor(continuation)))
	}
	if r.cfg != nil {
		WriteKV(b, "codex_work_config_executor", strings.TrimSpace(r.cfg.Work.Executor))
		WriteKV(b, "codex_work_config_auto_order", strings.Join(r.cfg.Work.AutoOrder, " -> "))
		WriteKV(b, "codex_work_config_app_server", strings.TrimSpace(r.cfg.Work.Codex.AppServerAddress))
	}
	recentWorkEvents := 0
	if events, eventErr := r.store.ExecutionEventsRecent(120); eventErr == nil {
		for _, event := range events {
			if strings.HasPrefix(strings.TrimSpace(event.EventType), "work.executor.") {
				recentWorkEvents++
			}
		}
		WriteKV(b, "codex_work_recent_executor_events", strconv.Itoa(recentWorkEvents))
	} else {
		WriteLine(b, "codex_work_recent_events_error="+strconv.Quote(eventErr.Error()))
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
	WriteKV(b, "codex_work_evidence_status", status)
	WriteLine(b, "codex_work_evidence_next=\"Before expanding Codex runtime features, confirm live operation_state.work carries codex_events, patch_preview, commit_lane_status, thread ids, and recent work.executor execution events.\"")
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
		WriteLine(b, "session: unavailable")
		return
	}
	WriteKV(b, "session_id", sess.SessionID)
	WriteKV(b, "turn_count", strconv.Itoa(sess.TurnCount))
	WriteKV(b, "message_count", strconv.Itoa(len(sess.Messages)))
	WriteKV(b, "last_provider", sess.LastProvider)
	WriteKV(b, "last_model", sess.LastModel)
	WriteKV(b, "last_error", truncatePreview(sess.LastError, 500))
	WriteKV(b, "last_floor_preview", truncatePreview(sess.LastFloorText, 800))
	WriteKV(b, "total_input_tokens", strconv.FormatInt(sess.TotalInputTokens, 10))
	WriteKV(b, "total_output_tokens", strconv.FormatInt(sess.TotalOutputTokens, 10))
	WriteKV(b, "total_cache_read", strconv.FormatInt(sess.TotalCacheRead, 10))
	WriteKV(b, "total_cache_write", strconv.FormatInt(sess.TotalCacheWrite, 10))
	WriteKV(b, "continuation_status", string(sess.ContinuationState.Status))
	WriteKV(b, "active_tool_calls", strconv.Itoa(sess.ActiveToolCalls))
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
	WriteLine(b, "recent_messages:")
	for _, msg := range sess.Messages[start:] {
		WriteLine(b, fmt.Sprintf("- turn=%d role=%s compacted=%t chars=%d preview=%q",
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
		WriteLine(b, "mission_ledger: unavailable")
		return
	}
	health, err := r.store.MissionLedgerHealth(now)
	if err != nil {
		WriteLine(b, "mission_ledger_error="+strconv.Quote(err.Error()))
		return
	}
	WriteKV(b, "mission_active", strconv.Itoa(health.ActiveCount))
	WriteKV(b, "mission_pinned", strconv.Itoa(health.PinnedCount))
	WriteKV(b, "mission_recurring", strconv.Itoa(health.RecurringCount))
	WriteKV(b, "mission_blocked", strconv.Itoa(health.BlockedCount))
	WriteKV(b, "mission_self_continuation_enabled", strconv.Itoa(health.SelfContinuationEnabledCount))
	WriteKV(b, "mission_stale_candidates", strconv.Itoa(health.StaleCandidateCount))
	WriteKV(b, "mission_pending_handoffs", strconv.Itoa(health.PendingHandoffCount))
	if working, err := r.store.WorkingObjective(key); err == nil && strings.TrimSpace(working.Objective) != "" {
		WriteKV(b, "working_objective", truncatePreview(working.Objective, 400))
	}
	handoffs, err := r.store.MissionHandoffs(session.MissionHandoffFilter{Status: "pending", Limit: 5})
	if err != nil {
		WriteLine(b, "mission_handoff_error="+strconv.Quote(err.Error()))
	} else {
		WriteLine(b, "pending_mission_handoffs:")
		if len(handoffs) == 0 {
			WriteLine(b, "- none")
		}
		for _, handoff := range handoffs {
			WriteLine(b, fmt.Sprintf("- id=%s mission_id=%s operation_id=%s action=%q question=%q", handoff.ID, handoff.MissionID, handoff.OperationID, truncatePreview(handoff.PlannedAction, 120), truncatePreview(handoff.RecoveryQuestion, 120)))
		}
	}
	results, err := r.store.MissionResults(5)
	if err != nil {
		WriteLine(b, "mission_result_error="+strconv.Quote(err.Error()))
	} else {
		WriteLine(b, "recent_mission_results:")
		if len(results) == 0 {
			WriteLine(b, "- none")
		}
		for _, result := range results {
			WriteLine(b, fmt.Sprintf("- id=%s handoff_id=%s mission_id=%s operation_id=%s status=%s summary=%q", result.ID, result.HandoffID, result.MissionID, result.OperationID, result.Status, truncatePreview(result.Summary, 120)))
		}
	}
	missions, err := r.store.Missions(session.MissionFilter{Limit: 12})
	if err != nil {
		WriteLine(b, "mission_list_error="+strconv.Quote(err.Error()))
		return
	}
	WriteLine(b, "recent_missions:")
	if len(missions) == 0 {
		WriteLine(b, "- none")
		return
	}
	for _, mission := range missions {
		WriteLine(b, fmt.Sprintf("- id=%s status=%s pinned=%t owner=%s title=%q self_continue=%t", mission.ID, mission.Status, mission.Pinned, mission.Owner, truncatePreview(mission.Title, 120), mission.Authority.CanSelfContinue))
	}
}

func writeDoctorPromptInventory(b *strings.Builder, ctx *workspace.PromptContext) {
	if ctx == nil {
		WriteLine(b, "prompt_context: unavailable")
		return
	}
	WriteKV(b, "prompt_workspace", ctx.Workspace)
	WriteLine(b, "stable_files:")
	for _, file := range ctx.Stable {
		writeDoctorLoadedFile(b, file)
	}
	WriteLine(b, "dynamic_files:")
	for _, file := range ctx.Dynamic {
		writeDoctorLoadedFile(b, file)
	}
}

func writeDoctorLoadedFile(b *strings.Builder, file workspace.LoadedFile) {
	WriteLine(b, fmt.Sprintf("- path=%s chars=%d truncated=%t preview=%q",
		strings.TrimSpace(file.Path),
		len(file.Content),
		file.Truncated,
		truncatePreview(file.Content, FilePreviewChars),
	))
}

func (r *Runtime) writeDoctorMemoryFootprint(b *strings.Builder, scope sandbox.Scope, now time.Time) {
	root := DynamicPromptRoot(scope)
	WriteKV(b, "memory_root", root)
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

func writeDoctorReleaseMetadata(b *strings.Builder) {
	current := releaseinfo.CurrentBuild()
	notice, err := releaseinfo.NewerReleaseNotice(current, "")
	WriteKV(b, "running_version", firstNonEmpty(current.Version, "unknown"))
	WriteKV(b, "release_metadata_path", notice.MetadataPath)
	if err != nil {
		WriteKV(b, "release_metadata_status", "unreadable")
		WriteKV(b, "release_metadata_error", err.Error())
		return
	}
	if notice.Available {
		WriteKV(b, "update_available", notice.LatestVersion)
		WriteKV(b, "update_notice", notice.Reason)
		WriteLine(b, "update_next=approve install/release/deploy separately; doctor does not auto-install")
		return
	}
	WriteKV(b, "update_available", "false")
}
