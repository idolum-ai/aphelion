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
	PlanEvents                 []string
	OperationActive            bool
	OperationObjective         string
	OperationStatus            string
	OperationStage             string
	OperationSummary           string
	OperationDigest            []string
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

type awarenessRole string

const (
	awarenessRoleGovernor awarenessRole = "governor"
	awarenessRoleFace     awarenessRole = "face"
)

type awarenessField struct {
	key    string
	render func(RuntimeAwareness) string
}

var sharedStableAwarenessFields = []awarenessField{
	stringAwarenessField("session_kind", func(aw RuntimeAwareness) string { return aw.SessionKind }),
	stringAwarenessField("run_kind", func(aw RuntimeAwareness) string { return aw.RunKind }),
	stringAwarenessField("channel", func(aw RuntimeAwareness) string { return aw.Channel }),
	stringAwarenessField("event_origin", func(aw RuntimeAwareness) string { return aw.EventOrigin }),
	stringAwarenessField("artifact_mode", func(aw RuntimeAwareness) string { return aw.ArtifactMode }),
}

var sharedTurnAwarenessFields = []awarenessField{
	stringAwarenessField("active_provider", func(aw RuntimeAwareness) string { return aw.ActiveProvider }),
	boolAwarenessField("fallback_active", func(aw RuntimeAwareness) bool { return aw.FallbackActive }),
	boolAwarenessField("hidden_inputs_active", func(aw RuntimeAwareness) bool { return aw.HiddenInputsActive }),
	listAwarenessField("hidden_input_categories", func(aw RuntimeAwareness) []string { return aw.HiddenInputCategories }),
	stringAwarenessField("provenance_summary", func(aw RuntimeAwareness) string { return aw.ProvenanceSummary }),
	boolAwarenessField("plan_active", func(aw RuntimeAwareness) bool { return aw.PlanActive }),
	stringAwarenessField("plan_summary", func(aw RuntimeAwareness) string { return aw.PlanSummary }),
	listAwarenessField("plan_events", func(aw RuntimeAwareness) []string { return aw.PlanEvents }),
	boolAwarenessField("operation_active", func(aw RuntimeAwareness) bool { return aw.OperationActive }),
	stringAwarenessField("operation_objective", func(aw RuntimeAwareness) string { return aw.OperationObjective }),
	stringAwarenessField("operation_status", func(aw RuntimeAwareness) string { return aw.OperationStatus }),
	stringAwarenessField("operation_stage", func(aw RuntimeAwareness) string { return aw.OperationStage }),
	stringAwarenessField("operation_summary", func(aw RuntimeAwareness) string { return aw.OperationSummary }),
	listAwarenessField("operation_digest", func(aw RuntimeAwareness) []string { return aw.OperationDigest }),
	boolAwarenessField("media_attached", func(aw RuntimeAwareness) bool { return aw.MediaAttached }),
	stringAwarenessField("media_mode", func(aw RuntimeAwareness) string { return aw.MediaMode }),
}

