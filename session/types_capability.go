//go:build linux

package session

import (
	"strings"
	"time"
)

type CapabilityKind string

type CapabilityReviewStatus string

type CapabilityGrantStatus string

type CapabilityRequest struct {
	RequestID       string                 `json:"request_id"`
	RequestedBy     string                 `json:"requested_by,omitempty"`
	RequestedFor    string                 `json:"requested_for,omitempty"`
	ParentPrincipal string                 `json:"parent_principal,omitempty"`
	AdminPrincipal  string                 `json:"admin_principal,omitempty"`
	Kind            CapabilityKind         `json:"kind,omitempty"`
	TargetResource  string                 `json:"target_resource,omitempty"`
	Purpose         string                 `json:"purpose,omitempty"`
	RiskClass       string                 `json:"risk_class,omitempty"`
	Contract        string                 `json:"contract,omitempty"`
	Constraints     string                 `json:"constraints,omitempty"`
	ReviewStatus    CapabilityReviewStatus `json:"review_status,omitempty"`
	GrantID         string                 `json:"grant_id,omitempty"`
	CreatedAt       time.Time              `json:"created_at,omitempty"`
	UpdatedAt       time.Time              `json:"updated_at,omitempty"`
}

type CapabilityGrantSpec struct {
	RequestID      string         `json:"request_id,omitempty"`
	GrantID        string         `json:"grant_id,omitempty"`
	Kind           CapabilityKind `json:"kind,omitempty"`
	TargetResource string         `json:"target_resource,omitempty"`
	GrantedTo      string         `json:"granted_to,omitempty"`
	AllowedActions []string       `json:"allowed_actions,omitempty"`
	Contract       string         `json:"contract,omitempty"`
	Constraints    string         `json:"constraints,omitempty"`
	ExpiresAt      time.Time      `json:"expires_at,omitempty"`
}

type CapabilityReview struct {
	ReviewID     string                 `json:"review_id"`
	RequestID    string                 `json:"request_id"`
	Reviewer     string                 `json:"reviewer,omitempty"`
	ReviewerRole string                 `json:"reviewer_role,omitempty"`
	Status       CapabilityReviewStatus `json:"status,omitempty"`
	Rationale    string                 `json:"rationale,omitempty"`
	CreatedAt    time.Time              `json:"created_at,omitempty"`
}

type DurableChildAgreementStatus string

type DurableChildAgreement struct {
	AgreementID         string                      `json:"agreement_id"`
	AgentID             string                      `json:"agent_id,omitempty"`
	ParentPrincipal     string                      `json:"parent_principal,omitempty"`
	ChildPrincipal      string                      `json:"child_principal,omitempty"`
	SourceSurface       string                      `json:"source_surface,omitempty"`
	SourceRequestID     string                      `json:"source_request_id,omitempty"`
	SourceReviewEventID int64                       `json:"source_review_event_id,omitempty"`
	Summary             string                      `json:"summary,omitempty"`
	BoundedEffect       string                      `json:"bounded_effect,omitempty"`
	Status              DurableChildAgreementStatus `json:"status,omitempty"`
	ArtifactRefs        []RecordReference           `json:"artifact_refs,omitempty"`
	CreatedAt           time.Time                   `json:"created_at,omitempty"`
	UpdatedAt           time.Time                   `json:"updated_at,omitempty"`
}

type CapabilityGrant struct {
	GrantID            string                `json:"grant_id"`
	RequestID          string                `json:"request_id,omitempty"`
	GrantedBy          string                `json:"granted_by,omitempty"`
	GrantedTo          string                `json:"granted_to,omitempty"`
	Kind               CapabilityKind        `json:"kind,omitempty"`
	TargetResource     string                `json:"target_resource,omitempty"`
	AllowedActions     []string              `json:"allowed_actions,omitempty"`
	Contract           string                `json:"contract,omitempty"`
	Constraints        string                `json:"constraints,omitempty"`
	Status             CapabilityGrantStatus `json:"status,omitempty"`
	BaselinePolicyHash string                `json:"baseline_policy_hash,omitempty"`
	CurrentPolicyHash  string                `json:"current_policy_hash,omitempty"`
	AnchorFingerprint  string                `json:"anchor_fingerprint,omitempty"`
	DriftSource        ToolDriftSource       `json:"drift_source,omitempty"`
	StaleReason        string                `json:"stale_reason,omitempty"`
	InvocationCount    int                   `json:"invocation_count,omitempty"`
	FailureCount       int                   `json:"failure_count,omitempty"`
	CreatedAt          time.Time             `json:"created_at,omitempty"`
	UpdatedAt          time.Time             `json:"updated_at,omitempty"`
	GrantedAt          time.Time             `json:"granted_at,omitempty"`
	ExpiresAt          time.Time             `json:"expires_at,omitempty"`
	RevokedAt          time.Time             `json:"revoked_at,omitempty"`
	LastInvokedAt      time.Time             `json:"last_invoked_at,omitempty"`
	LastFailureAt      time.Time             `json:"last_failure_at,omitempty"`
}

