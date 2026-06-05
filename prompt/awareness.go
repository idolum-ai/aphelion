//go:build linux

package prompt

import (
	"fmt"
	"strings"
)

type RuntimeAwareness struct {
	SessionKind                string
	RunKind                    string
	Channel                    string
	EventOrigin                string
	TurnAuthorizationKind      string
	GovernorBackend            string
	GovernorProvider           string
	GovernorModel              string
	GovernorProviderPath       []string
	ActiveProvider             string
	FallbackActive             bool
	ReasoningEffort            string
	ReasoningSummary           string
	GovernorEffortRecipe       string
	ArtifactMode               string
	BrokerageActive            bool
	BrokeragePhase             string
	SuggestedExecutionContract string
	BrokerageRatification      string
	RatifiedExecutionContract  string
	SignalJudgment             string
	FaceBackend                string
	FaceProvider               string
	FaceModel                  string
	PersonaEffortRecipe        string
	DeliveryMode               string
	StreamReply                bool
	InboundWasVoice            bool
	ReplyModalityDefault       string
	ReplyModalityReason        string
	ReplyModalityOverride      string
	MediaAttached              bool
	MediaMode                  string
	HiddenInputsActive         bool
	HiddenInputCategories      []string
	ProvenanceSummary          string
	PlanActive                 bool
	PlanSummary                string
	PlanSteps                  []string
	OperationActive            bool
	OperationObjective         string
	OperationStatus            string
	OperationStage             string
	OperationSummary           string
	ProposalActive             bool
	ProposalKind               string
	ProposalStatus             string
	ProposalSummary            string
	ProposalWhyNow             string
	ProposalBoundedEffect      string
	PhasePlanActive            bool
	PhasePlanID                string
	PhasePlanGoal              string
	PhasePlanCurrentPhaseID    string
	OperationPhases            []string
	OperationFindings          []string
	OperationArtifacts         []string
	ContinuationStatus         string
	ContinuationActive         bool
	ContinuationPersonaIntent  string
	ContinuationPersonaWhy     string
	ContinuationGovernorIntent string
	ContinuationGovernorWhy    string
	ContinuationRatified       bool
	ContinuationBlockedReason  string
	PromptRoot                 string
	ExecRoot                   string
	SharedMemoryRoot           string
	UserWorkspaceRoot          string
	UserMemoryRoot             string
	WorkingRoot                string
	SandboxMode                string
	NetworkPolicy              string
}

type AwarenessRole string

const (
	AwarenessRoleGovernor AwarenessRole = "governor"
	AwarenessRoleFace     AwarenessRole = "face"
)

func renderGovernorRuntimeAwarenessBlock(aw RuntimeAwareness) string {
	return renderRuntimeAwarenessBlock(aw, AwarenessRoleGovernor, "## Runtime Awareness")
}

func renderFaceAwarenessBlock(aw RuntimeAwareness) string {
	return renderRuntimeAwarenessBlock(aw, AwarenessRoleFace, "## Delivery Awareness")
}

func renderRuntimeAwarenessBlock(aw RuntimeAwareness, role AwarenessRole, heading string) string {
	lines := []string{heading}
	lines = append(lines, renderSharedAwarenessLines(aw)...)
	switch role {
	case AwarenessRoleFace:
		lines = append(lines, renderFaceAwarenessLines(aw)...)
	default:
		lines = append(lines, renderGovernorAwarenessLines(aw)...)
	}
	return strings.Join(compactLines(lines), "\n")
}

func renderSharedAwarenessLines(aw RuntimeAwareness) []string {
	lines := []string{
		nonEmptyAwarenessLine("session_kind", aw.SessionKind),
		nonEmptyAwarenessLine("run_kind", aw.RunKind),
		nonEmptyAwarenessLine("channel", aw.Channel),
		nonEmptyAwarenessLine("event_origin", aw.EventOrigin),
		nonEmptyAwarenessLine("active_provider", aw.ActiveProvider),
		fmt.Sprintf("- fallback_active: %t", aw.FallbackActive),
		nonEmptyAwarenessLine("artifact_mode", aw.ArtifactMode),
		fmt.Sprintf("- hidden_inputs_active: %t", aw.HiddenInputsActive),
		nonEmptyAwarenessLine("hidden_input_categories", formatAwarenessList(aw.HiddenInputCategories)),
		nonEmptyAwarenessLine("provenance_summary", aw.ProvenanceSummary),
		fmt.Sprintf("- plan_active: %t", aw.PlanActive),
		nonEmptyAwarenessLine("plan_summary", aw.PlanSummary),
		fmt.Sprintf("- operation_active: %t", aw.OperationActive),
		nonEmptyAwarenessLine("operation_objective", aw.OperationObjective),
		nonEmptyAwarenessLine("operation_status", aw.OperationStatus),
		nonEmptyAwarenessLine("operation_stage", aw.OperationStage),
		nonEmptyAwarenessLine("operation_summary", aw.OperationSummary),
		fmt.Sprintf("- media_attached: %t", aw.MediaAttached),
		nonEmptyAwarenessLine("media_mode", aw.MediaMode),
	}
	return lines
}

