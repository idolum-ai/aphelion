//go:build linux

package session

import (
	"fmt"
	"strings"
	"time"
)

type PlanStatus string

type PlanStep struct {
	Step   string     `json:"step"`
	Status PlanStatus `json:"status"`
}

type PlanState struct {
	Explanation string     `json:"explanation,omitempty"`
	Steps       []PlanStep `json:"steps,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
}

type PlanEventKind string

type PlanEvent struct {
	ID        int64
	SessionID string
	Kind      PlanEventKind
	PlanState PlanState
	CreatedAt time.Time
}

type OperationStatus string

type ProposalStatus string

type FindingConfidence string

type OperationProposal struct {
	ID            string         `json:"id,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	OperatorTitle string         `json:"operator_title,omitempty"`
	PlanTitle     string         `json:"plan_title,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	WhyNow        string         `json:"why_now,omitempty"`
	BoundedEffect string         `json:"bounded_effect,omitempty"`
	Status        ProposalStatus `json:"status,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at,omitempty"`
}

type OperationPhase struct {
	ID                       string                `json:"id,omitempty"`
	OperatorTitle            string                `json:"operator_title,omitempty"`
	PlanTitle                string                `json:"plan_title,omitempty"`
	Summary                  string                `json:"summary,omitempty"`
	Status                   PlanStatus            `json:"status,omitempty"`
	AuthorityClass           string                `json:"authority_class,omitempty"`
	WhyNow                   string                `json:"why_now,omitempty"`
	BoundedEffect            string                `json:"bounded_effect,omitempty"`
	AllowedActions           []string              `json:"allowed_actions,omitempty"`
	ForbiddenActions         []string              `json:"forbidden_actions,omitempty"`
	ValidationPlan           []string              `json:"validation_plan,omitempty"`
	GateLevel                string                `json:"gate_level,omitempty"`
	GateReasonCode           string                `json:"gate_reason_code,omitempty"`
	ApprovalSubject          string                `json:"approval_subject,omitempty"`
	AutoApproveEligible      *bool                 `json:"autoapprove_eligible,omitempty"`
	BlockedReasonCode        string                `json:"blocked_reason_code,omitempty"`
	RequiresConsent          bool                  `json:"requires_consent,omitempty"`
	RequiresOptIn            bool                  `json:"requires_opt_in,omitempty"`
	SupersedesPhaseIDs       []string              `json:"supersedes_phase_ids,omitempty"`
	StaleAuthority           bool                  `json:"stale_authority,omitempty"`
	RequiresApproval         bool                  `json:"requires_approval,omitempty"`
	RequiredCapabilityGrants []CapabilityGrantSpec `json:"required_capability_grants,omitempty"`
	LeaseID                  string                `json:"lease_id,omitempty"`
	CompletedAt              time.Time             `json:"completed_at,omitempty"`
}

type OperationPhasePlan struct {
	ID             string           `json:"id,omitempty"`
	Goal           string           `json:"goal,omitempty"`
	CurrentPhaseID string           `json:"current_phase_id,omitempty"`
	Phases         []OperationPhase `json:"phases,omitempty"`
	UpdatedAt      time.Time        `json:"updated_at,omitempty"`
}

type OperationFinding struct {
	Claim      string            `json:"claim"`
	Confidence FindingConfidence `json:"confidence,omitempty"`
	Basis      string            `json:"basis,omitempty"`
}

type OperationArtifact struct {
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref"`
}

type OperationRecoveryHandoff struct {
	Contract          string `json:"contract,omitempty"`
	OperationKind     string `json:"operation_kind,omitempty"`
	OperationTool     string `json:"operation_tool,omitempty"`
	RetryPolicy       string `json:"retry_policy,omitempty"`
	RequiredAuthority string `json:"required_authority,omitempty"`
	ResourceBlocker   string `json:"resource_blocker,omitempty"`
	DurableAgentID    string `json:"durable_agent_id,omitempty"`
	AgentID           string `json:"agent_id,omitempty"`
	BlockerKind       string `json:"blocker_kind,omitempty"`
	TaskPacketID      string `json:"task_packet_id,omitempty"`
	ChildResultID     string `json:"child_result_id,omitempty"`
	Tool              string `json:"tool,omitempty"`
	Adapter           string `json:"adapter,omitempty"`
	DiagnosticOnly    bool   `json:"diagnostic_only,omitempty"`
	NoContentProbe    bool   `json:"no_content_probe,omitempty"`
}

