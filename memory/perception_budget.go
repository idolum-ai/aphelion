//go:build linux

package memory

import (
	"fmt"
	"sort"
	"strings"
)

const perceptionBudgetCharsPerToken = 4

// PerceptionPosture names the context-shaping posture used before model
// inference. It is intentionally small: posture should move memory closer or
// farther without weakening authority or provenance labels.
type PerceptionPosture string

const (
	PerceptionPostureImplementation PerceptionPosture = "implementation"
	PerceptionPostureRepair         PerceptionPosture = "repair"
	PerceptionPostureReflective     PerceptionPosture = "reflective"
	PerceptionPostureDurableGoal    PerceptionPosture = "durable_goal"
	PerceptionPostureDiagnostic     PerceptionPosture = "diagnostic"
)

// PerceptionLayerName is a typed context layer admitted to or suppressed from
// the assembled inference field.
type PerceptionLayerName string

const (
	PerceptionLayerAuthority       PerceptionLayerName = "authority"
	PerceptionLayerCurrentInput    PerceptionLayerName = "current_input"
	PerceptionLayerToolEvidence    PerceptionLayerName = "tool_evidence"
	PerceptionLayerRecentSession   PerceptionLayerName = "recent_session"
	PerceptionLayerCuratedMemory   PerceptionLayerName = "curated_memory"
	PerceptionLayerSemanticRecall  PerceptionLayerName = "semantic_recall"
	PerceptionLayerRhizome         PerceptionLayerName = "rhizome"
	PerceptionLayerDreams          PerceptionLayerName = "dreams"
	PerceptionLayerImportedArchive PerceptionLayerName = "imported_archive"
	PerceptionLayerUnknown         PerceptionLayerName = "unknown"
)

// PerceptionEpistemicStatus describes what kind of claim a layer may carry once
// admitted. It is not an authority grant.
type PerceptionEpistemicStatus string

const (
	PerceptionStatusBinding    PerceptionEpistemicStatus = "binding"
	PerceptionStatusObserved   PerceptionEpistemicStatus = "observed"
	PerceptionStatusCurrent    PerceptionEpistemicStatus = "current"
	PerceptionStatusCurated    PerceptionEpistemicStatus = "curated"
	PerceptionStatusRecalled   PerceptionEpistemicStatus = "recalled"
	PerceptionStatusImported   PerceptionEpistemicStatus = "imported"
	PerceptionStatusMotif      PerceptionEpistemicStatus = "motif"
	PerceptionStatusHypothesis PerceptionEpistemicStatus = "hypothesis"
)

// PerceptionLayerRequest describes a candidate context layer before admission.
type PerceptionLayerRequest struct {
	Name            PerceptionLayerName
	Source          string
	EpistemicStatus PerceptionEpistemicStatus
	Text            string
	EstimatedTokens int
	MaxTokens       int
	Required        bool
	AdmissionReason string
	ImportState     SemanticImportState
}

// PerceptionBudgetRequest contains the posture, context ceiling, and candidate
// layers for a measurable pre-inference context shape decision.
type PerceptionBudgetRequest struct {
	Posture         PerceptionPosture
	ContextWindow   int
	MaxContextRatio float64
	Layers          []PerceptionLayerRequest
}

// PerceptionLayerAccounting is the admitted view of a context layer.
type PerceptionLayerAccounting struct {
	Name            PerceptionLayerName
	Source          string
	EpistemicStatus PerceptionEpistemicStatus
	EstimatedTokens int
	MaxTokens       int
	AdmissionReason string
	Priority        int
	MemoryLayer     bool
	LowAuthority    bool
}

// SuppressedPerceptionLayer records a context layer that was intentionally kept
// out of the assembled inference field.
type SuppressedPerceptionLayer struct {
	Name            PerceptionLayerName
	Source          string
	EpistemicStatus PerceptionEpistemicStatus
	EstimatedTokens int
	Reason          string
}

// PerceptionBudgetContract is the testable accounting artifact for one context
// assembly posture. It says which layers entered, which were suppressed, and why.
type PerceptionBudgetContract struct {
	Posture                 PerceptionPosture
	ContextWindow           int
	MaxContextRatio         float64
	TotalBudgetTokens       int
	MemoryBudgetTokens      int
	TotalEstimatedTokens    int
	MemoryEstimatedTokens   int
	CurrentInputTokens      int
	ToolEvidenceTokens      int
	RemainingHeadroomTokens int
	Admitted                []PerceptionLayerAccounting
	Suppressed              []SuppressedPerceptionLayer
	Attestations            []string
	Risks                   []string
}

