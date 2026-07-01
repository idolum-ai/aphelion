//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

const maxStartupRecoveryRuns = 20
const maxStartupRecoveryTelegramIngressReplay = 10

type startupRecoveryMissionEvidence struct {
	PendingHandoffs []session.MissionHandoff
	RecentResults   []session.MissionResult
}

type RecoveryRecord struct {
	RunID              int64    `json:"run_id"`
	Cause              string   `json:"cause"`
	LastTool           string   `json:"last_tool,omitempty"`
	LastToolPreview    string   `json:"last_tool_preview,omitempty"`
	LastPhase          string   `json:"last_phase,omitempty"`
	DoNotAssume        []string `json:"do_not_assume,omitempty"`
	SafeOptions        []string `json:"safe_options,omitempty"`
	LedgerPointer      string   `json:"ledger_pointer"`
	ToolCallsStarted   int      `json:"tool_calls_started,omitempty"`
	ToolCallsFinished  int      `json:"tool_calls_finished,omitempty"`
	LastToolResultHint string   `json:"last_tool_result_hint,omitempty"`
	LastToolError      string   `json:"last_tool_error,omitempty"`
}

func (r *Runtime) StartStartupRecovery(ctx context.Context, logger func(string, ...any)) {
	if r == nil {
		return
	}
	if logger == nil {
		logger = log.Printf
	}

	r.startupRecoveryWG.Add(1)
	go func() {
		defer r.startupRecoveryWG.Done()
		if err := r.runStartupRecoveryOnce(ctx, time.Now()); err != nil {
			logger("WARN startup recovery failed: %v", err)
		}
	}()
}

