//go:build linux

package session

import (
	"fmt"
	"strings"
	"time"
)

type ActionProposal struct {
	ID                  string         `json:"id,omitempty"`
	OperationID         string         `json:"operation_id,omitempty"`
	MissionID           string         `json:"mission_id,omitempty"`
	OperatorTitle       string         `json:"operator_title,omitempty"`
	PlanTitle           string         `json:"plan_title,omitempty"`
	Summary             string         `json:"summary,omitempty"`
	WhyNow              string         `json:"why_now,omitempty"`
	BoundedEffect       string         `json:"bounded_effect,omitempty"`
	RiskClass           string         `json:"risk_class,omitempty"`
	AllowedActions      []string       `json:"allowed_actions,omitempty"`
	ForbiddenActions    []string       `json:"forbidden_actions,omitempty"`
	ValidationPlan      []string       `json:"validation_plan,omitempty"`
	AutoApproveEligible *bool          `json:"autoapprove_eligible,omitempty"`
	ExpiresAt           time.Time      `json:"expires_at,omitempty"`
	PlanHash            string         `json:"plan_hash,omitempty"`
	Status              ProposalStatus `json:"status,omitempty"`
	CreatedAt           time.Time      `json:"created_at,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at,omitempty"`
}

type ContinuationLeaseStatus string

type ContinuationLeaseClass string

type ContinuationLease struct {
	ID                       string                  `json:"id,omitempty"`
	ProposalID               string                  `json:"proposal_id,omitempty"`
	MissionID                string                  `json:"mission_id,omitempty"`
	OperatorTitle            string                  `json:"operator_title,omitempty"`
	PlanTitle                string                  `json:"plan_title,omitempty"`
	Status                   ContinuationLeaseStatus `json:"status,omitempty"`
	MaxTurns                 int                     `json:"max_turns,omitempty"`
	RemainingTurns           int                     `json:"remaining_turns,omitempty"`
	ApprovedBy               int64                   `json:"approved_by,omitempty"`
	LeaseClass               ContinuationLeaseClass  `json:"lease_class,omitempty"`
	Constraints              map[string]string       `json:"constraints,omitempty"`
	AllowedActions           []string                `json:"allowed_actions,omitempty"`
	ForbiddenActions         []string                `json:"forbidden_actions,omitempty"`
	ValidationPlan           []string                `json:"validation_plan,omitempty"`
	RequiredCapabilityGrants []CapabilityGrantSpec   `json:"required_capability_grants,omitempty"`
	CapabilityGrantIDs       []string                `json:"capability_grant_ids,omitempty"`
	ExpiresAt                time.Time               `json:"expires_at,omitempty"`
	PlanHash                 string                  `json:"plan_hash,omitempty"`
	CreatedAt                time.Time               `json:"created_at,omitempty"`
	UpdatedAt                time.Time               `json:"updated_at,omitempty"`
	ApprovedAt               time.Time               `json:"approved_at,omitempty"`
	ConsumedAt               time.Time               `json:"consumed_at,omitempty"`
	RevokedAt                time.Time               `json:"revoked_at,omitempty"`
}

type ContinuationApprovalBundlePhase struct {
	ID                       string                  `json:"id,omitempty"`
	OperationPhaseID         string                  `json:"operation_phase_id,omitempty"`
	Index                    int                     `json:"index,omitempty"`
	PhaseFingerprint         string                  `json:"phase_fingerprint,omitempty"`
	OperatorTitle            string                  `json:"operator_title,omitempty"`
	PlanTitle                string                  `json:"plan_title,omitempty"`
	Summary                  string                  `json:"summary,omitempty"`
	AuthorityClass           string                  `json:"authority_class,omitempty"`
	WhyNow                   string                  `json:"why_now,omitempty"`
	BoundedEffect            string                  `json:"bounded_effect,omitempty"`
	AllowedActions           []string                `json:"allowed_actions,omitempty"`
	ForbiddenActions         []string                `json:"forbidden_actions,omitempty"`
	ValidationPlan           []string                `json:"validation_plan,omitempty"`
	RequiredCapabilityGrants []CapabilityGrantSpec   `json:"required_capability_grants,omitempty"`
	Status                   ContinuationLeaseStatus `json:"status,omitempty"`
	ApprovedAt               time.Time               `json:"approved_at,omitempty"`
	ActivatedAt              time.Time               `json:"activated_at,omitempty"`
	ConsumedAt               time.Time               `json:"consumed_at,omitempty"`
	DeferredAt               time.Time               `json:"deferred_at,omitempty"`
}