type PlanLeaseStatus string

type OperationPlanLeaseLane struct {
	ID               string   `json:"id,omitempty"`
	OperatorTitle    string   `json:"operator_title,omitempty"`
	PlanTitle        string   `json:"plan_title,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	AuthorityClass   string   `json:"authority_class,omitempty"`
	ExpectedTurns    int      `json:"expected_turns,omitempty"`
	AllowedActions   []string `json:"allowed_actions,omitempty"`
	ForbiddenActions []string `json:"forbidden_actions,omitempty"`
}

type OperationPlanLeaseEvidenceDigest struct {
	TurnsSpent         int       `json:"turns_spent,omitempty"`
	LanesUsed          []string  `json:"lanes_used,omitempty"`
	Completed          []string  `json:"completed,omitempty"`
	Blocked            []string  `json:"blocked,omitempty"`
	InterruptsRaised   []string  `json:"interrupts_raised,omitempty"`
	EvidenceRefs       []string  `json:"evidence_refs,omitempty"`
	ChangesMade        []string  `json:"changes_made,omitempty"`
	ResidualRisk       string    `json:"residual_risk,omitempty"`
	SuggestedNextLease string    `json:"suggested_next_lease,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type OperationPlanLease struct {
	ID                   string                           `json:"id,omitempty"`
	OperatorTitle        string                           `json:"operator_title,omitempty"`
	PlanTitle            string                           `json:"plan_title,omitempty"`
	Summary              string                           `json:"summary,omitempty"`
	Objective            string                           `json:"objective,omitempty"`
	MissionID            string                           `json:"mission_id,omitempty"`
	OperationID          string                           `json:"operation_id,omitempty"`
	Status               PlanLeaseStatus                  `json:"status,omitempty"`
	TurnBudget           int                              `json:"turn_budget,omitempty"`
	RemainingTurns       int                              `json:"remaining_turns,omitempty"`
	CoveredPhaseIDs      []string                         `json:"covered_phase_ids,omitempty"`
	ExpiresAt            time.Time                        `json:"expires_at,omitempty"`
	Lanes                []OperationPlanLeaseLane         `json:"lanes,omitempty"`
	AllowedActions       []string                         `json:"allowed_actions,omitempty"`
	ForbiddenActions     []string                         `json:"forbidden_actions,omitempty"`
	ValidationGates      []string                         `json:"validation_gates,omitempty"`
	ExitConditions       []string                         `json:"exit_conditions,omitempty"`
	HardInterrupts       []string                         `json:"hard_interrupts,omitempty"`
	ChildInitiationLanes []string                         `json:"child_initiation_lanes,omitempty"`
	EvidenceDigest       OperationPlanLeaseEvidenceDigest `json:"evidence_digest,omitempty"`
	ApprovedBy           int64                            `json:"approved_by,omitempty"`
	ApprovedAt           time.Time                        `json:"approved_at,omitempty"`
	CreatedAt            time.Time                        `json:"created_at,omitempty"`
	UpdatedAt            time.Time                        `json:"updated_at,omitempty"`
}

type WorkCodexEvent struct {
	Kind     string `json:"kind,omitempty"`
	Method   string `json:"method,omitempty"`
	Status   string `json:"status,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Path     string `json:"path,omitempty"`
	Command  string `json:"command,omitempty"`
	Preview  string `json:"preview,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	TurnID   string `json:"turn_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Server   string `json:"server,omitempty"`
	Tool     string `json:"tool,omitempty"`
}

