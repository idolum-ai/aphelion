//go:build linux

package core

type HiddenInput struct {
	Category string `json:"category"`
	Summary  string `json:"summary"`
}

type FloorMetadata struct {
	HiddenInputs      []HiddenInput `json:"hidden_inputs,omitempty"`
	ProvenanceSummary string        `json:"provenance_summary,omitempty"`
}

func (m FloorMetadata) Empty() bool {
	return len(m.HiddenInputs) == 0 && m.ProvenanceSummary == ""
}
