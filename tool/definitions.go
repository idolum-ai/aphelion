//go:build linux

package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

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
		if name == webSearchToolName {
			allowed, err := r.webSearchAccessAllowed(p)
			if err == nil && allowed {
				filtered = append(filtered, def)
			}
			continue
		}
		if name == remoteHostToolName {
			allowed, err := r.remoteHostAccessAllowed(p)
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
			freshnessScope := sandbox.Scope{}
			if durableAgentExternalProcessTool(p, manifest) {
				scope, err := r.scopeForPrincipalToolExecution(p)
				if err != nil {
					continue
				}
				if err := r.requireDurableAgentProcessSandbox(p, manifest, scope); err != nil {
					continue
				}
				freshnessScope = scope
			} else {
				var err error
				freshnessScope, err = r.externalToolFreshnessScope(p)
				if err != nil {
					continue
				}
			}
			if err := r.ensureExternalToolFresh(manifest, freshnessScope); err != nil {
				continue
			}
		} else if err := r.requireDurableAgentProcessSandbox(p, manifest, sandbox.Scope{}); err != nil {
			continue
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
	if def, ok := r.webSearchToolDefinition(); ok {
		defs = append(defs, def)
	}
	defs = append(defs, remoteHostToolDefinition())
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
		defs = append(defs, requestApprovalToolDefinition())
		defs = append(defs, agent.ToolDef{
			Name:        "update_operation",
			Description: "Persist or inspect the current operational state for this session. Use this to track the objective, stage, proposal, durable phase plan, findings, and artifacts as work evolves across turns. Pass empty input to inspect the full persisted operation state; non-empty updates return a compact acknowledgement rather than echoing the full state.",
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
								"kind": {"type": "string", "description": "Proposal kind such as capability_acquisition, possible_delete_command, or service_interruption_command"},
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
					"action": {"type": "string", "enum": ["list", "create", "create_from_archetype", "activate", "park", "resume", "retire", "connection_test", "policy_show", "bootstrap_show", "policy_apply", "bootstrap_update", "enrollment_show", "enrollment_update", "wizard_start", "wizard_answer", "wizard_show", "wizard_finalize", "wizard_cancel", "archetype_list", "archetype_show", "access_show", "access_grant", "access_revoke", "conversation_show", "conversation_send", "delegation_request", "delegation_report", "memory_review", "memory_delegate", "profile_show", "profile_apply", "artifact_put", "artifact_list", "artifact_show", "snapshot_create", "snapshot_list", "snapshot_restore"], "description": "Durable-agent governance operation"},
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
									"mode": {"type": "string", "enum": ["sketch", "local", "external", "live"], "description": "Child footprint. sketch keeps an idea/prototype record; local gives a local-only child; external adds read-only adapter setup; live allows the child to operate on a live channel within policy."},
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
						"wakeup_mode": {"type": "string", "description": "Optional wakeup mode for create. Example: poll"},
						"network_policy": {"type": "string", "description": "Optional network policy for create"},
						"secret_scopes": {"type": "array", "items": {"type": "string"}, "description": "Optional secret scopes for create"},
						"channel_config": {"type": "object", "description": "Optional structured channel configuration for create. Adapter-specific details belong in child-owned runtime agreements."},
						"wizard_answers": {
							"type": "object",
							"description": "Wizard answer patch for wizard_answer (generic durable child setup).",
								"properties": {
									"mode": {"type": "string", "enum": ["sketch", "local", "external", "live"]},
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
