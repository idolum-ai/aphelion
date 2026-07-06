//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/runtime/doctor"
	"github.com/idolum-ai/aphelion/session"
)

const doctorRunTimeout = 5 * time.Minute

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

func (r *Runtime) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.DoctorReportRecord{}, false, fmt.Errorf("runtime doctor dependencies are unavailable")
	}
	actor, err := r.resolveDoctorAdmin(core.InboundMessage{
		ChatID:   chatID,
		SenderID: senderID,
		ChatType: "private",
	})
	if err != nil {
		return session.DoctorReportRecord{}, false, err
	}
	if actor.Role != principal.RoleAdmin {
		return session.DoctorReportRecord{}, false, ErrPrincipalDenied
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.store.LatestDoctorReport(key)
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

	progress := r.newDoctorProgressReporter(key, msg)
	monitor, err := r.startTurnMonitor(ctx, key, session.TurnRunKindDoctor, "/health diagnose", progress, nil, msg)
	if err != nil {
		return err
	}
	ctx = monitor.Context()
	var monitorErr error
	defer func() {
		if monitorErr != nil {
			surfaceDoctorProgress(ctx, progress, "Health diagnosis failed: "+trimError(monitorErr.Error()))
		}
		monitor.Finish(ctx, monitorErr)
	}()

	doctorRuntime := r.doctorRuntime()
	maintainer, err := doctorRuntime.MaintainerDelegate()
	if err != nil {
		monitorErr = fmt.Errorf("load doctor maintainer delegate: %w", err)
		return monitorErr
	}

	surfaceDoctorProgress(ctx, progress, "Loading prompt and memory context")
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		monitorErr = fmt.Errorf("load doctor prompt context: %w", err)
		return monitorErr
	}
	prepared := pipeline.TurnPrepareContract{
		UserText:   "/health diagnose",
		LedgerText: "/health diagnose",
	}
	exec := r.executionForTurn(prepared)
	r.applyModelSlotExecution(&exec, core.ModelSlotDoctor)
	surfaceDoctorProgress(ctx, progress, "Collecting session, memory, log, and runtime evidence")
	var operation session.OperationState
	if _, opState, exists, err := r.store.PlanAndOperationStateIfExists(key); err == nil && exists {
		operation = opState
	}
	packet := doctorRuntime.BuildDiagnosticPacket(ctx, doctor.DiagnosticInput{
		Message:       msg,
		Actor:         actor,
		Key:           key,
		Session:       sess,
		Operation:     operation,
		Scope:         scope,
		PromptContext: promptContext,
		Exec:          exec,
		Maintainer:    maintainer,
		Now:           now,
	})

	awareness := r.governorRuntimeAwareness(scope, session.TurnRunKindDoctor, "telegram", exec)
	systemBlocks := prompt.BuildGovernorPromptBlocks(prompt.GovernorRequest{
		GovernorName:    r.governorName(),
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
		{Role: "system", Content: doctor.ReadOnlySystemNote()},
		{Role: "user", Content: packet},
	}
	if note := doctor.MaintainerSystemNote(maintainer); note != "" {
		input = []agent.Message{input[0], input[1], {Role: "system", Content: note}, input[2]}
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
		monitorErr = fmt.Errorf("run health diagnosis: %w", runErr)
		return monitorErr
	}
	if turnResult == nil {
		monitorErr = fmt.Errorf("health diagnosis returned no turn result")
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
		payload := map[string]any{
			"backend":  strings.TrimSpace(exec.Backend),
			"provider": strings.TrimSpace(exec.ProviderName),
			"model":    strings.TrimSpace(exec.ModelName),
			"run_kind": string(session.TurnRunKindDoctor),
		}
		appendTokenUsagePayload(payload, turnResult.TokenUsage)
		r.recordExecutionEvent(key, core.ExecutionEventProviderAttemptSucceeded, "provider", "succeeded", payload, time.Now().UTC())
	}

	report := strings.TrimSpace(turnResult.Text)
	if report == "" {
		report = doctor.ReportFallbackText
	}
	report = doctor.RedactText(report)
	telegramReport, summaryUsage := doctorRuntime.TelegramReport(ctx, key, exec, systemPrompt, systemBlocks, report, progress)
	var maintainerArtifact string
	if maintainer != nil {
		surfaceDoctorProgress(ctx, progress, "Storing the full report in maintainer child artifacts")
		if artifact, artifactErr := doctorRuntime.WriteMaintainerReport(*maintainer, report, telegramReport, now); artifactErr != nil {
			r.reportOperationalIssueAsync("doctor_maintainer_artifact", artifactErr)
		} else {
			maintainerArtifact = artifact
		}
	}
	surfaceDoctorProgress(ctx, progress, "Saving the health diagnosis report into chat history")
	newMessages := appendSyntheticTurn(sess, "/health diagnose", report, telegramReport, doctor.FloorMetadata(report, telegramReport, maintainer, maintainerArtifact))
	if err := r.store.Save(sess, newMessages, addTokenUsage(turnResult.TokenUsage, summaryUsage)); err != nil {
		monitorErr = fmt.Errorf("save health diagnosis report: %w", err)
		return monitorErr
	}
	surfaceDoctorProgress(ctx, progress, "Sending the health diagnosis report to Telegram")
	msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    telegramReport,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		monitorErr = fmt.Errorf("send health diagnosis report: %w", err)
		return monitorErr
	}
	if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "doctor"); err != nil {
		monitorErr = fmt.Errorf("record doctor outbound: %w", err)
		return monitorErr
	}
	return nil
}

