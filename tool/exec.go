//go:build linux

package tool

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	defaultMaxOutputBytes          = 32 * 1024
	capabilityGrantObserverTimeout = 10 * time.Second
)

type Registry struct {
	workspace                       string
	timeout                         time.Duration
	maxOutputBytes                  int
	execApprover                    ExecApprover
	durableMemoryDelegationApprover DurableMemoryDelegationApprover
	durableSnapshotRestoreApprover  DurableSnapshotRestoreApprover
	sandbox                         *sandbox.Resolver
	runner                          *sandbox.Runner
	store                           *session.SQLiteStore
	fileStore                       memstore.FileStore
	filePurpose                     string
	retrievalStore                  memstore.RetrievalStore
	defaultStore                    string
	semantic                        *memstore.SemanticEngine
	durableAgentBootstrapLLM        core.NodeLLMBootstrap
	externalManifests               []ExternalToolManifest
	externalExecutor                ExternalToolExecutor
	codexImageGenerationProvider    agent.Provider
	durableAgentPrincipalFallback   bool
	capabilityGrantObserver         func(context.Context, session.SessionKey, session.CapabilityGrant)
}

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
	Action                    string                              `json:"action"`
	AgentID                   string                              `json:"agent_id,omitempty"`
	ChannelKind               string                              `json:"channel_kind,omitempty"`
	ReviewEventID             int64                               `json:"review_event_id,omitempty"`
	ReviewTargetChatID        int64                               `json:"review_target_chat_id,omitempty"`
	Archetype                 string                              `json:"archetype,omitempty"`
	Reason                    string                              `json:"reason,omitempty"`
	BootstrapProfile          string                              `json:"bootstrap_profile,omitempty"`
	BootstrapLLM              *core.NodeLLMBootstrap              `json:"bootstrap_llm,omitempty"`
	PolicyPatch               *durableAgentPolicyPatchInput       `json:"policy_patch,omitempty"`
	PolicyOverrides           *durableAgentPolicyOverridesInput   `json:"policy_overrides,omitempty"`
	Charter                   string                              `json:"charter,omitempty"`
	Autonomy                  string                              `json:"autonomy,omitempty"`
	Visibility                string                              `json:"visibility,omitempty"`
	SharedContext             string                              `json:"shared_context,omitempty"`
	Capabilities              []string                            `json:"capabilities,omitempty"`
	OutboundMode              string                              `json:"outbound_mode,omitempty"`
	DriftPolicy               string                              `json:"drift_policy,omitempty"`
	PublicSurfaceMode         string                              `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string                              `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string                              `json:"shared_inference_reuse_scope,omitempty"`
	WakeupMode                string                              `json:"wakeup_mode,omitempty"`
	NetworkPolicy             string                              `json:"network_policy,omitempty"`
	SecretScopes              []string                            `json:"secret_scopes,omitempty"`
	ChannelConfig             json.RawMessage                     `json:"channel_config,omitempty"`
	WizardAnswers             *durableAgentWizardAnswersInput     `json:"wizard_answers,omitempty"`
	MemoryDelegation          *durableAgentMemoryDelegationInput  `json:"memory_delegation,omitempty"`
	Snapshot                  *durableAgentSnapshotInput          `json:"snapshot,omitempty"`
	ProfileEdit               *durableAgentProfileEditInput       `json:"profile_edit,omitempty"`
	Artifact                  *durableAgentArtifactInput          `json:"artifact,omitempty"`
	DelegationRequest         *durableAgentDelegationRequestInput `json:"delegation_request,omitempty"`
	DelegationReport          *durableAgentDelegationReportInput  `json:"delegation_report,omitempty"`
	Operation                 string                              `json:"operation,omitempty"`
	Secret                    string                              `json:"secret,omitempty"`
	Message                   string                              `json:"message,omitempty"`
	History                   int                                 `json:"history,omitempty"`
	TelegramUserID            int64                               `json:"telegram_user_id,omitempty"`
	TelegramUserIDs           []int64                             `json:"telegram_user_ids,omitempty"`
}

func NewRegistry(workspace string, timeout time.Duration) *Registry {
	return &Registry{
		workspace:        workspace,
		timeout:          timeout,
		maxOutputBytes:   defaultMaxOutputBytes,
		externalExecutor: defaultExternalToolExecutor{},
	}
}

func NewRegistryWithSandbox(workspace string, timeout time.Duration, resolver *sandbox.Resolver) *Registry {
	registry := NewRegistry(workspace, timeout)
	registry.sandbox = resolver
	registry.runner = sandbox.NewRunner()
	return registry
}

func (r *Registry) WithRunner(runner *sandbox.Runner) *Registry {
	r.runner = runner
	return r
}

func (r *Registry) WithSessionStore(store *session.SQLiteStore) *Registry {
	r.store = store
	return r
}

func (r *Registry) WithCapabilityGrantObserver(observer func(context.Context, session.SessionKey, session.CapabilityGrant)) *Registry {
	if r != nil {
		r.capabilityGrantObserver = observer
	}
	return r
}

func (r *Registry) notifyCapabilityGrantObserver(key session.SessionKey, grant session.CapabilityGrant) {
	if r == nil || r.capabilityGrantObserver == nil {
		return
	}
	observer := r.capabilityGrantObserver
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), capabilityGrantObserverTimeout)
		defer cancel()
		observer(ctx, key, grant)
	}()
}

func (r *Registry) WithCodexImageGenerationProvider(provider agent.Provider) *Registry {
	r.SetCodexImageGenerationProvider(provider)
	return r
}

func (r *Registry) SetCodexImageGenerationProvider(provider agent.Provider) {
	if r == nil {
		return
	}
	r.codexImageGenerationProvider = provider
}

func (r *Registry) WithDurableAgentPrincipalFallback() *Registry {
	if r != nil {
		r.durableAgentPrincipalFallback = true
	}
	return r
}

func (r *Registry) WithExecApprover(approver ExecApprover) *Registry {
	r.execApprover = approver
	return r
}

func (r *Registry) WithDurableMemoryDelegationApprover(approver DurableMemoryDelegationApprover) *Registry {
	r.durableMemoryDelegationApprover = approver
	return r
}

func (r *Registry) WithDurableSnapshotRestoreApprover(approver DurableSnapshotRestoreApprover) *Registry {
	r.durableSnapshotRestoreApprover = approver
	return r
}

func (r *Registry) WithFileStore(store memstore.FileStore, purpose string) *Registry {
	r.fileStore = store
	r.filePurpose = strings.TrimSpace(purpose)
	return r
}

func (r *Registry) WithRetrievalStore(store memstore.RetrievalStore, defaultStore string) *Registry {
	r.retrievalStore = store
	r.defaultStore = strings.TrimSpace(defaultStore)
	return r
}

func (r *Registry) WithSemanticEngine(engine *memstore.SemanticEngine) *Registry {
	r.semantic = engine
	return r
}

func (r *Registry) WithDurableAgentBootstrapLLM(bootstrap core.NodeLLMBootstrap) *Registry {
	r.durableAgentBootstrapLLM = core.NormalizeNodeLLMBootstrap(bootstrap)
	return r
}

func (r *Registry) WithExternalToolExecutor(executor ExternalToolExecutor) *Registry {
	r.externalExecutor = executor
	return r
}

func (r *Registry) WithExternalToolManifestDir(dir string) (*Registry, error) {
	manifests, err := LoadExternalToolManifestDir(dir)
	if err != nil {
		return nil, err
	}
	return r.WithExternalToolManifests(manifests)
}

func (r *Registry) WithExternalToolManifests(manifests []ExternalToolManifest) (*Registry, error) {
	if r == nil {
		return nil, fmt.Errorf("registry is nil")
	}
	normalized := make([]ExternalToolManifest, 0, len(manifests))
	seen := make(map[string]struct{}, len(manifests))
	for _, manifest := range manifests {
		manifest = NormalizeExternalToolManifest(manifest)
		if err := validateExternalToolManifest(manifest); err != nil {
			return nil, err
		}
		if _, exists := seen[manifest.Name]; exists {
			return nil, fmt.Errorf("duplicate external tool manifest name %q", manifest.Name)
		}
		seen[manifest.Name] = struct{}{}
		normalized = append(normalized, manifest)
	}
	for _, def := range r.Definitions() {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		for _, manifest := range normalized {
			if strings.EqualFold(name, manifest.Name) {
				return nil, fmt.Errorf("external tool manifest name %q collides with native tool definition", manifest.Name)
			}
		}
	}
	r.externalManifests = append([]ExternalToolManifest(nil), normalized...)
	return r, nil
}

func (r *Registry) SupportsPrincipal(p principal.Principal) bool {
	if r == nil || r.sandbox == nil {
		return false
	}

	scope, err := r.scopeForPrincipalToolExecution(p)
	if err != nil {
		return false
	}
	if r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && strings.TrimSpace(p.DurableAgentID) != "" {
		return true
	}
	if r.runner == nil {
		return p.Role == principal.RoleAdmin
	}
	return r.runner.Supports(scope)
}

