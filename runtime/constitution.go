//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	constitutionRuleProgressGovernorLeakage = "progress_governor_leakage"
	constitutionRuleFinalGovernorLeakage    = "final_governor_leakage"
	constitutionRuleMediaReplyContradiction = "media_reply_contradiction"
	constitutionRuleMediaNeedsNarration     = "media_needs_narration"
)

type ConstitutionViolation struct {
	Rule    string `json:"rule"`
	Surface string `json:"surface"`
	Detail  string `json:"detail"`
}

type TurnToolAudit struct {
	Name          string `json:"name"`
	InputPreview  string `json:"input_preview,omitempty"`
	OutputPreview string `json:"output_preview,omitempty"`
	Error         string `json:"error,omitempty"`
}

type BrokerageRoundAudit struct {
	Round             int      `json:"round"`
	Mode              string   `json:"mode,omitempty"`
	IdolumNote        string   `json:"idolum_note,omitempty"`
	SuggestedTurnMode string   `json:"suggested_turn_mode,omitempty"`
	Ratification      string   `json:"ratification,omitempty"`
	RatifiedTurnMode  string   `json:"ratified_turn_mode,omitempty"`
	SignalJudgment    string   `json:"signal_judgment,omitempty"`
	RatifiedSteps     []string `json:"ratified_steps,omitempty"`
	Error             string   `json:"error,omitempty"`
}

type TurnAudit struct {
	SessionID              string                  `json:"session_id"`
	Scope                  session.ScopeRef        `json:"scope"`
	Channel                string                  `json:"channel"`
	PrincipalRole          string                  `json:"principal_role"`
	UserText               string                  `json:"user_text,omitempty"`
	BrokerageRounds        []BrokerageRoundAudit   `json:"brokerage_rounds,omitempty"`
	BrokerageConverged     bool                    `json:"brokerage_converged"`
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
	if detail := detectGovernorRelationshipLeakage(text); detail != "" {
		return []ConstitutionViolation{{
			Rule:    constitutionRuleProgressGovernorLeakage,
			Surface: "progress",
			Detail:  detail,
		}}
	}
	return nil
}

func (defaultTurnConstitutionGate) ValidateFinal(audit TurnAudit) []ConstitutionViolation {
	violations := make([]ConstitutionViolation, 0, 3)
	if detail := detectGovernorRelationshipLeakage(audit.FinalReplyText); detail != "" {
		violations = append(violations, ConstitutionViolation{
			Rule:    constitutionRuleFinalGovernorLeakage,
			Surface: "final_reply",
			Detail:  detail,
		})
	}
	if len(audit.FinalReplyMedia) > 0 && looksVisibleRefusal(audit.FinalReplyText) {
		violations = append(violations, ConstitutionViolation{
			Rule:    constitutionRuleMediaReplyContradiction,
			Surface: "final_reply",
			Detail:  "reply refuses or claims inability while media is being delivered",
		})
	}
	if len(audit.FinalReplyMedia) > 0 && strings.TrimSpace(audit.FinalReplyText) == "" {
		violations = append(violations, ConstitutionViolation{
			Rule:    constitutionRuleMediaNeedsNarration,
			Surface: "final_reply",
			Detail:  "media delivery requires a visible face-owned narration or caption",
		})
	}
	return violations
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

func detectGovernorRelationshipLeakage(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"the governor",
		"as the governor",
		"deferred to aphelion",
		"handed this to aphelion",
		"asked aphelion",
		"aphelion handled",
		"aphelion will handle",
		"idolum and aphelion",
		"i deferred this",
		"i deferred it",
		"i passed this to",
	} {
		if strings.Contains(lower, marker) {
			return "user-visible text exposes the idolum/aphelion relationship boundary"
		}
	}
	return ""
}

func looksVisibleRefusal(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"i can't",
		"i cannot",
		"i can not",
		"i'm unable",
		"i am unable",
		"i won't be able",
		"i could not",
		"i couldn't",
		"i did not make",
		"i wasn't able",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
