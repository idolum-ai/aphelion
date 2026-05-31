//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/workspace"
)

const defaultPerceptionAccountingContextWindow = 128000

type turnPerceptionBudgetInput struct {
	RunKind       session.TurnRunKind
	HiddenInputs  hiddenInputSet
	PromptContext *workspace.PromptContext
	SystemBlocks  []agent.SystemBlock
	ExtraSystem   []agent.Message
	History       []agent.Message
	UserText      string
}

func buildTurnPerceptionBudgetContract(input turnPerceptionBudgetInput) memstore.PerceptionBudgetContract {
	layers := []memstore.PerceptionLayerRequest{
		{
			Name:            memstore.PerceptionLayerAuthority,
			Source:          "prompt.system_blocks",
			EpistemicStatus: memstore.PerceptionStatusBinding,
			EstimatedTokens: estimateSystemAuthorityTokens(input.SystemBlocks),
			Required:        true,
			AdmissionReason: "assembled_governor_system_blocks",
		},
		{
			Name:            memstore.PerceptionLayerCurrentInput,
			Source:          "turn.user_text",
			EpistemicStatus: memstore.PerceptionStatusCurrent,
			Text:            input.UserText,
			Required:        true,
			AdmissionReason: "prepared_current_user_input",
		},
	}

	layers = append(layers, workspaceMemoryPerceptionLayers(input.PromptContext)...)
	layers = append(layers, systemMessagePerceptionLayers(input.ExtraSystem)...)
	if historyTokens := estimateHistoryTokens(input.History); historyTokens > 0 {
		layers = append(layers, memstore.PerceptionLayerRequest{
			Name:            memstore.PerceptionLayerRecentSession,
			Source:          "session.history",
			EpistemicStatus: memstore.PerceptionStatusCurated,
			EstimatedTokens: historyTokens,
			AdmissionReason: "session_history_in_turn_input",
		})
	}
	if toolTokens := estimateHistoryToolEvidenceTokens(input.History); toolTokens > 0 {
		layers = append(layers, memstore.PerceptionLayerRequest{
			Name:            memstore.PerceptionLayerToolEvidence,
			Source:          "session.history.tool_messages",
			EpistemicStatus: memstore.PerceptionStatusObserved,
			EstimatedTokens: toolTokens,
			AdmissionReason: "tool_evidence_present_in_session_history",
		})
	}

	return memstore.BuildPerceptionBudgetContract(memstore.PerceptionBudgetRequest{
		Posture:         perceptionPostureForTurn(input.RunKind, input.HiddenInputs),
		ContextWindow:   defaultPerceptionAccountingContextWindow,
		MaxContextRatio: 0.75,
		Layers:          layers,
	})
}

func perceptionPostureForTurn(runKind session.TurnRunKind, hidden hiddenInputSet) memstore.PerceptionPosture {
	if hidden.ReflectiveOutreachEligible() {
		return memstore.PerceptionPostureDurableGoal
	}
	switch runKind {
	case session.TurnRunKindDoctor, session.TurnRunKindRecovery:
		return memstore.PerceptionPostureDiagnostic
	case session.TurnRunKindHeartbeat, session.TurnRunKindCron:
		return memstore.PerceptionPostureReflective
	default:
		return memstore.PerceptionPostureImplementation
	}
}

func perceptionBudgetExecutionPayload(contract memstore.PerceptionBudgetContract) map[string]any {
	return map[string]any{
		"perception_posture":                   string(contract.Posture),
		"perception_total_budget_tokens":       contract.TotalBudgetTokens,
		"perception_total_estimated_tokens":    contract.TotalEstimatedTokens,
		"perception_memory_budget_tokens":      contract.MemoryBudgetTokens,
		"perception_memory_estimated_tokens":   contract.MemoryEstimatedTokens,
		"perception_current_input_tokens":      contract.CurrentInputTokens,
		"perception_tool_evidence_tokens":      contract.ToolEvidenceTokens,
		"perception_remaining_headroom_tokens": contract.RemainingHeadroomTokens,
		"perception_admitted_layers":           perceptionLayerNames(contract.Admitted),
		"perception_suppressed_layers":         perceptionSuppressedLayerNames(contract.Suppressed),
		"perception_risks":                     append([]string(nil), contract.Risks...),
	}
}