type ContinuationApprovalBundle struct {
	ID              string                            `json:"id,omitempty"`
	OperationID     string                            `json:"operation_id,omitempty"`
	PhasePlanID     string                            `json:"phase_plan_id,omitempty"`
	PlanFingerprint string                            `json:"plan_fingerprint,omitempty"`
	Status          ContinuationLeaseStatus           `json:"status,omitempty"`
	CurrentPhaseID  string                            `json:"current_phase_id,omitempty"`
	ApprovedBy      int64                             `json:"approved_by,omitempty"`
	Phases          []ContinuationApprovalBundlePhase `json:"phases,omitempty"`
	ExpiresAt       time.Time                         `json:"expires_at,omitempty"`
	CreatedAt       time.Time                         `json:"created_at,omitempty"`
	UpdatedAt       time.Time                         `json:"updated_at,omitempty"`
	ApprovedAt      time.Time                         `json:"approved_at,omitempty"`
	ConsumedAt      time.Time                         `json:"consumed_at,omitempty"`
	RevokedAt       time.Time                         `json:"revoked_at,omitempty"`
}

type TurnAuthorizationKind string

type TurnAuthorizationStatus string

type ContinuationIntentDecision string

type ContinuationIntent struct {
	Decision    ContinuationIntentDecision `json:"decision,omitempty"`
	Rationale   string                     `json:"rationale,omitempty"`
	NextStep    string                     `json:"next_step,omitempty"`
	Constraints string                     `json:"constraints,omitempty"`
	Confidence  string                     `json:"confidence,omitempty"`
	Ratified    bool                       `json:"ratified,omitempty"`
	UpdatedAt   time.Time                  `json:"updated_at,omitempty"`
}

type TurnAuthorizationState struct {
	Kind                   TurnAuthorizationKind           `json:"kind,omitempty"`
	Status                 TurnAuthorizationStatus         `json:"status,omitempty"`
	DecisionID             string                          `json:"decision_id,omitempty"`
	DecisionMessageID      int64                           `json:"decision_message_id,omitempty"`
	Objective              string                          `json:"objective,omitempty"`
	StageSummary           string                          `json:"stage_summary,omitempty"`
	RemainingTurns         int                             `json:"remaining_turns,omitempty"`
	ApprovedBy             int64                           `json:"approved_by,omitempty"`
	PersonaIntent          ContinuationIntent              `json:"persona_intent,omitempty"`
	GovernorIntent         ContinuationIntent              `json:"governor_intent,omitempty"`
	ActionProposal         ActionProposal                  `json:"action_proposal,omitempty"`
	ContinuationLease      ContinuationLease               `json:"continuation_lease,omitempty"`
	ApprovalBundle         ContinuationApprovalBundle      `json:"approval_bundle,omitempty"`
	VerificationTarget     *ContinuationVerificationTarget `json:"verification_target,omitempty"`
	HandshakeBlockedReason string                          `json:"handshake_blocked_reason,omitempty"`
	ParkedAt               time.Time                       `json:"parked_at,omitempty"`
	ParkedReason           string                          `json:"parked_reason,omitempty"`
	ParkedSource           string                          `json:"parked_source,omitempty"`
	UpdatedAt              time.Time                       `json:"updated_at,omitempty"`
}

type ContinuationStatus = TurnAuthorizationStatus

type ContinuationState = TurnAuthorizationState

type ContinuationVerificationTarget struct {
	Kind                      string    `json:"kind,omitempty"`
	ReasonCode                string    `json:"reason_code,omitempty"`
	OperationID               string    `json:"operation_id,omitempty"`
	PhaseID                   string    `json:"phase_id,omitempty"`
	OriginalLeaseID           string    `json:"original_lease_id,omitempty"`
	OriginalActionProposalID  string    `json:"original_action_proposal_id,omitempty"`
	OriginalActionOperationID string    `json:"original_action_operation_id,omitempty"`
	OriginalWorkMode          string    `json:"original_work_mode,omitempty"`
	RepoRoot                  string    `json:"repo_root,omitempty"`
	Workdir                   string    `json:"workdir,omitempty"`
	WindowStart               time.Time `json:"window_start,omitempty"`
	WindowEnd                 time.Time `json:"window_end,omitempty"`
	ClaimedSummary            string    `json:"claimed_summary,omitempty"`
	CandidatePaths            []string  `json:"candidate_paths,omitempty"`
	EvidenceRefs              []string  `json:"evidence_refs,omitempty"`
}

