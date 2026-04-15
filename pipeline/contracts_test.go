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

func TestDecideInteractiveFacePolicy(t *testing.T) {
	t.Parallel()

	p := DecideInteractiveFacePolicy(nil, "hello there")
	if !p.Proposal || !p.Render {
		t.Fatalf("policy = %#v, want proposal and render", p)
	}

	p = DecideInteractiveFacePolicy(nil, "/command")
	if p.Proposal || p.Render {
		t.Fatalf("policy = %#v, want empty for command input", p)
	}
}

func TestShouldRenderIdolumReplyUsesRenderPolicy(t *testing.T) {
	t.Parallel()

	policy := FacePolicy{Render: true}
	if !ShouldRenderIdolumReply(policy, "", "", nil, nil) {
		t.Fatal("ShouldRenderIdolumReply() = false, want true")
	}

	policy.Render = false
	if ShouldRenderIdolumReply(policy, "", "", nil, nil) {
		t.Fatal("ShouldRenderIdolumReply() = true, want false")
	}
}

func TestShouldUseMaterialFloorContract(t *testing.T) {
	t.Parallel()

	if !ShouldUseMaterialFloorContract("", FacePolicy{Proposal: true}) {
		t.Fatal("ShouldUseMaterialFloorContract() = false, want true")
	}
	if ShouldUseMaterialFloorContract("", FacePolicy{}) {
		t.Fatal("ShouldUseMaterialFloorContract() = true, want false")
	}
}