func mergePerceptionBudgetPayload(payload map[string]any, contract memstore.PerceptionBudgetContract) map[string]any {
	if payload == nil {
		payload = make(map[string]any)
	}
	for key, value := range perceptionBudgetExecutionPayload(contract) {
		payload[key] = value
	}
	return payload
}

func estimateSystemAuthorityTokens(blocks []agent.SystemBlock) int {
	var total int
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" || strings.HasPrefix(text, "## Dynamic Workspace Files") {
			continue
		}
		total += memstore.EstimatePerceptionTokens(text)
	}
	return total
}

func workspaceMemoryPerceptionLayers(ctx *workspace.PromptContext) []memstore.PerceptionLayerRequest {
	if ctx == nil {
		return nil
	}
	files := append([]workspace.LoadedFile(nil), ctx.Stable...)
	files = append(files, ctx.Dynamic...)
	layers := make([]memstore.PerceptionLayerRequest, 0, len(files))
	for _, file := range files {
		name, ok := memoryLayerForWorkspaceFile(file.Path)
		if !ok {
			continue
		}
		layers = append(layers, memstore.PerceptionLayerRequest{
			Name:            name,
			Source:          strings.TrimSpace(file.Path),
			EpistemicStatus: epistemicStatusForMemoryLayer(name),
			Text:            file.Content,
			AdmissionReason: "workspace_memory_file_loaded_for_prompt",
		})
	}
	return layers
}

func memoryLayerForWorkspaceFile(path string) (memstore.PerceptionLayerName, bool) {
	path = strings.ToLower(strings.TrimSpace(path))
	switch path {
	case "memory/rhizome.md":
		return memstore.PerceptionLayerRhizome, true
	case "memory/dreams.md":
		return memstore.PerceptionLayerDreams, true
	case "memory.md":
		return memstore.PerceptionLayerCuratedMemory, true
	}
	if strings.HasPrefix(path, "memory/") {
		return memstore.PerceptionLayerCuratedMemory, true
	}
	return "", false
}

func epistemicStatusForMemoryLayer(name memstore.PerceptionLayerName) memstore.PerceptionEpistemicStatus {
	switch name {
	case memstore.PerceptionLayerRhizome:
		return memstore.PerceptionStatusMotif
	case memstore.PerceptionLayerDreams:
		return memstore.PerceptionStatusHypothesis
	default:
		return memstore.PerceptionStatusCurated
	}
}

func systemMessagePerceptionLayers(messages []agent.Message) []memstore.PerceptionLayerRequest {
	layers := make([]memstore.PerceptionLayerRequest, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != "system" {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		name := memstore.PerceptionLayerAuthority
		status := memstore.PerceptionStatusBinding
		reason := "extra_system_context"
		if strings.Contains(content, "## Semantic Memory Recall") || strings.Contains(content, "Semantic Memory Recall") {
			name = memstore.PerceptionLayerSemanticRecall
			status = memstore.PerceptionStatusRecalled
			reason = "semantic_recall_prefetch_system_message"
		}
		layers = append(layers, memstore.PerceptionLayerRequest{
			Name:            name,
			Source:          "turn.extra_system_message",
			EpistemicStatus: status,
			Text:            content,
			AdmissionReason: reason,
		})
	}
	return layers
}

func estimateHistoryTokens(messages []agent.Message) int {
	var total int
	for _, msg := range messages {
		if msg.Role == "tool" {
			continue
		}
		total += memstore.EstimatePerceptionTokens(msg.Content)
	}
	return total
}

func estimateHistoryToolEvidenceTokens(messages []agent.Message) int {
	var total int
	for _, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		total += memstore.EstimatePerceptionTokens(msg.Content)
	}
	return total
}

func perceptionLayerNames(layers []memstore.PerceptionLayerAccounting) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, string(layer.Name))
	}
	return out
}

func perceptionSuppressedLayerNames(layers []memstore.SuppressedPerceptionLayer) []string {
	out := make([]string, 0, len(layers))
	for _, layer := range layers {
		out = append(out, string(layer.Name)+":"+strings.TrimSpace(layer.Reason))
	}
	return out
}