func (l OperatorAutoApprovalLease) ActiveAt(now time.Time) bool {
	lease := NormalizeOperatorAutoApprovalLease(l)
	if lease.ID == "" || lease.AdminUserID <= 0 || lease.ChatID == 0 {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !lease.RevokedAt.IsZero() {
		return false
	}
	if lease.ExpiresAt.IsZero() || !lease.ExpiresAt.After(now) {
		return false
	}
	return lease.MaxUses <= 0 || lease.UsedCount < lease.MaxUses
}

func NormalizeTurnAuthorizationState(state TurnAuthorizationState) TurnAuthorizationState {
	state.Kind = TurnAuthorizationKind(strings.TrimSpace(string(state.Kind)))
	state.Status = TurnAuthorizationStatus(strings.TrimSpace(string(state.Status)))
	state.DecisionID = strings.TrimSpace(state.DecisionID)
	if state.DecisionMessageID < 0 {
		state.DecisionMessageID = 0
	}
	state.Objective = strings.TrimSpace(state.Objective)
	state.StageSummary = strings.TrimSpace(state.StageSummary)
	state.PersonaIntent = normalizeContinuationIntent(state.PersonaIntent)
	state.GovernorIntent = normalizeContinuationIntent(state.GovernorIntent)
	state.ActionProposal = NormalizeActionProposal(state.ActionProposal)
	state.ContinuationLease = NormalizeContinuationLease(state.ContinuationLease)
	state.ApprovalBundle = NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	state.VerificationTarget = NormalizeContinuationVerificationTarget(state.VerificationTarget)
	state.HandshakeBlockedReason = normalizeContinuationStage(state.HandshakeBlockedReason)
	if !state.ParkedAt.IsZero() {
		state.ParkedAt = state.ParkedAt.UTC()
	}
	state.ParkedReason = strings.TrimSpace(state.ParkedReason)
	state.ParkedSource = strings.TrimSpace(state.ParkedSource)
	if state.Kind == "" && (state.Status != "" || state.DecisionID != "" || state.DecisionMessageID > 0 || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0 || state.ActionProposal.Active() || state.ContinuationLease.ID != "" || state.ContinuationLease.ProposalID != "" || state.ApprovalBundle.Active()) {
		state.Kind = TurnAuthorizationKindContinuation
	}
	if state.RemainingTurns < 0 {
		state.RemainingTurns = 0
	}
	if state.Status == TurnAuthorizationStatusIdle || state.Status == TurnAuthorizationStatusRevoked {
		state.ApprovedBy = 0
		state.DecisionID = ""
		state.DecisionMessageID = 0
		state.ParkedAt = time.Time{}
		state.ParkedReason = ""
		state.ParkedSource = ""
	}
	if state.UpdatedAt.IsZero() && (state.Kind != "" || state.Status != "" || state.DecisionID != "" || state.DecisionMessageID > 0 || state.Objective != "" || state.StageSummary != "" || state.RemainingTurns > 0 || state.ApprovedBy > 0 || state.ActionProposal.Active() || state.ContinuationLease.ID != "" || state.ContinuationLease.ProposalID != "" || state.ApprovalBundle.Active() || !state.ParkedAt.IsZero() || state.ParkedReason != "" || state.ParkedSource != "") {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func NormalizeContinuationVerificationTarget(target *ContinuationVerificationTarget) *ContinuationVerificationTarget {
	if target == nil {
		return nil
	}
	normalized := *target
	normalized.Kind = normalizeEnumValue(normalized.Kind)
	normalized.ReasonCode = normalizeEnumValue(normalized.ReasonCode)
	normalized.OperationID = strings.TrimSpace(normalized.OperationID)
	normalized.PhaseID = strings.TrimSpace(normalized.PhaseID)
	normalized.OriginalLeaseID = strings.TrimSpace(normalized.OriginalLeaseID)
	normalized.OriginalActionProposalID = strings.TrimSpace(normalized.OriginalActionProposalID)
	normalized.OriginalActionOperationID = strings.TrimSpace(normalized.OriginalActionOperationID)
	normalized.OriginalWorkMode = normalizeEnumValue(normalized.OriginalWorkMode)
	normalized.RepoRoot = strings.TrimSpace(normalized.RepoRoot)
	normalized.Workdir = strings.TrimSpace(normalized.Workdir)
	if !normalized.WindowStart.IsZero() {
		normalized.WindowStart = normalized.WindowStart.UTC()
	}
	if !normalized.WindowEnd.IsZero() {
		normalized.WindowEnd = normalized.WindowEnd.UTC()
	}
	normalized.ClaimedSummary = strings.TrimSpace(normalized.ClaimedSummary)
	normalized.CandidatePaths = normalizeStringSlicePreserveCase(normalized.CandidatePaths)
	normalized.EvidenceRefs = normalizeStringSlicePreserveCase(normalized.EvidenceRefs)
	if normalized.Kind == "" &&
		normalized.ReasonCode == "" &&
		normalized.OperationID == "" &&
		normalized.PhaseID == "" &&
		normalized.OriginalLeaseID == "" &&
		normalized.OriginalActionProposalID == "" &&
		normalized.OriginalActionOperationID == "" &&
		normalized.OriginalWorkMode == "" &&
		normalized.RepoRoot == "" &&
		normalized.Workdir == "" &&
		normalized.WindowStart.IsZero() &&
		normalized.WindowEnd.IsZero() &&
		normalized.ClaimedSummary == "" &&
		len(normalized.CandidatePaths) == 0 &&
		len(normalized.EvidenceRefs) == 0 {
		return nil
	}
	return &normalized
}

func normalizeStringSlicePreserveCase(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func NormalizeContinuationState(state ContinuationState) ContinuationState {
	if strings.TrimSpace(string(state.Kind)) == "" {
		state.Kind = TurnAuthorizationKindContinuation
	}
	return NormalizeTurnAuthorizationState(state)
}

func NormalizeContinuationLeaseClass(class ContinuationLeaseClass) ContinuationLeaseClass {
	value := normalizeEnumValue(string(class))
	switch ContinuationLeaseClass(value) {
	case ContinuationLeaseClassLocalWorkspace,
		ContinuationLeaseClassDataAccess,
		ContinuationLeaseClassChildWake,
		ContinuationLeaseClassCapabilityGrant,
		ContinuationLeaseClassDeployRestart:
		return ContinuationLeaseClass(value)
	default:
		return ""
	}
}

func InferContinuationLeaseClass(riskClass string, allowedActions []string, boundedEffect string) ContinuationLeaseClass {
	_ = boundedEffect
	if contract, ok := AuthorityContractFor(riskClass, allowedActions, ""); ok && contract.LeaseClass != "" {
		return contract.LeaseClass
	}
	return ""
}

func ContinuationLeaseClassLabel(class ContinuationLeaseClass) string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return "local workspace"
	case ContinuationLeaseClassDataAccess:
		return "data access"
	case ContinuationLeaseClassChildWake:
		return "child wake"
	case ContinuationLeaseClassCapabilityGrant:
		return "capability grant"
	case ContinuationLeaseClassDeployRestart:
		return "deploy/restart"
	default:
		return "generic"
	}
}

func ContinuationLeaseClassBoundary(class ContinuationLeaseClass) string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return "local repo/workspace work only; no repository history, deploy, restart, credentials, or external effects unless separately granted"
	case ContinuationLeaseClassDataAccess:
		return "read exactly the approved resource descriptors; no silent broad ingestion, retention, or external-account access"
	case ContinuationLeaseClassChildWake:
		return "wake only the named child and approved count; no policy drift, grants, or external effects beyond the child charter"
	case ContinuationLeaseClassCapabilityGrant:
		return "request/review authority only unless a separate active capability grant exists; leases do not grant capabilities by themselves"
	case ContinuationLeaseClassDeployRestart:
		return "release-class work requires fresh evidence, handoff, verification, and rollback/stop gates; no unbounded restart/deploy loops"
	default:
		return "bounded continuation only; do not infer authority outside explicit allowed actions"
	}
}