// BuildPerceptionBudgetContract admits and suppresses context layers according
// to posture, budget, provenance, and low-authority motif rules. It measures the
// context shape; it does not decide model behavior or grant authority.
func BuildPerceptionBudgetContract(req PerceptionBudgetRequest) PerceptionBudgetContract {
	posture := normalizePerceptionPosture(req.Posture)
	contextWindow := req.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	ratio := req.MaxContextRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 0.75
	}
	totalBudget := int(float64(contextWindow) * ratio)
	if totalBudget <= 0 {
		totalBudget = contextWindow
	}
	memoryBudget := int(float64(totalBudget) * postureMemoryFraction(posture))
	if memoryBudget < 1 {
		memoryBudget = 1
	}

	candidates := normalizePerceptionLayerRequests(req.Layers)
	stableSortPerceptionCandidates(posture, candidates)

	contract := PerceptionBudgetContract{
		Posture:            posture,
		ContextWindow:      contextWindow,
		MaxContextRatio:    ratio,
		TotalBudgetTokens:  totalBudget,
		MemoryBudgetTokens: memoryBudget,
	}

	for _, layer := range candidates {
		if suppressReason, suppressed := perceptionSuppressionReason(posture, layer, contract); suppressed {
			contract.Suppressed = append(contract.Suppressed, SuppressedPerceptionLayer{
				Name:            layer.Name,
				Source:          strings.TrimSpace(layer.Source),
				EpistemicStatus: layer.EpistemicStatus,
				EstimatedTokens: layer.EstimatedTokens,
				Reason:          suppressReason,
			})
			continue
		}

		priority := perceptionLayerPriority(posture, layer)
		accounting := PerceptionLayerAccounting{
			Name:            layer.Name,
			Source:          strings.TrimSpace(layer.Source),
			EpistemicStatus: layer.EpistemicStatus,
			EstimatedTokens: layer.EstimatedTokens,
			MaxTokens:       layer.MaxTokens,
			AdmissionReason: firstNonEmpty(strings.TrimSpace(layer.AdmissionReason), defaultPerceptionAdmissionReason(posture, layer)),
			Priority:        priority,
			MemoryLayer:     perceptionMemoryLayer(layer.Name),
			LowAuthority:    perceptionLowAuthority(layer),
		}
		contract.Admitted = append(contract.Admitted, accounting)
		contract.TotalEstimatedTokens += layer.EstimatedTokens
		if accounting.MemoryLayer {
			contract.MemoryEstimatedTokens += layer.EstimatedTokens
		}
		switch layer.Name {
		case PerceptionLayerCurrentInput:
			contract.CurrentInputTokens += layer.EstimatedTokens
		case PerceptionLayerToolEvidence:
			contract.ToolEvidenceTokens += layer.EstimatedTokens
		}
	}

	contract.RemainingHeadroomTokens = totalBudget - contract.TotalEstimatedTokens
	contract.Attestations = perceptionAttestations(contract)
	contract.Risks = perceptionRisks(contract)
	return contract
}

func normalizePerceptionPosture(posture PerceptionPosture) PerceptionPosture {
	switch PerceptionPosture(strings.ToLower(strings.TrimSpace(string(posture)))) {
	case PerceptionPostureRepair:
		return PerceptionPostureRepair
	case PerceptionPostureReflective:
		return PerceptionPostureReflective
	case PerceptionPostureDurableGoal:
		return PerceptionPostureDurableGoal
	case PerceptionPostureDiagnostic:
		return PerceptionPostureDiagnostic
	default:
		return PerceptionPostureImplementation
	}
}

func normalizePerceptionLayerRequests(layers []PerceptionLayerRequest) []PerceptionLayerRequest {
	out := make([]PerceptionLayerRequest, 0, len(layers))
	for _, layer := range layers {
		layer.Name = normalizePerceptionLayerName(layer.Name)
		layer.Source = strings.TrimSpace(layer.Source)
		if layer.EpistemicStatus == "" {
			layer.EpistemicStatus = defaultPerceptionEpistemicStatus(layer.Name)
		}
		if layer.EstimatedTokens <= 0 {
			layer.EstimatedTokens = EstimatePerceptionTokens(layer.Text)
		}
		if layer.MaxTokens > 0 && layer.EstimatedTokens > layer.MaxTokens {
			layer.EstimatedTokens = layer.MaxTokens
		}
		out = append(out, layer)
	}
	return out
}

