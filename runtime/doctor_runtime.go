//go:build linux

package runtime

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/runtime/doctor"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type DiagnosticInput = doctor.DiagnosticInput

const RequestMarker = doctor.RequestMarker
const doctorSummaryMarker = doctor.SummaryMarker

func (r *Runtime) writeDoctorCodexWorkEvidenceReview(ctx context.Context, b *strings.Builder, input DiagnosticInput) {
	r.doctorRuntime().WriteCodexWorkEvidenceReview(ctx, b, input)
}

func (r *Runtime) writeDoctorRuntimeConfig(b *strings.Builder, exec pipeline.TurnExecutionContract, scope sandbox.Scope) {
	r.doctorRuntime().WriteRuntimeConfig(b, exec, scope)
}

func (r *Runtime) writeDoctorPerceptionBudget(b *strings.Builder, key session.SessionKey, now time.Time) {
	if r == nil || r.store == nil {
		doctor.WriteLine(b, "perception_budget: unavailable")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := r.store.ExecutionEventsByChat(key.ChatID, now.Add(-24*time.Hour), 200)
	if err != nil {
		doctor.WriteLine(b, "perception_budget_error="+strconv.Quote(err.Error()))
		return
	}
	latest, ok := latestPerceptionBudgetForSessionFromExecutionEvents(events, key.ChatID)
	if !ok {
		doctor.WriteLine(b, "perception_budget: unavailable")
		doctor.WriteLine(b, "perception_budget_source=execution_events.provider.attempt.started")
		return
	}
	doctor.WriteKV(b, "perception_budget_source", "execution_events.provider.attempt.started")
	doctor.WriteKV(b, "perception_budget_posture", latest.Posture)
	doctor.WriteKV(b, "perception_budget_total_tokens", strconv.FormatInt(latest.TotalBudgetTokens, 10))
	doctor.WriteKV(b, "perception_budget_estimated_tokens", strconv.FormatInt(latest.TotalEstimatedTokens, 10))
	doctor.WriteKV(b, "perception_budget_remaining_headroom_tokens", strconv.FormatInt(latest.RemainingHeadroomTokens, 10))
	doctor.WriteKV(b, "perception_budget_current_input_tokens", strconv.FormatInt(latest.CurrentInputTokens, 10))
	doctor.WriteKV(b, "perception_budget_tool_evidence_tokens", strconv.FormatInt(latest.ToolEvidenceTokens, 10))
	if len(latest.AdmittedLayers) > 0 {
		doctor.WriteKV(b, "perception_budget_admitted_layers", strings.Join(latest.AdmittedLayers, ","))
	}
	if len(latest.SuppressedLayers) > 0 {
		doctor.WriteKV(b, "perception_budget_suppressed_layers", strings.Join(latest.SuppressedLayers, ","))
	}
	if len(latest.ObservedEvidenceSources) > 0 {
		doctor.WriteKV(b, "perception_budget_observed_evidence_sources", strings.Join(latest.ObservedEvidenceSources, ","))
	}
}

func (r *Runtime) writeDoctorAutonomyStatus(b *strings.Builder, key session.SessionKey, senderID int64, now time.Time) {
	r.doctorRuntime().WriteAutonomyStatus(b, key, senderID, now)
}

func (r *Runtime) writeDoctorSandboxReadiness(b *strings.Builder, now time.Time) {
	r.doctorRuntime().WriteSandboxReadiness(b, now)
}

func (r *Runtime) writeDoctorIssueStatusChecks(b *strings.Builder, input DiagnosticInput) {
	r.doctorRuntime().WriteIssueStatusChecks(b, input)
}

func (r *Runtime) writeDoctorDesignPrincipleHealth(b *strings.Builder, input DiagnosticInput) {
	r.doctorRuntime().WriteDesignPrincipleHealth(b, input)
}

func (r *Runtime) writeDoctorExternalToolInvocationReadiness(b *strings.Builder, input DiagnosticInput) {
	r.doctorRuntime().WriteExternalToolInvocationReadiness(b, input)
}

func (r *Runtime) writeDoctorRuntimeAdjudications(ctx context.Context, b *strings.Builder, key session.SessionKey, now time.Time) {
	r.doctorRuntime().WriteRuntimeAdjudications(ctx, b, key, now)
}

func (r *Runtime) writeDoctorMissionLedger(b *strings.Builder, key session.SessionKey, now time.Time) {
	r.doctorRuntime().WriteMissionLedger(b, key, now)
}

func (r *Runtime) writeDoctorTelegramThreads(b *strings.Builder, key session.SessionKey) {
	r.doctorRuntime().WriteTelegramThreads(b, key)
}