func (r *Registry) scopeForPrincipalToolExecution(p principal.Principal) (sandbox.Scope, error) {
	if r == nil || r.sandbox == nil {
		return sandbox.Scope{}, fmt.Errorf("principal-aware execution requires sandbox resolver")
	}
	if scope, ok, err := r.durableAgentScopeForPrincipalToolExecution(p); ok || err != nil {
		return scope, err
	}
	scope, err := r.sandbox.Resolve(p)
	if err == nil {
		return scope, nil
	}
	return sandbox.Scope{}, err
}

func (r *Registry) durableAgentScopeForPrincipalToolExecution(p principal.Principal) (sandbox.Scope, bool, error) {
	if p.Role != principal.RoleDurableAgent {
		return sandbox.Scope{}, false, nil
	}
	agentID := strings.TrimSpace(p.DurableAgentID)
	if agentID == "" {
		return sandbox.Scope{}, true, fmt.Errorf("durable_agent principal requires durable agent id")
	}

	globalRoot := strings.TrimSpace(r.workspace)
	if r.sandbox != nil {
		roots := r.sandbox.Roots()
		globalRoot = firstNonEmpty(roots.GlobalRoot, globalRoot)
	}

	var workingRoot, memoryRoot, networkPolicy string
	if r.store != nil {
		agent, err := r.store.DurableAgent(agentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return sandbox.Scope{}, true, fmt.Errorf("load durable agent %q for tool scope: %w", agentID, err)
		}
		if agent != nil {
			workingRoot, memoryRoot = durableagent.LocalRoots(agent.AgentID, agent.LocalStorageRoots)
			if workingRoot == "" || memoryRoot == "" {
				workingRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agent.AgentID)
			}
			networkPolicy = agent.NetworkPolicy
		}
		if workingRoot == "" || memoryRoot == "" {
			workingRoot, memoryRoot = durableagent.DefaultLocalRoots(r.store.DBPath(), agentID)
		}
	}

	if workingRoot == "" || memoryRoot == "" {
		if r.durableAgentPrincipalFallback && strings.TrimSpace(r.workspace) != "" {
			workingRoot = strings.TrimSpace(r.workspace)
			memoryRoot = strings.TrimSpace(r.workspace)
		} else {
			return sandbox.Scope{}, true, fmt.Errorf("durable_agent principal %q requires durable local roots", agentID)
		}
	}

	scope, err := sandbox.DurableAgentScope(agentID, globalRoot, workingRoot, memoryRoot, networkPolicy)
	if err != nil {
		return sandbox.Scope{}, true, err
	}
	return scope, true, nil
}

func (r *Registry) DefinitionsForPrincipal(p principal.Principal) []agent.ToolDef {
	defs := r.nativeDefinitionsForPrincipal(p)
	defs = append(defs, r.externalToolDefinitions(r.externalManifestsForPrincipal(p))...)
	return defs
}

