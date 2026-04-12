//go:build linux

package face

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestSerializeFloorFallbackUsesStructuredPublicSections(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.MaterialPacket{
		Facts:            []string{"The repo was inspected."},
		AllowedActions:   []string{"Propose the strongest next steps."},
		SceneConstraints: []string{"Keep the tone practical."},
	}, "FACTS:\n- The repo was inspected.")

	want := strings.Join([]string{
		"What is true:",
		"- The repo was inspected.",
		"",
		"What I can do:",
		"- Propose the strongest next steps.",
	}, "\n")
	if got != want {
		t.Fatalf("SerializeFloorFallback() = %q, want %q", got, want)
	}
	if strings.Contains(got, "Keep the tone practical.") {
		t.Fatalf("SerializeFloorFallback() leaked scene constraints: %q", got)
	}
}

func TestSerializeFloorFallbackPreservesLegacyText(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.LegacyMaterialPacket("legacy canonical"), "legacy canonical")
	if got != "legacy canonical" {
		t.Fatalf("SerializeFloorFallback() = %q, want legacy canonical", got)
	}
}

func TestSerializeFloorFallbackHandlesEmptyFloor(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.MaterialPacket{}, "")
	if got != "(no response)" {
		t.Fatalf("SerializeFloorFallback() = %q, want (no response)", got)
	}
}
