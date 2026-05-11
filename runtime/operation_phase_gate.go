//go:build linux

package runtime

import "github.com/idolum-ai/aphelion/session"

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
			ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, operationPhaseStructuredGateReasonCode(phase), normalizeOperationPhaseReasonCode(phase.BlockedReasonCode)),
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
	if operationPhaseHasTypedManualApprovalGate(phase) {
		gate := operationPhaseGate{
			Level:               operationGateLevelEscalatedOperatorApproval,
			ReasonCode:          operationPhaseStructuredGateReasonCode(phase),
			ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, operationGateSubjectOperator),
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
				ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, normalizeOperationPhaseReasonCode(phase.BlockedReasonCode), operationPhaseStructuredGateReasonCode(phase), "operator_consent"),
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
			ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, normalizeOperationPhaseReasonCode(phase.BlockedReasonCode), operationPhaseStructuredGateReasonCode(phase)),
			ApprovalSubject:     firstNonEmptyContinuation(phase.ApprovalSubject, "third_party"),
			BlockedReason:       hardReason,
			AutoApproveEligible: false,
		}
	}
	gate := operationPhaseGate{
		Level:               operationGateLevelNormalApproval,
		ReasonCode:          firstNonEmptyContinuation(phase.GateReasonCode, operationPhaseStructuredGateReasonCode(phase)),
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
	if reason == "" || operationPhaseReasonCodeRequiresOptIn(reason) {
		return false
	}
	if !operationPhaseReasonCodeRequiresConsent(reason) {
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
		return "waiting for a clearer approval boundary"
	}
	return ""
}

func operationPhaseHasTypedManualApprovalGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	reason := operationPhaseStructuredGateReasonCode(phase)
	if reason == "" {
		return false
	}
	if operationPhaseHasThirdPartyPrivateDataGate(phase) && !operationPhaseApprovalSubjectIsOperatorControlled(phase.ApprovalSubject) {
		return false
	}
	if phase.RequiresApproval {
		return true
	}
	switch normalizeOperationPhaseReasonCode(phase.BlockedReasonCode) {
	case "waiting_for_explicit_approval", "explicit_approval_required", "approval_required":
		return operationPhaseReasonCodeIsManualGate(reason)
	default:
		return (phase.RequiresConsent || phase.RequiresOptIn) && operationPhaseReasonCodeIsManualGate(reason)
	}
}

func operationPhaseHasThirdPartyPrivateDataGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	return operationPhaseHasStructuredCode(phase,
		"private_data_intake",
		"external_account_email_read_public_web_read",
		"mailbox_content",
		"mailbox_read",
		"email_read",
		"external_account_email_read",
		"resource_owner_data_intake",
		"resource_owner_profile_intake",
		"private_profile_intake",
		"profile_evaluation_rubric",
		"cv_ingestion",
		"private_material_processing",
		"rank_private_material",
		"scout_public_opportunities",
		"read_mailbox_contents",
		"run_mailbox_adapter_query",
		"run_configured_mailbox_adapter_query_once",
		"read_only_mailbox_smoke",
	)
}

func operationPhaseStructuredGateReasonCode(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	if code := normalizeOperationPhaseReasonCode(phase.GateReasonCode); code != "" {
		return code
	}
	if code := normalizeOperationPhaseReasonCode(phase.BlockedReasonCode); code != "" && !operationPhaseReasonCodeIsGenericApproval(code) {
		return code
	}
	for _, code := range operationPhaseStructuredCodes(phase) {
		switch code {
		case "external_account_auth_status", "external_account_status_check", "read_only_auth_status_check", "run_external_account_auth_status_or_identity_check":
			return "external_account_auth_status"
		case "credential_state_check", "credential_state_inspection", "credential_access", "read_credentials_or_tokens", "token_health_check":
			return "credential_state_check"
		case "credential_metadata", "credential_metadata_check", "inspect_token_file_metadata", "inspect_secret_path_metadata":
			return "credential_metadata_check"
		case "capability_grant", "capability_acquisition", "grant_capability", "grant_set", "capability_authority", "grant_or_revoke_capability", "capability_revoke", "capability_access_check":
			return "capability_grant"
		case "mailbox_content", "mailbox_read", "email_read", "external_account_email_read", "read_mailbox_contents", "run_mailbox_adapter_query", "run_configured_mailbox_adapter_query_once", "read_only_mailbox_smoke":
			return "mailbox_content"
		case "private_data_intake", "resource_owner_data_intake", "resource_owner_profile_intake", "private_profile_intake", "profile_evaluation_rubric", "cv_ingestion":
			return "third_party_opt_in"
		}
	}
	return ""
}

func operationPhaseReasonCodeIsGenericApproval(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "waiting_for_explicit_approval", "explicit_approval_required", "approval_required":
		return true
	default:
		return false
	}
}

func operationPhaseReasonCodeRequiresOptIn(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "waiting_for_explicit_opt_in", "waiting_for_opt_in", "requires_opt_in", "missing_opt_in", "no_opt_in", "opt_in_required", "third_party_opt_in":
		return true
	default:
		return false
	}
}

func operationPhaseReasonCodeRequiresConsent(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "waiting_for_explicit_consent", "waiting_for_consent", "requires_consent", "missing_consent", "no_consent", "consent_required", "blocked_on_consent", "consent_blocked", "operator_consent":
		return true
	default:
		return false
	}
}

func operationPhaseReasonCodeIsManualGate(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "external_account_auth_status",
		"credential_metadata_check",
		"credential_state_check",
		"capability_grant",
		"operator_consent":
		return true
	default:
		return false
	}
}

func operationPhaseHasStructuredCode(phase session.OperationPhase, codes ...string) bool {
	if len(codes) == 0 {
		return false
	}
	want := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if normalized := normalizeOperationPhaseReasonCode(code); normalized != "" {
			want[normalized] = struct{}{}
		}
	}
	for _, code := range operationPhaseStructuredCodes(phase) {
		if _, ok := want[code]; ok {
			return true
		}
	}
	return false
}

func operationPhaseStructuredCodes(phase session.OperationPhase) []string {
	phase = normalizeSingleOperationPhase(phase)
	values := []string{
		phase.AuthorityClass,
		phase.GateReasonCode,
		phase.BlockedReasonCode,
	}
	values = append(values, phase.AllowedActions...)
	codes := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		code := normalizeOperationPhaseReasonCode(value)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes
}
