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
	OperationFindings          []string
	OperationArtifacts         []string
	PromptRoot                 string
	ExecRoot                   string
	SharedMemoryRoot           string
	UserWorkspaceRoot          string
	UserMemoryRoot             string
	WorkingRoot                string
	SandboxMode                string
	NetworkPolicy              string
}

func renderGovernorRuntimeAwarenessBlock(aw RuntimeAwareness) string {
	lines := []string{"## Runtime Awareness"}
	lines = append(lines, nonEmptyAwarenessLine("session_kind", aw.SessionKind))
	lines = append(lines, nonEmptyAwarenessLine("run_kind", aw.RunKind))
	lines = append(lines, nonEmptyAwarenessLine("channel", aw.Channel))
	lines = append(lines, nonEmptyAwarenessLine("governor_backend", aw.GovernorBackend))
	lines = append(lines, nonEmptyAwarenessLine("governor_provider", aw.GovernorProvider))
	lines = append(lines, nonEmptyAwarenessLine("governor_model", aw.GovernorModel))
	if path := formatProviderPath(aw.GovernorProviderPath); path != "" {
		lines = append(lines, fmt.Sprintf("- configured_provider_path: %s", path))
	}
	lines = append(lines, nonEmptyAwarenessLine("active_provider", aw.ActiveProvider))
	lines = append(lines, fmt.Sprintf("- fallback_active: %t", aw.FallbackActive))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_effort", aw.ReasoningEffort))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_summary", aw.ReasoningSummary))
	lines = append(lines, nonEmptyAwarenessLine("governor_effort_recipe", aw.GovernorEffortRecipe))
	lines = append(lines, nonEmptyAwarenessLine("artifact_mode", aw.ArtifactMode))
	lines = append(lines, fmt.Sprintf("- brokerage_active: %t", aw.BrokerageActive))
	lines = append(lines, nonEmptyAwarenessLine("brokerage_phase", aw.BrokeragePhase))
	lines = append(lines, nonEmptyAwarenessLine("idolum_suggested_execution_contract", aw.SuggestedExecutionContract))
	lines = append(lines, nonEmptyAwarenessLine("brokerage_ratification", aw.BrokerageRatification))
	lines = append(lines, nonEmptyAwarenessLine("ratified_execution_contract", aw.RatifiedExecutionContract))
	lines = append(lines, nonEmptyAwarenessLine("signal_judgment", aw.SignalJudgment))
	lines = append(lines, fmt.Sprintf("- hidden_inputs_active: %t", aw.HiddenInputsActive))
	lines = append(lines, nonEmptyAwarenessLine("hidden_input_categories", formatAwarenessList(aw.HiddenInputCategories)))
	lines = append(lines, nonEmptyAwarenessLine("provenance_summary", aw.ProvenanceSummary))
	lines = append(lines, fmt.Sprintf("- plan_active: %t", aw.PlanActive))
	lines = append(lines, nonEmptyAwarenessLine("plan_summary", aw.PlanSummary))
	lines = append(lines, fmt.Sprintf("- operation_active: %t", aw.OperationActive))
	lines = append(lines, nonEmptyAwarenessLine("operation_objective", aw.OperationObjective))
	lines = append(lines, nonEmptyAwarenessLine("operation_status", aw.OperationStatus))
	lines = append(lines, nonEmptyAwarenessLine("operation_stage", aw.OperationStage))
	lines = append(lines, nonEmptyAwarenessLine("operation_summary", aw.OperationSummary))
	lines = append(lines, fmt.Sprintf("- proposal_active: %t", aw.ProposalActive))
	lines = append(lines, nonEmptyAwarenessLine("proposal_kind", aw.ProposalKind))
	lines = append(lines, nonEmptyAwarenessLine("proposal_status", aw.ProposalStatus))
	lines = append(lines, nonEmptyAwarenessLine("proposal_summary", aw.ProposalSummary))
	lines = append(lines, fmt.Sprintf("- media_attached: %t", aw.MediaAttached))
	lines = append(lines, nonEmptyAwarenessLine("media_mode", aw.MediaMode))
	lines = append(lines, nonEmptyAwarenessLine("prompt_root", aw.PromptRoot))
	lines = append(lines, nonEmptyAwarenessLine("exec_root", aw.ExecRoot))
	lines = append(lines, nonEmptyAwarenessLine("shared_memory_root", aw.SharedMemoryRoot))
	lines = append(lines, nonEmptyAwarenessLine("user_workspace_root", aw.UserWorkspaceRoot))
	lines = append(lines, nonEmptyAwarenessLine("user_memory_root", aw.UserMemoryRoot))
	lines = append(lines, nonEmptyAwarenessLine("working_root", aw.WorkingRoot))
	lines = append(lines, nonEmptyAwarenessLine("sandbox_mode", aw.SandboxMode))
	lines = append(lines, nonEmptyAwarenessLine("network_policy", aw.NetworkPolicy))
	return strings.Join(compactLines(lines), "\n")
}

