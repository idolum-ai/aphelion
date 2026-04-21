//go:build linux

package tool

import (
	"bytes"
	"context"
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
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const defaultMaxOutputBytes = 32 * 1024

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

type updateOperationFindingInput struct {
	Claim      string `json:"claim"`
	Confidence string `json:"confidence,omitempty"`
	Basis      string `json:"basis,omitempty"`
}

type updateOperationArtifactInput struct {
	Label string `json:"label,omitempty"`
	Ref   string `json:"ref"`
}

type updateOperationInput struct {
	ID        string                         `json:"id,omitempty"`
	Objective string                         `json:"objective,omitempty"`
	Status    string                         `json:"status,omitempty"`
	Stage     string                         `json:"stage,omitempty"`
	Summary   string                         `json:"summary,omitempty"`
	Merge     bool                           `json:"merge,omitempty"`
	Proposal  *updateOperationProposalInput  `json:"proposal,omitempty"`
	Findings  []updateOperationFindingInput  `json:"findings,omitempty"`
	Artifacts []updateOperationArtifactInput `json:"artifacts,omitempty"`
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
	OutboundMode              string `json:"outbound_mode,omitempty"`
	PublicSurfaceMode         string `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string `json:"shared_inference_reuse_scope,omitempty"`
}

type durableAgentWizardAnswersInput struct {
	Address          string   `json:"address,omitempty"`
	Account          string   `json:"account,omitempty"`
	Adapter          string   `json:"adapter,omitempty"`
	Query            string   `json:"query,omitempty"`
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

type durableAgentCapacityContractInput struct {
	Status              string   `json:"status,omitempty"`
	ParentProposal      string   `json:"parent_proposal,omitempty"`
	ChildSelfAssessment string   `json:"child_self_assessment,omitempty"`
	Can                 []string `json:"can,omitempty"`
	Cannot              []string `json:"cannot,omitempty"`
	Uncertain           []string `json:"uncertain,omitempty"`
	SuccessCriteria     []string `json:"success_criteria,omitempty"`
	EvidenceSignals     []string `json:"evidence_signals,omitempty"`
	ProbeChecklist      []string `json:"probe_checklist,omitempty"`
	ProbeResults        []string `json:"probe_results,omitempty"`
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

type durableAgentSnapshotInput struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type durableAgentInput struct {
	Action                    string                             `json:"action"`
	AgentID                   string                             `json:"agent_id,omitempty"`
	ChannelKind               string                             `json:"channel_kind,omitempty"`
	ReviewEventID             int64                              `json:"review_event_id,omitempty"`
	ReviewTargetChatID        int64                              `json:"review_target_chat_id,omitempty"`
	Reason                    string                             `json:"reason,omitempty"`
	PolicyPatch               *durableAgentPolicyPatchInput      `json:"policy_patch,omitempty"`
	PolicyOverrides           *durableAgentPolicyOverridesInput  `json:"policy_overrides,omitempty"`
	Charter                   string                             `json:"charter,omitempty"`
	Autonomy                  string                             `json:"autonomy,omitempty"`
	Visibility                string                             `json:"visibility,omitempty"`
	SharedContext             string                             `json:"shared_context,omitempty"`
	Capabilities              []string                           `json:"capabilities,omitempty"`
	OutboundMode              string                             `json:"outbound_mode,omitempty"`
	DriftPolicy               string                             `json:"drift_policy,omitempty"`
	PublicSurfaceMode         string                             `json:"public_surface_mode,omitempty"`
	SharedInferenceReuse      string                             `json:"shared_inference_reuse,omitempty"`
	SharedInferenceReuseScope string                             `json:"shared_inference_reuse_scope,omitempty"`
	WakeupMode                string                             `json:"wakeup_mode,omitempty"`
	NetworkPolicy             string                             `json:"network_policy,omitempty"`
	SecretScopes              []string                           `json:"secret_scopes,omitempty"`
	ChannelConfig             json.RawMessage                    `json:"channel_config,omitempty"`
	WizardAnswers             *durableAgentWizardAnswersInput    `json:"wizard_answers,omitempty"`
	CapacityContract          *durableAgentCapacityContractInput `json:"capacity_contract,omitempty"`
	MemoryDelegation          *durableAgentMemoryDelegationInput `json:"memory_delegation,omitempty"`
	Snapshot                  *durableAgentSnapshotInput         `json:"snapshot,omitempty"`
	Operation                 string                             `json:"operation,omitempty"`
	Secret                    string                             `json:"secret,omitempty"`
	Message                   string                             `json:"message,omitempty"`
	History                   int                                `json:"history,omitempty"`
	TelegramUserID            int64                              `json:"telegram_user_id,omitempty"`
	TelegramUserIDs           []int64                            `json:"telegram_user_ids,omitempty"`
}

func NewRegistry(workspace string, timeout time.Duration) *Registry {
	return &Registry{
		workspace:      workspace,
		timeout:        timeout,
		maxOutputBytes: defaultMaxOutputBytes,
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

func (r *Registry) SupportsPrincipal(p principal.Principal) bool {
	if r == nil || r.sandbox == nil {
		return false
	}

	scope, err := r.sandbox.Resolve(p)
	if err != nil {
		return false
	}
	if r.runner == nil {
		return p.Role == principal.RoleAdmin
	}
	return r.runner.Supports(scope)
}

func (r *Registry) Definitions() []agent.ToolDef {
	defs := []agent.ToolDef{
		{
			Name:        "exec",
			Description: "Run a shell command in the configured workspace. Use this for git, file inspection, builds, tests, and repository edits.",
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
		{
			Name:        "memory",
			Description: "Write curated memory for the current principal. Use this for compact durable notes, knowledge, decisions, questions, or rhizome associations.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "enum": ["add", "replace", "remove"], "description": "Memory write operation"},
					"scope": {"type": "string", "enum": ["shared", "principal"], "description": "Shared memory for admin, or principal-local memory for isolated users"},
					"store": {"type": "string", "enum": ["memory", "knowledge", "decisions", "questions", "rhizome"], "description": "Curated memory store to edit"},
					"content": {"type": "string", "description": "Content to add or replacement content"},
					"match": {"type": "string", "description": "Exact existing text to replace or remove"},
					"source_tag": {"type": "string", "enum": ["direct", "observed", "inferred", "hypothesized", "shared"], "description": "Optional provenance tag for added or replaced entries"},
					"confidence": {"type": "number", "minimum": 0, "maximum": 1, "description": "Optional confidence for added or replaced entries"}
				},
				"required": ["action", "store"]
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
			Description: "Persist or inspect the current operational state for this session. Use this to track the objective, stage, proposal, findings, and artifacts as work evolves across turns.",
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
		defs = append(defs, agent.ToolDef{
			Name:        "durable_agent",
			Description: "Inspect and ratify durable-agent governance from conversation. Admin only. For policy_apply, prefer policy_patch (conversational policy intent) and use policy_overrides only when a low-level override is explicitly needed. For ordinary behavior/privacy/shared-context changes, use policy_apply directly; enrollment actions are only for remote control-plane lifecycle.",
			Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
					"action": {"type": "string", "enum": ["list", "create", "activate", "connection_test", "policy_show", "policy_apply", "enrollment_show", "enrollment_update", "wizard_start", "wizard_answer", "wizard_show", "wizard_finalize", "wizard_cancel", "access_show", "access_grant", "access_revoke", "capacity_show", "capacity_negotiate", "capacity_probe", "capacity_attest", "conversation_show", "conversation_send", "memory_review", "memory_delegate", "snapshot_create", "snapshot_list", "snapshot_restore"], "description": "Durable-agent governance operation"},
					"agent_id": {"type": "string", "description": "Durable agent id for show/update actions"},
						"channel_kind": {"type": "string", "description": "Required for create. Example: inbox (currently mapped to email adapter profile)"},
						"review_event_id": {"type": "integer", "minimum": 1, "description": "Optional source review event id for policy ratification provenance"},
						"review_target_chat_id": {"type": "integer", "description": "Optional admin review target chat id override for create"},
						"reason": {"type": "string", "description": "Optional operator reason for the change"},
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
								"shared_inference_reuse_scope": {"type": "string", "description": "Low-level shared inference reuse scope override"}
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
						"channel_config": {"type": "object", "description": "Optional structured channel configuration for create. Current inbox profile accepts either email or alias inbox object."},
						"wizard_answers": {
							"type": "object",
							"description": "Wizard answer patch for wizard_answer (durable child setup; current profile support is inbox/email).",
							"properties": {
								"address": {"type": "string"},
								"account": {"type": "string"},
								"adapter": {"type": "string"},
								"query": {"type": "string"},
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
						"capacity_contract": {
							"type": "object",
							"description": "Bidirectional parent-child capacity contract details for capacity_negotiate/probe/attest actions.",
							"properties": {
								"status": {"type": "string", "enum": ["unattested", "provisional", "verified", "stale"]},
								"parent_proposal": {"type": "string"},
								"child_self_assessment": {"type": "string"},
								"can": {"type": "array", "items": {"type": "string"}},
								"cannot": {"type": "array", "items": {"type": "string"}},
								"uncertain": {"type": "array", "items": {"type": "string"}},
								"success_criteria": {"type": "array", "items": {"type": "string"}},
								"evidence_signals": {"type": "array", "items": {"type": "string"}},
								"probe_checklist": {"type": "array", "items": {"type": "string"}},
								"probe_results": {"type": "array", "items": {"type": "string"}}
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
					"operation": {"type": "string", "enum": ["revoke", "reactivate", "decommission", "rotate_secret"], "description": "Enrollment lifecycle operation for enrollment_update"},
					"secret": {"type": "string", "description": "Replacement control-plane secret for enrollment_update when operation=rotate_secret"},
					"message": {"type": "string", "description": "Parent message text for conversation_send"},
					"history": {"type": "integer", "minimum": 1, "maximum": 20, "description": "Recent policy update entries to show for policy_show"},
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

	scope, err := r.sandbox.Resolve(p)
	if err != nil {
		return "", err
	}
	if err := ensureScopeReady(scope); err != nil {
		return "", err
	}
	if r.runner == nil {
		return "", fmt.Errorf("principal-aware execution requires sandbox runner")
	}
	if !r.runner.Supports(scope) {
		return "", fmt.Errorf("no supported sandbox backend for principal role %q", p.Role)
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
	switch name {
	case "exec":
		return r.exec(ctx, input, scope, p, key)
	case "memory":
		return r.memory(ctx, input, scope)
	case "session_search":
		return r.sessionSearch(ctx, input, p, key)
	case "update_operation":
		return r.updateOperation(ctx, input, key)
	case "update_plan":
		return r.updatePlan(ctx, input, key)
	case "semantic_search":
		return r.semanticSearch(ctx, input, scope)
	case "openai_file":
		return r.openAIFile(ctx, input, scope, p)
	case "openai_vector_store":
		return r.openAIVectorStore(ctx, input, p)
	case "durable_agent":
		return r.durableAgent(ctx, input, p, key, scope)
	default:
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

	workdir, err := resolveWorkdir(scope.WorkingRoot, in.Workdir)
	if err != nil {
		return "", err
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

	result, err := memstore.ApplyWrite(memstore.WriteRequest{
		Root:       root,
		Store:      in.Store,
		Action:     in.Action,
		Content:    in.Content,
		Match:      in.Match,
		SourceTag:  in.SourceTag,
		Confidence: in.Confidence,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("memory_%s_ok scope=%s store=%s path=%s", result.Action, effectiveScope, result.Store, result.Path), nil
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
	base, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
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
		return "", fmt.Errorf("resolve workdir: %w", err)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("check workdir: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workdir %q escapes workspace %q", raw, base)
	}

	return target, nil
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
