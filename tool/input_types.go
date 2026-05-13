//go:build linux

package tool

import (
	"encoding/json"

	"github.com/idolum-ai/aphelion/core"
)

type execInput struct {
	Command    string `json:"command"`
	Workdir    string `json:"workdir,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type memoryInput struct {
	Action     string   `json:"action"`
	Scope      string   `json:"scope,omitempty"`
	Store      string   `json:"store"`
	Content    string   `json:"content,omitempty"`
	Match      string   `json:"match,omitempty"`
	SourceTag  string   `json:"source_tag,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	ProposalID string   `json:"proposal_id,omitempty"`
	Status     string   `json:"status,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

type sessionSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Scope string `json:"scope,omitempty"`
}

type semanticSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
	Scope string `json:"scope,omitempty"`
}

type updatePlanStepInput struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type updatePlanInput struct {
	Explanation string                `json:"explanation,omitempty"`
	Merge       bool                  `json:"merge,omitempty"`
	Plan        []updatePlanStepInput `json:"plan,omitempty"`
}

type updateOperationProposalInput struct {
	ID            string `json:"id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Summary       string `json:"summary,omitempty"`
	WhyNow        string `json:"why_now,omitempty"`
	BoundedEffect string `json:"bounded_effect,omitempty"`
	Status        string `json:"status,omitempty"`
}

type updateOperationPhaseInput struct {
	ID                  string   `json:"id,omitempty"`
	Summary             string   `json:"summary,omitempty"`
	Status              string   `json:"status,omitempty"`
	AuthorityClass      string   `json:"authority_class,omitempty"`
	WhyNow              string   `json:"why_now,omitempty"`
	BoundedEffect       string   `json:"bounded_effect,omitempty"`
	AllowedActions      []string `json:"allowed_actions,omitempty"`
	ForbiddenActions    []string `json:"forbidden_actions,omitempty"`
	ValidationPlan      []string `json:"validation_plan,omitempty"`
	GateLevel           string   `json:"gate_level,omitempty"`
	GateReasonCode      string   `json:"gate_reason_code,omitempty"`
	ApprovalSubject     string   `json:"approval_subject,omitempty"`
	AutoApproveEligible *bool    `json:"autoapprove_eligible,omitempty"`
	BlockedReasonCode   string   `json:"blocked_reason_code,omitempty"`
	RequiresConsent     *bool    `json:"requires_consent,omitempty"`
	RequiresOptIn       *bool    `json:"requires_opt_in,omitempty"`
	SupersedesPhaseIDs  []string `json:"supersedes_phase_ids,omitempty"`
	StaleAuthority      *bool    `json:"stale_authority,omitempty"`
	RequiresApproval    *bool    `json:"requires_approval,omitempty"`
	LeaseID             string   `json:"lease_id,omitempty"`
}

type updateOperationPhasePlanInput struct {
	ID             string                      `json:"id,omitempty"`
	Goal           string                      `json:"goal,omitempty"`
	CurrentPhaseID string                      `json:"current_phase_id,omitempty"`
	Phases         []updateOperationPhaseInput `json:"phases,omitempty"`
}

type updateOperationFindingInput struct {
	Claim      string `json:"claim"`
	Confidence string `json:"confidence,omitempty"`
	Basis      string `json:"basis,omitempty"`
}

type updateOperationArtifactInput struct {
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref"`
}

type updateOperationPlanLeaseLaneInput struct {
	ID               string   `json:"id,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	AuthorityClass   string   `json:"authority_class,omitempty"`
	ExpectedTurns    int      `json:"expected_turns,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
	ForbiddenActions []string `json:"forbidden_actions,omitempty"`
}