func renderFaceAwarenessBlock(aw RuntimeAwareness, principalRole string, mode string) string {
	lines := []string{"## Delivery Awareness"}
	lines = append(lines, nonEmptyAwarenessLine("session_kind", aw.SessionKind))
	lines = append(lines, nonEmptyAwarenessLine("run_kind", aw.RunKind))
	lines = append(lines, nonEmptyAwarenessLine("channel", aw.Channel))
	lines = append(lines, nonEmptyAwarenessLine("principal_role", principalRole))
	lines = append(lines, nonEmptyAwarenessLine("mode", mode))
	lines = append(lines, nonEmptyAwarenessLine("governor_backend", aw.GovernorBackend))
	lines = append(lines, nonEmptyAwarenessLine("governor_provider", aw.GovernorProvider))
	lines = append(lines, nonEmptyAwarenessLine("governor_model", aw.GovernorModel))
	lines = append(lines, nonEmptyAwarenessLine("active_provider", aw.ActiveProvider))
	lines = append(lines, fmt.Sprintf("- fallback_active: %t", aw.FallbackActive))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_effort", aw.ReasoningEffort))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_summary", aw.ReasoningSummary))
	lines = append(lines, nonEmptyAwarenessLine("governor_effort_recipe", aw.GovernorEffortRecipe))
	lines = append(lines, nonEmptyAwarenessLine("artifact_mode", aw.ArtifactMode))
	lines = append(lines, fmt.Sprintf("- brokerage_active: %t", aw.BrokerageActive))
	lines = append(lines, nonEmptyAwarenessLine("brokerage_phase", aw.BrokeragePhase))
	lines = append(lines, nonEmptyAwarenessLine("idolum_suggested_execution_contract", aw.SuggestedExecutionContract))
	lines = append(lines, nonEmptyAwarenessLine("brokerage_ratification", aw.BrokerageRatification))
	lines = append(lines, nonEmptyAwarenessLine("ratified_execution_contract", aw.RatifiedExecutionContract))
	lines = append(lines, nonEmptyAwarenessLine("signal_judgment", aw.SignalJudgment))
	lines = append(lines, fmt.Sprintf("- hidden_inputs_active: %t", aw.HiddenInputsActive))
	lines = append(lines, nonEmptyAwarenessLine("hidden_input_categories", formatAwarenessList(aw.HiddenInputCategories)))
	lines = append(lines, nonEmptyAwarenessLine("provenance_summary", aw.ProvenanceSummary))
	lines = append(lines, nonEmptyAwarenessLine("face_backend", aw.FaceBackend))
	lines = append(lines, nonEmptyAwarenessLine("face_provider", aw.FaceProvider))
	lines = append(lines, nonEmptyAwarenessLine("face_model", aw.FaceModel))
	lines = append(lines, nonEmptyAwarenessLine("persona_effort_recipe", aw.PersonaEffortRecipe))
	lines = append(lines, nonEmptyAwarenessLine("delivery_mode", aw.DeliveryMode))
	lines = append(lines, fmt.Sprintf("- stream_reply: %t", aw.StreamReply))
	lines = append(lines, fmt.Sprintf("- proposal_active: %t", aw.ProposalActive))
	lines = append(lines, nonEmptyAwarenessLine("proposal_status", aw.ProposalStatus))
	lines = append(lines, fmt.Sprintf("- media_attached: %t", aw.MediaAttached))
	lines = append(lines, nonEmptyAwarenessLine("media_mode", aw.MediaMode))
	return strings.Join(compactLines(lines), "\n")
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
