//go:build linux

package pipeline

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
	}, "FACTS:\n- The repo was inspected.", FallbackOptions{Channel: "telegram"})

	want := strings.Join([]string{
		"What matters:",
		"- The repo was inspected.",
		"",
		"Next:",
		"- Propose the strongest next steps.",
	}, "\n")
	if got != want {
		t.Fatalf("SerializeFloorFallback() = %q, want %q", got, want)
	}
	if strings.Contains(got, "Keep the tone practical.") {
		t.Fatalf("SerializeFloorFallback() leaked scene constraints: %q", got)
	}
}

func TestSerializeFloorFallbackPreservesPlainText(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.TextMaterialPacket("plain canonical"), "plain canonical", FallbackOptions{Channel: "telegram"})
	if got != "plain canonical" {
		t.Fatalf("SerializeFloorFallback() = %q, want plain canonical", got)
	}
}

func TestSerializeFloorFallbackHandlesEmptyFloor(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.MaterialPacket{}, "", FallbackOptions{Channel: "telegram"})
	if got != "(no response)" {
		t.Fatalf("SerializeFloorFallback() = %q, want (no response)", got)
	}
}

func TestSerializeFloorFallbackUsesVoiceOverlay(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.MaterialPacket{
		Facts:       []string{"The repo was inspected."},
		Commitments: []string{"Keep the answer focused on the next move."},
		Refusals:    []string{"Pretend the tests passed when they did not."},
	}, "FACTS:\n- The repo was inspected.", FallbackOptions{Channel: "telegram", Voice: true})

	want := "Here's what matters: The repo was inspected. I'll keep the answer focused on the next move. I won't pretend the tests passed when they did not."
	if got != want {
		t.Fatalf("SerializeFloorFallback() = %q, want %q", got, want)
	}
}

func TestSerializeFloorFallbackTelegramFlattensSingleNote(t *testing.T) {
	t.Parallel()

	got := SerializeFloorFallback(core.MaterialPacket{
		Facts: []string{"The build completed."},
		Notes: []string{"Logs are available if you want them."},
	}, "FACTS:\n- The build completed.", FallbackOptions{Channel: "telegram"})

	want := strings.Join([]string{
		"What matters:",
		"- The build completed.",
		"",
		"Note: Logs are available if you want them.",
	}, "\n")
	if got != want {
		t.Fatalf("SerializeFloorFallback() = %q, want %q", got, want)
	}
}

func TestShapeInternalContinuityForPresentationFoldsRecoveryIntoOutcome(t *testing.T) {
	t.Parallel()

	packet, floorText, changed := ShapeInternalContinuityForPresentation(core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		Facts: []string{
			"Recovered and completed the clean replacement release-PR route.",
			"Pushed `release/v0.2.5` with `--force-with-lease`.",
		},
		Commitments: []string{"Stopped before merge/publication."},
	}, "original floor")
	if !changed {
		t.Fatal("ShapeInternalContinuityForPresentation() changed = false, want true")
	}
	got := SerializeFloorFallback(packet, floorText, FallbackOptions{Channel: "telegram"})
	if strings.Contains(strings.ToLower(got), "recovered") {
		t.Fatalf("fallback kept recovery headline: %q", got)
	}
	for _, want := range []string{
		"Completed the clean replacement release-PR route.",
		"Pushed `release/v0.2.5` with `--force-with-lease`.",
		"Stopped before merge/publication.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback missing %q: %q", want, got)
		}
	}
	if len(packet.SceneConstraints) == 0 || !strings.Contains(packet.SceneConstraints[0], "task outcome") {
		t.Fatalf("scene constraints = %#v, want internal continuity presentation constraint", packet.SceneConstraints)
	}
}

func TestShapeInternalContinuityForPresentationLeavesUserRelevantRecovery(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind:  core.MaterialPacketKindStatusReport,
		Facts: []string{"I ran out of token room before finishing, so I need one narrower approved step."},
	}
	shaped, floorText, changed := ShapeInternalContinuityForPresentation(packet, packet.Text())
	if changed {
		t.Fatalf("changed = true for user-relevant recovery; shaped=%#v floor=%q", shaped, floorText)
	}
}