func DefaultContinuationLeaseConstraints(class ContinuationLeaseClass) map[string]string {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace:
		return map[string]string{
			"scope":       "local workspace/repository only",
			"history":     "commit requires explicit lease authority; push requires separate lease",
			"externality": "no deploy, restart, credentials, purchases, public contact, or external accounts",
			"validation":  "focused tests or diff checks before report",
		}
	case ContinuationLeaseClassDataAccess:
		return map[string]string{
			"resource":  "explicit descriptor required: artifact/file/attachment/url/account surface",
			"scope":     "one approved resource or bounded resource set",
			"retention": "ephemeral by default; durable retention requires explicit approval",
			"redaction": "apply connector redaction before model consumption when available",
		}
	case ContinuationLeaseClassChildWake:
		return map[string]string{
			"agent":      "named durable child required",
			"wake_count": "bounded count/cadence required",
			"outbound":   "child policy controls outbound effects",
			"no_drift":   "no policy/bootstrap/grant changes without separate approval",
		}
	case ContinuationLeaseClassCapabilityGrant:
		return map[string]string{
			"request":    "request_id or target_resource required",
			"grant":      "grant_set/access_check remains separate capability_authority state",
			"actions":    "allowed actions must be explicit; wildcard is insufficient",
			"activation": "approved lease may prepare/review, not silently activate broad authority",
		}
	case ContinuationLeaseClassDeployRestart:
		return map[string]string{
			"handoff":      "pre-restart/deploy handoff required",
			"verification": "post-action status/journal/smoke evidence required",
			"rollback":     "stop or rollback path must be named when risk is nontrivial",
			"separation":   "commit/push/deploy/restart should remain separately visible steps",
		}
	default:
		return nil
	}
}