// WaitForStartupRecovery blocks until the startup recovery goroutine started
// by StartStartupRecovery completes, or until ctx is canceled. Returns
// ctx.Err() on timeout/cancellation, nil otherwise. Safe to call when no
// startup recovery is in flight (returns immediately).
func (r *Runtime) WaitForStartupRecovery(ctx context.Context) error {
	if r == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		r.startupRecoveryWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) runStartupRecoveryOnce(ctx context.Context, now time.Time) (err error) {
	maintenanceKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	defer func() {
		if err != nil {
			r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryFailed, "recovery", "failed", map[string]any{
				"error": trimError(err.Error()),
			}, time.Now().UTC())
		}
	}()

	interrupted, err := r.store.InterruptRunningTurnRuns()
	if err != nil {
		return fmt.Errorf("interrupt running turn runs: %w", err)
	}
	if len(interrupted) > 0 {
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
			"interrupted_count": len(interrupted),
		}, time.Now().UTC())
		r.supersedeInterruptedProgressMessages(ctx, interrupted, "inspecting")
	}
	reconciledIngress, err := r.store.ReconcileRunningTelegramIngressWithTerminalTurnRuns()
	if err != nil {
		return fmt.Errorf("reconcile running telegram ingress: %w", err)
	}
	if reconciledIngress > 0 {
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
			"telegram_ingress_reconciled": reconciledIngress,
			"phase":                       "telegram_ingress_reconcile",
		}, time.Now().UTC())
	}
	repairedApprovals, err := r.repairInvalidPendingContinuationApprovals(ctx, now)
	if err != nil {
		return fmt.Errorf("repair invalid pending continuation approvals: %w", err)
	}
	if repairedApprovals > 0 {
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
			"invalid_pending_approvals_repaired": repairedApprovals,
			"phase":                              "continuation_approval_repair",
		}, time.Now().UTC())
	}
	repairedAuthority, err := r.repairContinuationAuthorityContradictions(ctx, now)
	if err != nil {
		return fmt.Errorf("repair continuation authority contradictions: %w", err)
	}
	if repairedAuthority > 0 {
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
			"continuation_authority_repaired": repairedAuthority,
			"phase":                           "continuation_authority_repair",
		}, time.Now().UTC())
	}
	replayedIngress, failedIngress, err := r.replayPendingTelegramIngress(ctx, "telegram:primary", now)
	if err != nil {
		return fmt.Errorf("replay pending telegram ingress: %w", err)
	}
	if replayedIngress > 0 || failedIngress > 0 {
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
			"phase":                     "telegram_ingress_replay",
			"telegram_ingress_replayed": replayedIngress,
			"telegram_ingress_failed":   failedIngress,
		}, time.Now().UTC())
	}

	runs, err := r.store.PendingRecoveryTurnRuns(maxStartupRecoveryRuns)
	if err != nil {
		return fmt.Errorf("load pending recovery turn runs: %w", err)
	}
	missionEvidence, err := r.startupRecoveryMissionEvidence()
	if err != nil {
		return fmt.Errorf("load mission recovery evidence: %w", err)
	}
	if len(runs) == 0 {
		resumeResult, resumeErr := r.resumeRestartParkedContinuations(ctx, now)
		memoryNote := "continuity loaded; no recovery rows pending"
		if len(missionEvidence.PendingHandoffs) > 0 {
			memoryNote += fmt.Sprintf("; mission handoffs pending=%d", len(missionEvidence.PendingHandoffs))
		}
		if len(missionEvidence.RecentResults) > 0 {
			memoryNote += fmt.Sprintf("; mission results recorded=%d", len(missionEvidence.RecentResults))
		}
		if repairedApprovals > 0 {
			memoryNote += fmt.Sprintf("; invalid pending approvals repaired=%d", repairedApprovals)
		}
		if repairedAuthority > 0 {
			memoryNote += fmt.Sprintf("; continuation authority repaired=%d", repairedAuthority)
		}
		if replayedIngress > 0 {
			memoryNote += fmt.Sprintf("; telegram ingress replayed=%d", replayedIngress)
		}
		if failedIngress > 0 {
			memoryNote += fmt.Sprintf("; telegram ingress failed=%d", failedIngress)
		}
		if summary := resumeResult.summary(); summary != "" {
			memoryNote += "; " + summary
		}
		if resumeErr != nil {
			log.Printf("WARN parked continuation resume failed during startup recovery: %v", resumeErr)
			r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryFailed, "recovery", "failed", map[string]any{
				"phase": "parked_continuation_resume",
				"error": trimError(resumeErr.Error()),
			}, time.Now().UTC())
			memoryNote += "; parked_continuation_resume warning=" + trimError(resumeErr.Error())
		}
		if err := r.deliverRestartAwakeSignal(ctx, now, 0, 0, memoryNote); err != nil {
			return fmt.Errorf("deliver restart awake signal: %w", err)
		}
		payload := map[string]any{
			"interrupted_count": 0,
			"recovered_count":   0,
			"delivery_sent":     true,
		}
		if resumeResult.total() > 0 {
			payload["parked_continuations"] = recordRestartResumeSummaryPayload(resumeResult)
		}
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryAwake, "recovery", "awake", payload, time.Now().UTC())
		return nil
	}
	r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
		"pending_count": len(runs),
	}, time.Now().UTC())
	r.flushRecoveryRunMemory(ctx, runs, "startup_recovery")

	scope, err := r.scopeForPrincipal(principal.Principal{Role: principal.RoleAdmin})
	if err != nil {
		return fmt.Errorf("resolve recovery scope: %w", err)
	}

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
	requestText := renderStartupRecoveryRequest(runs, missionEvidence)
	r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryIssued, "recovery", "issued", map[string]any{
		"pending_count": len(runs),
		"request_text":  truncatePreview(strings.TrimSpace(requestText), 220),
	}, time.Now().UTC())

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
		GovernorName:          r.governorName(),
		FaceName:              r.faceName(),
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
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryCompleted, "recovery", "completed", map[string]any{
			"pending_count":   len(runs),
			"persisted":       false,
			"delivery_sent":   false,
			"recovered_count": 0,
		}, time.Now().UTC())
		return nil
	}
	floorText := strings.TrimSpace(turnResult.FloorText)
	recoverySummary := startupRecoverySummaryForPersistence(runs, floorText)

	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	if err := r.store.MarkTurnRunsRecovered(ids, recoverySummary); err != nil {
		return fmt.Errorf("mark turn runs recovered: %w", err)
	}
	r.supersedeInterruptedProgressMessages(ctx, runs, "recovered")
	if err := r.deliverStartupRecoveryCatchup(ctx, maintenanceSession.SystemPrompt, runs, recoverySummary); err != nil {
		return fmt.Errorf("deliver startup recovery catch-up: %w", err)
	}
	resumeProposalResult := r.startStartupRecoveryResumeProposals(ctx, runs, now)
	resumeResult, resumeErr := r.resumeRestartParkedContinuations(ctx, now)
	if resumeErr != nil {
		log.Printf("WARN parked continuation resume failed during startup recovery: %v", resumeErr)
		r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryFailed, "recovery", "failed", map[string]any{
			"phase": "parked_continuation_resume",
			"error": trimError(resumeErr.Error()),
		}, time.Now().UTC())
	}
	awakePayload := map[string]any{
		"interrupted_count": len(runs),
		"recovered_count":   len(ids),
		"delivery_sent":     true,
	}
	completedPayload := map[string]any{
		"pending_count":   len(runs),
		"persisted":       true,
		"delivery_sent":   true,
		"recovered_count": len(ids),
	}
	if resumeResult.total() > 0 {
		resumePayload := recordRestartResumeSummaryPayload(resumeResult)
		awakePayload["parked_continuations"] = resumePayload
		completedPayload["parked_continuations"] = resumePayload
	}
	if resumeProposalResult.total() > 0 {
		resumeProposalPayload := recordStartupRecoveryResumeProposalPayload(resumeProposalResult)
		awakePayload["resume_proposals"] = resumeProposalPayload
		completedPayload["resume_proposals"] = resumeProposalPayload
	}
	r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryAwake, "recovery", "awake", awakePayload, time.Now().UTC())
	r.recordExecutionEvent(maintenanceKey, core.ExecutionEventRecoveryCompleted, "recovery", "completed", completedPayload, time.Now().UTC())
	return nil
}

