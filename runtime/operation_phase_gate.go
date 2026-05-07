//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

const (
	operationGateLevelNormalApproval            = "normal_approval"
	operationGateLevelEscalatedOperatorApproval = "escalated_operator_approval"
	operationGateLevelHardConsentBlock          = "hard_consent_block"

	operationGateSubjectOperator = "operator"
)

type operationPhaseGate struct {
	Level               string
	ReasonCode          string
	ApprovalSubject     string
	BlockedReason       string
	AutoApproveEligible bool
	Explicit            bool
}

func operationPhaseApprovalGate(phase session.OperationPhase) operationPhaseGate {
	phase = normalizeSingleOperationPhase(phase)
	explicitLevel := normalizeOperationGateLevel(phase.GateLevel)
	hardReason := operationPhaseHardBlockedReason(phase)
	if explicitLevel == operationGateLevelEscalatedOperatorApproval &&
		operationPhaseHasThirdPartyPrivateDataGate(phase) &&
		hardReason != "" &&
		!operationPhaseHardBlockCanBeSatisfiedByOperator(phase, hardReason) {
		explicitLevel = operationGateLevelHardConsentBlock
	}
	if explicitLevel != "" {
		gate := operationPhaseGate{
			Level:               explicitLevel,
			ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, inferOperationGateReasonCode(phase), normalizeOperationPhaseReasonCode(phase.BlockedReasonCode)),
			ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, operationGateSubjectOperator),
			AutoApproveEligible: explicitLevel == operationGateLevelNormalApproval,
			Explicit:            true,
		}
		if phase.AutoApproveEligible != nil {
			gate.AutoApproveEligible = *phase.AutoApproveEligible
		} else if explicitLevel == operationGateLevelEscalatedOperatorApproval || explicitLevel == operationGateLevelHardConsentBlock {
			gate.AutoApproveEligible = false
		}
		if explicitLevel == operationGateLevelHardConsentBlock {
			gate.BlockedReason = hardReason
			if gate.BlockedReason == "" {
				gate.BlockedReason = "waiting for explicit consent"
			}
		}
		return gate
	}
	if operationPhaseIsEscalatedOperatorApproval(phase) {
		gate := operationPhaseGate{
			Level:               operationGateLevelEscalatedOperatorApproval,
			ReasonCode:          inferOperationGateReasonCode(phase),
			ApprovalSubject:     operationGateSubjectOperator,
			AutoApproveEligible: false,
		}
		if phase.AutoApproveEligible != nil {
			gate.AutoApproveEligible = *phase.AutoApproveEligible
		}
		return gate
	}
	if hardReason != "" {
		if operationPhaseHardBlockCanBeSatisfiedByOperator(phase, hardReason) {
			gate := operationPhaseGate{
				Level:               operationGateLevelEscalatedOperatorApproval,
				ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, normalizeOperationPhaseReasonCode(phase.BlockedReasonCode), inferOperationGateReasonCode(phase), "operator_consent"),
				ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, operationGateSubjectOperator),
				AutoApproveEligible: false,
			}
			if phase.AutoApproveEligible != nil {
				gate.AutoApproveEligible = *phase.AutoApproveEligible
			}
			return gate
		}
		return operationPhaseGate{
			Level:               operationGateLevelHardConsentBlock,
			ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, normalizeOperationPhaseReasonCode(phase.BlockedReasonCode), inferOperationGateReasonCode(phase)),
			ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, "third_party"),
			BlockedReason:       hardReason,
			AutoApproveEligible: false,
		}
	}
	gate := operationPhaseGate{
		Level:               operationGateLevelNormalApproval,
		ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, inferOperationGateReasonCode(phase)),
		ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, operationGateSubjectOperator),
		AutoApproveEligible: true,
	}
	if phase.AutoApproveEligible != nil {
		gate.AutoApproveEligible = *phase.AutoApproveEligible
	}
	return gate
}

func operationPhaseHardBlockCanBeSatisfiedByOperator(phase session.OperationPhase, reason string) bool {
	reason = normalizeOperationPhaseReasonCode(reason)
	if reason == "" || strings.Contains(reason, "opt_in") {
		return false
	}
	if !strings.Contains(reason, "consent") {
		return false
	}
	return operationPhaseApprovalSubjectIsOperatorControlled(phase.ApprovalSubject)
}

func operationPhaseApprovalSubjectIsOperatorControlled(subject string) bool {
	switch normalizeOperationPhaseReasonCode(subject) {
	case operationGateSubjectOperator, "admin", "administrator", "resource_owner", "owner", "self", "current_user", "principal":
		return true
	default:
		return false
	}
}

func normalizeOperationGateLevel(level string) string {
	switch normalizeOperationPhaseReasonCode(level) {
	case "", "none":
		return ""
	case "normal", "normal_approval", "standard_approval":
		return operationGateLevelNormalApproval
	case "escalated", "elevated", "escalated_approval", "elevated_approval", "operator_escalation", "escalated_operator_approval":
		return operationGateLevelEscalatedOperatorApproval
	case "hard", "blocked", "hard_block", "hard_consent", "hard_consent_block", "consent_block":
		return operationGateLevelHardConsentBlock
	default:
		return ""
	}
}