var governorAwarenessFields = []awarenessField{
	stringAwarenessField("turn_authorization_kind", func(aw RuntimeAwareness) string { return aw.TurnAuthorizationKind }),
	stringAwarenessField("governor_backend", func(aw RuntimeAwareness) string { return aw.GovernorBackend }),
	stringAwarenessField("governor_provider", func(aw RuntimeAwareness) string { return aw.GovernorProvider }),
	stringAwarenessField("governor_model", func(aw RuntimeAwareness) string { return aw.GovernorModel }),
	{
		key: "configured_provider_path",
		render: func(aw RuntimeAwareness) string {
			if path := formatProviderPath(aw.GovernorProviderPath); path != "" {
				return fmt.Sprintf("- configured_provider_path: %s", path)
			}
			return ""
		},
	},
	stringAwarenessField("reasoning_effort", func(aw RuntimeAwareness) string { return aw.ReasoningEffort }),
	stringAwarenessField("reasoning_summary", func(aw RuntimeAwareness) string { return aw.ReasoningSummary }),
	stringAwarenessField("governor_effort_recipe", func(aw RuntimeAwareness) string { return aw.GovernorEffortRecipe }),
	boolAwarenessField("brokerage_active", func(aw RuntimeAwareness) bool { return aw.BrokerageActive }),
	stringAwarenessField("brokerage_phase", func(aw RuntimeAwareness) string { return aw.BrokeragePhase }),
	stringAwarenessField("idolum_suggested_execution_contract", func(aw RuntimeAwareness) string { return aw.SuggestedExecutionContract }),
	stringAwarenessField("brokerage_ratification", func(aw RuntimeAwareness) string { return aw.BrokerageRatification }),
	stringAwarenessField("ratified_execution_contract", func(aw RuntimeAwareness) string { return aw.RatifiedExecutionContract }),
	stringAwarenessField("signal_judgment", func(aw RuntimeAwareness) string { return aw.SignalJudgment }),
	boolAwarenessField("proposal_active", func(aw RuntimeAwareness) bool { return aw.ProposalActive }),
	stringAwarenessField("proposal_kind", func(aw RuntimeAwareness) string { return aw.ProposalKind }),
	stringAwarenessField("proposal_status", func(aw RuntimeAwareness) string { return aw.ProposalStatus }),
	stringAwarenessField("proposal_summary", func(aw RuntimeAwareness) string { return aw.ProposalSummary }),
	stringAwarenessField("proposal_why_now", func(aw RuntimeAwareness) string { return aw.ProposalWhyNow }),
	stringAwarenessField("proposal_bounded_effect", func(aw RuntimeAwareness) string { return aw.ProposalBoundedEffect }),
	boolAwarenessField("phase_plan_active", func(aw RuntimeAwareness) bool { return aw.PhasePlanActive }),
	stringAwarenessField("phase_plan_id", func(aw RuntimeAwareness) string { return aw.PhasePlanID }),
	stringAwarenessField("phase_plan_goal", func(aw RuntimeAwareness) string { return aw.PhasePlanGoal }),
	stringAwarenessField("phase_plan_current_phase_id", func(aw RuntimeAwareness) string { return aw.PhasePlanCurrentPhaseID }),
	listAwarenessField("operation_phases", func(aw RuntimeAwareness) []string { return aw.OperationPhases }),
	listAwarenessField("operation_findings", func(aw RuntimeAwareness) []string { return aw.OperationFindings }),
	listAwarenessField("operation_artifacts", func(aw RuntimeAwareness) []string { return aw.OperationArtifacts }),
	stringAwarenessField("continuation_status", func(aw RuntimeAwareness) string { return aw.ContinuationStatus }),
	boolAwarenessField("continuation_active", func(aw RuntimeAwareness) bool { return aw.ContinuationActive }),
	stringAwarenessField("continuation_persona_intent", func(aw RuntimeAwareness) string { return aw.ContinuationPersonaIntent }),
	stringAwarenessField("continuation_persona_why", func(aw RuntimeAwareness) string { return aw.ContinuationPersonaWhy }),
	stringAwarenessField("continuation_governor_intent", func(aw RuntimeAwareness) string { return aw.ContinuationGovernorIntent }),
	stringAwarenessField("continuation_governor_why", func(aw RuntimeAwareness) string { return aw.ContinuationGovernorWhy }),
	boolAwarenessField("continuation_governor_ratified", func(aw RuntimeAwareness) bool { return aw.ContinuationRatified }),
	stringAwarenessField("continuation_blocked_reason", func(aw RuntimeAwareness) string { return aw.ContinuationBlockedReason }),
	stringAwarenessField("prompt_root", func(aw RuntimeAwareness) string { return aw.PromptRoot }),
	stringAwarenessField("exec_root", func(aw RuntimeAwareness) string { return aw.ExecRoot }),
	stringAwarenessField("shared_memory_root", func(aw RuntimeAwareness) string { return aw.SharedMemoryRoot }),
	stringAwarenessField("user_workspace_root", func(aw RuntimeAwareness) string { return aw.UserWorkspaceRoot }),
	stringAwarenessField("user_memory_root", func(aw RuntimeAwareness) string { return aw.UserMemoryRoot }),
	stringAwarenessField("working_root", func(aw RuntimeAwareness) string { return aw.WorkingRoot }),
	stringAwarenessField("sandbox_mode", func(aw RuntimeAwareness) string { return aw.SandboxMode }),
	stringAwarenessField("network_policy", func(aw RuntimeAwareness) string { return aw.NetworkPolicy }),
}

var faceAwarenessFields = []awarenessField{
	stringAwarenessField("face_backend", func(aw RuntimeAwareness) string { return aw.FaceBackend }),
	stringAwarenessField("face_provider", func(aw RuntimeAwareness) string { return aw.FaceProvider }),
	stringAwarenessField("face_model", func(aw RuntimeAwareness) string { return aw.FaceModel }),
	stringAwarenessField("persona_effort_recipe", func(aw RuntimeAwareness) string { return aw.PersonaEffortRecipe }),
	stringAwarenessField("delivery_mode", func(aw RuntimeAwareness) string { return aw.DeliveryMode }),
	boolAwarenessField("stream_reply", func(aw RuntimeAwareness) bool { return aw.StreamReply }),
	boolAwarenessField("inbound_was_voice", func(aw RuntimeAwareness) bool { return aw.InboundWasVoice }),
	stringAwarenessField("reply_modality_default", func(aw RuntimeAwareness) string { return aw.ReplyModalityDefault }),
	stringAwarenessField("reply_modality_reason", func(aw RuntimeAwareness) string { return aw.ReplyModalityReason }),
	stringAwarenessField("reply_modality_override", func(aw RuntimeAwareness) string { return aw.ReplyModalityOverride }),
}

