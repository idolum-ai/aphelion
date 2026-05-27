//go:build linux

package face

import (
	"strings"
	"testing"
)

func TestRenderInlinePanelEmpty(t *testing.T) {
	t.Parallel()

	got := RenderInlinePanel(InlinePanel{})
	if got != "" {
		t.Fatalf("RenderInlinePanel(empty) = %q, want empty string", got)
	}
}

func TestRenderInlinePanelTitleOnly(t *testing.T) {
	t.Parallel()

	got := RenderInlinePanel(InlinePanel{Title: "Health"})
	if got != "Health" {
		t.Fatalf("RenderInlinePanel(title only) = %q, want %q", got, "Health")
	}
}

func TestRenderInlinePanelAllFieldsHasNoStatusOrNextLabels(t *testing.T) {
	t.Parallel()

	got := RenderInlinePanel(InlinePanel{
		Title:   "Health",
		State:   "ready",
		Next:    "Pick the view that matches your question.",
		Details: []string{"Status shows live chat state.", "Trace expands the status ledger."},
	})

	for _, forbidden := range []string{"Status:", "Why:", "Next:", "Details:", "Evidence:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("RenderInlinePanel = %q, must not contain template label %q", got, forbidden)
		}
	}
	for _, want := range []string{
		"Health",
		"ready",
		"Pick the view that matches your question.",
		"- Status shows live chat state.",
		"- Trace expands the status ledger.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderInlinePanel = %q, want substring %q", got, want)
		}
	}
}

func TestRenderInlinePanelTrimsBlankDetails(t *testing.T) {
	t.Parallel()

	got := RenderInlinePanel(InlinePanel{
		Title:   "Health",
		Details: []string{"a", "", "  ", "b"},
	})
	want := "Health\n- a\n- b"
	if got != want {
		t.Fatalf("RenderInlinePanel = %q, want %q", got, want)
	}
}

func TestRenderInlinePanelStateAndNextOnly(t *testing.T) {
	t.Parallel()

	got := RenderInlinePanel(InlinePanel{
		State: "4 active, 2 blocked",
		Next:  "Inspect blocked missions before relying on the ledger.",
	})
	want := "4 active, 2 blocked\nInspect blocked missions before relying on the ledger."
	if got != want {
		t.Fatalf("RenderInlinePanel = %q, want %q", got, want)
	}
}