func (r *Registry) nativeDefinitionsForPrincipal(p principal.Principal) []agent.ToolDef {
	defs := r.Definitions()
	if len(defs) == 0 {
		return defs
	}

	filtered := make([]agent.ToolDef, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == codexImageGenerationToolName {
			allowed, err := r.codexImageGenerationAccessAllowed(p)
			if err == nil && allowed {
				filtered = append(filtered, def)
			}
			continue
		}
		if !r.authorityManagedTool(name) {
			filtered = append(filtered, def)
			continue
		}
		allowed, err := r.toolAuthorityAccessAllowed(name, p)
		if err != nil || !allowed {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

func (r *Registry) externalManifestsForPrincipal(p principal.Principal) []ExternalToolManifest {
	if len(r.externalManifests) == 0 {
		return nil
	}
	filtered := make([]ExternalToolManifest, 0, len(r.externalManifests))
	for _, manifest := range r.externalManifests {
		name := strings.TrimSpace(manifest.Name)
		if !r.authorityManagedTool(name) {
			filtered = append(filtered, manifest)
			continue
		}
		allowed, err := r.toolAuthorityAccessAllowed(name, p)
		if err != nil || !allowed {
			continue
		}
		if r.externalExecutor != nil && r.externalExecutor.Supports(manifest) && r.store != nil {
			scope, err := r.externalToolFreshnessScope(p)
			if err != nil {
				continue
			}
			if err := r.ensureExternalToolFresh(manifest, scope); err != nil {
				continue
			}
		}
		filtered = append(filtered, manifest)
	}
	return filtered
}

func (r *Registry) externalToolFreshnessScope(p principal.Principal) (sandbox.Scope, error) {
	if r.sandbox != nil {
		scope, err := r.sandbox.Resolve(p)
		if err == nil {
			return scope, nil
		}
	}
	root := strings.TrimSpace(r.workspace)
	if root == "" {
		return sandbox.Scope{}, fmt.Errorf("external tool freshness check requires workspace root")
	}
	return sandbox.Scope{
		Principal:   p,
		GlobalRoot:  root,
		WorkingRoot: root,
	}, nil
}

func (r *Registry) externalToolDefinitions(manifests []ExternalToolManifest) []agent.ToolDef {
	if len(manifests) == 0 {
		return nil
	}
	defs := make([]agent.ToolDef, 0, len(manifests))
	for _, manifest := range manifests {
		manifest = NormalizeExternalToolManifest(manifest)
		defs = append(defs, agent.ToolDef{
			Name:        manifest.Name,
			Description: fmt.Sprintf("External tool owned by %s.", firstNonEmpty(manifest.Owner, "unknown owner")),
			Parameters:  manifest.IO.InputSchema,
		})
	}
	return defs
}

func (r *Registry) Definitions() []agent.ToolDef {
	defs := []agent.ToolDef{
		{
			Name:        "exec",
			Description: "Run a shell command in the configured workspace. Use this for git, file inspection, builds, tests, and repository edits. Repository-history changes such as git commit require explicit proposal approval.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to run with bash -lc"},
					"workdir": {"type": "string", "description": "Optional subdirectory within the workspace"},
					"timeout_sec": {"type": "integer", "minimum": 1, "description": "Optional per-command timeout in seconds"}
				},
				"required": ["command"]
			}`),
		},
	}
	defs = append(defs, nativeFileToolDefinitions()...)
	defs = append(defs, []agent.ToolDef{
		{
			Name:        "memory",
			Description: "Write curated memory for the current principal. Use this for compact durable notes, knowledge, decisions, questions, or rhizome associations.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
						"action": {"type": "string", "enum": ["add", "replace", "remove", "proposal_list", "proposal_show", "proposal_approve", "proposal_reject"], "description": "Memory write or proposal operation"},
						"scope": {"type": "string", "enum": ["shared", "principal"], "description": "Shared memory for admin, or principal-local memory for isolated users"},
						"store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome", "dreams"], "description": "Curated memory store to edit"},
						"content": {"type": "string", "description": "Content to add or replacement content"},
						"match": {"type": "string", "description": "Exact existing text to replace or remove"},
						"source_tag": {"type": "string", "enum": ["direct", "observed", "inferred", "hypothesized", "shared"], "description": "Optional provenance tag for added or replaced entries"},
						"confidence": {"type": "number", "minimum": 0, "maximum": 1, "description": "Optional confidence for added, replaced, or approved entries"},
						"proposal_id": {"type": "string", "description": "Memory proposal id for proposal_show/proposal_approve/proposal_reject"},
						"status": {"type": "string", "enum": ["proposed", "approved", "rejected"], "description": "Proposal status filter for proposal_list"},
						"limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum proposal list items"}
					},
					"required": ["action"]
				}`),
		},
		{
			Name:        "session_search",
			Description: "Search prior transcript messages explicitly. Use this to recall earlier conversations without silently flattening history into memory.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search text"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum number of hits"},
					"scope": {"type": "string", "enum": ["session", "all"], "description": "Search only the current session or all visible sessions"}
				},
				"required": ["query"]
			}`),
		},
	}...)
	if def, ok := r.codexImageGenerationToolDefinition(); ok {
		defs = append(defs, def)
	}
	if r.semantic != nil && r.semantic.Enabled() {
		defs = append(defs, agent.ToolDef{
			Name:        "semantic_search",
			Description: "Search curated memory semantically. Use this for related prior knowledge, decisions, or notes without ambient prompt injection.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Semantic search query"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum number of hits"},
					"scope": {"type": "string", "enum": ["shared", "principal"], "description": "Shared curated memory for admin, or principal-local memory for isolated users"}
				},
				"required": ["query"]
			}`),
		})
	}
	if r.fileStore != nil {
		defs = append(defs, agent.ToolDef{
			Name:        "openai_file",
			Description: "Use OpenAI file storage for durable external file objects. Admin only. Do not use this for Telegram/user-visible attachments; for those, generate a local file and attach it in the reply with the normal MEDIA path contract.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["put", "list", "get_metadata", "delete"], "description": "OpenAI files operation"},
					"path": {"type": "string", "description": "Local file path to upload when action=put"},
					"file_id": {"type": "string", "description": "Existing OpenAI file id for get_metadata or delete"},
					"purpose": {"type": "string", "description": "Optional purpose override for put/list; defaults to openai.files.purpose"}
				},
				"required": ["action"]
			}`),
		})
	}
	if r.retrievalStore != nil {
		defs = append(defs, agent.ToolDef{
			Name:        "openai_vector_store",
			Description: "Create, attach, and search OpenAI vector stores for auxiliary retrieval. Admin only.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["create", "attach", "search"], "description": "OpenAI vector store operation"},
					"store_id": {"type": "string", "description": "Vector store id. Optional when openai.vector_stores.default_store is configured"},
					"name": {"type": "string", "description": "Store name when action=create"},
					"file_id": {"type": "string", "description": "OpenAI file id when action=attach"},
					"query": {"type": "string", "description": "Search query when action=search"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Maximum hits when action=search"}
				},
				"required": ["action"]
			}`),
		})
	}
	if r.store != nil {
		defs = append(defs, agent.ToolDef{
			Name:        "update_operation",
			Description: "Persist or inspect the current operational state for this session. Use this to track the objective, stage, proposal, durable phase plan, findings, and artifacts as work evolves across turns.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Optional stable operation id"},
					"objective": {"type": "string", "description": "Current operation objective"},
					"status": {"type": "string", "enum": ["idle", "active", "blocked", "completed", "failed"], "description": "Current operation status"},
					"stage": {"type": "string", "description": "Current operational stage such as intake, assessment, proposal, execution, synthesis, or delivery"},
					"summary": {"type": "string", "description": "Short current-state summary"},
					"merge": {"type": "boolean", "description": "When true, merge the provided fields into the existing operation state instead of replacing it wholesale"},
					"proposal": {
						"type": "object",
						"description": "Optional current or most recent proposal gate",
						"properties": {
							"id": {"type": "string", "description": "Optional stable proposal id"},
							"kind": {"type": "string", "description": "Proposal kind such as capability_acquisition or destructive_mutation"},
							"summary": {"type": "string", "description": "Short proposal summary"},
							"why_now": {"type": "string", "description": "Why this proposal is needed now"},
							"bounded_effect": {"type": "string", "description": "What will happen if approved"},
							"status": {"type": "string", "enum": ["pending", "approved", "denied", "expired", "superseded"], "description": "Current proposal status"}
						}
					},
					"phase_plan": {
						"type": "object",
						"description": "Optional durable multi-phase operation plan. Use this when a broad goal must survive across approval leases; each pending phase can be materialized as its own bounded approval.",
						"properties": {
							"id": {"type": "string", "description": "Optional stable phase plan id"},
							"goal": {"type": "string", "description": "Broad end-to-end goal this phase plan serves"},
							"current_phase_id": {"type": "string", "description": "Current or next phase id; defaults to the first in-progress or pending phase"},
							"phases": {
								"type": "array",
								"description": "Durable phases. Omit during merge to keep existing phases; include one or more phases to update by id or append.",
								"items": {
									"type": "object",
									"properties": {
										"id": {"type": "string", "description": "Stable phase id"},
										"summary": {"type": "string", "description": "Bounded phase summary"},
										"status": {"type": "string", "enum": ["pending", "in_progress", "completed"], "description": "Current phase status"},
										"authority_class": {"type": "string", "description": "Authority/risk class such as read_only_review, status_check, workspace_write, commit, deploy, or system_change"},
										"why_now": {"type": "string", "description": "Why this phase should be offered next"},
										"bounded_effect": {"type": "string", "description": "What the phase approval permits"},
										"allowed_actions": {"type": "array", "items": {"type": "string"}, "description": "Allowed action labels for this phase"},
										"forbidden_actions": {"type": "array", "items": {"type": "string"}, "description": "Forbidden action labels for this phase"},
										"validation_plan": {"type": "array", "items": {"type": "string"}, "description": "Evidence checks expected after this phase"},
										"gate_level": {"type": "string", "enum": ["normal_approval", "escalated_operator_approval", "hard_consent_block"], "description": "Typed approval gate. Use escalated_operator_approval for bounded sensitive operator approvals such as external-account auth status checks; use hard_consent_block only for third-party opt-in/private-content gates that operator auto-approval must not bypass."},
										"gate_reason_code": {"type": "string", "description": "Typed gate reason such as external_account_auth_status, credential_metadata_check, credential_recovery, mailbox_content, third_party_opt_in, or capability_grant."},
										"approval_subject": {"type": "string", "description": "Who can satisfy this gate: operator, third_party, or resource_owner."},
										"autoapprove_eligible": {"type": "boolean", "description": "Whether operator auto-approval may consume this phase. Sensitive escalated gates should set false."},
										"blocked_reason_code": {"type": "string", "description": "Typed blocker code such as waiting_for_opt_in, waiting_for_consent, blocked_on_consent, external_dependency, or stale_authority. Prefer this over prose-only blockers."},
										"requires_consent": {"type": "boolean", "description": "True when the phase must wait for explicit consent before approval materialization."},
										"requires_opt_in": {"type": "boolean", "description": "True when the phase must wait for explicit opt-in before approval materialization."},
										"supersedes_phase_ids": {"type": "array", "items": {"type": "string"}, "description": "Phase ids this phase replaces or supersedes."},
										"stale_authority": {"type": "boolean", "description": "True when this phase is stale/superseded and must not be offered or executed."},
										"requires_approval": {"type": "boolean", "description": "Whether this phase requires a button-backed approval lease; defaults to true for active non-completed phases"}
									},
									"required": ["summary"]
								}
							}
						}
					},
					"findings": {
						"type": "array",
						"description": "Optional bounded findings to replace or append, depending on merge",
						"items": {
							"type": "object",
							"properties": {
								"claim": {"type": "string", "description": "Bounded claim"},
								"confidence": {"type": "string", "enum": ["low", "medium", "high"], "description": "Confidence level"},
								"basis": {"type": "string", "description": "Short provenance or basis statement"}
							},
							"required": ["claim"]
						}
					},
					"artifacts": {
						"type": "array",
						"description": "Optional artifact references to replace or append, depending on merge",
						"items": {
							"type": "object",
							"properties": {
								"label": {"type": "string", "description": "Human-readable label"},
								"ref": {"type": "string", "description": "Path, id, or other stable reference"}
							},
							"required": ["ref"]
						}
					}
				}
			}`),
		})
		defs = append(defs, agent.ToolDef{
			Name:        "operation_artifact",
			Description: "Inspect operation artifacts and resolve a safe local artifact into a MEDIA directive for user-visible attachment. Use this only when the user explicitly asks to receive an existing operation artifact; it never sends by itself.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["list", "resolve_sendable"], "description": "List known artifacts or resolve one artifact into a final-reply MEDIA directive"},
					"ref": {"type": "string", "description": "Exact artifact ref to resolve"},
					"label": {"type": "string", "description": "Artifact label or label fragment to resolve"},
					"latest": {"type": "boolean", "description": "Resolve the latest sendable artifact when no ref or label is given"},
					"type": {"type": "string", "enum": ["any", "pdf"], "description": "Optional artifact type filter"}
				},
				"required": ["action"]
			}`),
		})
		defs = append(defs, agent.ToolDef{
			Name:        "update_plan",
			Description: "Persist or inspect the current execution plan for this session. Use this for genuinely multi-step work, keep statuses current, and keep at most one step in progress.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"explanation": {"type": "string", "description": "Optional short explanation for the current plan"},
					"merge": {"type": "boolean", "description": "When true, merge the provided steps into the existing plan instead of replacing it wholesale"},
					"plan": {
						"type": "array",
						"description": "Optional plan update. Omit with no explanation to inspect the current plan state.",
						"items": {
							"type": "object",
							"properties": {
								"step": {"type": "string", "description": "Concrete plan step"},
								"status": {"type": "string", "enum": ["pending", "in_progress", "completed"], "description": "Current step status"}
							},
							"required": ["step", "status"]
						}
					}
				}
			}`),
		})
		defs = append(defs, missionLedgerToolDefinition())
		defs = append(defs, agent.ToolDef{
			Name:        "capability_request",
			Description: "Request a governed capability or delegation. Covers tools, local devices, external accounts, purchases, public web, communication, file/network access, and emergent permissions under one reviewable contract.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["request_submit", "request_show", "request_list"], "description": "Capability request operation"},
					"request_id": {"type": "string", "description": "Request id for request_show or optional submit id"},
					"kind": {"type": "string", "enum": ["tool", "local_device", "external_account", "purchase", "public_web", "communication", "file_access", "network_access", "generic_delegation", "system_change"], "description": "Capability class"},
					"target_resource": {"type": "string", "description": "Tool name, device/app, account, vendor, web surface, path, network target, or emergent resource"},
					"requested_for": {"type": "string", "description": "Optional target principal; defaults to the requester"},
					"parent_principal": {"type": "string", "description": "Optional parent/guardian principal that may endorse before admin approval"},
					"admin_principal": {"type": "string", "description": "Optional admin principal expected to make the final approval"},
					"purpose": {"type": "string", "description": "Why this capability is needed and what bounded work it enables"},
						"risk_class": {"type": "string", "description": "Operator-facing risk label such as low, medium, high, sensitive, spend, or public"},
						"contract": {"type": "object", "description": "Proposed behavior contract, escalation rules, attribution, or success criteria"},
						"constraints": {"type": "object", "description": "Proposed boundaries such as max spend, paths, domains, accounts, retention, model/message limits, or review cadence"},
						"capability_update_plan": {"type": "object", "description": "Optional concrete update plan to embed in the reviewable contract. For durable children this can include agent_id, policy_patch, policy_overrides, provisioning, attestation, grant_actions, reason, and notes."},
						"review_target_chat_id": {"type": "integer", "description": "Optional Telegram chat id to queue a pending review event for this request"},
						"review_summary": {"type": "string", "description": "Optional concise summary for the queued review event"},
						"limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "Optional list limit"}
				},
				"required": ["action"]
			}`),
		})
		defs = append(defs, agent.ToolDef{
			Name:        "capability_authority",
			Description: "Review and grant governed capability/delegation requests. Parent principals may endorse/reject matching requests; admin principals approve, grant, revoke, and inspect all.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["request_show", "request_list", "request_review", "grant_set", "grant_show", "grant_list", "grant_revoke", "access_check"], "description": "Capability authority operation"},
					"request_id": {"type": "string", "description": "Request id for review or grant creation"},
					"grant_id": {"type": "string", "description": "Grant id for grant_show/grant_revoke or optional grant_set id"},
					"kind": {"type": "string", "enum": ["tool", "local_device", "external_account", "purchase", "public_web", "communication", "file_access", "network_access", "generic_delegation", "system_change"], "description": "Capability class"},
					"target_resource": {"type": "string", "description": "Capability target for grants or access checks"},
					"capability_action": {"type": "string", "description": "Action being granted or checked; use invoke for tool runtime access"},
					"principal": {"type": "string", "description": "Principal receiving a grant or being checked"},
					"allowed_actions": {"type": "array", "items": {"type": "string"}, "description": "Allowed actions for grant_set; supports invoke and *"},
					"review_status": {"type": "string", "enum": ["parent_approved", "approved", "rejected"], "description": "Review status for request_review"},
						"grant_status": {"type": "string", "enum": ["pending", "active", "stale", "revoked", "expired", "failed"], "description": "Grant status for grant_set/list filtering"},
						"contract": {"type": "object", "description": "Grant contract override; defaults from request"},
						"constraints": {"type": "object", "description": "Grant constraints override; defaults from request"},
						"capability_update_plan": {"type": "object", "description": "Optional contract-embedded update plan override for grant_set. Active durable-agent policy patches are applied before the grant becomes active."},
						"rationale": {"type": "string", "description": "Review, grant, or revocation rationale"},
						"expires_in_seconds": {"type": "integer", "minimum": 1, "description": "Optional relative expiration for grant_set"},
						"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Optional list limit"}
				},
				"required": ["action"]
			}`),
		})
		defs = append(defs, agent.ToolDef{
			Name:        "tool_authority",
			Description: "Manage tool lifecycle records for governor-controlled install, audit, verification, and registration. Admin only. Use capability_request/capability_authority for proposals and access grants.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["register", "registered_show", "registered_list", "install_set", "install_show", "install_list", "install_execute", "audit_run", "audit_show", "audit_list", "probe_run", "probe_show", "probe_list", "access_check"], "description": "Tool-authority operation"},
					"tool_name": {"type": "string", "description": "Tool name for register/install/audit/probe/access actions"},
					"implementation_ref": {"type": "string", "description": "Implementation reference for register"},
					"registered": {"type": "boolean", "description": "Optional explicit registered flag for register; defaults to true"},
					"principal": {"type": "string", "description": "Principal id for access checks"},
					"status": {"type": "string", "enum": ["pending", "installed", "verified", "failed", "stale"], "description": "Install/probe lifecycle status for install_set or install_list filtering"},
					"installer": {"type": "string", "description": "Who installed or provisioned the external tool"},
					"install_ref": {"type": "string", "description": "Reference to the install artifact, path, image, or package set"},
					"limit": {"type": "integer", "minimum": 1, "maximum": 200, "description": "Optional list limit"}
				},
				"required": ["action"]
			}`),
		})
		defs = append(defs, agent.ToolDef{
			Name:        "durable_agent",
			Description: "Inspect and ratify durable-agent governance from conversation. Admin only. For policy_apply, prefer policy_patch (conversational policy intent) and use policy_overrides only when a low-level override is explicitly needed. For ordinary behavior/privacy/shared-context changes, use policy_apply directly; enrollment actions are only for remote control-plane lifecycle.",
			Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
					"action": {"type": "string", "enum": ["list", "create", "create_from_archetype", "activate", "connection_test", "policy_show", "bootstrap_show", "policy_apply", "bootstrap_update", "enrollment_show", "enrollment_update", "wizard_start", "wizard_answer", "wizard_show", "wizard_finalize", "wizard_cancel", "archetype_list", "archetype_show", "access_show", "access_grant", "access_revoke", "conversation_show", "conversation_send", "delegation_request", "delegation_report", "memory_review", "memory_delegate", "profile_show", "profile_apply", "artifact_put", "artifact_list", "artifact_show", "snapshot_create", "snapshot_list", "snapshot_restore"], "description": "Durable-agent governance operation"},
					"agent_id": {"type": "string", "description": "Durable agent id for show/update actions"},
						"archetype": {"type": "string", "description": "Repo archetype name for archetype_show or create_from_archetype"},
						"channel_kind": {"type": "string", "description": "Required for create. Example: external_channel or telegram_group"},
						"review_event_id": {"type": "integer", "minimum": 1, "description": "Optional source review event id for policy ratification provenance"},
						"review_target_chat_id": {"type": "integer", "description": "Optional admin review target chat id override for create"},
						"reason": {"type": "string", "description": "Optional operator reason for the change"},
					"bootstrap_profile": {"type": "string", "enum": ["inherit_parent"], "description": "For bootstrap_update: replace the child bootstrap with the parent-default inherited bootstrap."},
					"bootstrap_llm": {"type": "object", "description": "For bootstrap_update: explicit replacement child bootstrap record.", "properties": {"backend": {"type": "string", "enum": ["native", "codex"]}, "native_provider": {"type": "string"}, "api_key": {"type": "string"}, "base_url": {"type": "string"}, "model": {"type": "string"}, "max_tokens": {"type": "integer", "minimum": 0}, "codex_auth_source": {"type": "string"}, "codex_home": {"type": "string"}, "codex_base_url": {"type": "string"}}},
						"policy_patch": {
							"type": "object",
							"description": "Optional conversational policy patch for policy_apply/create. Prefer this surface.",
							"properties": {
								"charter": {"type": "string", "description": "Optional charter text"},
								"autonomy": {"type": "string", "description": "High-level autonomy posture: observe_only, local_drafts, review_before_reply, or reply_within_charter"},
								"visibility": {"type": "string", "description": "Visibility posture: private, parent_relay_only, or public_channel"},
								"shared_context": {"type": "string", "description": "Inference-sharing posture: isolated or public_only"},
								"capabilities": {"type": "array", "items": {"type": "string"}, "description": "Optional capability envelope"},
								"drift_policy": {"type": "string", "description": "Optional drift policy"}
							}
						},
						"policy_overrides": {
							"type": "object",
							"description": "Optional low-level overrides for policy_apply/create when direct policy axes must be set explicitly.",
							"properties": {
								"outbound_mode": {"type": "string", "description": "Low-level outbound mode override"},
								"public_surface_mode": {"type": "string", "description": "Low-level public surface mode override"},
								"shared_inference_reuse": {"type": "string", "description": "Low-level shared inference reuse override"},
								"shared_inference_reuse_scope": {"type": "string", "description": "Low-level shared inference reuse scope override"},
								"tailnet_mode": {"type": "string", "description": "Declare a child tailnet identity without starting a live node. Supported: tsnet, tagged_node, disabled"},
								"tailnet_hostname": {"type": "string", "description": "Declared MagicDNS hostname for the child tailnet identity"},
								"tailnet_tags": {"type": "array", "items": {"type": "string"}, "description": "Declared Tailscale tags for the child identity"},
								"tailnet_surface_policy": {"type": "string", "description": "Declared private tailnet surface policy. Supported: private_status, private_services, none"}
							}
						},
						"charter": {"type": "string", "description": "Legacy top-level charter override (prefer policy_patch.charter)"},
						"autonomy": {"type": "string", "description": "Legacy top-level autonomy (prefer policy_patch.autonomy)"},
						"visibility": {"type": "string", "description": "Legacy top-level visibility (prefer policy_patch.visibility)"},
						"shared_context": {"type": "string", "description": "Legacy top-level shared_context (prefer policy_patch.shared_context)"},
						"capabilities": {"type": "array", "items": {"type": "string"}, "description": "Legacy top-level capabilities (prefer policy_patch.capabilities)"},
						"drift_policy": {"type": "string", "description": "Legacy top-level drift policy (prefer policy_patch.drift_policy)"},
						"outbound_mode": {"type": "string", "description": "Legacy low-level override (prefer policy_overrides.outbound_mode)"},
						"public_surface_mode": {"type": "string", "description": "Legacy low-level override (prefer policy_overrides.public_surface_mode)"},
						"shared_inference_reuse": {"type": "string", "description": "Legacy low-level override (prefer policy_overrides.shared_inference_reuse)"},
						"shared_inference_reuse_scope": {"type": "string", "description": "Legacy low-level override (prefer policy_overrides.shared_inference_reuse_scope)"},
						"wakeup_mode": {"type": "string", "description": "Optional wakeup mode for create. Example: poll"},
						"network_policy": {"type": "string", "description": "Optional network policy for create"},
						"secret_scopes": {"type": "array", "items": {"type": "string"}, "description": "Optional secret scopes for create"},
						"channel_config": {"type": "object", "description": "Optional structured channel configuration for create. Adapter-specific details belong in child-owned runtime agreements."},
						"wizard_answers": {
							"type": "object",
							"description": "Wizard answer patch for wizard_answer (generic durable child setup).",
							"properties": {
								"address": {"type": "string"},
								"account": {"type": "string"},
								"adapter": {"type": "string"},
								"query": {"type": "string"},
								"bootstrap_profile": {"type": "string", "enum": ["inherit_parent", "child_custom"], "description": "How bootstrap LLM settings are sourced: inherited from the parent defaults or explicitly customized for this child."},
								"bootstrap_model": {"type": "string", "description": "Optional child model pin when bootstrap_profile=child_custom; keeps provider credentials from inherited/current bootstrap."},
								"charter": {"type": "string"},
								"autonomy": {"type": "string"},
								"wakeup_mode": {"type": "string"},
								"poll_interval": {"type": "string"},
								"surface_rules": {"type": "array", "items": {"type": "string"}},
								"summarize_pdfs": {"type": "boolean"},
								"synthesis_cadence": {"type": "string"},
								"capabilities": {"type": "array", "items": {"type": "string"}},
								"never_retain": {"type": "array", "items": {"type": "string"}},
								"drift_policy": {"type": "string"}
							}
						},
						"memory_delegation": {
							"type": "object",
							"description": "Memory delegation review/apply payload for memory_review and memory_delegate actions.",
							"properties": {
								"limit": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Candidate limit for memory_review"},
								"candidate_ids": {"type": "array", "items": {"type": "string"}, "description": "Candidate ids selected from memory_review output"},
								"target_store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome"], "description": "Optional default child memory store for delegated items"},
								"reason": {"type": "string", "description": "Why this delegation is being requested"},
								"entries": {
									"type": "array",
									"description": "Optional explicit memory entries for delegation",
									"items": {
										"type": "object",
										"properties": {
											"candidate_id": {"type": "string", "description": "Optional candidate id from memory_review output"},
											"source_store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome"]},
											"target_store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome"]},
											"content": {"type": "string"}
										}
									}
								}
							}
						},
						"snapshot": {
							"type": "object",
							"description": "Durable child snapshot payload for snapshot_create/list/restore actions.",
							"properties": {
								"snapshot_id": {"type": "string", "description": "Snapshot id for snapshot_restore"},
								"reason": {"type": "string", "description": "Snapshot creation or restore rationale"},
								"limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "Snapshot list limit"}
							}
						},
						"profile_edit": {
							"type": "object",
							"description": "Admin-approved child-authored profile edit for profile_apply.",
							"properties": {
								"target_file": {"type": "string", "enum": ["persona.md", "skills.md", "notes.md"]},
								"content": {"type": "string"},
								"reason": {"type": "string"}
							}
						},
						"artifact": {
							"type": "object",
							"description": "Child-specific artifact payload for artifact_put, artifact_list, and artifact_show. Artifacts are stored under the child memory root, not the parent runtime repository.",
							"properties": {
								"path": {"type": "string", "description": "Relative artifact path under artifacts/. Rejects absolute paths and .. traversal."},
								"content": {"type": "string", "description": "Artifact content for artifact_put"},
								"kind": {"type": "string", "description": "Optional kind such as schema, runtime_plan, status_contract, or note"},
								"reason": {"type": "string", "description": "Why this child-specific artifact is being stored"}
							}
						},
						"delegation_request": {
							"type": "object",
							"description": "Generic governed delegation request for durable agents. Creates a canonical capability_request and queues a durable review artifact for operator review.",
							"properties": {
								"request_id": {"type": "string", "description": "Optional idempotency key; generated when omitted"},
								"kind": {"type": "string", "enum": ["tool", "local_device", "external_account", "purchase", "public_web", "communication", "file_access", "network_access", "generic_delegation", "system_change"], "description": "Capability kind; defaults to generic_delegation"},
								"target_resource": {"type": "string", "description": "Resource, account, device, surface, purchase domain, or other permission target"},
								"requested_by": {"type": "string", "description": "Optional requesting principal; defaults to the durable agent id"},
								"requested_for": {"type": "string", "description": "Optional principal receiving the grant; defaults to the durable agent id"},
								"parent_principal": {"type": "string", "description": "Optional parent approver principal such as telegram:123"},
								"admin_principal": {"type": "string", "description": "Optional admin approver principal; defaults to the current admin principal"},
								"purpose": {"type": "string", "description": "Why the capability is needed"},
									"risk_class": {"type": "string", "description": "Operator-visible risk class such as spend, secrets, local_device, public_surface, or account_access"},
									"contract": {"type": "object", "description": "Reviewable behavioral contract for the requested permission"},
									"constraints": {"type": "object", "description": "Reviewable constraints, ceilings, budgets, time bounds, or allowed actions"},
									"capability_update_plan": {"type": "object", "description": "Optional explicit update plan embedded into the capability request contract"},
									"policy_patch": {"type": "object", "description": "Optional durable-agent policy patch to apply after approval and active grant", "properties": {"charter": {"type": "string"}, "autonomy": {"type": "string"}, "visibility": {"type": "string"}, "shared_context": {"type": "string"}, "capabilities": {"type": "array", "items": {"type": "string"}}, "drift_policy": {"type": "string"}}},
									"policy_overrides": {"type": "object", "description": "Optional low-level durable-agent policy overrides to apply after approval and active grant", "properties": {"outbound_mode": {"type": "string"}, "public_surface_mode": {"type": "string"}, "shared_inference_reuse": {"type": "string"}, "shared_inference_reuse_scope": {"type": "string"}, "tailnet_mode": {"type": "string"}, "tailnet_hostname": {"type": "string"}, "tailnet_tags": {"type": "array", "items": {"type": "string"}}, "tailnet_surface_policy": {"type": "string"}}},
									"provisioning": {"type": "array", "items": {"type": "string"}, "description": "Provisioning steps the operator should perform or verify before grant"},
									"attestation": {"type": "array", "items": {"type": "string"}, "description": "Evidence required before grant"},
									"grant_actions": {"type": "array", "items": {"type": "string"}, "description": "Suggested allowed actions for the resulting capability grant"},
									"update_reason": {"type": "string", "description": "Reason recorded if a durable-agent policy update is applied from this request"},
									"summary": {"type": "string", "description": "Optional review artifact summary"},
									"local_actions": {"type": "array", "items": {"type": "string"}, "description": "Actions already taken locally before escalation"},
									"questions": {"type": "array", "items": {"type": "string"}, "description": "Questions for parent/admin review"},
								"risk_flags": {"type": "array", "items": {"type": "string"}, "description": "Operator-visible risks"},
								"artifact_refs": {"type": "array", "items": {"type": "string"}, "description": "References to supporting artifacts"},
								"metadata": {"type": "object", "additionalProperties": {"type": "string"}, "description": "String metadata copied into the review artifact"},
								"review_target_chat_id": {"type": "integer", "description": "Optional admin review target chat override"}
							}
						},
						"delegation_report": {
							"type": "object",
							"description": "Generic durable-agent report for delegation progress, outcomes, or risks. Queues a durable review artifact without creating a new request.",
							"properties": {
								"request_id": {"type": "string", "description": "Optional capability request id this report concerns"},
								"grant_id": {"type": "string", "description": "Optional capability grant id this report concerns"},
								"status": {"type": "string", "description": "Report status such as pending, blocked, completed, failed, or needs_review"},
								"outcome": {"type": "string", "description": "Short outcome description"},
								"summary": {"type": "string", "description": "Review artifact summary"},
								"local_actions": {"type": "array", "items": {"type": "string"}, "description": "Actions taken locally"},
								"questions": {"type": "array", "items": {"type": "string"}, "description": "Questions for parent/admin review"},
								"risk_flags": {"type": "array", "items": {"type": "string"}, "description": "Operator-visible risks"},
								"artifact_refs": {"type": "array", "items": {"type": "string"}, "description": "References to supporting artifacts"},
								"metadata": {"type": "object", "additionalProperties": {"type": "string"}, "description": "String metadata copied into the review artifact"},
								"review_target_chat_id": {"type": "integer", "description": "Optional admin review target chat override"}
							}
						},
					"operation": {"type": "string", "enum": ["revoke", "reactivate", "decommission", "rotate_secret"], "description": "Enrollment lifecycle operation for enrollment_update"},
					"secret": {"type": "string", "description": "Replacement control-plane secret for enrollment_update when operation=rotate_secret"},
					"message": {"type": "string", "description": "Parent message text for conversation_send"},
					"history": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Recent update entries to show for policy_show or bootstrap_show"},
					"telegram_user_id": {"type": "integer", "minimum": 1, "description": "Single Telegram user id for access_grant or access_revoke"},
					"telegram_user_ids": {"type": "array", "items": {"type": "integer", "minimum": 1}, "description": "Telegram user ids for access_grant or access_revoke"}
				},
				"required": ["action"]
			}`),
		})
	}
	return defs
}

func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	return r.executeWithRoot(ctx, name, input, r.workspace)
}

func (r *Registry) ExecuteForPrincipal(ctx context.Context, p principal.Principal, name string, input json.RawMessage) (string, error) {
	return r.ExecuteForSessionPrincipal(ctx, p, session.SessionKey{}, name, input)
}

func (r *Registry) ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error) {
	if r.sandbox == nil {
		return "", fmt.Errorf("principal-aware execution requires sandbox resolver")
	}

	scope, err := r.scopeForPrincipalToolExecution(p)
	if err != nil {
		return "", err
	}
	if err := ensureScopeReady(scope); err != nil {
		return "", err
	}
	if r.runner == nil {
		if !(r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && name != "exec") {
			return "", fmt.Errorf("principal-aware execution requires sandbox runner")
		}
	} else if !r.runner.Supports(scope) {
		if !(r.durableAgentPrincipalFallback && p.Role == principal.RoleDurableAgent && name != "exec") {
			return "", fmt.Errorf("no supported sandbox backend for principal role %q", p.Role)
		}
	}
	return r.executeWithScopeAndPrincipal(ctx, name, input, scope, p, key)
}

func (r *Registry) executeWithRoot(ctx context.Context, name string, input json.RawMessage, root string) (string, error) {
	return r.executeWithScopeAndPrincipal(ctx, name, input, sandbox.Scope{
		WorkingRoot:      root,
		SharedMemoryRoot: root,
	}, principal.Principal{}, session.SessionKey{})
}

func (r *Registry) executeWithScopeAndPrincipal(ctx context.Context, name string, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	authorityGrant, authorityManaged, err := r.requireAuthorityToolAccess(name, p, input)
	if err != nil {
		return "", err
	}

	switch name {
	case "exec":
		return r.exec(ctx, input, scope, p, key)
	case "read_file":
		return r.readFile(ctx, input, scope)
	case "write_file":
		return r.writeFile(ctx, input, scope)
	case "list_dir":
		return r.listDir(ctx, input, scope)
	case "search":
		return r.searchFiles(ctx, input, scope)
	case "fetch_url":
		return r.fetchURL(ctx, input, scope, p)
	case "memory":
		return r.memory(ctx, input, scope)
	case "session_search":
		return r.sessionSearch(ctx, input, p, key)
	case "update_operation":
		return r.updateOperation(ctx, input, key)
	case "operation_artifact":
		return r.operationArtifact(ctx, input, scope, key)
	case "update_plan":
		return r.updatePlan(ctx, input, key)
	case missionLedgerToolName:
		return r.missionLedger(ctx, input, p, key)
	case "tool_authority":
		return r.toolAuthority(ctx, input, p, key, scope)
	case "capability_request":
		return r.capabilityRequest(ctx, input, p, key)
	case "capability_authority":
		return r.capabilityAuthority(ctx, input, p, key)
	case "semantic_search":
		return r.semanticSearch(ctx, input, scope)
	case "openai_file":
		return r.openAIFile(ctx, input, scope, p)
	case "openai_vector_store":
		return r.openAIVectorStore(ctx, input, p)
	case "durable_agent":
		return r.durableAgent(ctx, input, p, key, scope)
	case codexImageGenerationToolName:
		return r.codexImageGeneration(ctx, input, scope, p)
	default:
		if manifest, ok := r.externalManifestByName(name); ok {
			if r.externalExecutor != nil && r.externalExecutor.Supports(manifest) {
				if err := r.ensureExternalToolFresh(manifest, scope); err != nil {
					return "", err
				}
				access := ExternalToolExecutionAccess{}
				if authorityManaged {
					var err error
					access, err = externalToolExecutionAccessFromGrant(p, authorityGrant)
					if err != nil {
						return "", err
					}
				}
				return r.externalExecutor.Execute(ctx, manifest, input, scope, r.runner, r.maxOutputBytes, access)
			}
			if err := validateExternalProcessPolicy(manifest); err != nil {
				return "", err
			}
			return "", fmt.Errorf("external tool %q is present in the manifest but not yet executable in core (owner=%s)", manifest.Name, manifest.Owner)
		}
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

func (r *Registry) exec(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal, key session.SessionKey) (string, error) {
	var in execInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode exec input: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return "", fmt.Errorf("exec command is required")
	}

	workdir, escaped, err := resolveWorkdirForExec(scope.WorkingRoot, in.Workdir)
	if err != nil {
		return "", err
	}
	if escaped {
		if p.Role != principal.RoleAdmin {
			return "", fmt.Errorf("workdir %q escapes workspace %q", in.Workdir, filepath.Clean(scope.WorkingRoot))
		}
		proposal := session.OperationProposal{
			Kind:          "workspace_escape",
			Summary:       "Run command outside the configured workspace",
			WhyNow:        "The requested command needs an explicit admin-approved working directory outside the current sandbox root.",
			BoundedEffect: fmt.Sprintf("The command will run in %s for this execution only.", workdir),
		}
		if r.execApprover == nil {
			return "", fmt.Errorf("command requires an approved proposal: workspace escape")
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusPending); err != nil {
			return "", err
		}
		decision, err := r.execApprover.ConfirmExec(ctx, ExecApprovalRequest{
			Principal:  p,
			SessionKey: key,
			Scope:      scope,
			Command:    in.Command,
			Workdir:    workdir,
			Reason:     "workspace escape",
			Proposal:   proposal,
		})
		if err != nil {
			if persistErr := r.persistExecProposalState(key, proposal, session.ProposalStatusExpired); persistErr != nil {
				return "", persistErr
			}
			return "", err
		}
		if !decision.Approved {
			if err := r.persistExecProposalState(key, proposal, session.ProposalStatusDenied); err != nil {
				return "", err
			}
			return "", fmt.Errorf("proposal denied: workspace escape")
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
	}
	if proposal, reason := proposalForCommand(in.Command); reason != "" {
		if r.execApprover == nil {
			return "", fmt.Errorf("command requires an approved proposal: %s", reason)
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusPending); err != nil {
			return "", err
		}
		decision, err := r.execApprover.ConfirmExec(ctx, ExecApprovalRequest{
			Principal:  p,
			SessionKey: key,
			Scope:      scope,
			Command:    in.Command,
			Workdir:    workdir,
			Reason:     reason,
			Proposal:   proposal,
		})
		if err != nil {
			if persistErr := r.persistExecProposalState(key, proposal, session.ProposalStatusExpired); persistErr != nil {
				return "", persistErr
			}
			return "", err
		}
		if !decision.Approved {
			if err := r.persistExecProposalState(key, proposal, session.ProposalStatusDenied); err != nil {
				return "", err
			}
			return "", fmt.Errorf("proposal denied: %s", reason)
		}
		if err := r.persistExecProposalState(key, proposal, session.ProposalStatusApproved); err != nil {
			return "", err
		}
	}

	timeout := r.timeout
	if in.TimeoutSec > 0 {
		timeout = time.Duration(in.TimeoutSec) * time.Second
	}
	timeout = defaultTimeout(timeout)

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout, stderr, err := r.runCommand(runCtx, scope, in.Command, workdir)
	out := renderOutput(stdout, stderr, r.maxOutputBytes)
	if err == nil {
		return out, nil
	}

	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("command timed out after %s", timeout)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out, fmt.Errorf("command failed with exit code %d", exitErr.ExitCode())
	}

	return out, fmt.Errorf("run command: %w", err)
}

func (r *Registry) memory(_ context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	var in memoryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode memory input: %w", err)
	}

	root, effectiveScope, err := resolveMemoryRoot(scope, in.Scope)
	if err != nil {
		return "", err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "proposal_list":
		proposals, err := memstore.ListProposals(memstore.ProposalListOptions{Root: root, Status: in.Status, Limit: in.Limit})
		if err != nil {
			return "", err
		}
		return renderMemoryProposalList(effectiveScope, proposals), nil
	case "proposal_show":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_show requires proposal_id")
		}
		proposal, err := memstore.LoadProposal(root, in.ProposalID)
		if err != nil {
			return "", err
		}
		return renderMemoryProposal(*proposal), nil
	case "proposal_approve":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_approve requires proposal_id")
		}
		result, err := memstore.ApproveProposal(root, in.ProposalID, in.SourceTag, in.Confidence)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("memory_proposal_approved scope=%s proposal_id=%s store=%s path=%s", effectiveScope, strings.TrimSpace(in.ProposalID), result.Store, result.Path), nil
	case "proposal_reject":
		if strings.TrimSpace(in.ProposalID) == "" {
			return "", fmt.Errorf("memory proposal_reject requires proposal_id")
		}
		proposal, err := memstore.RejectProposal(root, in.ProposalID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("memory_proposal_rejected scope=%s proposal_id=%s store=%s", effectiveScope, proposal.ID, proposal.Store), nil
	case "add", "replace", "remove", "":
	default:
		return "", fmt.Errorf("unsupported memory action %q", in.Action)
	}

	result, err := memstore.ApplyWrite(memstore.WriteRequest{
		Root:       root,
		Store:      in.Store,
		Action:     action,
		Content:    in.Content,
		Match:      in.Match,
		SourceTag:  in.SourceTag,
		Scope:      effectiveScope,
		Confidence: in.Confidence,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("memory_%s_ok scope=%s store=%s path=%s", result.Action, effectiveScope, result.Store, result.Path), nil
}

func renderMemoryProposalList(scope string, proposals []memstore.MemoryProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "scope: %s\n", firstNonEmpty(strings.TrimSpace(scope), "-"))
	if len(proposals) == 0 {
		b.WriteString("proposals: none")
		return b.String()
	}
	b.WriteString("proposals:\n")
	for _, proposal := range proposals {
		fmt.Fprintf(&b, "- id=%s status=%s store=%s source=%s created_at=%s sha=%s\n",
			proposal.ID,
			firstNonEmpty(proposal.Status, "-"),
			firstNonEmpty(proposal.Store, "-"),
			firstNonEmpty(proposal.SourceKind, "-"),
			proposal.CreatedAt.UTC().Format(time.RFC3339),
			firstNonEmpty(proposal.ContentSHA256, "-"),
		)
	}
	return strings.TrimSpace(b.String())
}

func renderMemoryProposal(proposal memstore.MemoryProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proposal_id: %s\n", proposal.ID)
	fmt.Fprintf(&b, "status: %s\n", firstNonEmpty(proposal.Status, "-"))
	fmt.Fprintf(&b, "scope: %s\n", firstNonEmpty(proposal.Scope, "-"))
	fmt.Fprintf(&b, "store: %s\n", firstNonEmpty(proposal.Store, "-"))
	fmt.Fprintf(&b, "source_kind: %s\n", firstNonEmpty(proposal.SourceKind, "-"))
	fmt.Fprintf(&b, "source_ref: %s\n", firstNonEmpty(proposal.SourceRef, "-"))
	fmt.Fprintf(&b, "reason: %s\n", firstNonEmpty(proposal.Reason, "-"))
	fmt.Fprintf(&b, "content_sha256: %s\n", firstNonEmpty(proposal.ContentSHA256, "-"))
	b.WriteString("content:\n")
	b.WriteString(strings.TrimSpace(proposal.Content))
	return b.String()
}

func (r *Registry) runCommand(ctx context.Context, scope sandbox.Scope, command string, workdir string) (string, string, error) {
	if r.runner != nil && strings.TrimSpace(string(scope.Principal.Role)) != "" {
		res, err := r.runner.Run(ctx, sandbox.ExecRequest{
			Scope:   scope,
			Command: command,
			Workdir: workdir,
		})
		return res.Stdout, res.Stderr, err
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = workdir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func defaultTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 60 * time.Second
	}
	return timeout
}

func ensureScopeReady(scope sandbox.Scope) error {
	if err := os.MkdirAll(scope.WorkingRoot, 0o755); err != nil {
		return fmt.Errorf("prepare working root %q: %w", scope.WorkingRoot, err)
	}
	if strings.TrimSpace(scope.UserMemory) != "" {
		if err := os.MkdirAll(scope.UserMemory, 0o755); err != nil {
			return fmt.Errorf("prepare user memory root %q: %w", scope.UserMemory, err)
		}
	}
	return nil
}

func resolveWorkdir(root, raw string) (string, error) {
	workdir, _, err := resolveWorkdirForExec(root, raw)
	return workdir, err
}

func resolveWorkdirForExec(root, raw string) (string, bool, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", false, fmt.Errorf("resolve workspace root: %w", err)
	}

	target := base
	if strings.TrimSpace(raw) != "" {
		if filepath.IsAbs(raw) {
			target = filepath.Clean(raw)
		} else {
			target = filepath.Join(base, raw)
		}
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", false, fmt.Errorf("resolve workdir: %w", err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", false, fmt.Errorf("check workdir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return target, true, nil
	}

	return target, false, nil
}

func renderOutput(stdout, stderr string, limit int) string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout:\n"+truncate(stdout, limit))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr:\n"+truncate(stderr, limit))
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n\n")
}

func truncate(raw string, limit int) string {
	if len(raw) <= limit || limit <= 0 {
		return raw
	}
	if limit <= 64 {
		return raw[:limit]
	}
	head := limit / 2
	tail := limit / 2
	return raw[:head] + "\n...[truncated]...\n" + raw[len(raw)-tail:]
}

func resolveMemoryRoot(scope sandbox.Scope, requested string) (string, string, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		if scope.Principal.Role == principal.RoleApprovedUser && strings.TrimSpace(scope.UserMemory) != "" {
			requested = "principal"
		} else {
			requested = "shared"
		}
	}

	switch requested {
	case "shared":
		if scope.Principal.Role == principal.RoleApprovedUser {
			return "", "", fmt.Errorf("approved users may not write shared memory")
		}
		root := strings.TrimSpace(scope.SharedMemoryRoot)
		if root == "" {
			root = strings.TrimSpace(scope.WorkingRoot)
		}
		if root == "" {
			return "", "", fmt.Errorf("shared memory root is not configured")
		}
		return root, requested, nil
	case "principal":
		root := strings.TrimSpace(scope.UserMemory)
		if root == "" {
			if scope.Principal.Role == principal.RoleAdmin {
				sharedRoot := strings.TrimSpace(scope.SharedMemoryRoot)
				if sharedRoot == "" {
					sharedRoot = strings.TrimSpace(scope.WorkingRoot)
				}
				if sharedRoot == "" {
					return "", "", fmt.Errorf("shared memory root is not configured")
				}
				return sharedRoot, "shared", nil
			}
			return "", "", fmt.Errorf("principal memory root is not available for this principal")
		}
		return root, requested, nil
	default:
		return "", "", fmt.Errorf("memory scope must be shared or principal")
	}
}

func (r *Registry) sessionSearch(_ context.Context, input json.RawMessage, p principal.Principal, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("session search requires transcript store")
	}

	var in sessionSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode session_search input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("session_search query is required")
	}

	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	var filter *session.SessionKey
	switch {
	case p.Role == principal.RoleApprovedUser:
		filter = &key
		scope = "session"
	case scope == "", scope == "all":
		filter = nil
		scope = "all"
	case scope == "session":
		filter = &key
	default:
		return "", fmt.Errorf("session_search scope must be session or all")
	}

	hits, err := r.store.SearchMessages(in.Query, in.Limit, filter)
	if err != nil {
		return "", err
	}
	return renderSessionSearchResults(scope, in.Query, hits), nil
}

func (r *Registry) semanticSearch(ctx context.Context, input json.RawMessage, scope sandbox.Scope) (string, error) {
	if r.semantic == nil || !r.semantic.Enabled() {
		return "", fmt.Errorf("semantic search is not configured")
	}

	var in semanticSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode semantic_search input: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("semantic_search query is required")
	}

	root, effectiveScope, err := resolveMemoryRoot(scope, in.Scope)
	if err != nil {
		return "", err
	}

	principalID := ""
	if effectiveScope == "principal" && scope.Principal.TelegramUserID > 0 {
		principalID = strconv.FormatInt(scope.Principal.TelegramUserID, 10)
	}
	hits, err := r.semantic.Search(ctx, memstore.SemanticSearchRequest{
		Root:        root,
		Scope:       effectiveScope,
		PrincipalID: principalID,
		Query:       in.Query,
		Mode:        memstore.SemanticModeInteractive,
		Limit:       in.Limit,
		Now:         time.Now(),
	})
	if err != nil {
		return "", err
	}
	return renderSemanticSearchResults(effectiveScope, in.Query, hits), nil
}

func renderSessionSearchResults(scope string, query string, hits []session.SearchHit) string {
	var b strings.Builder
	b.WriteString("[SESSION_RECALL]\n")
	b.WriteString("scope: ")
	b.WriteString(scope)
	b.WriteString("\nquery: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/SESSION_RECALL]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. chat=%d turn=%d role=%s\n", i+1, hit.ChatID, hit.TurnIndex, hit.Role)
		b.WriteString("content: ")
		b.WriteString(truncate(strings.TrimSpace(hit.Content), 600))
		b.WriteString("\n")
	}
	b.WriteString("[/SESSION_RECALL]")
	return b.String()
}

func renderSemanticSearchResults(scope string, query string, hits []memstore.SemanticHit) string {
	var b strings.Builder
	b.WriteString("[SEMANTIC_RECALL]\n")
	b.WriteString("scope: ")
	b.WriteString(scope)
	b.WriteString("\nquery: ")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n")
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/SEMANTIC_RECALL]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. source=%s scope=%s", i+1, hit.Source, hit.Scope)
		if strings.TrimSpace(hit.PrincipalID) != "" {
			fmt.Fprintf(&b, " principal=%s", hit.PrincipalID)
		}
		fmt.Fprintf(&b, " kind=%s provenance=%s score=%.2f\n", hit.Kind, firstNonEmpty(strings.TrimSpace(hit.Provenance), "native"), hit.Score)
		b.WriteString("excerpt: ")
		b.WriteString(truncate(strings.TrimSpace(hit.Excerpt), 600))
		b.WriteString("\n")
	}
	b.WriteString("[/SEMANTIC_RECALL]")
	return b.String()
}

func (r *Registry) openAIFile(ctx context.Context, input json.RawMessage, scope sandbox.Scope, p principal.Principal) (string, error) {
	if r.fileStore == nil {
		return "", fmt.Errorf("openai file storage is not configured")
	}
	if err := requireAdminTool(p, "openai_file"); err != nil {
		return "", err
	}

	var in openAIFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode openai_file input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "put":
		localPath, err := resolveUploadPath(scope, in.Path)
		if err != nil {
			return "", err
		}
		purpose := firstNonEmpty(strings.TrimSpace(in.Purpose), r.filePurpose)
		if purpose == "" {
			return "", fmt.Errorf("openai_file purpose is required")
		}
		stored, err := r.fileStore.Put(ctx, localPath, purpose)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_file_put_ok file_id=%s filename=%s bytes=%d purpose=%s", stored.ID, stored.Filename, stored.Bytes, stored.Purpose), nil
	case "list":
		purpose := strings.TrimSpace(in.Purpose)
		if purpose == "" {
			purpose = r.filePurpose
		}
		files, err := r.fileStore.List(ctx, purpose)
		if err != nil {
			return "", err
		}
		return renderOpenAIFileList(purpose, files), nil
	case "get_metadata":
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_file file_id is required for get_metadata")
		}
		body, meta, err := r.fileStore.Get(ctx, fileID)
		if err != nil {
			return "", err
		}
		if body != nil {
			_, _ = io.Copy(io.Discard, body)
			_ = body.Close()
		}
		return renderOpenAIFileMetadata(meta), nil
	case "delete":
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_file file_id is required for delete")
		}
		if err := r.fileStore.Delete(ctx, fileID); err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_file_delete_ok file_id=%s", fileID), nil
	default:
		return "", fmt.Errorf("openai_file action must be one of put|list|get_metadata|delete")
	}
}

func (r *Registry) openAIVectorStore(ctx context.Context, input json.RawMessage, p principal.Principal) (string, error) {
	if r.retrievalStore == nil {
		return "", fmt.Errorf("openai vector store is not configured")
	}
	if err := requireAdminTool(p, "openai_vector_store"); err != nil {
		return "", err
	}

	var in openAIVectorStoreInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("decode openai_vector_store input: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "create":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return "", fmt.Errorf("openai_vector_store name is required for create")
		}
		store, err := r.retrievalStore.CreateStore(ctx, name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_vector_store_create_ok store_id=%s name=%s", store.ID, store.Name), nil
	case "attach":
		storeID, err := r.resolveVectorStoreID(in.StoreID)
		if err != nil {
			return "", err
		}
		fileID := strings.TrimSpace(in.FileID)
		if fileID == "" {
			return "", fmt.Errorf("openai_vector_store file_id is required for attach")
		}
		if err := r.retrievalStore.AttachFile(ctx, storeID, fileID); err != nil {
			return "", err
		}
		return fmt.Sprintf("openai_vector_store_attach_ok store_id=%s file_id=%s", storeID, fileID), nil
	case "search":
		storeID, err := r.resolveVectorStoreID(in.StoreID)
		if err != nil {
			return "", err
		}
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return "", fmt.Errorf("openai_vector_store query is required for search")
		}
		hits, err := r.retrievalStore.Search(ctx, storeID, query, in.Limit)
		if err != nil {
			return "", err
		}
		return renderOpenAIVectorSearchResults(storeID, query, hits), nil
	default:
		return "", fmt.Errorf("openai_vector_store action must be one of create|attach|search")
	}
}

func requireAdminTool(p principal.Principal, toolName string) error {
	if p.Role == "" || p.Role == principal.RoleAdmin {
		return nil
	}
	return fmt.Errorf("%s is admin-only", toolName)
}

func resolveUploadPath(scope sandbox.Scope, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("openai_file path is required for put")
	}
	candidates := make([]string, 0, 3)
	if filepath.IsAbs(raw) {
		candidates = append(candidates, filepath.Clean(raw))
	} else {
		for _, root := range []string{scope.WorkingRoot, scope.SharedMemoryRoot, scope.UserMemory} {
			root = strings.TrimSpace(root)
			if root == "" {
				continue
			}
			candidates = append(candidates, filepath.Join(root, raw))
		}
	}
	allowedRoots := nonEmptyRoots(scope.WorkingRoot, scope.SharedMemoryRoot, scope.UserMemory)
	for _, candidate := range candidates {
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if !pathWithinAnyRoot(resolved, allowedRoots) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", fmt.Errorf("openai_file path %q is a directory", raw)
		}
		return resolved, nil
	}
	return "", fmt.Errorf("openai_file path %q is not readable within the current roots", raw)
}

func nonEmptyRoots(roots ...string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		out = append(out, root)
	}
	return out
}

func pathWithinAnyRoot(target string, roots []string) bool {
	for _, root := range roots {
		base, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, target)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func renderOpenAIFileList(purpose string, files []memstore.StoredFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[OPENAI_FILES]\npurpose: %s\n", strings.TrimSpace(purpose))
	if len(files) == 0 {
		b.WriteString("no_files\n[/OPENAI_FILES]")
		return b.String()
	}
	for i, file := range files {
		fmt.Fprintf(&b, "\n%d. id=%s filename=%s bytes=%d purpose=%s", i+1, file.ID, file.Filename, file.Bytes, file.Purpose)
		if !file.CreatedAt.IsZero() {
			fmt.Fprintf(&b, " created_at=%s", file.CreatedAt.UTC().Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	b.WriteString("[/OPENAI_FILES]")
	return b.String()
}

func renderOpenAIFileMetadata(meta *memstore.StoredFile) string {
	if meta == nil {
		return "[OPENAI_FILE]\nmissing_metadata\n[/OPENAI_FILE]"
	}
	var b strings.Builder
	b.WriteString("[OPENAI_FILE]\n")
	fmt.Fprintf(&b, "id: %s\nfilename: %s\nbytes: %d\npurpose: %s\n", meta.ID, meta.Filename, meta.Bytes, meta.Purpose)
	if !meta.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", meta.CreatedAt.UTC().Format(time.RFC3339))
	}
	b.WriteString("[/OPENAI_FILE]")
	return b.String()
}

func (r *Registry) resolveVectorStoreID(raw string) (string, error) {
	storeID := firstNonEmpty(raw, r.defaultStore)
	if storeID == "" {
		return "", fmt.Errorf("openai_vector_store store_id is required when no default store is configured")
	}
	return storeID, nil
}

func renderOpenAIVectorSearchResults(storeID string, query string, hits []memstore.RetrievalHit) string {
	var b strings.Builder
	b.WriteString("[VECTOR_SEARCH]\n")
	fmt.Fprintf(&b, "store_id: %s\nquery: %s\n", storeID, strings.TrimSpace(query))
	if len(hits) == 0 {
		b.WriteString("no_hits\n[/VECTOR_SEARCH]")
		return b.String()
	}
	for i, hit := range hits {
		fmt.Fprintf(&b, "\n%d. file_id=%s score=%.3f\n", i+1, hit.FileID, hit.Score)
		if strings.TrimSpace(hit.Content) != "" {
			b.WriteString("content: ")
			b.WriteString(truncate(strings.TrimSpace(hit.Content), 600))
			b.WriteString("\n")
		}
		if len(hit.Metadata) > 0 {
			b.WriteString("metadata: ")
			first := true
			for key, value := range hit.Metadata {
				if !first {
					b.WriteString(", ")
				}
				first = false
				b.WriteString(key)
				b.WriteByte('=')
				b.WriteString(value)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("[/VECTOR_SEARCH]")
	return b.String()
}
