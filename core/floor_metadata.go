//go:build linux

package core

type HiddenInput struct {
	Category string               `json:"category"`
	Summary  string               `json:"summary"`
	Claim    *InterpretationClaim `json:"claim,omitempty"`
}

type InteriorSignalSummary struct {
	Category         string  `json:"category"`
	SubjectKey       string  `json:"subject_key"`
	Summary          string  `json:"summary,omitempty"`
	Intensity        float64 `json:"intensity,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	ObservationCount int     `json:"observation_count,omitempty"`
	Trend            string  `json:"trend,omitempty"`
}

type ArtifactReference struct {
	ArtifactID       string `json:"artifact_id"`
	Kind             string `json:"kind,omitempty"`
	SourceType       string `json:"source_type,omitempty"`
	Summary          string `json:"summary,omitempty"`
	Handling         string `json:"handling,omitempty"`
	Retention        string `json:"retention,omitempty"`
	Interpretation   string `json:"interpretation,omitempty"`
	DerivedOutput    string `json:"derived_output,omitempty"`
	ProvenanceScope  string `json:"provenance_scope,omitempty"`
	FetchState       string `json:"fetch_state,omitempty"`
	DecisionSummary  string `json:"decision_summary,omitempty"`
	MaterializedPath string `json:"materialized_path,omitempty"`
}

type FloorMetadata struct {
	HiddenInputs      []HiddenInput           `json:"hidden_inputs,omitempty"`
	InteriorSignals   []InteriorSignalSummary `json:"interior_signals,omitempty"`
	Artifacts         []ArtifactReference     `json:"artifacts,omitempty"`
	ProvenanceSummary string                  `json:"provenance_summary,omitempty"`
}

func (m FloorMetadata) Empty() bool {
	return len(m.HiddenInputs) == 0 && len(m.InteriorSignals) == 0 && len(m.Artifacts) == 0 && m.ProvenanceSummary == ""
}
