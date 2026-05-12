//go:build linux

package pipeline

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
)

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

func TestFloorArtifactAllowsStructuredOrPlainSurface(t *testing.T) {
	structured := FloorArtifact{Text: "FACTS:\n- grounded", Structured: true}
	plain := FloorArtifact{Text: "plain floor text", Structured: false}
	if !structured.Structured {
		t.Fatal("structured floor lost structured marker")
	}
	if plain.Structured {
		t.Fatal("plain floor incorrectly marked structured")
	}
}

func TestDecideInteractiveFacePolicy(t *testing.T) {
	t.Parallel()

	p := DecideInteractiveFacePolicy("hello there")
	if !p.Proposal || !p.Render {
		t.Fatalf("policy = %#v, want proposal and render", p)
	}

	p = DecideInteractiveFacePolicy("/command")
	if p.Proposal || p.Render {
		t.Fatalf("policy = %#v, want empty for command input", p)
	}
}

func TestShouldRenderInteractiveIdolumReplyUsesRenderPolicy(t *testing.T) {
	t.Parallel()

	policy := FacePolicy{Render: true}
	t.Run("no response should not render without tool context", func(t *testing.T) {
		t.Parallel()
		if ShouldRenderInteractiveIdolumReply(policy, RenderDecisionInput{
			UserText:  "say hi",
			FloorText: "(no response)",
		}) {
			t.Fatal("ShouldRenderInteractiveIdolumReply() = true, want false for empty fallback without tool context")
		}
	})
	t.Run("tool log should render even when fallback empty", func(t *testing.T) {
		t.Parallel()
		if !ShouldRenderInteractiveIdolumReply(policy, RenderDecisionInput{
			UserText:  "say hi",
			FloorText: "(no response)",
			ToolLog:   []string{"bash ls"},
		}) {
			t.Fatal("ShouldRenderInteractiveIdolumReply() = false, want true when tool log present")
		}
	})
	t.Run("ordinary response text should render", func(t *testing.T) {
		t.Parallel()
		if !ShouldRenderInteractiveIdolumReply(policy, RenderDecisionInput{
			UserText:  "say hi",
			FloorText: "floor text",
		}) {
			t.Fatal("ShouldRenderInteractiveIdolumReply() = false, want true for ordinary policy+text")
		}
	})
	t.Run("slash command should not render", func(t *testing.T) {
		t.Parallel()
		if ShouldRenderInteractiveIdolumReply(policy, RenderDecisionInput{
			UserText:  "/help",
			FloorText: "floor text",
		}) {
			t.Fatal("ShouldRenderInteractiveIdolumReply() = true, want false for slash commands")
		}
	})
	t.Run("generated tool messages should trigger render", func(t *testing.T) {
		t.Parallel()
		if !ShouldRenderInteractiveIdolumReply(policy, RenderDecisionInput{
			UserText:          "say hi",
			FloorText:         "(no response)",
			GeneratedMessages: []agent.Message{{Role: "tool", Content: "done"}},
		}) {
			t.Fatal("ShouldRenderInteractiveIdolumReply() = false, want true when generated tool messages are present")
		}
	})

	if ShouldRenderInteractiveIdolumReply(FacePolicy{Render: false}, RenderDecisionInput{
		UserText:  "say hi",
		FloorText: "floor text",
	}) {
		t.Fatal("ShouldRenderInteractiveIdolumReply() = true, want false")
	}
}

func TestShouldUseMaterialFloorContract(t *testing.T) {
	t.Parallel()

	if !ShouldUseMaterialFloorContract(FacePolicy{Proposal: true}) {
		t.Fatal("ShouldUseMaterialFloorContract() = false, want true")
	}
	if ShouldUseMaterialFloorContract(FacePolicy{}) {
		t.Fatal("ShouldUseMaterialFloorContract() = true, want false")
	}
}