func (r *Runtime) replayPendingTelegramIngress(ctx context.Context, surface string, now time.Time) (replayed int, failed int, err error) {
	if r == nil || r.store == nil {
		return 0, 0, nil
	}
	surface = strings.TrimSpace(surface)
	if surface == "" {
		return 0, 0, nil
	}
	pending, err := r.store.PendingTelegramIngressUpdates(surface, maxStartupRecoveryTelegramIngressReplay)
	if err != nil {
		return 0, 0, err
	}
	for _, record := range pending {
		if ctx.Err() != nil {
			return replayed, failed, ctx.Err()
		}
		if !session.TelegramIngressUpdateStatusDispatchable(record.Status) {
			continue
		}
		msg, decodeErr := telegramIngressReplayMessage(record)
		if decodeErr != nil {
			if markErr := r.store.MarkTelegramIngressFailed(record.Surface, record.UpdateID, "startup_replay_decode_failed: "+trimError(decodeErr.Error()), now); markErr != nil {
				return replayed, failed, markErr
			}
			failed++
			continue
		}
		key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
		r.recordExecutionEvent(key, core.ExecutionEventRecoveryResume, "telegram_ingress", "replaying", map[string]any{
			"ingress_surface":   record.Surface,
			"ingress_update_id": record.UpdateID,
			"message_id":        record.MessageID,
			"queued_at":         record.QueuedAt.UTC().Format(time.RFC3339Nano),
		}, now)
		if _, handleErr := r.HandleInbound(ctx, msg); handleErr != nil {
			stored, ok, lookupErr := r.store.TelegramIngressUpdate(record.Surface, record.UpdateID)
			if lookupErr != nil {
				return replayed, failed, lookupErr
			}
			if ok && session.TelegramIngressUpdateStatusDispatchable(stored.Status) {
				if markErr := r.store.MarkTelegramIngressFailed(record.Surface, record.UpdateID, "startup_replay_failed: "+trimError(handleErr.Error()), now); markErr != nil {
					return replayed, failed, markErr
				}
			}
			failed++
			continue
		}
		replayed++
	}
	return replayed, failed, nil
}

