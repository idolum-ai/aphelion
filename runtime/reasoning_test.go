//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestReasoningOptionsForRunDefaultsByKind(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Thinking: config.ThinkingConfig{
			Effort:  "medium",
			Summary: "auto",
			Defaults: config.ThinkingDefaultsConfig{
				Default:   "medium",
				Heartbeat: "low",
				Cron:      "low",
				Recovery:  "medium",
			},
		},
	}

	if got := reasoningOptionsForRun(cfg, session.TurnRunKindInteractive); got.Reasoning.Effort != agent.ReasoningEffortMedium {
		t.Fatalf("interactive effort = %q, want medium", got.Reasoning.Effort)
	}
	if got := reasoningOptionsForRun(cfg, session.TurnRunKindHeartbeat); got.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("heartbeat effort = %q, want low", got.Reasoning.Effort)
	}
	if got := reasoningOptionsForRun(cfg, session.TurnRunKindCron); got.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("cron effort = %q, want low", got.Reasoning.Effort)
	}
	if got := reasoningOptionsForRun(cfg, session.TurnRunKindRecovery); got.Reasoning.Effort != agent.ReasoningEffortMedium {
		t.Fatalf("recovery effort = %q, want medium", got.Reasoning.Effort)
	}
}

func TestReasoningOptionsSummarySeparateFromEffort(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Thinking: config.ThinkingConfig{
			Effort:  "high",
			Summary: "compact",
			Defaults: config.ThinkingDefaultsConfig{
				Default: "low",
			},
		},
	}

	got := reasoningOptionsForRun(cfg, session.TurnRunKindInteractive)
	if got.Reasoning.Effort != agent.ReasoningEffortLow {
		t.Fatalf("effort = %q, want low", got.Reasoning.Effort)
	}
	if got.Reasoning.Summary != agent.ReasoningSummaryCompact {
		t.Fatalf("summary = %q, want compact", got.Reasoning.Summary)
	}
}
