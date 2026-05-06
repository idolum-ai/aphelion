//go:build linux

package pipeline

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

const (
	RuleProgressGovernorLeakage = "progress_governor_leakage"
	RuleFinalGovernorLeakage    = "final_governor_leakage"
	RuleMediaReplyContradiction = "media_reply_contradiction"
	RuleMediaNeedsNarration     = "media_needs_narration"
)

// ConstitutionViolation captures one constitution gate failure on visible text.
type ConstitutionViolation struct {
	Rule    string `json:"rule"`
	Surface string `json:"surface"`
	Detail  string `json:"detail"`
}

// ValidateProgressText flags user-visible progress leakage.
func ValidateProgressText(text string) []ConstitutionViolation {
	if detail := detectGovernorRelationshipLeakage(text); detail != "" {
		return []ConstitutionViolation{{
			Rule:    RuleProgressGovernorLeakage,
			Surface: "progress",
			Detail:  detail,
		}}
	}
	return nil
}

// ValidateFinalReply applies final scene constitution checks.
func ValidateFinalReply(text string, media []core.Media) []ConstitutionViolation {
	violations := make([]ConstitutionViolation, 0, 3)
	if detail := detectGovernorRelationshipLeakage(text); detail != "" {
		violations = append(violations, ConstitutionViolation{
			Rule:    RuleFinalGovernorLeakage,
			Surface: "final_reply",
			Detail:  detail,
		})
	}
	if len(media) > 0 && looksVisibleRefusal(text) {
		violations = append(violations, ConstitutionViolation{
			Rule:    RuleMediaReplyContradiction,
			Surface: "final_reply",
			Detail:  "reply refuses or claims inability while media is being delivered",
		})
	}
	if len(media) > 0 && strings.TrimSpace(text) == "" {
		violations = append(violations, ConstitutionViolation{
			Rule:    RuleMediaNeedsNarration,
			Surface: "final_reply",
			Detail:  "media delivery requires a visible face-owned narration or caption",
		})
	}
	return violations
}

// BuildRepairNotes derives repair prompt notes from violations.
func BuildRepairNotes(violations []ConstitutionViolation) []string {
	notes := make([]string, 0, len(violations))
	for _, violation := range violations {
		detail := strings.TrimSpace(violation.Detail)
		if detail == "" {
			detail = strings.TrimSpace(violation.Rule)
		}
		if detail == "" {
			continue
		}
		notes = append(notes, detail)
	}
	return notes
}

// BuildRepairContract normalizes repair intent and runtime awareness for a
// constitution repair pass.
func BuildRepairContract(base RepairContract, violations []ConstitutionViolation) (RepairContract, bool) {
	notes := BuildRepairNotes(violations)
	if len(notes) == 0 {
		return RepairContract{}, false
	}
	contract := base
	contract.Channel = strings.TrimSpace(contract.Channel)
	contract.PrincipalRole = strings.TrimSpace(contract.PrincipalRole)
	contract.UserText = strings.TrimSpace(contract.UserText)
	contract.Candidate = strings.TrimSpace(contract.Candidate)
	contract.FloorText = strings.TrimSpace(contract.FloorText)
	contract.Violations = notes
	contract.Adjudications = core.NormalizeRuntimeAdjudications(contract.Adjudications)
	if contract.MediaCount < 0 {
		contract.MediaCount = 0
	}

	awareness := contract.Runtime
	awareness.DeliveryMode = "constitution_repair"
	awareness.StreamReply = false
	awareness.MediaAttached = contract.MediaCount > 0
	if contract.MediaCount > 0 {
		awareness.MediaMode = "attachments"
	}
	contract.Runtime = awareness
	return contract, true
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
		"deferred to idolum (system)",
		"handed this to idolum (system)",
		"asked idolum (system)",
		"idolum (system) handled",
		"idolum (system) will handle",
		"idolum and aphelion",
		"idolum and idolum (system)",
		"i deferred this",
		"i deferred it",
		"i passed this to",
	} {
		if strings.Contains(lower, marker) {
			return "user-visible text exposes internal relationship boundaries"
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
		"can't",
		"cannot",
		"unable",
		"won't",
		"will not",
		"don't have access",
		"do not have access",
		"can't do that",
		"cannot do that",
		"can't help with that",
		"cannot help with that",
		"i can't",
		"i cannot",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
