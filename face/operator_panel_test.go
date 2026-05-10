//go:build linux

package face

import (
	"strings"
	"testing"
)

func TestRenderOperatorPanelUsesOperatorContractOrder(t *testing.T) {
	t.Parallel()

	out := RenderOperatorPanel(OperatorPanel{
		Title:    "Tailnet",
		State:    "ready",
		Why:      "private status is reachable.",
		Next:     "refresh after any network change.",
		Details:  []string{"2 surfaces registered"},
		Evidence: []string{"backend: tailscale"},
	})
	for _, want := range []string{"Tailnet", "Status: ready", "Why: private status is reachable.", "Next: refresh after any network change.", "Details:", "- 2 surfaces registered", "Evidence:", "- backend: tailscale"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderOperatorPanel() = %q, want %q", out, want)
		}
	}
	if strings.Index(out, "Status: ready") > strings.Index(out, "Next: refresh") {
		t.Fatalf("RenderOperatorPanel() = %q, want state before next action", out)
	}
}