func (r *Runtime) supersedeInterruptedProgressMessages(ctx context.Context, runs []session.TurnRun, status string) {
	if r == nil || r.store == nil || r.outbound == nil || len(runs) == 0 {
		return
	}
	text := startupRecoveryProgressSupersedeText(status)
	if text == "" {
		return
	}
	clearer, hasClearer := r.outbound.(messageKeyboardClearer)
	editor, hasEditor := r.outbound.(messageEditor)
	if !hasClearer && !hasEditor {
		return
	}
	for _, run := range runs {
		if run.ProgressMessageID <= 0 {
			continue
		}
		key := session.SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: run.Scope}
		chatID := r.progressDeliveryChatIDForRun(key, run)
		if chatID == 0 {
			r.recordStartupRecoveryProgressSupersedeFailure(key, run, "startup_recovery_no_delivery_chat", fmt.Errorf("progress delivery chat unavailable"))
			continue
		}
		method := "startup_recovery_edit_text"
		var err error
		if hasClearer {
			method = "startup_recovery_clear_keyboard"
			err = clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, run.ProgressMessageID, text, "")
		} else {
			err = editor.EditMessageText(ctx, chatID, run.ProgressMessageID, text, "")
		}
		if err != nil {
			r.recordStartupRecoveryProgressSupersedeFailure(key, run, method, err)
			r.reportOperationalIssueAsync("startup_recovery_progress", fmt.Errorf("supersede progress run_id=%d msg_id=%d: %w", run.ID, run.ProgressMessageID, err))
			continue
		}
		r.recordExecutionEvent(key, core.ExecutionEventDeliveryProgressEdited, "progress", "superseded", map[string]any{
			"method":           method,
			"message_id":       run.ProgressMessageID,
			"chat_id":          chatID,
			"run_id":           run.ID,
			"progress_phase":   "startup_recovery",
			"source_class":     "canonical",
			"source_surface":   "outbound_transport_ledger",
			"visibility":       "human_render_unknown",
			"transport_status": "acknowledged",
		}, time.Now().UTC())
	}
}

func startupRecoveryProgressSupersedeText(status string) string {
	switch strings.TrimSpace(status) {
	case "inspecting":
		return "Interrupted by restart.\n\nStartup recovery is inspecting persisted state."
	case "recovered":
		return "Recovered after restart.\n\nStartup recovery recorded the interrupted turn. Continue from the newest recovery, approval, or status surface."
	default:
		return ""
	}
}

func (r *Runtime) progressDeliveryChatIDForRun(key session.SessionKey, run session.TurnRun) int64 {
	if r == nil || r.store == nil || run.ProgressMessageID <= 0 {
		return 0
	}
	events, err := r.store.ExecutionEventsByTurnRun(key, run.ID, 80)
	if err != nil {
		log.Printf("WARN lookup progress delivery chat failed run_id=%d msg_id=%d err=%v", run.ID, run.ProgressMessageID, err)
		return startupRecoveryProgressFallbackChatID(run)
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventDeliveryProgressSent, core.ExecutionEventDeliveryProgressEdited:
		default:
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		messageID, ok := payloadInt64(payload, "message_id")
		if !ok || messageID != run.ProgressMessageID {
			continue
		}
		chatID, ok := payloadInt64(payload, "chat_id")
		if ok && chatID > 0 {
			return chatID
		}
	}
	return startupRecoveryProgressFallbackChatID(run)
}

func startupRecoveryProgressFallbackChatID(run session.TurnRun) int64 {
	if run.ChatID <= 0 {
		return 0
	}
	switch run.Scope.Kind {
	case session.ScopeKindDurableAgent, session.ScopeKindHeartbeat, session.ScopeKindRecovery:
		return 0
	default:
		return run.ChatID
	}
}

