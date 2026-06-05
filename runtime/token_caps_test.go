//go:build linux

package runtime

import (
	"testing"

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
	if faceRenderMaxTokens != 512 || compactionMaxTokens != 512 {
		t.Fatalf("face/compaction caps = %d/%d, want 512/512", faceRenderMaxTokens, compactionMaxTokens)
	}
	if faceRenderMaxTokens >= interactiveRunMaxTokens {
		t.Fatalf("face cap = %d should remain below interactive cap %d", faceRenderMaxTokens, interactiveRunMaxTokens)
	}
}