type updateOperationPlanLeaseEvidenceInput struct {
	TurnsSpent         int      `json:"turns_spent,omitempty"`
	LanesUsed          []string `json:"lanes_used,omitempty"`
	Completed          []string `json:"completed,omitempty"`
	Blocked            []string `json:"blocked,omitempty"`
	InterruptsRaised   []string `json:"interrupts_raised,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ChangesMade        []string `json:"changes_made,omitempty"`
	ResidualRisk       string   `json:"residual_risk,omitempty"`
	SuggestedNextLease string   `json:"suggested_next_lease,omitempty"`
}

type updateOperationPlanLeaseInput struct {
	ID                   string                                 `json:"id,omitempty"`
	Summary              string                                 `json:"summary,omitempty"`
	Objective            string                                 `json:"objective,omitempty"`
	MissionID            string                                 `json:"mission_id,omitempty"`
	OperationID          string                                 `json:"operation_id,omitempty"`
	Status               string                                 `json:"status,omitempty"`
	TurnBudget           int                                    `json:"turn_budget,omitempty"`
	RemainingTurns       int                                    `json:"remaining_turns,omitempty"`
	CoveredPhaseIDs      []string                               `json:"covered_phase_ids,omitempty"`
	ExpiresAt            string                                 `json:"expires_at,omitempty"`
	Lanes                []updateOperationPlanLeaseLaneInput    `json:"lanes,omitempty"`
	AllowedActions       []string                               `json:"allowed_actions,omitempty"`
	ForbiddenActions     []string                               `json:"forbidden_actions,omitempty"`
	ValidationGates      []string                               `json:"validation_gates,omitempty"`
	ExitConditions       []string                               `json:"exit_conditions,omitempty"`
	HardInterrupts       []string                               `json:"hard_interrupts,omitempty"`
	ChildInitiationLanes []string                               `json:"child_initiation_lanes,omitempty"`
	EvidenceDigest       *updateOperationPlanLeaseEvidenceInput `json:"evidence_digest,omitempty"`
	ApprovedBy           int64                                  `json:"approved_by,omitempty"`
	ApprovedAt           string                                 `json:"approved_at,omitempty"`
}

type updateOperationInput struct {
	ID        string                         `json:"id,omitempty"`
	Objective string                         `json:"objective,omitempty"`
	Status    string                         `json:"status,omitempty"`
	Stage     string                         `json:"stage,omitempty"`
	Summary   string                         `json:"summary,omitempty"`
	Merge     bool                           `json:"merge,omitempty"`
	Proposal  *updateOperationProposalInput  `json:"proposal,omitempty"`
	PhasePlan *updateOperationPhasePlanInput `json:"phase_plan,omitempty"`
	PlanLease *updateOperationPlanLeaseInput `json:"plan_lease,omitempty"`
	Findings  []updateOperationFindingInput  `json:"findings,omitempty"`
	Artifacts []updateOperationArtifactInput `json:"artifacts,omitempty"`
}

type toolAuthorityInput struct {
	Action            string `json:"action"`
	ToolName          string `json:"tool_name,omitempty"`
	ImplementationRef string `json:"implementation_ref,omitempty"`
	Registered        *bool  `json:"registered,omitempty"`
	Principal         string `json:"principal,omitempty"`
	Status            string `json:"status,omitempty"`
	Installer         string `json:"installer,omitempty"`
	InstallRef        string `json:"install_ref,omitempty"`
	ProbeStatus       string `json:"probe_status,omitempty"`
	ProbeOutput       string `json:"probe_output,omitempty"`
	Rationale         string `json:"rationale,omitempty"`
	Limit             int    `json:"limit,omitempty"`
}

type capabilityInput struct {
	Action               string                     `json:"action"`
	RequestID            string                     `json:"request_id,omitempty"`
	GrantID              string                     `json:"grant_id,omitempty"`
	Kind                 string                     `json:"kind,omitempty"`
	TargetResource       string                     `json:"target_resource,omitempty"`
	CapabilityAction     string                     `json:"capability_action,omitempty"`
	RequestedFor         string                     `json:"requested_for,omitempty"`
	ParentPrincipal      string                     `json:"parent_principal,omitempty"`
	AdminPrincipal       string                     `json:"admin_principal,omitempty"`
	Purpose              string                     `json:"purpose,omitempty"`
	RiskClass            string                     `json:"risk_class,omitempty"`
	Contract             json.RawMessage            `json:"contract,omitempty"`
	Constraints          json.RawMessage            `json:"constraints,omitempty"`
	CapabilityUpdatePlan *capabilityUpdatePlanInput `json:"capability_update_plan,omitempty"`
	ReviewTargetChatID   int64                      `json:"review_target_chat_id,omitempty"`
	ReviewSummary        string                     `json:"review_summary,omitempty"`
	ReviewStatus         string                     `json:"review_status,omitempty"`
	GrantStatus          string                     `json:"grant_status,omitempty"`
	Principal            string                     `json:"principal,omitempty"`
	AllowedActions       []string                   `json:"allowed_actions,omitempty"`
	Rationale            string                     `json:"rationale,omitempty"`
	ExpiresInSeconds     int                        `json:"expires_in_seconds,omitempty"`
	Limit                int                        `json:"limit,omitempty"`
}

type openAIFileInput struct {
	Action  string `json:"action"`
	Path    string `json:"path,omitempty"`
	FileID  string `json:"file_id,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

type openAIVectorStoreInput struct {
	Action  string `json:"action"`
	StoreID string `json:"store_id,omitempty"`
	Name    string `json:"name,omitempty"`
	FileID  string `json:"file_id,omitempty"`
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type durableAgentPolicyPatchInput struct {
	Mode          string   `json:"mode,omitempty"`
	Charter       string   `json:"charter,omitempty"`
	Autonomy      string   `json:"autonomy,omitempty"`
	Visibility    string   `json:"visibility,omitempty"`
	SharedContext string   `json:"shared_context,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	DriftPolicy   string   `json:"drift_policy,omitempty"`
}

type durableAgentPolicyOverridesInput struct {
	OutboundMode              string   `json:"outbound_mode,omitempty"`
	PublicSurfaceMode         string   `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string   `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string   `json:"shared_inference_reuse_scope,omitempty"`
	TailnetMode               string   `json:"tailnet_mode,omitempty"`
	TailnetHostname           string   `json:"tailnet_hostname,omitempty"`
	TailnetTags               []string `json:"tailnet_tags,omitempty"`
	TailnetSurfacePolicy      string   `json:"tailnet_surface_policy,omitempty"`
}

type durableAgentWizardAnswersInput struct {
	Mode             string   `json:"mode,omitempty"`
	Address          string   `json:"address,omitempty"`
	Account          string   `json:"account,omitempty"`
	Adapter          string   `json:"adapter,omitempty"`
	Query            string   `json:"query,omitempty"`
	BootstrapProfile string   `json:"bootstrap_profile,omitempty"`
	BootstrapModel   string   `json:"bootstrap_model,omitempty"`
	Charter          string   `json:"charter,omitempty"`
	Autonomy         string   `json:"autonomy,omitempty"`
	WakeupMode       string   `json:"wakeup_mode,omitempty"`
	PollInterval     string   `json:"poll_interval,omitempty"`
	SurfaceRules     []string `json:"surface_rules,omitempty"`
	SummarizePDFs    *bool    `json:"summarize_pdfs,omitempty"`
	SynthesisCadence string   `json:"synthesis_cadence,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	NeverRetain      []string `json:"never_retain,omitempty"`
	DriftPolicy      string   `json:"drift_policy,omitempty"`
}

type durableAgentMemoryDelegationEntryInput struct {
	CandidateID string `json:"candidate_id,omitempty"`
	SourceStore string `json:"source_store,omitempty"`
	TargetStore string `json:"target_store,omitempty"`
	Content     string `json:"content,omitempty"`
}

type durableAgentMemoryDelegationInput struct {
	Limit        int                                      `json:"limit,omitempty"`
	CandidateIDs []string                                 `json:"candidate_ids,omitempty"`
	Entries      []durableAgentMemoryDelegationEntryInput `json:"entries,omitempty"`
	TargetStore  string                                   `json:"target_store,omitempty"`
	Reason       string                                   `json:"reason,omitempty"`
}

type durableAgentProfileEditInput struct {
	TargetFile string `json:"target_file,omitempty"`
	Content    string `json:"content,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type durableAgentArtifactInput struct {
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type durableAgentSnapshotInput struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type durableAgentDelegationRequestInput struct {
	RequestID            string                            `json:"request_id,omitempty"`
	Kind                 string                            `json:"kind,omitempty"`
	TargetResource       string                            `json:"target_resource,omitempty"`
	RequestedFor         string                            `json:"requested_for,omitempty"`
	RequestedBy          string                            `json:"requested_by,omitempty"`
	ParentPrincipal      string                            `json:"parent_principal,omitempty"`
	AdminPrincipal       string                            `json:"admin_principal,omitempty"`
	Purpose              string                            `json:"purpose,omitempty"`
	RiskClass            string                            `json:"risk_class,omitempty"`
	Contract             json.RawMessage                   `json:"contract,omitempty"`
	Constraints          json.RawMessage                   `json:"constraints,omitempty"`
	CapabilityUpdatePlan *capabilityUpdatePlanInput        `json:"capability_update_plan,omitempty"`
	PolicyPatch          *durableAgentPolicyPatchInput     `json:"policy_patch,omitempty"`
	PolicyOverrides      *durableAgentPolicyOverridesInput `json:"policy_overrides,omitempty"`
	Provisioning         []string                          `json:"provisioning,omitempty"`
	Attestation          []string                          `json:"attestation,omitempty"`
	GrantActions         []string                          `json:"grant_actions,omitempty"`
	UpdateReason         string                            `json:"update_reason,omitempty"`
	Summary              string                            `json:"summary,omitempty"`
	LocalActions         []string                          `json:"local_actions,omitempty"`
	Questions            []string                          `json:"questions,omitempty"`
	RiskFlags            []string                          `json:"risk_flags,omitempty"`
	ArtifactRefs         []string                          `json:"artifact_refs,omitempty"`
	Metadata             map[string]string                 `json:"metadata,omitempty"`
	ReviewTargetChatID   int64                             `json:"review_target_chat_id,omitempty"`
}

type durableAgentDelegationReportInput struct {
	RequestID          string            `json:"request_id,omitempty"`
	GrantID            string            `json:"grant_id,omitempty"`
	Status             string            `json:"status,omitempty"`
	Outcome            string            `json:"outcome,omitempty"`
	Summary            string            `json:"summary,omitempty"`
	LocalActions       []string          `json:"local_actions,omitempty"`
	Questions          []string          `json:"questions,omitempty"`
	RiskFlags          []string          `json:"risk_flags,omitempty"`
	ArtifactRefs       []string          `json:"artifact_refs,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	ReviewTargetChatID int64             `json:"review_target_chat_id,omitempty"`
}

type durableAgentInput struct {
	Action             string                              `json:"action"`
	AgentID            string                              `json:"agent_id,omitempty"`
	ChannelKind        string                              `json:"channel_kind,omitempty"`
	ReviewEventID      int64                               `json:"review_event_id,omitempty"`
	ReviewTargetChatID int64                               `json:"review_target_chat_id,omitempty"`
	Archetype          string                              `json:"archetype,omitempty"`
	Reason             string                              `json:"reason,omitempty"`
	BootstrapProfile   string                              `json:"bootstrap_profile,omitempty"`
	BootstrapLLM       *core.NodeLLMBootstrap              `json:"bootstrap_llm,omitempty"`
	PolicyPatch        *durableAgentPolicyPatchInput       `json:"policy_patch,omitempty"`
	PolicyOverrides    *durableAgentPolicyOverridesInput   `json:"policy_overrides,omitempty"`
	WakeupMode         string                              `json:"wakeup_mode,omitempty"`
	NetworkPolicy      string                              `json:"network_policy,omitempty"`
	SecretScopes       []string                            `json:"secret_scopes,omitempty"`
	ChannelConfig      json.RawMessage                     `json:"channel_config,omitempty"`
	WizardAnswers      *durableAgentWizardAnswersInput     `json:"wizard_answers,omitempty"`
	MemoryDelegation   *durableAgentMemoryDelegationInput  `json:"memory_delegation,omitempty"`
	Snapshot           *durableAgentSnapshotInput          `json:"snapshot,omitempty"`
	ProfileEdit        *durableAgentProfileEditInput       `json:"profile_edit,omitempty"`
	Artifact           *durableAgentArtifactInput          `json:"artifact,omitempty"`
	DelegationRequest  *durableAgentDelegationRequestInput `json:"delegation_request,omitempty"`
	DelegationReport   *durableAgentDelegationReportInput  `json:"delegation_report,omitempty"`
	Operation          string                              `json:"operation,omitempty"`
	Secret             string                              `json:"secret,omitempty"`
	Message            string                              `json:"message,omitempty"`
	History            int                                 `json:"history,omitempty"`
	TelegramUserID     int64                               `json:"telegram_user_id,omitempty"`
	TelegramUserIDs    []int64                             `json:"telegram_user_ids,omitempty"`
}