func normalizePerceptionLayerName(name PerceptionLayerName) PerceptionLayerName {
	switch PerceptionLayerName(strings.ToLower(strings.TrimSpace(string(name)))) {
	case PerceptionLayerAuthority:
		return PerceptionLayerAuthority
	case PerceptionLayerCurrentInput:
		return PerceptionLayerCurrentInput
	case PerceptionLayerToolEvidence:
		return PerceptionLayerToolEvidence
	case PerceptionLayerRecentSession:
		return PerceptionLayerRecentSession
	case PerceptionLayerCuratedMemory:
		return PerceptionLayerCuratedMemory
	case PerceptionLayerSemanticRecall:
		return PerceptionLayerSemanticRecall
	case PerceptionLayerRhizome:
		return PerceptionLayerRhizome
	case PerceptionLayerDreams:
		return PerceptionLayerDreams
	case PerceptionLayerImportedArchive:
		return PerceptionLayerImportedArchive
	case PerceptionLayerUnknown:
		return PerceptionLayerUnknown
	default:
		return PerceptionLayerUnknown
	}
}

func defaultPerceptionEpistemicStatus(name PerceptionLayerName) PerceptionEpistemicStatus {
	switch name {
	case PerceptionLayerAuthority:
		return PerceptionStatusBinding
	case PerceptionLayerToolEvidence:
		return PerceptionStatusObserved
	case PerceptionLayerCurrentInput:
		return PerceptionStatusCurrent
	case PerceptionLayerCuratedMemory, PerceptionLayerRecentSession:
		return PerceptionStatusCurated
	case PerceptionLayerSemanticRecall:
		return PerceptionStatusRecalled
	case PerceptionLayerRhizome:
		return PerceptionStatusMotif
	case PerceptionLayerDreams:
		return PerceptionStatusHypothesis
	case PerceptionLayerImportedArchive:
		return PerceptionStatusImported
	case PerceptionLayerUnknown:
		return PerceptionStatusHypothesis
	default:
		return PerceptionStatusHypothesis
	}
}

func EstimatePerceptionTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	tokens := (len([]rune(text)) + perceptionBudgetCharsPerToken - 1) / perceptionBudgetCharsPerToken
	if tokens < 1 {
		return 1
	}
	return tokens
}

func stableSortPerceptionCandidates(posture PerceptionPosture, layers []PerceptionLayerRequest) {
	sort.SliceStable(layers, func(i, j int) bool {
		li := layers[i]
		lj := layers[j]
		pi := perceptionLayerPriority(posture, li)
		pj := perceptionLayerPriority(posture, lj)
		if li.Required != lj.Required {
			return li.Required
		}
		if pi != pj {
			return pi > pj
		}
		if li.Name != lj.Name {
			return li.Name < lj.Name
		}
		return li.Source < lj.Source
	})
}

func perceptionSuppressionReason(posture PerceptionPosture, layer PerceptionLayerRequest, contract PerceptionBudgetContract) (string, bool) {
	if layer.Name == PerceptionLayerUnknown {
		return "unknown_layer", true
	}
	if layer.Name == PerceptionLayerImportedArchive && layer.ImportState != SemanticImportStateApproved {
		return "import_state_not_approved", true
	}
	if layer.ImportState == SemanticImportStateQuarantine || layer.ImportState == SemanticImportStateRejected {
		return "import_state_not_approved", true
	}
	if !postureAllowsMotifs(posture) && perceptionMotifLayer(layer.Name) && !layer.Required {
		return "posture_precision_suppresses_motifs", true
	}
	if perceptionMemoryLayer(layer.Name) && contract.MemoryEstimatedTokens+layer.EstimatedTokens > contract.MemoryBudgetTokens && !layer.Required {
		return "memory_budget_cap", true
	}
	if contract.TotalEstimatedTokens+layer.EstimatedTokens > contract.TotalBudgetTokens && !layer.Required {
		return "total_budget_cap", true
	}
	return "", false
}

func postureAllowsMotifs(posture PerceptionPosture) bool {
	switch posture {
	case PerceptionPostureReflective, PerceptionPostureDurableGoal, PerceptionPostureDiagnostic:
		return true
	default:
		return false
	}
}

func postureMemoryFraction(posture PerceptionPosture) float64 {
	switch posture {
	case PerceptionPostureDiagnostic:
		return 0.40
	case PerceptionPostureReflective, PerceptionPostureDurableGoal:
		return 0.30
	case PerceptionPostureRepair:
		return 0.12
	default:
		return 0.15
	}
}