func renderGovernorAwarenessLines(aw RuntimeAwareness) []string {
	lines := []string{
		nonEmptyAwarenessLine("turn_authorization_kind", aw.TurnAuthorizationKind),
		nonEmptyAwarenessLine("governor_backend", aw.GovernorBackend),
		nonEmptyAwarenessLine("governor_provider", aw.GovernorProvider),
		nonEmptyAwarenessLine("governor_model", aw.GovernorModel),
	}
	if path := formatProviderPath(aw.GovernorProviderPath); path != "" {
		lines = append(lines, fmt.Sprintf("- configured_provider_path: %s", path))
	}
	lines = append(lines,
		nonEmptyAwarenessLine("reasoning_effort", aw.ReasoningEffort),
		nonEmptyAwarenessLine("reasoning_summary", aw.ReasoningSummary),
		nonEmptyAwarenessLine("governor_effort_recipe", aw.GovernorEffortRecipe),
		fmt.Sprintf("- brokerage_active: %t", aw.BrokerageActive),
		nonEmptyAwarenessLine("brokerage_phase", aw.BrokeragePhase),
		nonEmptyAwarenessLine("idolum_suggested_execution_contract", aw.SuggestedExecutionContract),
		nonEmptyAwarenessLine("brokerage_ratification", aw.BrokerageRatification),
		nonEmptyAwarenessLine("ratified_execution_contract", aw.RatifiedExecutionContract),
		nonEmptyAwarenessLine("signal_judgment", aw.SignalJudgment),
		fmt.Sprintf("- proposal_active: %t", aw.ProposalActive),
		nonEmptyAwarenessLine("proposal_kind", aw.ProposalKind),
		nonEmptyAwarenessLine("proposal_status", aw.ProposalStatus),
		nonEmptyAwarenessLine("proposal_summary", aw.ProposalSummary),
		nonEmptyAwarenessLine("proposal_why_now", aw.ProposalWhyNow),
		nonEmptyAwarenessLine("proposal_bounded_effect", aw.ProposalBoundedEffect),
		fmt.Sprintf("- phase_plan_active: %t", aw.PhasePlanActive),
		nonEmptyAwarenessLine("phase_plan_id", aw.PhasePlanID),
		nonEmptyAwarenessLine("phase_plan_goal", aw.PhasePlanGoal),
		nonEmptyAwarenessLine("phase_plan_current_phase_id", aw.PhasePlanCurrentPhaseID),
		nonEmptyAwarenessLine("operation_phases", formatAwarenessList(aw.OperationPhases)),
		nonEmptyAwarenessLine("operation_findings", formatAwarenessList(aw.OperationFindings)),
		nonEmptyAwarenessLine("operation_artifacts", formatAwarenessList(aw.OperationArtifacts)),
		nonEmptyAwarenessLine("continuation_status", aw.ContinuationStatus),
		fmt.Sprintf("- continuation_active: %t", aw.ContinuationActive),
		nonEmptyAwarenessLine("continuation_persona_intent", aw.ContinuationPersonaIntent),
		nonEmptyAwarenessLine("continuation_persona_why", aw.ContinuationPersonaWhy),
		nonEmptyAwarenessLine("continuation_governor_intent", aw.ContinuationGovernorIntent),
		nonEmptyAwarenessLine("continuation_governor_why", aw.ContinuationGovernorWhy),
		fmt.Sprintf("- continuation_governor_ratified: %t", aw.ContinuationRatified),
		nonEmptyAwarenessLine("continuation_blocked_reason", aw.ContinuationBlockedReason),
		nonEmptyAwarenessLine("prompt_root", aw.PromptRoot),
		nonEmptyAwarenessLine("exec_root", aw.ExecRoot),
		nonEmptyAwarenessLine("shared_memory_root", aw.SharedMemoryRoot),
		nonEmptyAwarenessLine("user_workspace_root", aw.UserWorkspaceRoot),
		nonEmptyAwarenessLine("user_memory_root", aw.UserMemoryRoot),
		nonEmptyAwarenessLine("working_root", aw.WorkingRoot),
		nonEmptyAwarenessLine("sandbox_mode", aw.SandboxMode),
		nonEmptyAwarenessLine("network_policy", aw.NetworkPolicy),
	)
	return lines
}

func renderFaceAwarenessLines(aw RuntimeAwareness) []string {
	return []string{
		nonEmptyAwarenessLine("face_backend", aw.FaceBackend),
		nonEmptyAwarenessLine("face_provider", aw.FaceProvider),
		nonEmptyAwarenessLine("face_model", aw.FaceModel),
		nonEmptyAwarenessLine("persona_effort_recipe", aw.PersonaEffortRecipe),
		nonEmptyAwarenessLine("delivery_mode", aw.DeliveryMode),
		fmt.Sprintf("- stream_reply: %t", aw.StreamReply),
		fmt.Sprintf("- inbound_was_voice: %t", aw.InboundWasVoice),
		nonEmptyAwarenessLine("reply_modality_default", aw.ReplyModalityDefault),
		nonEmptyAwarenessLine("reply_modality_reason", aw.ReplyModalityReason),
		nonEmptyAwarenessLine("reply_modality_override", aw.ReplyModalityOverride),
	}
}

func nonEmptyAwarenessLine(key, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return fmt.Sprintf("- %s: %s", key, trimmed)
}

func compactLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func formatProviderPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	out := make([]string, 0, len(path))
	for _, segment := range path {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " -> ")
}

func awarenessHasAnyCategory(aw RuntimeAwareness, categories ...string) bool {
	wanted := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category != "" {
			wanted[category] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return false
	}
	for _, category := range aw.HiddenInputCategories {
		if _, ok := wanted[strings.TrimSpace(category)]; ok {
			return true
		}
	}
	return false
}

func formatAwarenessList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, ", ")
}