func normalizeContinuationLeaseConstraints(class ContinuationLeaseClass, constraints map[string]string) map[string]string {
	defaults := DefaultContinuationLeaseConstraints(class)
	if len(defaults) == 0 && len(constraints) == 0 {
		return nil
	}
	out := make(map[string]string, len(defaults)+len(constraints))
	for key, value := range defaults {
		key = normalizeEnumValue(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	for key, value := range constraints {
		key = normalizeEnumValue(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func continuationLeaseClassRequiresExactActions(class ContinuationLeaseClass) bool {
	switch NormalizeContinuationLeaseClass(class) {
	case ContinuationLeaseClassLocalWorkspace, ContinuationLeaseClassDataAccess, ContinuationLeaseClassChildWake, ContinuationLeaseClassCapabilityGrant, ContinuationLeaseClassDeployRestart:
		return true
	default:
		return false
	}
}

type ContinuationLeaseAccessDecision struct {
	LeaseID string `json:"lease_id,omitempty"`
	Action  string `json:"action,omitempty"`
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

func (l ContinuationLease) ActiveAt(now time.Time) bool {
	lease := NormalizeContinuationLease(l)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if lease.Status != ContinuationLeaseStatusActive || lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now)
}

func CheckContinuationLeaseAction(lease ContinuationLease, action string, now time.Time) ContinuationLeaseAccessDecision {
	lease = NormalizeContinuationLease(lease)
	action = normalizeEnumValue(action)
	decision := ContinuationLeaseAccessDecision{LeaseID: lease.ID, Action: action}
	if action == "" {
		decision.Reason = "action_required"
		return decision
	}
	if !lease.ActiveAt(now) {
		decision.Reason = "lease_inactive_or_expired"
		return decision
	}
	if actionListMatches(lease.ForbiddenActions, action) {
		decision.Reason = "action_forbidden"
		return decision
	}
	exactAllowed := actionListMatches(lease.AllowedActions, action)
	if actionListMatches(lease.AllowedActions, "*") && !exactAllowed && continuationLeaseClassRequiresExactActions(lease.LeaseClass) {
		decision.Reason = "lease_class_requires_explicit_action"
		return decision
	}
	if actionListMatches(lease.AllowedActions, "*") || exactAllowed {
		decision.Allowed = true
		decision.Reason = "allowed"
		return decision
	}
	decision.Reason = "action_not_allowed"
	return decision
}

func actionListMatches(values []string, action string) bool {
	action = normalizeEnumValue(action)
	if action == "" {
		return false
	}
	for _, value := range values {
		if normalizeEnumValue(value) == action {
			return true
		}
	}
	return false
}

func SanitizeActionProposalAuthority(proposal ActionProposal) ActionProposal {
	proposal.AllowedActions = sanitizeAllowedActionsAgainstForbidden(proposal.AllowedActions, proposal.ForbiddenActions)
	return proposal
}

func ContinuationStateAuthorityNeedsSanitization(state ContinuationState) bool {
	return CompileContinuationAuthorityContract(state).Invalid() ||
		actionAuthorityHasContradiction(state.ActionProposal.AllowedActions, state.ActionProposal.ForbiddenActions) ||
		actionAuthorityHasContradiction(state.ContinuationLease.AllowedActions, state.ContinuationLease.ForbiddenActions) ||
		continuationLeaseClassContradictedByActions(state.ContinuationLease.LeaseClass, sanitizeAllowedActionsAgainstForbidden(state.ContinuationLease.AllowedActions, state.ContinuationLease.ForbiddenActions), state.ContinuationLease.ForbiddenActions)
}

func actionAuthorityHasContradiction(allowedActions []string, forbiddenActions []string) bool {
	allowed := normalizeActionStringSlice(allowedActions)
	forbidden := normalizeActionStringSlice(forbiddenActions)
	if len(allowed) == 0 || len(forbidden) == 0 {
		return false
	}
	forbiddenExact := make(map[string]struct{}, len(forbidden))
	for _, action := range forbidden {
		if normalized := normalizeAuthorityMatchText(action); normalized != "" {
			forbiddenExact[normalized] = struct{}{}
		}
	}
	broadDeployRestartForbidden := authorityForbiddenIncludesDeployRestart(forbidden)
	for _, action := range allowed {
		normalized := normalizeAuthorityMatchText(action)
		if normalized == "" {
			continue
		}
		if _, forbidden := forbiddenExact[normalized]; forbidden {
			return true
		}
		if broadDeployRestartForbidden && authorityActionIsDeployRestartGrant(normalized) {
			return true
		}
	}
	return false
}

func sanitizeAllowedActionsAgainstForbidden(allowedActions []string, forbiddenActions []string) []string {
	allowed := normalizeActionStringSlice(allowedActions)
	if len(allowed) == 0 {
		return nil
	}
	forbidden := normalizeActionStringSlice(forbiddenActions)
	if len(forbidden) == 0 {
		return allowed
	}
	forbiddenExact := make(map[string]struct{}, len(forbidden))
	for _, action := range forbidden {
		if normalized := normalizeAuthorityMatchText(action); normalized != "" {
			forbiddenExact[normalized] = struct{}{}
		}
	}
	broadDeployRestartForbidden := authorityForbiddenIncludesDeployRestart(forbidden)
	out := make([]string, 0, len(allowed))
	for _, action := range allowed {
		normalized := normalizeAuthorityMatchText(action)
		if normalized == "" {
			continue
		}
		if _, forbidden := forbiddenExact[normalized]; forbidden {
			continue
		}
		if broadDeployRestartForbidden && authorityActionIsDeployRestartGrant(normalized) {
			continue
		}
		out = append(out, action)
	}
	return normalizeActionStringSlice(out)
}

func authorityForbiddenIncludesDeployRestart(actions []string) bool {
	for _, action := range actions {
		switch normalizeAuthorityMatchText(action) {
		case "deploy",
			"restart",
			"restart_service",
			"service_restart",
			"deploy_restart",
			"restart_deploy",
			"deploy_or_restart",
			"restart_or_deploy",
			"deploy_or_enable_systemd",
			"deploy_or_enable_service",
			"deploy_service_restart",
			"restart_or_service_restart":
			return true
		}
	}
	return false
}

func authorityActionIsDeployRestartGrant(action string) bool {
	switch normalizeAuthorityMatchText(action) {
	case "deploy",
		"restart",
		"restart_service",
		"service_restart",
		"live_deploy",
		"run_deploy",
		"system_change",
		"prepare_release_handoff",
		"run_explicit_release_step",
		"post_restart_verification",
		"report_release_result":
		return true
	default:
		return false
	}
}

func continuationAllowedSupportsDeployRestart(actions []string) bool {
	for _, action := range actions {
		if authorityActionIsDeployRestartGrant(action) {
			return true
		}
	}
	return false
}

func continuationLeaseClassContradictedByActions(class ContinuationLeaseClass, allowedActions []string, forbiddenActions []string) bool {
	return NormalizeContinuationLeaseClass(class) == ContinuationLeaseClassDeployRestart &&
		authorityForbiddenIncludesDeployRestart(forbiddenActions) &&
		!continuationAllowedSupportsDeployRestart(allowedActions)
}

func NormalizeActionProposal(proposal ActionProposal) ActionProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.OperationID = strings.TrimSpace(proposal.OperationID)
	proposal.MissionID = strings.TrimSpace(proposal.MissionID)
	proposal.OperatorTitle = strings.TrimSpace(proposal.OperatorTitle)
	proposal.PlanTitle = strings.TrimSpace(proposal.PlanTitle)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.BoundedEffect = strings.TrimSpace(proposal.BoundedEffect)
	proposal.RiskClass = normalizeEnumValue(proposal.RiskClass)
	proposal.AllowedActions = normalizeActionStringSlice(proposal.AllowedActions)
	proposal.ForbiddenActions = normalizeActionStringSlice(proposal.ForbiddenActions)
	proposal.AllowedActions = sanitizeAllowedActionsAgainstForbidden(proposal.AllowedActions, proposal.ForbiddenActions)
	proposal.ValidationPlan = normalizeActionStringSlice(proposal.ValidationPlan)
	proposal.PlanHash = strings.TrimSpace(proposal.PlanHash)
	proposal.Status = NormalizeProposalStatus(proposal.Status)
	if !proposal.ExpiresAt.IsZero() {
		proposal.ExpiresAt = proposal.ExpiresAt.UTC()
	}
	if !proposal.CreatedAt.IsZero() {
		proposal.CreatedAt = proposal.CreatedAt.UTC()
	}
	if !proposal.UpdatedAt.IsZero() {
		proposal.UpdatedAt = proposal.UpdatedAt.UTC()
	}
	if proposal.Status == "" && proposal.Active() {
		proposal.Status = ProposalStatusPending
	}
	if proposal.CreatedAt.IsZero() && proposal.Active() {
		proposal.CreatedAt = time.Now().UTC()
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func NormalizeContinuationLeaseStatus(status ContinuationLeaseStatus) ContinuationLeaseStatus {
	value := normalizeEnumValue(string(status))
	switch ContinuationLeaseStatus(value) {
	case ContinuationLeaseStatusPending, ContinuationLeaseStatusActive, ContinuationLeaseStatusConsumed, ContinuationLeaseStatusDeferred, ContinuationLeaseStatusRevoked, ContinuationLeaseStatusExpired:
		return ContinuationLeaseStatus(value)
	default:
		return ""
	}
}

func NormalizeContinuationApprovalBundle(bundle ContinuationApprovalBundle) ContinuationApprovalBundle {
	bundle.ID = strings.TrimSpace(bundle.ID)
	bundle.OperationID = strings.TrimSpace(bundle.OperationID)
	bundle.PhasePlanID = strings.TrimSpace(bundle.PhasePlanID)
	bundle.PlanFingerprint = strings.TrimSpace(bundle.PlanFingerprint)
	bundle.Status = NormalizeContinuationLeaseStatus(bundle.Status)
	bundle.CurrentPhaseID = strings.TrimSpace(bundle.CurrentPhaseID)
	phases := make([]ContinuationApprovalBundlePhase, 0, len(bundle.Phases))
	seen := make(map[string]struct{}, len(bundle.Phases))
	for i, phase := range bundle.Phases {
		phase = NormalizeContinuationApprovalBundlePhase(phase)
		if !phase.Active() {
			continue
		}
		if phase.Index <= 0 {
			phase.Index = i + 1
		}
		baseID := strings.TrimSpace(phase.ID)
		if baseID == "" {
			baseID = strings.TrimSpace(phase.OperationPhaseID)
		}
		if baseID == "" {
			baseID = fmt.Sprintf("phase-%d", phase.Index)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seen[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		phase.ID = id
		seen[id] = struct{}{}
		phases = append(phases, phase)
	}
	bundle.Phases = phases
	if bundle.CurrentPhaseID != "" {
		if _, ok := seen[bundle.CurrentPhaseID]; !ok {
			bundle.CurrentPhaseID = ""
		}
	}
	if bundle.CurrentPhaseID == "" {
		for _, phase := range bundle.Phases {
			if phase.Status == ContinuationLeaseStatusActive || phase.Status == ContinuationLeaseStatusPending || phase.Status == "" {
				bundle.CurrentPhaseID = phase.ID
				break
			}
		}
	}
	if !bundle.ExpiresAt.IsZero() {
		bundle.ExpiresAt = bundle.ExpiresAt.UTC()
	}
	if !bundle.CreatedAt.IsZero() {
		bundle.CreatedAt = bundle.CreatedAt.UTC()
	}
	if !bundle.UpdatedAt.IsZero() {
		bundle.UpdatedAt = bundle.UpdatedAt.UTC()
	}
	if !bundle.ApprovedAt.IsZero() {
		bundle.ApprovedAt = bundle.ApprovedAt.UTC()
	}
	if !bundle.ConsumedAt.IsZero() {
		bundle.ConsumedAt = bundle.ConsumedAt.UTC()
	}
	if !bundle.RevokedAt.IsZero() {
		bundle.RevokedAt = bundle.RevokedAt.UTC()
	}
	if bundle.Status == "" && bundle.Active() {
		bundle.Status = ContinuationLeaseStatusPending
	}
	if bundle.CreatedAt.IsZero() && bundle.Active() {
		bundle.CreatedAt = time.Now().UTC()
	}
	if bundle.UpdatedAt.IsZero() && bundle.Active() {
		bundle.UpdatedAt = time.Now().UTC()
	}
	return bundle
}

func NormalizeContinuationApprovalBundlePhase(phase ContinuationApprovalBundlePhase) ContinuationApprovalBundlePhase {
	phase.ID = strings.TrimSpace(phase.ID)
	phase.OperationPhaseID = strings.TrimSpace(phase.OperationPhaseID)
	phase.PhaseFingerprint = strings.TrimSpace(phase.PhaseFingerprint)
	phase.OperatorTitle = strings.TrimSpace(phase.OperatorTitle)
	phase.PlanTitle = strings.TrimSpace(phase.PlanTitle)
	phase.Summary = strings.TrimSpace(phase.Summary)
	phase.AuthorityClass = normalizeEnumValue(phase.AuthorityClass)
	phase.WhyNow = strings.TrimSpace(phase.WhyNow)
	phase.BoundedEffect = strings.TrimSpace(phase.BoundedEffect)
	phase.AllowedActions = normalizeActionStringSlice(phase.AllowedActions)
	phase.ForbiddenActions = normalizeActionStringSlice(phase.ForbiddenActions)
	phase.AllowedActions = sanitizeAllowedActionsAgainstForbidden(phase.AllowedActions, phase.ForbiddenActions)
	phase.ValidationPlan = normalizeActionStringSlice(phase.ValidationPlan)
	phase.RequiredCapabilityGrants = NormalizeCapabilityGrantSpecs(phase.RequiredCapabilityGrants)
	phase.Status = NormalizeContinuationLeaseStatus(phase.Status)
	if !phase.ApprovedAt.IsZero() {
		phase.ApprovedAt = phase.ApprovedAt.UTC()
	}
	if !phase.ActivatedAt.IsZero() {
		phase.ActivatedAt = phase.ActivatedAt.UTC()
	}
	if !phase.ConsumedAt.IsZero() {
		phase.ConsumedAt = phase.ConsumedAt.UTC()
	}
	if !phase.DeferredAt.IsZero() {
		phase.DeferredAt = phase.DeferredAt.UTC()
	}
	if phase.Index < 0 {
		phase.Index = 0
	}
	return phase
}

func NormalizeContinuationLease(lease ContinuationLease) ContinuationLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.ProposalID = strings.TrimSpace(lease.ProposalID)
	lease.MissionID = strings.TrimSpace(lease.MissionID)
	lease.OperatorTitle = strings.TrimSpace(lease.OperatorTitle)
	lease.PlanTitle = strings.TrimSpace(lease.PlanTitle)
	lease.Status = NormalizeContinuationLeaseStatus(lease.Status)
	lease.LeaseClass = NormalizeContinuationLeaseClass(lease.LeaseClass)
	lease.AllowedActions = normalizeActionStringSlice(lease.AllowedActions)
	lease.ForbiddenActions = normalizeActionStringSlice(lease.ForbiddenActions)
	lease.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lease.AllowedActions, lease.ForbiddenActions)
	lease.ValidationPlan = normalizeActionStringSlice(lease.ValidationPlan)
	lease.RequiredCapabilityGrants = NormalizeCapabilityGrantSpecs(lease.RequiredCapabilityGrants)
	lease.CapabilityGrantIDs = normalizeActionStringSlice(lease.CapabilityGrantIDs)
	if continuationLeaseClassContradictedByActions(lease.LeaseClass, lease.AllowedActions, lease.ForbiddenActions) {
		lease.LeaseClass = ""
		lease.Constraints = nil
	}
	if lease.LeaseClass == "" {
		lease.LeaseClass = InferContinuationLeaseClass("", lease.AllowedActions, "")
	}
	lease.Constraints = normalizeContinuationLeaseConstraints(lease.LeaseClass, lease.Constraints)
	lease.PlanHash = strings.TrimSpace(lease.PlanHash)
	if lease.MaxTurns < 0 {
		lease.MaxTurns = 0
	}
	if lease.RemainingTurns < 0 {
		lease.RemainingTurns = 0
	}
	if lease.MaxTurns == 0 && lease.RemainingTurns > 0 {
		lease.MaxTurns = lease.RemainingTurns
	}
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	if !lease.ApprovedAt.IsZero() {
		lease.ApprovedAt = lease.ApprovedAt.UTC()
	}
	if !lease.ConsumedAt.IsZero() {
		lease.ConsumedAt = lease.ConsumedAt.UTC()
	}
	if !lease.RevokedAt.IsZero() {
		lease.RevokedAt = lease.RevokedAt.UTC()
	}
	if lease.Status == "" && (lease.ID != "" || lease.ProposalID != "" || lease.RemainingTurns > 0 || lease.MaxTurns > 0) {
		lease.Status = ContinuationLeaseStatusPending
	}
	switch lease.Status {
	case ContinuationLeaseStatusConsumed, ContinuationLeaseStatusRevoked, ContinuationLeaseStatusExpired:
		lease.RemainingTurns = 0
	}
	if lease.CreatedAt.IsZero() && (lease.ID != "" || lease.ProposalID != "" || lease.Status != "") {
		lease.CreatedAt = time.Now().UTC()
	}
	if lease.UpdatedAt.IsZero() && (lease.ID != "" || lease.ProposalID != "" || lease.Status != "") {
		lease.UpdatedAt = time.Now().UTC()
	}
	return lease
}

func normalizeActionStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeContinuationIntent(intent ContinuationIntent) ContinuationIntent {
	intent.Decision = normalizeContinuationIntentDecision(intent.Decision)
	intent.Rationale = strings.TrimSpace(intent.Rationale)
	intent.NextStep = strings.TrimSpace(intent.NextStep)
	intent.Constraints = strings.TrimSpace(intent.Constraints)
	intent.Confidence = normalizeContinuationConfidence(intent.Confidence)
	if intent.UpdatedAt.IsZero() && (intent.Decision != "" || intent.Rationale != "" || intent.NextStep != "" || intent.Constraints != "" || intent.Confidence != "" || intent.Ratified) {
		intent.UpdatedAt = time.Now().UTC()
	}
	return intent
}

func normalizeContinuationIntentDecision(decision ContinuationIntentDecision) ContinuationIntentDecision {
	switch normalizeEnumValue(string(decision)) {
	case string(ContinuationIntentDecisionContinue):
		return ContinuationIntentDecisionContinue
	case string(ContinuationIntentDecisionStop):
		return ContinuationIntentDecisionStop
	case string(ContinuationIntentDecisionHold):
		return ContinuationIntentDecisionHold
	default:
		return ""
	}
}

func normalizeContinuationConfidence(confidence string) string {
	switch normalizeEnumValue(confidence) {
	case "low", "medium", "high":
		return normalizeEnumValue(confidence)
	default:
		return ""
	}
}

func normalizeContinuationStage(value string) string {
	return normalizeEnumValue(value)
}