type CapabilityInvocation struct {
	InvocationID         int64     `json:"invocation_id,omitempty"`
	GrantID              string    `json:"grant_id"`
	Principal            string    `json:"principal,omitempty"`
	Action               string    `json:"action,omitempty"`
	Status               string    `json:"status,omitempty"`
	ErrorText            string    `json:"error_text,omitempty"`
	OutcomeStatus        string    `json:"outcome_status,omitempty"`
	OutcomeErrorText     string    `json:"outcome_error_text,omitempty"`
	SessionID            string    `json:"session_id,omitempty"`
	TurnRunID            int64     `json:"turn_run_id,omitempty"`
	ContinuationLeaseID  string    `json:"continuation_lease_id,omitempty"`
	OperationPlanLeaseID string    `json:"operation_plan_lease_id,omitempty"`
	AuthoritySource      string    `json:"authority_source,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	CompletedAt          time.Time `json:"completed_at,omitempty"`
}

type AuthorityUseRef struct {
	SessionID            string `json:"session_id,omitempty"`
	TurnRunID            int64  `json:"turn_run_id,omitempty"`
	ContinuationLeaseID  string `json:"continuation_lease_id,omitempty"`
	OperationPlanLeaseID string `json:"operation_plan_lease_id,omitempty"`
	AuthoritySource      string `json:"authority_source,omitempty"`
}

func NormalizeCapabilityKind(kind CapabilityKind) CapabilityKind {
	value := normalizeEnumValue(string(kind))
	switch CapabilityKind(value) {
	case CapabilityKindTool,
		CapabilityKindLocalDevice,
		CapabilityKindExternalAccount,
		CapabilityKindPurchase,
		CapabilityKindPublicWeb,
		CapabilityKindCommunication,
		CapabilityKindFileAccess,
		CapabilityKindNetworkAccess,
		CapabilityKindGenericDelegation,
		CapabilityKindSystemChange:
		return CapabilityKind(value)
	default:
		return ""
	}
}

func NormalizeCapabilityReviewStatus(status CapabilityReviewStatus) CapabilityReviewStatus {
	value := normalizeEnumValue(string(status))
	switch CapabilityReviewStatus(value) {
	case CapabilityReviewStatusProposed,
		CapabilityReviewStatusParentApproved,
		CapabilityReviewStatusApproved,
		CapabilityReviewStatusRejected:
		return CapabilityReviewStatus(value)
	default:
		return ""
	}
}

func NormalizeCapabilityGrantStatus(status CapabilityGrantStatus) CapabilityGrantStatus {
	value := normalizeEnumValue(string(status))
	switch CapabilityGrantStatus(value) {
	case CapabilityGrantStatusPending,
		CapabilityGrantStatusActive,
		CapabilityGrantStatusStale,
		CapabilityGrantStatusRevoked,
		CapabilityGrantStatusExpired,
		CapabilityGrantStatusFailed:
		return CapabilityGrantStatus(value)
	default:
		return ""
	}
}

func NormalizeCapabilityGrantSpec(spec CapabilityGrantSpec) CapabilityGrantSpec {
	spec.RequestID = strings.TrimSpace(spec.RequestID)
	spec.GrantID = strings.TrimSpace(spec.GrantID)
	spec.Kind = NormalizeCapabilityKind(spec.Kind)
	spec.TargetResource = strings.TrimSpace(spec.TargetResource)
	spec.GrantedTo = strings.TrimSpace(spec.GrantedTo)
	spec.AllowedActions = NormalizeCapabilityActions(spec.AllowedActions)
	spec.Contract = strings.TrimSpace(spec.Contract)
	spec.Constraints = strings.TrimSpace(spec.Constraints)
	if !spec.ExpiresAt.IsZero() {
		spec.ExpiresAt = spec.ExpiresAt.UTC()
	}
	return spec
}

func NormalizeCapabilityGrantSpecs(specs []CapabilityGrantSpec) []CapabilityGrantSpec {
	out := make([]CapabilityGrantSpec, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		spec = NormalizeCapabilityGrantSpec(spec)
		if !spec.Active() {
			continue
		}
		key := spec.RequestID + "\x00" + string(spec.Kind) + "\x00" + spec.TargetResource + "\x00" + spec.GrantedTo
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, spec)
	}
	return out
}

func NormalizeCapabilityActions(actions []string) []string {
	seen := make(map[string]struct{}, len(actions))
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		action = normalizeEnumValue(action)
		if action == "" {
			continue
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		out = append(out, action)
	}
	return out
}

func NormalizeDurableChildAgreementStatus(status DurableChildAgreementStatus) DurableChildAgreementStatus {
	switch DurableChildAgreementStatus(normalizeEnumValue(string(status))) {
	case DurableChildAgreementStatusProposed,
		DurableChildAgreementStatusApproved,
		DurableChildAgreementStatusRejected,
		DurableChildAgreementStatusSuperseded:
		return DurableChildAgreementStatus(normalizeEnumValue(string(status)))
	default:
		return ""
	}
}

func DurableChildAgreementStatusFromCapabilityReview(status CapabilityReviewStatus) DurableChildAgreementStatus {
	switch NormalizeCapabilityReviewStatus(status) {
	case CapabilityReviewStatusApproved:
		return DurableChildAgreementStatusApproved
	case CapabilityReviewStatusRejected:
		return DurableChildAgreementStatusRejected
	case CapabilityReviewStatusProposed, CapabilityReviewStatusParentApproved:
		return DurableChildAgreementStatusProposed
	default:
		return ""
	}
}

func NormalizeDurableChildAgreement(record DurableChildAgreement) DurableChildAgreement {
	record.AgreementID = strings.TrimSpace(record.AgreementID)
	record.AgentID = strings.TrimSpace(record.AgentID)
	record.ParentPrincipal = strings.TrimSpace(record.ParentPrincipal)
	record.ChildPrincipal = strings.TrimSpace(record.ChildPrincipal)
	record.SourceSurface = strings.TrimSpace(record.SourceSurface)
	record.SourceRequestID = strings.TrimSpace(record.SourceRequestID)
	record.Summary = strings.TrimSpace(record.Summary)
	record.BoundedEffect = strings.TrimSpace(record.BoundedEffect)
	record.Status = NormalizeDurableChildAgreementStatus(record.Status)
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	if record.Status == "" && record.Active() {
		record.Status = DurableChildAgreementStatusProposed
	}
	if record.CreatedAt.IsZero() && record.Active() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.UpdatedAt.IsZero() && record.Active() {
		record.UpdatedAt = time.Now().UTC()
	}
	return record
}

func NormalizeCapabilityRequest(request CapabilityRequest) CapabilityRequest {
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.RequestedBy = strings.TrimSpace(request.RequestedBy)
	request.RequestedFor = strings.TrimSpace(request.RequestedFor)
	request.ParentPrincipal = strings.TrimSpace(request.ParentPrincipal)
	request.AdminPrincipal = strings.TrimSpace(request.AdminPrincipal)
	request.Kind = NormalizeCapabilityKind(request.Kind)
	request.TargetResource = strings.TrimSpace(request.TargetResource)
	request.Purpose = strings.TrimSpace(request.Purpose)
	request.RiskClass = normalizeEnumValue(request.RiskClass)
	request.Contract = strings.TrimSpace(request.Contract)
	request.Constraints = strings.TrimSpace(request.Constraints)
	request.ReviewStatus = NormalizeCapabilityReviewStatus(request.ReviewStatus)
	request.GrantID = strings.TrimSpace(request.GrantID)
	if request.Kind == "" && request.Active() {
		request.Kind = CapabilityKindGenericDelegation
	}
	if request.ReviewStatus == "" && request.Active() {
		request.ReviewStatus = CapabilityReviewStatusProposed
	}
	if request.Contract == "" && request.Active() {
		request.Contract = "{}"
	}
	if request.Constraints == "" && request.Active() {
		request.Constraints = "{}"
	}
	if request.CreatedAt.IsZero() && request.Active() {
		request.CreatedAt = time.Now().UTC()
	}
	if request.UpdatedAt.IsZero() && request.Active() {
		request.UpdatedAt = time.Now().UTC()
	}
	return request
}

func NormalizeCapabilityReview(review CapabilityReview) CapabilityReview {
	review.ReviewID = strings.TrimSpace(review.ReviewID)
	review.RequestID = strings.TrimSpace(review.RequestID)
	review.Reviewer = strings.TrimSpace(review.Reviewer)
	review.ReviewerRole = normalizeEnumValue(review.ReviewerRole)
	review.Status = NormalizeCapabilityReviewStatus(review.Status)
	review.Rationale = strings.TrimSpace(review.Rationale)
	if review.CreatedAt.IsZero() && (review.ReviewID != "" || review.RequestID != "" || review.Reviewer != "" || review.Status != "" || review.Rationale != "") {
		review.CreatedAt = time.Now().UTC()
	}
	return review
}

func NormalizeCapabilityGrant(grant CapabilityGrant) CapabilityGrant {
	grant.GrantID = strings.TrimSpace(grant.GrantID)
	grant.RequestID = strings.TrimSpace(grant.RequestID)
	grant.GrantedBy = strings.TrimSpace(grant.GrantedBy)
	grant.GrantedTo = strings.TrimSpace(grant.GrantedTo)
	grant.Kind = NormalizeCapabilityKind(grant.Kind)
	grant.TargetResource = strings.TrimSpace(grant.TargetResource)
	grant.AllowedActions = NormalizeCapabilityActions(grant.AllowedActions)
	grant.Contract = strings.TrimSpace(grant.Contract)
	grant.Constraints = strings.TrimSpace(grant.Constraints)
	grant.Status = NormalizeCapabilityGrantStatus(grant.Status)
	grant.BaselinePolicyHash = strings.TrimSpace(grant.BaselinePolicyHash)
	grant.CurrentPolicyHash = strings.TrimSpace(grant.CurrentPolicyHash)
	grant.AnchorFingerprint = strings.TrimSpace(grant.AnchorFingerprint)
	grant.DriftSource = ToolDriftSource(strings.TrimSpace(string(grant.DriftSource)))
	grant.StaleReason = strings.TrimSpace(grant.StaleReason)
	if grant.Kind == "" && grant.GrantID != "" {
		grant.Kind = CapabilityKindGenericDelegation
	}
	if len(grant.AllowedActions) == 0 && grant.GrantID != "" {
		grant.AllowedActions = []string{"invoke"}
	}
	if grant.Status == "" && grant.GrantID != "" {
		grant.Status = CapabilityGrantStatusPending
	}
	if grant.Contract == "" && grant.GrantID != "" {
		grant.Contract = "{}"
	}
	if grant.Constraints == "" && grant.GrantID != "" {
		grant.Constraints = "{}"
	}
	if grant.CreatedAt.IsZero() && grant.GrantID != "" {
		grant.CreatedAt = time.Now().UTC()
	}
	if grant.UpdatedAt.IsZero() && grant.GrantID != "" {
		grant.UpdatedAt = time.Now().UTC()
	}
	if grant.GrantedAt.IsZero() && grant.Status == CapabilityGrantStatusActive {
		grant.GrantedAt = grant.UpdatedAt
	}
	return grant
}

func NormalizeCapabilityInvocation(invocation CapabilityInvocation) CapabilityInvocation {
	invocation.GrantID = strings.TrimSpace(invocation.GrantID)
	invocation.Principal = strings.TrimSpace(invocation.Principal)
	invocation.Action = normalizeEnumValue(invocation.Action)
	invocation.Status = normalizeEnumValue(invocation.Status)
	invocation.ErrorText = strings.TrimSpace(invocation.ErrorText)
	invocation.OutcomeStatus = normalizeEnumValue(invocation.OutcomeStatus)
	invocation.OutcomeErrorText = strings.TrimSpace(invocation.OutcomeErrorText)
	ref := NormalizeAuthorityUseRef(AuthorityUseRef{
		SessionID:            invocation.SessionID,
		TurnRunID:            invocation.TurnRunID,
		ContinuationLeaseID:  invocation.ContinuationLeaseID,
		OperationPlanLeaseID: invocation.OperationPlanLeaseID,
		AuthoritySource:      invocation.AuthoritySource,
	})
	invocation.SessionID = ref.SessionID
	invocation.TurnRunID = ref.TurnRunID
	invocation.ContinuationLeaseID = ref.ContinuationLeaseID
	invocation.OperationPlanLeaseID = ref.OperationPlanLeaseID
	invocation.AuthoritySource = ref.AuthoritySource
	if invocation.CreatedAt.IsZero() && invocation.GrantID != "" {
		invocation.CreatedAt = time.Now().UTC()
	}
	if !invocation.CompletedAt.IsZero() {
		invocation.CompletedAt = invocation.CompletedAt.UTC()
	}
	return invocation
}

func NormalizeAuthorityUseRef(ref AuthorityUseRef) AuthorityUseRef {
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	if ref.TurnRunID < 0 {
		ref.TurnRunID = 0
	}
	ref.ContinuationLeaseID = strings.TrimSpace(ref.ContinuationLeaseID)
	ref.OperationPlanLeaseID = strings.TrimSpace(ref.OperationPlanLeaseID)
	ref.AuthoritySource = normalizeEnumValue(ref.AuthoritySource)
	return ref
}
