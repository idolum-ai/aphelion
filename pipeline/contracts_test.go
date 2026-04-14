//go:build linux

package pipeline

import "testing"

func TestBrokerageArtifactPreservesBothSides(t *testing.T) {
	artifact := BrokerageArtifact{
		Proposal: BrokerageProposal{
			RawText: "INSPECT: yes\nQUESTION: no\nANSWER: yes",
			SuggestedContract: ExecutionContract{
				NeedsInspection: true,
				NeedsQuestion:   false,
				MayAnswerNow:    true,
			},
		},
		Ratification: BrokerageRatification{
			RawText: "INSPECT: yes\nQUESTION: no\nANSWER: yes\nRATIFICATION: adapt\nPLAN:\n- inspect first",
			RatifiedContract: ExecutionContract{
				NeedsInspection: true,
				NeedsQuestion:   false,
				MayAnswerNow:    true,
			},
			Disposition:   RatificationAdapt,
			RatifiedSteps: []string{"inspect first"},
		},
	}
	if artifact.Proposal.RawText == "" {
		t.Fatal("proposal side empty, want preserved face push")
	}
	if artifact.Ratification.RawText == "" {
		t.Fatal("ratification side empty, want preserved governor answer")
	}
}

func TestFloorArtifactAllowsStructuredOrLegacySurface(t *testing.T) {
	structured := FloorArtifact{Text: "FACTS:\n- grounded", Structured: true}
	legacy := FloorArtifact{Text: "plain floor text", Structured: false}
	if !structured.Structured {
		t.Fatal("structured floor lost structured marker")
	}
	if legacy.Structured {
		t.Fatal("legacy floor incorrectly marked structured")
	}
}