type WorkOperationMetadata struct {
	Executor              string           `json:"executor,omitempty"`
	ConfiguredExecutor    string           `json:"configured_executor,omitempty"`
	PreferredExecutor     string           `json:"preferred_executor,omitempty"`
	FallbackReason        string           `json:"fallback_reason,omitempty"`
	LastOperationID       string           `json:"last_operation_id,omitempty"`
	LastActionProposalID  string           `json:"last_action_proposal_id,omitempty"`
	LastActionOperationID string           `json:"last_action_operation_id,omitempty"`
	LastLeaseID           string           `json:"last_lease_id,omitempty"`
	LastWorkMode          string           `json:"last_work_mode,omitempty"`
	CodexThreadID         string           `json:"codex_thread_id,omitempty"`
	CodexLastTurnID       string           `json:"codex_last_turn_id,omitempty"`
	CodexLaneMode         string           `json:"codex_lane_mode,omitempty"`
	RepoRoot              string           `json:"repo_root,omitempty"`
	Workdir               string           `json:"workdir,omitempty"`
	ChangedFiles          []string         `json:"changed_files,omitempty"`
	Commands              []string         `json:"commands,omitempty"`
	CodexEvents           []WorkCodexEvent `json:"codex_events,omitempty"`
	PatchPreview          string           `json:"patch_preview,omitempty"`
	CommitLaneStatus      string           `json:"commit_lane_status,omitempty"`
	LastSummary           string           `json:"last_summary,omitempty"`
	LastError             string           `json:"last_error,omitempty"`
	PendingCodexApproval  string           `json:"pending_codex_approval,omitempty"`
	LastCompletedAt       time.Time        `json:"last_completed_at,omitempty"`
	LastExecutorUpdatedAt time.Time        `json:"last_executor_updated_at,omitempty"`
}

