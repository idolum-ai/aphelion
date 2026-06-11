//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestReasoningOptionsForRunAppliesMaxTokenCaps(t *testing.T) {
	cfg := &config.Config{}
	cases := []struct {
		kind session.TurnRunKind
		want int
	}{
		{session.TurnRunKindInteractive, interactiveRunMaxTokens},
		{session.TurnRunKindHeartbeat, heartbeatRunMaxTokens},
		{session.TurnRunKindCron, cronRunMaxTokens},
		{session.TurnRunKindRecovery, recoveryRunMaxTokens},
		{session.TurnRunKindCuriosity, curiosityRunMaxTokens},
		{session.TurnRunKindDoctor, doctorRunMaxTokens},
	}
	for _, tc := range cases {
		opts := reasoningOptionsForRun(cfg, tc.kind)
		if opts == nil || opts.MaxTokens != tc.want {
			t.Fatalf("reasoningOptionsForRun(%s).MaxTokens = %#v, want %d", tc.kind, opts, tc.want)
		}
	}
}

func TestFaceAndCompactionMaxTokenCapsAreSeparate(t *testing.T) {
	if interactiveRunMaxTokens != 2048 {
		t.Fatalf("interactive cap = %d, want 2048", interactiveRunMaxTokens)
	}
	if faceRenderMaxTokens != 512 || compactionMaxTokens != 512 {
		t.Fatalf("face/compaction caps = %d/%d, want 512/512", faceRenderMaxTokens, compactionMaxTokens)
	}
	if faceRenderMaxTokens >= interactiveRunMaxTokens {
		t.Fatalf("face cap = %d should remain below interactive cap %d", faceRenderMaxTokens, interactiveRunMaxTokens)
	}
}

func TestTokenAwareTurnBudgetUsesRunAndContextCaps(t *testing.T) {
	budget := tokenAwareTurnBudget(10, &agent.CompleteOptions{
		MaxTokens: 512,
		ContextBudget: &agent.ContextBudget{
			ContextWindow: 1000,
			MaxRatio:      0.50,
			HardRatio:     0.75,
		},
	})
	if budget.OutputTokenSoftLimit != 1024 || budget.OutputTokenHardLimit != 1536 {
		t.Fatalf("output token limits = %d/%d, want 1024/1536", budget.OutputTokenSoftLimit, budget.OutputTokenHardLimit)
	}
	if budget.InputTokenSoftLimit != 1000 || budget.InputTokenHardLimit != 2250 {
		t.Fatalf("input token limits = %d/%d, want 1000/2250", budget.InputTokenSoftLimit, budget.InputTokenHardLimit)
	}
}