func stringAwarenessField(key string, value func(RuntimeAwareness) string) awarenessField {
	return awarenessField{
		key: key,
		render: func(aw RuntimeAwareness) string {
			return nonEmptyAwarenessLine(key, value(aw))
		},
	}
}

func boolAwarenessField(key string, value func(RuntimeAwareness) bool) awarenessField {
	return awarenessField{
		key: key,
		render: func(aw RuntimeAwareness) string {
			return fmt.Sprintf("- %s: %t", key, value(aw))
		},
	}
}

func listAwarenessField(key string, value func(RuntimeAwareness) []string) awarenessField {
	return awarenessField{
		key: key,
		render: func(aw RuntimeAwareness) string {
			return nonEmptyAwarenessLine(key, formatAwarenessList(value(aw)))
		},
	}
}

func renderGovernorRuntimeAwarenessBlock(aw RuntimeAwareness) string {
	return renderRuntimeAwarenessBlock(aw, awarenessRoleGovernor, "## Runtime Awareness")
}

func renderFaceAwarenessBlock(aw RuntimeAwareness) string {
	return renderRuntimeAwarenessBlock(aw, awarenessRoleFace, "## Delivery Awareness")
}

func renderRuntimeAwarenessBlock(aw RuntimeAwareness, role awarenessRole, heading string) string {
	lines := []string{heading}
	lines = appendAwarenessSection(lines, "Shared Stable Facts", renderSharedStableAwarenessLines(aw))
	lines = appendAwarenessSection(lines, "Shared Turn State", renderSharedTurnAwarenessLines(aw))
	switch role {
	case awarenessRoleFace:
		lines = appendAwarenessSection(lines, "Face Delta", renderFaceAwarenessLines(aw))
	default:
		lines = appendAwarenessSection(lines, "Governor Delta", renderGovernorAwarenessLines(aw))
	}
	return strings.Join(compactLines(lines), "\n")
}

func renderSharedAwarenessLines(aw RuntimeAwareness) []string {
	lines := renderSharedStableAwarenessLines(aw)
	lines = append(lines, renderSharedTurnAwarenessLines(aw)...)
	return lines
}

func renderSharedStableAwarenessLines(aw RuntimeAwareness) []string {
	return renderAwarenessFields(sharedStableAwarenessFields, aw)
}

func renderSharedTurnAwarenessLines(aw RuntimeAwareness) []string {
	return renderAwarenessFields(sharedTurnAwarenessFields, aw)
}

func appendAwarenessSection(lines []string, title string, section []string) []string {
	section = compactLines(section)
	if len(section) == 0 {
		return lines
	}
	lines = append(lines, "### "+strings.TrimSpace(title))
	return append(lines, section...)
}

func renderGovernorAwarenessLines(aw RuntimeAwareness) []string {
	return renderAwarenessFields(governorAwarenessFields, aw)
}

func renderFaceAwarenessLines(aw RuntimeAwareness) []string {
	return renderAwarenessFields(faceAwarenessFields, aw)
}

func renderAwarenessFields(fields []awarenessField, aw RuntimeAwareness) []string {
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		lines = append(lines, field.render(aw))
	}
	return lines
}

func awarenessRoleLineKeys(role awarenessRole) []string {
	keys := append([]string{}, awarenessFieldKeys(sharedStableAwarenessFields)...)
	keys = append(keys, awarenessFieldKeys(sharedTurnAwarenessFields)...)
	switch role {
	case awarenessRoleFace:
		keys = append(keys, awarenessFieldKeys(faceAwarenessFields)...)
	default:
		keys = append(keys, awarenessFieldKeys(governorAwarenessFields)...)
	}
	return keys
}

func awarenessRoleExcludedLineKeys(role awarenessRole) []string {
	switch role {
	case awarenessRoleFace:
		return awarenessFieldKeys(governorAwarenessFields)
	default:
		return awarenessFieldKeys(faceAwarenessFields)
	}
}

func awarenessFieldKeys(fields []awarenessField) []string {
	keys := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		key := strings.TrimSpace(field.key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
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