func (r *Runtime) recordStartupRecoveryProgressSupersedeFailure(key session.SessionKey, run session.TurnRun, method string, err error) {
	if r == nil {
		return
	}
	payload := map[string]any{
		"method":         strings.TrimSpace(method),
		"message_id":     run.ProgressMessageID,
		"run_id":         run.ID,
		"progress_phase": "startup_recovery",
		"source_class":   "canonical",
		"source_surface": "outbound_transport_ledger",
		"visibility":     "human_render_unknown",
	}
	if err != nil {
		payload["error"] = trimError(err.Error())
	}
	r.recordExecutionEvent(key, core.ExecutionEventDeliveryProgressFailed, "progress", "failed", payload, time.Now().UTC())
}

func telegramIngressReplayMessage(record session.TelegramIngressUpdateRecord) (core.InboundMessage, error) {
	raw := strings.TrimSpace(record.InboundJSON)
	if raw == "" {
		return core.InboundMessage{}, fmt.Errorf("telegram ingress update %s/%d has no inbound payload", record.Surface, record.UpdateID)
	}
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return core.InboundMessage{}, fmt.Errorf("decode telegram ingress update %s/%d: %w", record.Surface, record.UpdateID, err)
	}
	if msg.ChatID == 0 {
		msg.ChatID = record.ChatID
	}
	if msg.SenderID == 0 {
		msg.SenderID = record.SenderID
	}
	if msg.MessageID == 0 {
		msg.MessageID = record.MessageID
	}
	if strings.TrimSpace(msg.IngressSurface) == "" {
		msg.IngressSurface = strings.TrimSpace(record.Surface)
	}
	if msg.IngressUpdateID <= 0 {
		msg.IngressUpdateID = record.UpdateID
	}
	if msg.IngressQueuedAt.IsZero() && !record.QueuedAt.IsZero() {
		msg.IngressQueuedAt = record.QueuedAt
	}
	if strings.TrimSpace(msg.ChatType) == "" {
		msg.ChatType = "private"
	}
	if msg.ChatID == 0 || msg.IngressUpdateID <= 0 || strings.TrimSpace(msg.IngressSurface) == "" {
		return core.InboundMessage{}, fmt.Errorf("telegram ingress update %s/%d is missing replay identity", record.Surface, record.UpdateID)
	}
	return msg, nil
}

func (r *Runtime) startupRecoveryMissionEvidence() (startupRecoveryMissionEvidence, error) {
	if r == nil || r.store == nil {
		return startupRecoveryMissionEvidence{}, nil
	}
	handoffs, err := r.store.MissionHandoffs(session.MissionHandoffFilter{Status: "pending", Limit: 5})
	if err != nil {
		return startupRecoveryMissionEvidence{}, err
	}
	results, err := r.store.MissionResults(5)
	if err != nil {
		return startupRecoveryMissionEvidence{}, err
	}
	return startupRecoveryMissionEvidence{PendingHandoffs: handoffs, RecentResults: results}, nil
}

func renderStartupRecoveryRequest(runs []session.TurnRun, evidence ...startupRecoveryMissionEvidence) string {
	lines := []string{
		"Startup recovery analysis.",
		"The previous service process ended while turns were still running.",
		"Use these typed RecoveryRecords; do not infer completion beyond the fields.",
		"",
	}

	if len(evidence) > 0 {
		lines = appendStartupRecoveryMissionEvidence(lines, evidence[0])
	}

	lines = append(lines, "recovery_records:")
	for _, run := range runs {
		for _, line := range renderRecoveryRecordLines(recoveryRecordForTurnRun(run)) {
			lines = append(lines, "  "+line)
		}
	}
	lines = append(lines, "")
	lines = append(lines, "Return 3-5 concise lines: likely stop point, do_not_assume items, and safest next check.")
	return strings.Join(lines, "\n")
}

func recoveryRecordForTurnRun(run session.TurnRun) RecoveryRecord {
	cause := strings.TrimSpace(run.ErrorText)
	if cause == "" {
		cause = "process_restarted_before_turn_completed"
	}
	return RecoveryRecord{
		RunID:              run.ID,
		Cause:              cause,
		LastTool:           strings.TrimSpace(run.LastToolName),
		LastToolPreview:    truncatePreview(strings.TrimSpace(run.LastToolPreview), 120),
		LastPhase:          strings.TrimSpace(string(run.Kind)),
		DoNotAssume:        []string{"turn_completed", "external_effect_completed", "verification_completed"},
		SafeOptions:        []string{"inspect_current_state", "check_turn_run_status", "non_mutating_health_check"},
		LedgerPointer:      fmt.Sprintf("turn_runs:%d", run.ID),
		ToolCallsStarted:   run.ToolCallsStarted,
		ToolCallsFinished:  run.ToolCallsFinished,
		LastToolResultHint: truncatePreview(strings.TrimSpace(run.LastToolResultPreview), 120),
		LastToolError:      truncatePreview(strings.TrimSpace(run.LastToolError), 120),
	}
}