func operationPhaseHardBlockedReason(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	if phase.RequiresOptIn {
		return "waiting for explicit opt-in"
	}
	if phase.RequiresConsent {
		return "waiting for explicit consent"
	}
	code := normalizeOperationPhaseReasonCode(phase.BlockedReasonCode)
	switch code {
	case "":
	case "waiting_for_opt_in", "requires_opt_in", "missing_opt_in", "no_opt_in", "opt_in_required":
		return "waiting for explicit opt-in"
	case "waiting_for_consent", "requires_consent", "missing_consent", "no_consent", "consent_required":
		return "waiting for explicit consent"
	case "blocked_on_consent", "consent_blocked":
		return "blocked on consent"
	case "stale_authority", "superseded", "superseded_phase", "stale_phase":
		return ""
	case "waiting_for_explicit_approval", "explicit_approval_required", "approval_required":
		return ""
	default:
		return "blocked: " + code
	}
	text := operationPhaseApprovalText(phase)
	switch {
	case strings.Contains(text, "no opt in") ||
		strings.Contains(text, "no opt-in") ||
		strings.Contains(text, "not opted in") ||
		strings.Contains(text, "missing opt in") ||
		strings.Contains(text, "missing opt-in"):
		return "waiting for explicit opt-in"
	case strings.Contains(text, "wait for her explicit opt in") ||
		strings.Contains(text, "wait for her explicit opt-in") ||
		strings.Contains(text, "wait for explicit opt in") ||
		strings.Contains(text, "wait for explicit opt-in"):
		return "waiting for explicit opt-in"
	case strings.Contains(text, "blocked:") && (strings.Contains(text, "consent") || strings.Contains(text, "opt in") || strings.Contains(text, "opt-in")):
		return "blocked on consent"
	case strings.Contains(text, "no consent") ||
		strings.Contains(text, "without consent") ||
		strings.Contains(text, "consent has not been observed") ||
		strings.Contains(text, "consent not observed"):
		return "waiting for explicit consent"
	default:
		return ""
	}
}

func operationPhaseIsEscalatedOperatorApproval(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	positiveParts := []string{
		phase.ID,
		phase.Summary,
		phase.AuthorityClass,
		phase.WhyNow,
		phase.BoundedEffect,
		phase.BlockedReasonCode,
		phase.GateReasonCode,
	}
	positiveParts = append(positiveParts, phase.AllowedActions...)
	positiveParts = append(positiveParts, phase.ValidationPlan...)
	text := normalizeOperationPhaseReasonCode(strings.Join(positiveParts, " "))
	containsAny := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(text, normalizeOperationPhaseReasonCode(value)) {
				return true
			}
		}
		return false
	}
	if operationPhaseHasThirdPartyPrivateDataGate(phase) ||
		containsAny("wife_owned_profile", "wife_provided_cv", "process_wife_provided", "cv_preferences", "private_data_intake") {
		return false
	}
	return containsAny(
		"external_account_auth_status",
		"read_only_auth_status_check",
		"auth_status_check",
		"identity_check",
		"credential_state_inspection",
		"credential_metadata",
		"token_health_check",
		"gog_cli_auth_status",
		"run_gog_cli_auth_status_or_identity_check",
		"capability_grant",
		"capability_revoke",
		"grant_or_revoke_capability",
	)
}

func operationPhaseHasThirdPartyPrivateDataGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	authorityText := normalizeOperationPhaseReasonCode(strings.Join([]string{
		phase.AuthorityClass,
		phase.GateReasonCode,
	}, " "))
	if strings.Contains(authorityText, "private_data_intake") ||
		strings.Contains(authorityText, "external_account_email_read_public_web_read") ||
		strings.Contains(authorityText, "mailbox_content") ||
		strings.Contains(authorityText, "wife_profile") ||
		strings.Contains(authorityText, "cv_ingestion") {
		return true
	}
	allowedText := normalizeOperationPhaseReasonCode(strings.Join(phase.AllowedActions, " "))
	return strings.Contains(allowedText, "read_mailbox_contents") ||
		strings.Contains(allowedText, "run_gog_cli_mail_query") ||
		strings.Contains(allowedText, "private_data_intake") ||
		strings.Contains(allowedText, "wife_profile") ||
		strings.Contains(allowedText, "cv_ingestion")
}

func inferOperationGateReasonCode(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	text := normalizeOperationPhaseReasonCode(operationPhaseApprovalText(phase))
	switch {
	case strings.Contains(text, "gog_cli") && (strings.Contains(text, "auth") || strings.Contains(text, "identity")):
		return "external_account_auth_status"
	case strings.Contains(text, "credential") && strings.Contains(text, "metadata"):
		return "credential_metadata_check"
	case strings.Contains(text, "credential") || strings.Contains(text, "token"):
		return "credential_state_check"
	case strings.Contains(text, "capability_grant") || strings.Contains(text, "grant_capability"):
		return "capability_grant"
	case strings.Contains(text, "mailbox_content") || strings.Contains(text, "read_mailbox_contents"):
		return "mailbox_content"
	case strings.Contains(text, "opt_in") || strings.Contains(text, "consent"):
		return "third_party_opt_in"
	default:
		return ""
	}
}