func (r *Runtime) doctorRuntime() *doctor.Runtime {
	deps := doctor.Dependencies{
		Config:                        r.cfg,
		Store:                         r.store,
		Provider:                      r.provider,
		GovernorName:                  r.governorName,
		FaceName:                      r.faceName,
		AutonomyStatusSnapshot:        r.AutonomyStatusSnapshot,
		AutonomyStatusSnapshotForChat: r.autonomyStatusSnapshot,
		ValidateAutonomyLiveOverride:  r.validateAutonomyLiveOverride,
		ShouldRouteContinuationThroughWorkExecutor:    r.shouldRouteContinuationThroughWorkExecutor,
		SandboxReadinessSnapshot:                      r.sandboxReadinessSnapshot,
		ToolLifecycleStatusSnapshot:                   r.toolLifecycleStatusSnapshot,
		CapabilityStatusSnapshot:                      r.capabilityStatusSnapshot,
		ExternalToolInvocationReadinessStatusSnapshot: r.externalToolInvocationReadinessStatusSnapshot,
		TailnetStatusSnapshot:                         r.TailnetStatusSnapshot,
		StaleRunningTurnRuns:                          r.staleRunningTurnRuns,
		WriteAuthorityProjection:                      r.writeDoctorAuthorityProjection,
		WriteProviderHealth:                           r.writeDoctorProviderHealth,
		WritePersistenceHealth:                        r.writeDoctorPersistenceHealth,
		WritePerceptionBudget:                         r.writeDoctorPerceptionBudget,
		WriteExternalChannelAdapterReadiness:          r.writeDoctorExternalChannelAdapterReadiness,
		ReasoningOptionsForRun:                        r.reasoningOptionsForRun,
		RecordExecutionEvent:                          r.recordExecutionEvent,
		ReportOperationalIssueAsync:                   r.reportOperationalIssueAsync,
	}
	if r != nil && r.workExecutor != nil {
		deps.WorkExecutorStatus = func() doctor.WorkExecutorStatus {
			status := r.workExecutor.Status()
			return doctor.WorkExecutorStatus{
				Configured:     status.Configured,
				Preferred:      status.Preferred,
				Active:         status.Active,
				LastAttempted:  status.LastAttempted,
				FallbackReason: status.FallbackReason,
				LastError:      status.LastError,
				UpdatedAt:      status.UpdatedAt,
			}
		}
	}
	return doctor.NewRuntime(deps)
}

func (r *Runtime) resolveDoctorAdmin(msg core.InboundMessage) (principal.Principal, error) {
	if r == nil || r.resolver == nil {
		return principal.Principal{}, fmt.Errorf("principal resolver is unavailable")
	}
	if chatType := strings.TrimSpace(msg.ChatType); chatType != "" && chatType != "private" && chatType != "dm" {
		return principal.Principal{}, fmt.Errorf("health diagnosis must be run from an admin private chat")
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok || actor.Role != principal.RoleAdmin {
		return principal.Principal{}, ErrPrincipalDenied
	}
	return actor, nil
}

func (r *Runtime) newDoctorProgressReporter(key session.SessionKey, msg core.InboundMessage) *toolProgressReporter {
	if r == nil {
		return nil
	}
	progress := r.newToolProgressReporter(key, msg, nil)
	if progress == nil {
		return nil
	}
	progress.suppressControls = true
	progress.controls = nil
	progress.taskSummary = "health diagnosis"
	progress.currentPlanStep = ""
	return progress
}

func surfaceDoctorProgress(ctx context.Context, progress *toolProgressReporter, text string) {
	if progress == nil {
		return
	}
	progress.Surface(ctx, text)
}