func renderRecoveryRecordLines(record RecoveryRecord) []string {
	lines := []string{fmt.Sprintf("- RecoveryRecord run_id=%d cause=%q ledger_pointer=%q", record.RunID, record.Cause, record.LedgerPointer)}
	if record.LastTool != "" {
		lines = append(lines, fmt.Sprintf("  last_tool=%s last_tool_preview=%q", record.LastTool, record.LastToolPreview))
	}
	if record.ToolCallsStarted > 0 || record.ToolCallsFinished > 0 {
		lines = append(lines, fmt.Sprintf("  tool_calls_started=%d tool_calls_finished=%d", record.ToolCallsStarted, record.ToolCallsFinished))
	}
	if record.LastToolResultHint != "" {
		lines = append(lines, fmt.Sprintf("  last_tool_result_hint=%q", record.LastToolResultHint))
	}
	if record.LastToolError != "" {
		lines = append(lines, fmt.Sprintf("  last_tool_error=%q", record.LastToolError))
	}
	lines = append(lines, "  do_not_assume="+strings.Join(record.DoNotAssume, ","))
	lines = append(lines, "  safe_options="+strings.Join(record.SafeOptions, ","))
	return lines
}

func appendStartupRecoveryMissionEvidence(lines []string, evidence startupRecoveryMissionEvidence) []string {
	if len(evidence.PendingHandoffs) == 0 && len(evidence.RecentResults) == 0 {
		return lines
	}
	lines = append(lines, "Mission handoff/result evidence:")
	if len(evidence.PendingHandoffs) > 0 {
		lines = append(lines, "pending_handoffs:")
		for _, handoff := range evidence.PendingHandoffs {
			lines = append(lines, fmt.Sprintf("  - handoff_id=%s mission_id=%s operation_id=%s status=%s", handoff.ID, handoff.MissionID, handoff.OperationID, handoff.Status))
			lines = append(lines, "    planned_action="+strconv.Quote(truncatePreview(handoff.PlannedAction, 180)))
			lines = append(lines, "    recovery_question="+strconv.Quote(truncatePreview(handoff.RecoveryQuestion, 180)))
			if strings.TrimSpace(handoff.ExpectedEvidenceJSON) != "" {
				lines = append(lines, "    expected_evidence="+truncatePreview(handoff.ExpectedEvidenceJSON, 220))
			}
		}
	}
	if len(evidence.RecentResults) > 0 {
		lines = append(lines, "recent_results:")
		for _, result := range evidence.RecentResults {
			lines = append(lines, fmt.Sprintf("  - result_id=%s handoff_id=%s mission_id=%s operation_id=%s status=%s", result.ID, result.HandoffID, result.MissionID, result.OperationID, result.Status))
			lines = append(lines, "    summary="+strconv.Quote(truncatePreview(result.Summary, 180)))
			if strings.TrimSpace(result.EvidenceRefsJSON) != "" {
				lines = append(lines, "    evidence_refs="+truncatePreview(result.EvidenceRefsJSON, 220))
			}
			if strings.TrimSpace(result.RemainingRisk) != "" {
				lines = append(lines, "    remaining_risk="+strconv.Quote(truncatePreview(result.RemainingRisk, 180)))
			}
		}
	}
	lines = append(lines, "")
	return lines
}