// OperationEvidenceStatus is a read-only projection of whether operation completion
// can be justified from typed work evidence. It is intentionally separate from the
// mutable phase state so status/doctor-style surfaces can explain evidence drift
// before a model-authored update attempts to close an operation.
type OperationEvidenceStatus struct {
	PhaseID        string     `json:"phase_id,omitempty"`
	AuthorityClass string     `json:"authority_class,omitempty"`
	Status         PlanStatus `json:"status,omitempty"`
	EvidenceKind   string     `json:"evidence_kind,omitempty"`
	Satisfied      bool       `json:"satisfied"`
	ReasonCode     string     `json:"reason_code,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	WorkMode       string     `json:"work_mode,omitempty"`
	LeaseID        string     `json:"lease_id,omitempty"`
}

type OperationState struct {
	ID              string                   `json:"id,omitempty"`
	Objective       string                   `json:"objective,omitempty"`
	Status          OperationStatus          `json:"status,omitempty"`
	Stage           string                   `json:"stage,omitempty"`
	Summary         string                   `json:"summary,omitempty"`
	Proposal        OperationProposal        `json:"proposal,omitempty"`
	PhasePlan       OperationPhasePlan       `json:"phase_plan,omitempty"`
	PlanLease       OperationPlanLease       `json:"plan_lease,omitempty"`
	Findings        []OperationFinding       `json:"findings,omitempty"`
	Artifacts       []OperationArtifact      `json:"artifacts,omitempty"`
	RecoveryHandoff OperationRecoveryHandoff `json:"recovery_handoff,omitempty"`
	Work            WorkOperationMetadata    `json:"work,omitempty"`
	UpdatedAt       time.Time                `json:"updated_at,omitempty"`
}

func NormalizePlanState(state PlanState) PlanState {
	state.Explanation = strings.TrimSpace(state.Explanation)
	steps := make([]PlanStep, 0, len(state.Steps))
	for _, step := range state.Steps {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			continue
		}
		status := NormalizePlanStatus(step.Status)
		if status == "" {
			status = PlanStatusPending
		}
		steps = append(steps, PlanStep{
			Step:   text,
			Status: status,
		})
	}
	state.Steps = steps
	if state.UpdatedAt.IsZero() && (len(state.Steps) > 0 || state.Explanation != "") {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func (s PlanState) FormattedSteps() []string {
	normalized := NormalizePlanState(s)
	out := make([]string, 0, len(normalized.Steps))
	for _, step := range normalized.Steps {
		out = append(out, "["+string(step.Status)+"] "+step.Step)
	}
	return out
}

func NormalizePlanStatus(status PlanStatus) PlanStatus {
	value := strings.ToLower(strings.TrimSpace(string(status)))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch PlanStatus(value) {
	case PlanStatusPending, PlanStatusInProgress, PlanStatusCompleted:
		return PlanStatus(value)
	default:
		return ""
	}
}

func NormalizeOperationState(state OperationState) OperationState {
	state.ID = strings.TrimSpace(state.ID)
	state.Objective = strings.TrimSpace(state.Objective)
	state.Status = NormalizeOperationStatus(state.Status)
	state.Stage = normalizeOperationStage(state.Stage)
	state.Summary = strings.TrimSpace(state.Summary)
	state.Proposal = normalizeOperationProposal(state.Proposal)
	state.PhasePlan = normalizeOperationPhasePlan(state.PhasePlan)
	state.PlanLease = NormalizeOperationPlanLease(state.PlanLease)

	findings := make([]OperationFinding, 0, len(state.Findings))
	for _, finding := range state.Findings {
		claim := strings.TrimSpace(finding.Claim)
		if claim == "" {
			continue
		}
		confidence := NormalizeFindingConfidence(finding.Confidence)
		if confidence == "" {
			confidence = FindingConfidenceMedium
		}
		findings = append(findings, OperationFinding{
			Claim:      claim,
			Confidence: confidence,
			Basis:      strings.TrimSpace(finding.Basis),
		})
	}
	state.Findings = findings

	artifacts := make([]OperationArtifact, 0, len(state.Artifacts))
	for _, artifact := range state.Artifacts {
		ref := strings.TrimSpace(artifact.Ref)
		if ref == "" {
			continue
		}
		artifacts = append(artifacts, OperationArtifact{
			Label: strings.TrimSpace(artifact.Label),
			Ref:   ref,
		})
	}
	state.Artifacts = artifacts
	state.RecoveryHandoff = NormalizeOperationRecoveryHandoff(state.RecoveryHandoff)
	state.Work = NormalizeWorkOperationMetadata(state.Work)

	if state.UpdatedAt.IsZero() && state.Active() {
		state.UpdatedAt = time.Now().UTC()
	}
	return state
}

func NormalizeOperationRecoveryHandoff(handoff OperationRecoveryHandoff) OperationRecoveryHandoff {
	handoff.Contract = strings.TrimSpace(handoff.Contract)
	handoff.OperationKind = normalizeEnumValue(handoff.OperationKind)
	handoff.OperationTool = strings.TrimSpace(handoff.OperationTool)
	handoff.RetryPolicy = normalizeEnumValue(handoff.RetryPolicy)
	handoff.RequiredAuthority = strings.TrimSpace(handoff.RequiredAuthority)
	handoff.ResourceBlocker = normalizeEnumValue(handoff.ResourceBlocker)
	handoff.DurableAgentID = strings.TrimSpace(handoff.DurableAgentID)
	handoff.AgentID = strings.TrimSpace(handoff.AgentID)
	handoff.BlockerKind = normalizeEnumValue(handoff.BlockerKind)
	handoff.TaskPacketID = strings.TrimSpace(handoff.TaskPacketID)
	handoff.ChildResultID = strings.TrimSpace(handoff.ChildResultID)
	handoff.Tool = strings.TrimSpace(handoff.Tool)
	handoff.Adapter = strings.TrimSpace(handoff.Adapter)
	if handoff.DurableAgentID == "" {
		handoff.DurableAgentID = handoff.AgentID
	}
	if handoff.AgentID == "" {
		handoff.AgentID = handoff.DurableAgentID
	}
	if handoff.Contract == "" &&
		handoff.OperationKind == "" &&
		handoff.OperationTool == "" &&
		handoff.RetryPolicy == "" &&
		handoff.RequiredAuthority == "" &&
		handoff.ResourceBlocker == "" &&
		handoff.DurableAgentID == "" &&
		handoff.AgentID == "" &&
		handoff.BlockerKind == "" &&
		handoff.TaskPacketID == "" &&
		handoff.ChildResultID == "" &&
		handoff.Tool == "" &&
		handoff.Adapter == "" &&
		!handoff.DiagnosticOnly &&
		!handoff.NoContentProbe {
		return OperationRecoveryHandoff{}
	}
	return handoff
}

func NormalizePlanLeaseStatus(status PlanLeaseStatus) PlanLeaseStatus {
	value := normalizeEnumValue(string(status))
	switch PlanLeaseStatus(value) {
	case PlanLeaseStatusProposed, PlanLeaseStatusApproved, PlanLeaseStatusActive, PlanLeaseStatusPaused, PlanLeaseStatusRevoked, PlanLeaseStatusExpired, PlanLeaseStatusCompleted:
		return PlanLeaseStatus(value)
	default:
		return ""
	}
}

func NormalizeOperationPlanLease(lease OperationPlanLease) OperationPlanLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.OperatorTitle = strings.TrimSpace(lease.OperatorTitle)
	lease.PlanTitle = strings.TrimSpace(lease.PlanTitle)
	lease.Summary = strings.TrimSpace(lease.Summary)
	lease.Objective = strings.TrimSpace(lease.Objective)
	lease.MissionID = strings.TrimSpace(lease.MissionID)
	lease.OperationID = strings.TrimSpace(lease.OperationID)
	lease.Status = NormalizePlanLeaseStatus(lease.Status)
	if lease.TurnBudget < 0 {
		lease.TurnBudget = 0
	}
	if lease.RemainingTurns < 0 {
		lease.RemainingTurns = 0
	}
	if lease.TurnBudget > 0 && lease.RemainingTurns == 0 && (lease.Status == "" || lease.Status == PlanLeaseStatusProposed || lease.Status == PlanLeaseStatusApproved || lease.Status == PlanLeaseStatusActive) {
		lease.RemainingTurns = lease.TurnBudget
	}
	lease.CoveredPhaseIDs = normalizeActionStringSlice(lease.CoveredPhaseIDs)
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	lease.Lanes = normalizeOperationPlanLeaseLanes(lease.Lanes)
	if lease.TurnBudget == 0 {
		for _, lane := range lease.Lanes {
			lease.TurnBudget += lane.ExpectedTurns
		}
	}
	if lease.TurnBudget > 0 && lease.RemainingTurns == 0 && (lease.Status == "" || lease.Status == PlanLeaseStatusProposed || lease.Status == PlanLeaseStatusApproved || lease.Status == PlanLeaseStatusActive) {
		lease.RemainingTurns = lease.TurnBudget
	}
	lease.AllowedActions = normalizeActionStringSlice(lease.AllowedActions)
	lease.ForbiddenActions = normalizeActionStringSlice(lease.ForbiddenActions)
	lease.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lease.AllowedActions, lease.ForbiddenActions)
	lease.ValidationGates = normalizeActionStringSlice(lease.ValidationGates)
	lease.ExitConditions = normalizeActionStringSlice(lease.ExitConditions)
	lease.HardInterrupts = normalizeActionStringSlice(lease.HardInterrupts)
	lease.ChildInitiationLanes = normalizeActionStringSlice(lease.ChildInitiationLanes)
	if lease.Active() {
		if len(lease.HardInterrupts) == 0 {
			lease.HardInterrupts = defaultPlanLeaseHardInterrupts()
		}
		if len(lease.ChildInitiationLanes) == 0 {
			lease.ChildInitiationLanes = defaultPlanLeaseChildInitiationLanes()
		}
	}
	lease.EvidenceDigest = normalizeOperationPlanLeaseEvidenceDigest(lease.EvidenceDigest)
	if !lease.ApprovedAt.IsZero() {
		lease.ApprovedAt = lease.ApprovedAt.UTC()
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	if lease.Status == "" && lease.Active() {
		lease.Status = PlanLeaseStatusProposed
	}
	if lease.CreatedAt.IsZero() && lease.Active() {
		lease.CreatedAt = time.Now().UTC()
	}
	if lease.UpdatedAt.IsZero() && lease.Active() {
		lease.UpdatedAt = time.Now().UTC()
	}
	return lease
}

func normalizeOperationPlanLeaseLanes(values []OperationPlanLeaseLane) []OperationPlanLeaseLane {
	out := make([]OperationPlanLeaseLane, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, lane := range values {
		lane.ID = strings.TrimSpace(lane.ID)
		lane.OperatorTitle = strings.TrimSpace(lane.OperatorTitle)
		lane.PlanTitle = strings.TrimSpace(lane.PlanTitle)
		lane.Summary = strings.TrimSpace(lane.Summary)
		lane.AuthorityClass = normalizeEnumValue(lane.AuthorityClass)
		if lane.ExpectedTurns < 0 {
			lane.ExpectedTurns = 0
		}
		lane.AllowedActions = normalizeActionStringSlice(lane.AllowedActions)
		lane.ForbiddenActions = normalizeActionStringSlice(lane.ForbiddenActions)
		lane.AllowedActions = sanitizeAllowedActionsAgainstForbidden(lane.AllowedActions, lane.ForbiddenActions)
		if !lane.Active() {
			continue
		}
		baseID := lane.ID
		if baseID == "" {
			baseID = fmt.Sprintf("lane-%d", index+1)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seen[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		lane.ID = id
		seen[id] = struct{}{}
		out = append(out, lane)
	}
	return out
}

func normalizeOperationPlanLeaseEvidenceDigest(summary OperationPlanLeaseEvidenceDigest) OperationPlanLeaseEvidenceDigest {
	if summary.TurnsSpent < 0 {
		summary.TurnsSpent = 0
	}
	summary.LanesUsed = normalizeOperationStringList(summary.LanesUsed)
	summary.Completed = normalizeOperationStringList(summary.Completed)
	summary.Blocked = normalizeOperationStringList(summary.Blocked)
	summary.InterruptsRaised = normalizeOperationStringList(summary.InterruptsRaised)
	summary.EvidenceRefs = normalizeOperationStringList(summary.EvidenceRefs)
	summary.ChangesMade = normalizeOperationStringList(summary.ChangesMade)
	summary.ResidualRisk = strings.TrimSpace(summary.ResidualRisk)
	summary.SuggestedNextLease = strings.TrimSpace(summary.SuggestedNextLease)
	if !summary.UpdatedAt.IsZero() {
		summary.UpdatedAt = summary.UpdatedAt.UTC()
	}
	return summary
}

func defaultPlanLeaseHardInterrupts() []string {
	return []string{
		"credentials_or_tokens",
		"mailbox_content_or_mutation",
		"external_account_mutation",
		"public_contact_or_posting",
		"purchases_or_spend",
		"policy_or_grant_change",
		"deploy_or_restart_without_parking",
		"destructive_migration",
		"child_authority_expansion",
	}
}

func defaultPlanLeaseChildInitiationLanes() []string {
	return []string{"scheduled_digest", "blocked_question", "capability_request", "urgent_interrupt", "result_report"}
}

func normalizeOperationPhasePlan(plan OperationPhasePlan) OperationPhasePlan {
	plan.ID = strings.TrimSpace(plan.ID)
	plan.Goal = strings.TrimSpace(plan.Goal)
	plan.CurrentPhaseID = strings.TrimSpace(plan.CurrentPhaseID)
	phases := make([]OperationPhase, 0, len(plan.Phases))
	seenIDs := make(map[string]struct{}, len(plan.Phases))
	for index, phase := range plan.Phases {
		phase = normalizeOperationPhase(phase, index)
		if !phase.Active() {
			continue
		}
		baseID := phase.ID
		if baseID == "" {
			baseID = fmt.Sprintf("phase-%d", index+1)
		}
		id := baseID
		for suffix := 2; ; suffix++ {
			if _, exists := seenIDs[id]; !exists {
				break
			}
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		phase.ID = id
		seenIDs[id] = struct{}{}
		phases = append(phases, phase)
	}
	plan.Phases = phases
	if plan.CurrentPhaseID != "" {
		currentStatus := PlanStatus("")
		for _, phase := range plan.Phases {
			if phase.ID == plan.CurrentPhaseID {
				currentStatus = phase.Status
				break
			}
		}
		if _, ok := seenIDs[plan.CurrentPhaseID]; !ok {
			plan.CurrentPhaseID = ""
		} else if currentStatus == PlanStatusCompleted {
			for _, phase := range plan.Phases {
				if phase.Status == PlanStatusInProgress || phase.Status == PlanStatusPending {
					plan.CurrentPhaseID = ""
					break
				}
			}
		}
	}
	if plan.CurrentPhaseID == "" {
		for _, phase := range plan.Phases {
			if phase.Status == PlanStatusInProgress || phase.Status == PlanStatusPending {
				plan.CurrentPhaseID = phase.ID
				break
			}
		}
	}
	if plan.CurrentPhaseID == "" && len(plan.Phases) > 0 {
		plan.CurrentPhaseID = plan.Phases[0].ID
	}
	if !plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = plan.UpdatedAt.UTC()
	}
	if plan.UpdatedAt.IsZero() && plan.Active() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return plan
}

func normalizeOperationPhase(phase OperationPhase, index int) OperationPhase {
	_ = index
	phase.ID = strings.TrimSpace(phase.ID)
	phase.OperatorTitle = strings.TrimSpace(phase.OperatorTitle)
	phase.PlanTitle = strings.TrimSpace(phase.PlanTitle)
	phase.Summary = strings.TrimSpace(phase.Summary)
	phase.Status = NormalizePlanStatus(phase.Status)
	phase.AuthorityClass = normalizeEnumValue(phase.AuthorityClass)
	phase.WhyNow = strings.TrimSpace(phase.WhyNow)
	phase.BoundedEffect = strings.TrimSpace(phase.BoundedEffect)
	phase.AllowedActions = normalizeActionStringSlice(phase.AllowedActions)
	phase.ForbiddenActions = normalizeActionStringSlice(phase.ForbiddenActions)
	phase.AllowedActions = sanitizeAllowedActionsAgainstForbidden(phase.AllowedActions, phase.ForbiddenActions)
	phase.ValidationPlan = normalizeActionStringSlice(phase.ValidationPlan)
	phase.GateLevel = normalizeEnumValue(phase.GateLevel)
	phase.GateReasonCode = normalizeEnumValue(phase.GateReasonCode)
	phase.ApprovalSubject = normalizeEnumValue(phase.ApprovalSubject)
	phase.BlockedReasonCode = normalizeEnumValue(phase.BlockedReasonCode)
	phase.SupersedesPhaseIDs = normalizeActionStringSlice(phase.SupersedesPhaseIDs)
	phase.LeaseID = strings.TrimSpace(phase.LeaseID)
	if !phase.CompletedAt.IsZero() {
		phase.CompletedAt = phase.CompletedAt.UTC()
	}
	if phase.Status == "" && phase.Active() {
		phase.Status = PlanStatusPending
	}
	if phase.Status != PlanStatusCompleted {
		phase.CompletedAt = time.Time{}
	}
	if phase.Status == PlanStatusCompleted && phase.CompletedAt.IsZero() {
		phase.CompletedAt = time.Now().UTC()
	}
	return phase
}

func NormalizeWorkOperationMetadata(work WorkOperationMetadata) WorkOperationMetadata {
	work.Executor = strings.TrimSpace(work.Executor)
	work.ConfiguredExecutor = strings.TrimSpace(work.ConfiguredExecutor)
	work.PreferredExecutor = strings.TrimSpace(work.PreferredExecutor)
	work.FallbackReason = strings.TrimSpace(work.FallbackReason)
	work.LastOperationID = strings.TrimSpace(work.LastOperationID)
	work.LastActionProposalID = strings.TrimSpace(work.LastActionProposalID)
	work.LastActionOperationID = strings.TrimSpace(work.LastActionOperationID)
	work.LastLeaseID = strings.TrimSpace(work.LastLeaseID)
	work.LastWorkMode = strings.TrimSpace(work.LastWorkMode)
	work.CodexThreadID = strings.TrimSpace(work.CodexThreadID)
	work.CodexLastTurnID = strings.TrimSpace(work.CodexLastTurnID)
	work.CodexLaneMode = strings.TrimSpace(work.CodexLaneMode)
	work.RepoRoot = strings.TrimSpace(work.RepoRoot)
	work.Workdir = strings.TrimSpace(work.Workdir)
	work.LastSummary = strings.TrimSpace(work.LastSummary)
	work.LastError = strings.TrimSpace(work.LastError)
	work.PendingCodexApproval = strings.TrimSpace(work.PendingCodexApproval)
	work.PatchPreview = truncateOperationString(strings.TrimSpace(work.PatchPreview), 4000)
	work.CommitLaneStatus = strings.TrimSpace(work.CommitLaneStatus)
	work.ChangedFiles = normalizeOperationStringList(work.ChangedFiles)
	work.Commands = normalizeOperationStringList(work.Commands)
	work.CodexEvents = normalizeWorkCodexEvents(work.CodexEvents)
	if work.LastExecutorUpdatedAt.IsZero() && (work.Executor != "" || work.LastSummary != "" || work.LastError != "") {
		work.LastExecutorUpdatedAt = time.Now().UTC()
	}
	return work
}

func normalizeWorkCodexEvents(values []WorkCodexEvent) []WorkCodexEvent {
	out := make([]WorkCodexEvent, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		event := WorkCodexEvent{
			Kind:     strings.TrimSpace(value.Kind),
			Method:   strings.TrimSpace(value.Method),
			Status:   strings.TrimSpace(value.Status),
			Subject:  strings.TrimSpace(value.Subject),
			Path:     strings.TrimSpace(value.Path),
			Command:  strings.TrimSpace(value.Command),
			Preview:  truncateOperationString(strings.TrimSpace(value.Preview), 1000),
			ThreadID: strings.TrimSpace(value.ThreadID),
			TurnID:   strings.TrimSpace(value.TurnID),
			AgentID:  strings.TrimSpace(value.AgentID),
			Server:   strings.TrimSpace(value.Server),
			Tool:     strings.TrimSpace(value.Tool),
		}
		if event.Kind == "" && event.Method == "" && event.Subject == "" && event.Path == "" && event.Command == "" {
			continue
		}
		key := strings.Join([]string{event.Kind, event.Method, event.Status, event.Subject, event.Path, event.Command, event.ThreadID, event.TurnID, event.AgentID, event.Server, event.Tool, event.Preview}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, event)
	}
	return out
}

func normalizeOperationStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func truncateOperationString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 12 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-12])) + " [truncated]"
}

func NormalizeOperationStatus(status OperationStatus) OperationStatus {
	value := normalizeEnumValue(string(status))
	switch OperationStatus(value) {
	case OperationStatusIdle, OperationStatusActive, OperationStatusBlocked, OperationStatusCompleted, OperationStatusFailed:
		return OperationStatus(value)
	default:
		return ""
	}
}

func NormalizeProposalStatus(status ProposalStatus) ProposalStatus {
	value := normalizeEnumValue(string(status))
	switch ProposalStatus(value) {
	case ProposalStatusPending, ProposalStatusApproved, ProposalStatusDenied, ProposalStatusExpired, ProposalStatusSuperseded:
		return ProposalStatus(value)
	default:
		return ""
	}
}

func NormalizeFindingConfidence(confidence FindingConfidence) FindingConfidence {
	value := normalizeEnumValue(string(confidence))
	switch FindingConfidence(value) {
	case FindingConfidenceLow, FindingConfidenceMedium, FindingConfidenceHigh:
		return FindingConfidence(value)
	default:
		return ""
	}
}

func normalizeOperationProposal(proposal OperationProposal) OperationProposal {
	proposal.ID = strings.TrimSpace(proposal.ID)
	proposal.Kind = normalizeOperationStage(proposal.Kind)
	proposal.OperatorTitle = strings.TrimSpace(proposal.OperatorTitle)
	proposal.PlanTitle = strings.TrimSpace(proposal.PlanTitle)
	proposal.Summary = strings.TrimSpace(proposal.Summary)
	proposal.WhyNow = strings.TrimSpace(proposal.WhyNow)
	proposal.BoundedEffect = strings.TrimSpace(proposal.BoundedEffect)
	proposal.Status = NormalizeProposalStatus(proposal.Status)
	if proposal.Status == "" && proposal.Active() {
		proposal.Status = ProposalStatusPending
	}
	if proposal.UpdatedAt.IsZero() && proposal.Active() {
		proposal.UpdatedAt = time.Now().UTC()
	}
	return proposal
}

func normalizeOperationStage(stage string) string {
	return normalizeEnumValue(stage)
}