func perceptionLayerPriority(posture PerceptionPosture, layer PerceptionLayerRequest) int {
	if layer.Required {
		return 1000
	}
	switch layer.Name {
	case PerceptionLayerAuthority:
		return 950
	case PerceptionLayerCurrentInput:
		return 900
	case PerceptionLayerToolEvidence:
		return 880
	case PerceptionLayerRecentSession:
		return 700
	case PerceptionLayerCuratedMemory:
		return 620
	case PerceptionLayerSemanticRecall:
		return 520
	case PerceptionLayerRhizome, PerceptionLayerDreams:
		if posture == PerceptionPostureDurableGoal {
			return 650
		}
		if posture == PerceptionPostureReflective {
			return 630
		}
		if posture == PerceptionPostureDiagnostic {
			return 360
		}
		return 120
	case PerceptionLayerImportedArchive:
		return 260
	default:
		return 100
	}
}

func perceptionMemoryLayer(name PerceptionLayerName) bool {
	switch name {
	case PerceptionLayerCuratedMemory, PerceptionLayerSemanticRecall, PerceptionLayerRhizome, PerceptionLayerDreams, PerceptionLayerImportedArchive:
		return true
	default:
		return false
	}
}

func perceptionMotifLayer(name PerceptionLayerName) bool {
	return name == PerceptionLayerRhizome || name == PerceptionLayerDreams
}

func perceptionLowAuthority(layer PerceptionLayerRequest) bool {
	return perceptionMotifLayer(layer.Name) || layer.EpistemicStatus == PerceptionStatusMotif || layer.EpistemicStatus == PerceptionStatusHypothesis || layer.EpistemicStatus == PerceptionStatusRecalled || layer.EpistemicStatus == PerceptionStatusImported
}

func defaultPerceptionAdmissionReason(posture PerceptionPosture, layer PerceptionLayerRequest) string {
	switch layer.Name {
	case PerceptionLayerAuthority:
		return "binding_authority_floor"
	case PerceptionLayerCurrentInput:
		return "live_user_input_priority"
	case PerceptionLayerToolEvidence:
		return "fresh_tool_evidence_priority"
	case PerceptionLayerRhizome, PerceptionLayerDreams:
		if posture == PerceptionPostureDurableGoal {
			return "durable_goal_continuity_signal"
		}
		return "reflective_associative_continuity"
	default:
		return "posture_relevant_context"
	}
}

func perceptionAttestations(contract PerceptionBudgetContract) []string {
	attestations := []string{
		fmt.Sprintf("posture=%s", contract.Posture),
		fmt.Sprintf("total_budget_tokens=%d", contract.TotalBudgetTokens),
		fmt.Sprintf("memory_budget_tokens=%d", contract.MemoryBudgetTokens),
		fmt.Sprintf("total_estimated_tokens=%d", contract.TotalEstimatedTokens),
		fmt.Sprintf("memory_estimated_tokens=%d", contract.MemoryEstimatedTokens),
		fmt.Sprintf("remaining_headroom_tokens=%d", contract.RemainingHeadroomTokens),
	}
	if contract.CurrentInputTokens > 0 {
		attestations = append(attestations, "current_input_admitted")
	}
	if contract.ToolEvidenceTokens > 0 {
		attestations = append(attestations, "tool_evidence_admitted")
	}
	for _, layer := range contract.Suppressed {
		attestations = append(attestations, fmt.Sprintf("suppressed:%s:%s", layer.Name, layer.Reason))
	}
	for _, layer := range contract.Admitted {
		if perceptionMotifLayer(layer.Name) && layer.LowAuthority {
			attestations = append(attestations, fmt.Sprintf("low_authority:%s", layer.Name))
		}
	}
	return attestations
}

func perceptionRisks(contract PerceptionBudgetContract) []string {
	risks := []string{}
	if contract.RemainingHeadroomTokens < 0 {
		risks = append(risks, "over_budget")
	}
	if contract.MemoryEstimatedTokens > contract.MemoryBudgetTokens {
		risks = append(risks, "memory_budget_exceeded")
	}
	for _, layer := range contract.Admitted {
		if perceptionMotifLayer(layer.Name) && !layer.LowAuthority {
			risks = append(risks, "motif_layer_without_low_authority")
		}
		if layer.Name == PerceptionLayerImportedArchive && layer.EpistemicStatus != PerceptionStatusImported {
			risks = append(risks, "imported_archive_without_imported_status")
		}
	}
	return risks
}
