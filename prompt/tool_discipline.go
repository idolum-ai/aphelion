//go:build linux

package prompt

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
)

type ToolCapabilities struct {
	Exec                bool
	ReadFile            bool
	ListDir             bool
	Search              bool
	UpdatePlan          bool
	UpdateOperation     bool
	OperationArtifact   bool
	CapabilityRequest   bool
	CapabilityAuthority bool
	DurableAgent        bool
}

func (c ToolCapabilities) Empty() bool {
	return !c.Exec &&
		!c.ReadFile &&
		!c.ListDir &&
		!c.Search &&
		!c.UpdatePlan &&
		!c.UpdateOperation &&
		!c.OperationArtifact &&
		!c.CapabilityRequest &&
		!c.CapabilityAuthority &&
		!c.DurableAgent
}

func renderPlanningDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.UpdatePlan {
		return ""
	}
	return strings.Join([]string{
		"## Planning Discipline",
		"Use update_plan for genuinely multi-step work where progress should survive long turns, compaction, or retries.",
		"Keep the plan concise, keep statuses current, and keep at most one step in_progress.",
		"Do not use update_plan for trivial one-step replies or to narrate work you are not about to execute.",
	}, "\n")
}

func renderOperationalDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.UpdateOperation {
		return ""
	}
	return strings.Join([]string{
		"## Operational Discipline",
		"Treat open-ended work as an operation with durable state rather than a one-turn improvisation.",
		"Use update_operation to keep the objective, current stage, proposal state, durable phase_plan, findings, and artifacts current when those details materially shape execution or delivery.",
		"For phase blockers and supersession, prefer typed fields such as gate_level, gate_reason_code, approval_subject, autoapprove_eligible, blocked_reason_code, requires_consent, requires_opt_in, supersedes_phase_ids, and stale_authority instead of encoding gates only in prose.",
		"Runtime approval gates are compiled from typed fields and exact structured codes, not from summary, why_now, or bounded_effect prose; if a gate matters, write the field.",
		"Use gate_level=escalated_operator_approval with autoapprove_eligible=false for bounded sensitive operator-owned checks such as external-account auth status, credential metadata, or capability grant review; reserve hard_consent_block/requires_opt_in/requires_consent for third-party opt-in or private-content gates.",
		"Operate autonomously between gates. When the next move materially expands capability, external effect, privacy scope, or irreversible risk, surface a bounded proposal instead of silently pushing through.",
	}, "\n")
}

func renderCapabilityDelegationDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.CapabilityRequest && !capabilities.CapabilityAuthority && !capabilities.DurableAgent {
		return ""
	}
	lines := []string{
		"## Capability Delegation Discipline",
		"When a child, tenant, agent, or conversation needs permission beyond its current envelope, route it through the generic capability delegation lane instead of inventing a one-off workflow.",
	}
	if capabilities.CapabilityRequest {
		lines = append(lines, "Use capability_request for direct broad permission requests across tools, local devices, external accounts, purchases, public web, communication surfaces, file/network access, and emergent permissions.")
	}
	if capabilities.DurableAgent {
		lines = append(lines, "For durable child-agent asks or progress reports, use durable_agent delegation_request/delegation_report; that bridge creates canonical capability state and queues review artifacts while preserving the child persona boundary.")
	}
	if capabilities.CapabilityAuthority {
		lines = append(lines, "Use capability_authority for parent/admin review, grant, revoke, and access_check. A proposed request is not an active grant.")
	}
	lines = append(lines, "Use specialized durable_agent actions only for already-modeled local operations; emergent permissions should stay conversation-derived, contract-bound, and reviewable.")
	return strings.Join(lines, "\n")
}

func appendToolDisciplineBlocks(parts []agent.SystemBlock, toolCaps ToolCapabilities) []agent.SystemBlock {
	if planning := renderPlanningDisciplineBlock(toolCaps); planning != "" {
		parts = append(parts, agent.SystemBlock{Text: planning})
	}
	if operations := renderOperationalDisciplineBlock(toolCaps); operations != "" {
		parts = append(parts, agent.SystemBlock{Text: operations})
	}
	if nativeFileExploration := renderNativeFileExplorationDisciplineBlock(toolCaps); nativeFileExploration != "" {
		parts = append(parts, agent.SystemBlock{Text: nativeFileExploration})
	}
	if artifacts := renderOperationArtifactDeliveryBlock(toolCaps); artifacts != "" {
		parts = append(parts, agent.SystemBlock{Text: artifacts})
	}
	if delegation := renderCapabilityDelegationDisciplineBlock(toolCaps); delegation != "" {
		parts = append(parts, agent.SystemBlock{Text: delegation})
	}
	if confirmation := renderConfirmationDisciplineBlock(toolCaps); confirmation != "" {
		parts = append(parts, agent.SystemBlock{Text: confirmation})
	}
	if validation := renderValidationDisciplineBlock(toolCaps); validation != "" {
		parts = append(parts, agent.SystemBlock{Text: validation})
	}
	if mediaDelivery := renderGeneratedMediaDeliveryBlock(toolCaps); mediaDelivery != "" {
		parts = append(parts, agent.SystemBlock{Text: mediaDelivery})
	}
	return parts
}

func renderNativeFileExplorationDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.ReadFile && !capabilities.ListDir && !capabilities.Search {
		return ""
	}
	return strings.Join([]string{
		"## Native File Exploration Discipline",
		"Prefer read_file, list_dir, and search for scoped repository and filesystem inspection; reserve exec for commands, validation, builds, service actions, or logic that native tools cannot express.",
		"When several independent reads, directory listings, or literal searches are needed, emit those native tool calls together in one assistant response so the runtime can execute the parallel-safe batch.",
		"Parallel batching contract: batch independent calls only when later inputs do not depend on earlier outputs; preserve sequential calls when there is data dependency, authority escalation, or destructive/external effect risk.",
		"Keep each native file call bounded to the smallest useful path, query, and byte or result limit.",
	}, "\n")
}

func renderConfirmationDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.Exec {
		return ""
	}
	return strings.Join([]string{
		"## Confirmation Discipline",
		"Ask for confirmation when authority genuinely depends on it, when intent is materially ambiguous, or when a destructive or irreversible action is next.",
		"Do not ask for confirmation as a politeness reflex when the next move is already obvious.",
		"When runtime proposal gating blocks execution, treat that as a real operational boundary rather than a stylistic suggestion.",
	}, "\n")
}

func renderOperationArtifactDeliveryBlock(capabilities ToolCapabilities) string {
	if !capabilities.OperationArtifact {
		return ""
	}
	return strings.Join([]string{
		"## Operation Artifact Delivery",
		"Operation artifacts are durable state, not ambient conversational intent.",
		"When the user explicitly asks to receive an existing operation artifact, call operation_artifact with action=resolve_sendable and include the returned MEDIA directive in the final reply.",
		"If the user only mentions sharing later, references an artifact ambiguously, or is continuing ordinary conversation, do not send an artifact; answer the turn normally or ask a concise clarification.",
		"Do not invent artifact paths or attach files without operation_artifact evidence that the path is sendable inside the active sandbox.",
	}, "\n")
}

func renderValidationDisciplineBlock(capabilities ToolCapabilities) string {
	if !capabilities.Exec {
		return ""
	}
	return strings.Join([]string{
		"## Validation Discipline",
		"Validate meaningful edits, migrations, generated files, service actions, or debugging conclusions with the narrowest relevant test, command, log read, or source check available.",
		"Report what was validated. Report what was not validated before delivery.",
		"If validation is blocked by permissions, missing dependencies, timeouts, or sandbox limits, say that plainly and preserve the remaining risk.",
	}, "\n")
}

func renderGeneratedMediaDeliveryBlock(capabilities ToolCapabilities) string {
	if !capabilities.Exec {
		return ""
	}
	return strings.Join([]string{
		"## Generated Media Delivery",
		"When tool execution creates local files that should be delivered to the user, keep the files inside the active working, shared-memory, or user-memory roots and include one structured directive line per deliverable artifact:",
		`MEDIA: {"path":"<path>"}`,
		"Relative paths resolve from the active working root; absolute paths are accepted only inside allowed runtime roots.",
		"Do not use bare MEDIA: text; the runtime only honors the structured JSON directive.",
		"Pair delivered media with a concise narration or caption in the candidate reply so the face can present the result as one voice.",
		"Do not claim inability to generate, render, attach, send, or provide media while attaching it.",
	}, "\n")
}

func ToolCapabilitiesFromDefs(defs []agent.ToolDef) ToolCapabilities {
	out := ToolCapabilities{}
	for _, def := range defs {
		switch normalizeToolName(def.Name) {
		case "exec":
			out.Exec = true
		case "read_file":
			out.ReadFile = true
		case "list_dir":
			out.ListDir = true
		case "search":
			out.Search = true
		case "update_plan":
			out.UpdatePlan = true
		case "update_operation":
			out.UpdateOperation = true
		case "operation_artifact":
			out.OperationArtifact = true
		case "capability_request":
			out.CapabilityRequest = true
		case "capability_authority":
			out.CapabilityAuthority = true
		case "durable_agent":
			out.DurableAgent = true
		}
	}
	return out
}

func toolCapabilitiesFromManifest(manifest string) ToolCapabilities {
	names := parseManifestToolNames(manifest)
	return ToolCapabilities{
		Exec:                manifestHasTool(names, "exec"),
		ReadFile:            manifestHasTool(names, "read_file"),
		ListDir:             manifestHasTool(names, "list_dir"),
		Search:              manifestHasTool(names, "search"),
		UpdatePlan:          manifestHasTool(names, "update_plan"),
		UpdateOperation:     manifestHasTool(names, "update_operation"),
		OperationArtifact:   manifestHasTool(names, "operation_artifact"),
		CapabilityRequest:   manifestHasTool(names, "capability_request"),
		CapabilityAuthority: manifestHasTool(names, "capability_authority"),
		DurableAgent:        manifestHasTool(names, "durable_agent"),
	}
}

func manifestHasTool(names map[string]struct{}, name string) bool {
	_, ok := names[name]
	return ok
}

func parseManifestToolNames(manifest string) map[string]struct{} {
	out := map[string]struct{}{}
	manifest = strings.TrimSpace(manifest)
	if manifest == "" {
		return out
	}
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "tools:"):
			inline := strings.TrimSpace(strings.TrimPrefix(line, "tools:"))
			if inline != "" {
				for _, token := range strings.Split(inline, ",") {
					addManifestToolName(out, token)
				}
			}
		case strings.HasPrefix(lower, "exec constraints:"):
			continue
		case strings.HasPrefix(line, "- "):
			name := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if idx := strings.Index(name, ":"); idx >= 0 {
				name = name[:idx]
			}
			addManifestToolName(out, name)
		case strings.Contains(line, ","):
			for _, token := range strings.Split(line, ",") {
				addManifestToolName(out, token)
			}
		}
	}
	return out
}

func addManifestToolName(out map[string]struct{}, raw string) {
	name := normalizeToolName(raw)
	if name == "" {
		return
	}
	out[name] = struct{}{}
}

func normalizeToolName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, "("); idx >= 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, ":"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return ""
	}
	return raw
}
