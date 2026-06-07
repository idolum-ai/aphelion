//go:build linux

package runtime

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestBuildCompactionSummaryUsesRenderLaneWhenConfigured(t *testing.T) {
	t.Parallel()

	cfg, store, governorProvider, sender := buildRuntimeFixtures(t)
	cfg.Providers.OpenAI.APIKey = "test-key"
	governorProvider.compactionReplyText = "governor summary"
	laneProvider := &fakeProvider{compactionReplyText: "render lane summary"}

	rt, err := New(cfg, store, governorProvider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.buildProviderHook = func(_ *config.Config, slot core.ModelSlotConfig) (agent.Provider, error) {
		if slot.Slot != core.ModelSlotPersona {
			t.Fatalf("build provider slot = %q, want persona render lane", slot.Slot)
		}
		return laneProvider, nil
	}

	if _, err := rt.SetModelSlotOverride(core.ModelSlotConfig{
		Slot:     core.ModelSlotPersona,
		Provider: core.ModelProviderOpenAI,
		Model:    "gpt-5.5",
	}, "test", "exercise render lane"); err != nil {
		t.Fatalf("SetModelSlotOverride() err = %v", err)
	}

	sess := &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: "old request", TurnIndex: 1},
			{Role: "assistant", Content: "old answer", TurnIndex: 1},
		},
	}
	got, err := rt.buildCompactionSummary(context.Background(), sess, 2)
	if err != nil {
		t.Fatalf("buildCompactionSummary() err = %v", err)
	}
	if got != "render lane summary" {
		t.Fatalf("summary = %q, want render lane provider response", got)
	}
	if len(governorProvider.lastGovernorMsgs) != 0 {
		t.Fatalf("governor provider was called for compaction: %#v", governorProvider.lastGovernorMsgs)
	}
}
