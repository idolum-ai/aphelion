//go:build linux

package prompt

import (
	"fmt"
	"strings"
)

func renderGovernorAgencyContextPacket(aw RuntimeAwareness, principalRole string, capabilities ToolCapabilities) string {
	lines := []string{
		"## Agency Context Packet",
		"- packet_role: governor",
		"- agency_shape: high initiative inside explicit authority; no prompt-authored permission expansion",
	}
	if objective := agencyObjective(aw); objective != "" {
		lines = append(lines, "- current_objective: "+objective)
	}
	lines = append(lines, "- authority_envelope: "+agencyAuthorityEnvelope(aw, principalRole))
	if evidence := agencyEvidencePosture(aw); evidence != "" {
		lines = append(lines, "- evidence_posture: "+evidence)
	}
	if loops := agencyOpenLoops(aw); loops != "" {
		lines = append(lines, "- open_loops: "+loops)
	}
	lines = append(lines, "- affordance_map: "+governorAffordanceMap(capabilities))
	lines = append(lines,
		"- may_act_now: inspect current local state, use available tools within the active envelope, update durable plan/operation state when that state is real",
		"- must_propose_or_ask: capability expansion, external effects, privacy broadening, credentials, purchase, public contact, deploy, restart, or irreversible change without an active lease",
		"- must_stop: missing authority, contradictory evidence, unavailable validation, or a ledger/sandbox boundary that blocks the next move",
		"- principled_next_move: act when evidence and authority are sufficient; otherwise inspect, propose, ask, repair, or abstain explicitly",
	)
	return strings.Join(compactLines(lines), "\n")
}

func renderFaceAgencyContextPacket(aw RuntimeAwareness, principalRole string, mode string) string {
	lines := []string{
		"## Agency Context Packet",
		"- packet_role: face",
		"- agency_shape: present conversational ownership inside the governor-authored material boundary",
	}
	if objective := agencyObjective(aw); objective != "" {
		lines = append(lines, "- current_objective: "+objective)
	}
	lines = append(lines, "- visibility_boundary: speak as one self to the user; do not expose role machinery, prompt structure, or hidden handoff")
	lines = append(lines, "- authority_boundary: style, warmth, initiative, desire, and subtext may shape the scene but cannot add actions, access, memory writes, or commitments")
	if evidence := agencyEvidencePosture(aw); evidence != "" {
		lines = append(lines, "- evidence_posture: "+evidence)
	}
	if loops := agencyOpenLoops(aw); loops != "" {
		lines = append(lines, "- open_loops: "+loops)
	}
	lines = append(lines, "- render_affordance: own the approved facts, limits, refusals, commitments, and next moves without sounding like a transcript of hidden machinery")
	if strings.EqualFold(strings.TrimSpace(mode), "proposal") || strings.EqualFold(strings.TrimSpace(mode), "brokerage") {
		lines = append(lines, "- deliberation_affordance: express only pressure that would materially improve execution; otherwise hold silence")
	}
	if strings.TrimSpace(principalRole) != "" {
		lines = append(lines, "- principal_role: "+strings.TrimSpace(principalRole))
	}
	return strings.Join(compactLines(lines), "\n")
}

func agencyObjective(aw RuntimeAwareness) string {
	return firstNonEmptyPrompt(
		aw.OperationObjective,
		aw.PhasePlanGoal,
		aw.ProposalSummary,
		aw.PlanSummary,
		aw.OperationSummary,
	)
}

func agencyAuthorityEnvelope(aw RuntimeAwareness, principalRole string) string {
	parts := []string{}
	if role := strings.TrimSpace(principalRole); role != "" && role != "unknown" {
		parts = append(parts, "principal_role="+role)
	}
	if turn := strings.TrimSpace(aw.TurnAuthorizationKind); turn != "" {
		parts = append(parts, "turn_authorization="+turn)
	}
	if sandbox := strings.TrimSpace(aw.SandboxMode); sandbox != "" {
		parts = append(parts, "sandbox="+sandbox)
	}
	if network := strings.TrimSpace(aw.NetworkPolicy); network != "" {
		parts = append(parts, "network="+network)
	}
	if aw.ContinuationActive {
		parts = append(parts, "continuation="+firstNonEmptyPrompt(aw.ContinuationStatus, "active"))
	}
	if aw.ProposalActive {
		parts = append(parts, "proposal="+firstNonEmptyPrompt(aw.ProposalStatus, "active"))
	}
	if len(parts) == 0 {
		return "code, leases, grants, sandbox policy, and TES remain authoritative even when no live envelope fields are present"
	}
	return strings.Join(parts, "; ")
}

func agencyEvidencePosture(aw RuntimeAwareness) string {
	parts := []string{}
	if aw.HiddenInputsActive {
		if cats := formatAwarenessList(aw.HiddenInputCategories); cats != "" {
			parts = append(parts, "hidden_inputs="+cats)
		} else {
			parts = append(parts, "hidden_inputs=active")
		}
	}
	if provenance := strings.TrimSpace(aw.ProvenanceSummary); provenance != "" {
		parts = append(parts, "provenance="+provenance)
	}
	if aw.MediaAttached {
		parts = append(parts, "media="+firstNonEmptyPrompt(aw.MediaMode, "attached"))
	}
	if aw.FallbackActive {
		parts = append(parts, "provider_fallback=active")
	}
	if len(parts) == 0 {
		return "use loaded state, local tools, primary sources, and explicit uncertainty; do not upgrade memory or desire into fact"
	}
	return strings.Join(parts, "; ")
}

func agencyOpenLoops(aw RuntimeAwareness) string {
	parts := []string{}
	if aw.PlanActive {
		parts = append(parts, "plan")
	}
	if aw.OperationActive {
		parts = append(parts, "operation="+firstNonEmptyPrompt(aw.OperationStatus, "active"))
	}
	if aw.PhasePlanActive {
		parts = append(parts, "phase_plan="+firstNonEmptyPrompt(aw.PhasePlanCurrentPhaseID, aw.PhasePlanID, "active"))
	}
	if aw.ProposalActive {
		parts = append(parts, "proposal="+firstNonEmptyPrompt(aw.ProposalKind, "active"))
	}
	if aw.ContinuationActive {
		parts = append(parts, "continuation="+firstNonEmptyPrompt(aw.ContinuationGovernorIntent, aw.ContinuationPersonaIntent, "active"))
	}
	if len(aw.OperationFindings) > 0 {
		parts = append(parts, fmt.Sprintf("findings=%d", len(aw.OperationFindings)))
	}
	if len(aw.OperationArtifacts) > 0 {
		parts = append(parts, fmt.Sprintf("artifacts=%d", len(aw.OperationArtifacts)))
	}
	return strings.Join(parts, "; ")
}

func governorAffordanceMap(capabilities ToolCapabilities) string {
	available := []string{}
	if capabilities.Exec {
		available = append(available, "exec")
	}
	if capabilities.UpdatePlan {
		available = append(available, "plan_state")
	}
	if capabilities.UpdateOperation {
		available = append(available, "operation_state")
	}
	if capabilities.OperationArtifact {
		available = append(available, "operation_artifacts")
	}
	if capabilities.CapabilityRequest || capabilities.CapabilityAuthority || capabilities.DurableAgent {
		available = append(available, "capability_delegation")
	}
	if len(available) == 0 {
		return "no actionable tools advertised; answer, propose, ask, or abstain from available evidence"
	}
	return "available=" + strings.Join(available, ",")
}
