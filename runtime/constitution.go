//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

const (
	constitutionRuleProgressGovernorLeakage  = pipeline.RuleProgressGovernorLeakage
	constitutionRuleFinalGovernorLeakage     = pipeline.RuleFinalGovernorLeakage
	constitutionRuleMediaReplyContradiction  = pipeline.RuleMediaReplyContradiction
	constitutionRuleMediaNeedsNarration      = pipeline.RuleMediaNeedsNarration
	constitutionRuleExecutionClaimUngrounded = "execution_claim_ungrounded"
)

type ConstitutionViolation = pipeline.ConstitutionViolation

type TurnToolAudit struct {
	Name          string `json:"name"`
	InputPreview  string `json:"input_preview,omitempty"`
	OutputPreview string `json:"output_preview,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ExecutionClaimFinding = core.RuntimeFinding

type BrokerageRoundAudit struct {
	Round                      int      `json:"round"`
	Phase                      string   `json:"phase,omitempty"`
	IdolumNote                 string   `json:"idolum_note,omitempty"`
	SuggestedExecutionContract string   `json:"suggested_execution_contract,omitempty"`
	Ratification               string   `json:"ratification,omitempty"`
	RatifiedExecutionContract  string   `json:"ratified_execution_contract,omitempty"`
	SignalJudgment             string   `json:"signal_judgment,omitempty"`
	RatifiedSteps              []string `json:"ratified_steps,omitempty"`
	Error                      string   `json:"error,omitempty"`
}

type TurnAudit struct {
	SessionID              string                  `json:"session_id"`
	Scope                  session.ScopeRef        `json:"scope"`
	Channel                string                  `json:"channel"`
	PrincipalRole          string                  `json:"principal_role"`
	UserText               string                  `json:"user_text,omitempty"`
	BrokerageRounds        []BrokerageRoundAudit   `json:"brokerage_rounds,omitempty"`
	BrokerageConverged     bool                    `json:"brokerage_converged"`
	BrokerageStopReason    string                  `json:"brokerage_stop_reason,omitempty"`
	BrokerageStopRound     int                     `json:"brokerage_stop_round,omitempty"`
	ToolCalls              []TurnToolAudit         `json:"tool_calls,omitempty"`
	ProgressMessages       []string                `json:"progress_messages,omitempty"`
	GovernorReplyText      string                  `json:"governor_reply_text,omitempty"`
	ParsedOutboundMedia    []core.Media            `json:"parsed_outbound_media,omitempty"`
	FinalReplyText         string                  `json:"final_reply_text,omitempty"`
	FinalReplyMedia        []core.Media            `json:"final_reply_media,omitempty"`
	FinalDeliveryMode      string                  `json:"final_delivery_mode,omitempty"`
	FaceRepairAttempted    bool                    `json:"face_repair_attempted"`
	FaceRepairApplied      bool                    `json:"face_repair_applied"`
	ConstitutionViolations []ConstitutionViolation `json:"constitution_violations,omitempty"`
	ExecutionClaimFindings []ExecutionClaimFinding `json:"execution_claim_findings,omitempty"`
}

type TurnConstitutionGate interface {
	ValidateProgressText(text string) []ConstitutionViolation
	ValidateFinal(audit TurnAudit) []ConstitutionViolation
}

type defaultTurnConstitutionGate struct{}

func DefaultTurnConstitutionGate() TurnConstitutionGate {
	return defaultTurnConstitutionGate{}
}

func (defaultTurnConstitutionGate) ValidateProgressText(text string) []ConstitutionViolation {
	return pipeline.ValidateProgressText(text)
}

func (defaultTurnConstitutionGate) ValidateFinal(audit TurnAudit) []ConstitutionViolation {
	return pipeline.ValidateFinalReply(audit.FinalReplyText, audit.FinalReplyMedia)
}

type turnAuditRecorder struct {
	audit TurnAudit
}

func newTurnAuditRecorder(key session.SessionKey, channel string, principalRole string, userText string) *turnAuditRecorder {
	return &turnAuditRecorder{
		audit: TurnAudit{
			SessionID:     session.SessionIDForKey(key),
			Scope:         session.NormalizeScopeRef(key.Scope),
			Channel:       strings.TrimSpace(channel),
			PrincipalRole: strings.TrimSpace(principalRole),
			UserText:      strings.TrimSpace(userText),
		},
	}
}

func (r *turnAuditRecorder) ToolStarted(name string, inputPreview string) {
	if r == nil {
		return
	}
	r.audit.ToolCalls = append(r.audit.ToolCalls, TurnToolAudit{
		Name:         strings.TrimSpace(name),
		InputPreview: strings.TrimSpace(inputPreview),
	})
}

func (r *turnAuditRecorder) ToolFinished(name string, outputPreview string, errText string) {
	if r == nil {
		return
	}
	name = strings.TrimSpace(name)
	for i := len(r.audit.ToolCalls) - 1; i >= 0; i-- {
		if strings.TrimSpace(r.audit.ToolCalls[i].Name) != name {
			continue
		}
		if r.audit.ToolCalls[i].OutputPreview == "" {
			r.audit.ToolCalls[i].OutputPreview = strings.TrimSpace(outputPreview)
			r.audit.ToolCalls[i].Error = strings.TrimSpace(errText)
			return
		}
	}
	r.audit.ToolCalls = append(r.audit.ToolCalls, TurnToolAudit{
		Name:          name,
		OutputPreview: strings.TrimSpace(outputPreview),
		Error:         strings.TrimSpace(errText),
	})
}

func (r *turnAuditRecorder) RecordProgress(text string) {
	if r == nil {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	r.audit.ProgressMessages = append(r.audit.ProgressMessages, trimmed)
}

func (r *turnAuditRecorder) RecordBrokerageRound(round BrokerageRoundAudit) {
	if r == nil {
		return
	}
	if round.Round <= 0 {
		round.Round = len(r.audit.BrokerageRounds) + 1
	}
	r.audit.BrokerageRounds = append(r.audit.BrokerageRounds, round)
}

func (r *turnAuditRecorder) MarkBrokerageConverged(converged bool) {
	if r == nil {
		return
	}
	r.audit.BrokerageConverged = converged
}

func (r *turnAuditRecorder) MarkBrokerageStopped(reason string, round int, converged bool) {
	if r == nil {
		return
	}
	r.audit.BrokerageStopReason = strings.TrimSpace(reason)
	r.audit.BrokerageStopRound = round
	r.audit.BrokerageConverged = converged
}

func (r *turnAuditRecorder) RecordGovernorReply(text string, media []core.Media) {
	if r == nil {
		return
	}
	r.audit.GovernorReplyText = strings.TrimSpace(text)
	r.audit.ParsedOutboundMedia = cloneAuditMedia(media)
}

func (r *turnAuditRecorder) RecordFinalReply(text string, media []core.Media, deliveryMode string) {
	if r == nil {
		return
	}
	r.audit.FinalReplyText = strings.TrimSpace(text)
	r.audit.FinalReplyMedia = cloneAuditMedia(media)
	r.audit.FinalDeliveryMode = strings.TrimSpace(deliveryMode)
}

func (r *turnAuditRecorder) MarkFaceRepairAttempted() {
	if r == nil {
		return
	}
	r.audit.FaceRepairAttempted = true
}

func (r *turnAuditRecorder) MarkFaceRepairApplied() {
	if r == nil {
		return
	}
	r.audit.FaceRepairApplied = true
}

func (r *turnAuditRecorder) RecordViolations(violations []ConstitutionViolation) {
	if r == nil || len(violations) == 0 {
		return
	}
	r.audit.ConstitutionViolations = append(r.audit.ConstitutionViolations, violations...)
}

func (r *turnAuditRecorder) RecordExecutionClaimFindings(findings []ExecutionClaimFinding) {
	if r == nil || len(findings) == 0 {
		return
	}
	r.audit.ExecutionClaimFindings = append(r.audit.ExecutionClaimFindings, findings...)
}

func (r *turnAuditRecorder) Snapshot() TurnAudit {
	if r == nil {
		return TurnAudit{}
	}
	snapshot := r.audit
	snapshot.ParsedOutboundMedia = cloneAuditMedia(snapshot.ParsedOutboundMedia)
	snapshot.FinalReplyMedia = cloneAuditMedia(snapshot.FinalReplyMedia)
	snapshot.ProgressMessages = append([]string(nil), snapshot.ProgressMessages...)
	snapshot.ToolCalls = append([]TurnToolAudit(nil), snapshot.ToolCalls...)
	snapshot.BrokerageRounds = append([]BrokerageRoundAudit(nil), snapshot.BrokerageRounds...)
	snapshot.ConstitutionViolations = append([]ConstitutionViolation(nil), snapshot.ConstitutionViolations...)
	snapshot.ExecutionClaimFindings = append([]ExecutionClaimFinding(nil), snapshot.ExecutionClaimFindings...)
	return snapshot
}

func cloneAuditMedia(items []core.Media) []core.Media {
	if len(items) == 0 {
		return nil
	}
	out := make([]core.Media, 0, len(items))
	for _, item := range items {
		item.Data = nil
		out = append(out, item)
	}
	return out
}