func (r *Runtime) flushRecoveryRunMemory(ctx context.Context, runs []session.TurnRun, reason string) {
	if r == nil || len(runs) == 0 || !r.aggressiveFlushEnabled() {
		return
	}
	seen := make(map[int64]struct{}, len(runs))
	for _, run := range runs {
		if run.ChatID == 0 {
			continue
		}
		if _, ok := seen[run.ChatID]; ok {
			continue
		}
		seen[run.ChatID] = struct{}{}
		if err := r.FlushChatMemory(ctx, run.ChatID, reason); err != nil {
			if isRecoveryMemoryFlushTimeout(err) {
				log.Printf("INFO recovery memory flush deferred chat_id=%d reason=%s err=%v", run.ChatID, strings.TrimSpace(reason), err)
				continue
			}
			log.Printf("WARN recovery memory flush skipped chat_id=%d reason=%s err=%v", run.ChatID, strings.TrimSpace(reason), err)
			r.reportOperationalIssueAsync("memory_recovery_flush", fmt.Errorf("chat_id=%d reason=%s: %w", run.ChatID, strings.TrimSpace(reason), err))
		}
	}
}

func (r *Runtime) deliverRestartAwakeSignal(ctx context.Context, startedAt time.Time, interruptedCount int, recoveredCount int, memoryNote string) error {
	if r == nil || r.cfg == nil || r.store == nil || r.outbound == nil {
		return nil
	}
	adminIDs := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(adminIDs) == 0 {
		return nil
	}
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	health, err := r.store.MissionLedgerHealth(time.Now().UTC())
	if err != nil {
		return fmt.Errorf("load mission ledger health for awake signal: %w", err)
	}
	targetChatID := r.lastActiveAdminChat(adminIDs)
	if targetChatID == 0 {
		targetChatID = adminIDs[0]
	}
	text := face.RenderRestartAwake(face.RestartAwakeNotice{
		StartedAtUTC:      startedAt.UTC().Format(time.RFC3339),
		InterruptedCount:  interruptedCount,
		RecoveredCount:    recoveredCount,
		CandidateMissions: health.CandidateCount,
		ActiveMissions:    health.ActiveCount,
		PendingHandoffs:   health.PendingHandoffCount,
		MemoryNote:        strings.TrimSpace(memoryNote),
	})
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: targetChatID, Text: text})
	if err != nil {
		return err
	}
	adminKey := session.SessionKey{ChatID: targetChatID, UserID: 0, Scope: telegramDMScopeRef(targetChatID)}
	unlockAdmin := r.lockSession(adminKey)
	defer unlockAdmin()
	adminSession, err := r.store.Load(adminKey)
	if err != nil {
		return fmt.Errorf("load restart awake target session: %w", err)
	}
	applySessionScope(adminSession, adminKey)
	adminSession.ChatType = "dm"
	if err := r.store.Save(adminSession, appendAssistantTurn(adminSession, text, text, ""), core.TokenUsage{}); err != nil {
		return fmt.Errorf("save restart awake admin session: %w", err)
	}
	if err := r.store.RecordOutbound(adminKey, adminSession.TurnCount, msgID, "restart_awake"); err != nil {
		return fmt.Errorf("record restart awake outbound: %w", err)
	}
	return nil
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
	summary = stripStartupRecoveryLedgerDisclaimer(summary)
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

func startupRecoverySummaryForPersistence(runs []session.TurnRun, raw string) string {
	summary := sanitizeStartupRecoveryCatchupSummary(raw)
	if summary == "" || startupRecoverySummaryIsLedgerDisclaimer(summary) {
		return fallbackRecoverySummary(runs)
	}
	return summary
}

func stripStartupRecoveryLedgerDisclaimer(summary string) string {
	summary = strings.TrimSpace(summary)
	if !startupRecoverySummaryIsLedgerDisclaimer(summary) {
		return summary
	}
	if idx := strings.Index(summary, "\n\n"); idx >= 0 {
		return strings.TrimSpace(summary[idx+2:])
	}
	if idx := strings.Index(summary, "."); idx >= 0 && idx+1 < len(summary) {
		return strings.TrimSpace(summary[idx+1:])
	}
	return ""
}

func startupRecoverySummaryIsLedgerDisclaimer(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	return strings.HasPrefix(lower, "cannot write the maintenance ledger")
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
