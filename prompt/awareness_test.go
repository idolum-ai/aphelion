//go:build linux

package prompt

import (
	"strings"
	"testing"
)

func TestRuntimeAwarenessRendersSharedSectionsBeforeRoleDelta(t *testing.T) {
	t.Parallel()

	got := renderGovernorRuntimeAwarenessBlock(RuntimeAwareness{
		SessionKind:           "interactive",
		RunKind:               "interactive",
		Channel:               "telegram",
		EventOrigin:           "message",
		ActiveProvider:        "openai",
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{"semantic_recurrence"},
		GovernorProvider:      "openai",
		GovernorModel:         "gpt-5.5",
	})

	assertSectionOrder(t, got, []string{
		"## Runtime Awareness",
		"### Shared Stable Facts",
		"- session_kind: interactive",
		"### Shared Turn State",
		"- active_provider: openai",
		"### Governor Delta",
		"- governor_provider: openai",
	})
}

func TestFaceAwarenessKeepsGovernorDeltaOut(t *testing.T) {
	t.Parallel()

	got := renderFaceAwarenessBlock(RuntimeAwareness{
		SessionKind:      "interactive",
		RunKind:          "interactive",
		Channel:          "telegram",
		ExecRoot:         "/tmp/exec",
		FaceBackend:      "provider",
		FaceProvider:     "anthropic",
		FaceModel:        "claude-sonnet",
		GovernorProvider: "openai",
	})

	if strings.Contains(got, "### Governor Delta") || strings.Contains(got, "exec_root") || strings.Contains(got, "governor_provider") {
		t.Fatalf("face awareness leaked governor-only fields: %q", got)
	}
	assertSectionOrder(t, got, []string{
		"## Delivery Awareness",
		"### Shared Stable Facts",
		"### Shared Turn State",
		"### Face Delta",
		"- face_provider: anthropic",
	})
}

func assertSectionOrder(t *testing.T, text string, sections []string) {
	t.Helper()
	last := -1
	for _, section := range sections {
		idx := strings.Index(text, section)
		if idx < 0 {
			t.Fatalf("text missing section %q: %q", section, text)
		}
		if idx < last {
			t.Fatalf("section %q appeared out of order in %q", section, text)
		}
		last = idx
	}
}
