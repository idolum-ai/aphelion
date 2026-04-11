//go:build linux

package runtime

import (
	"strings"
	"testing"
)

func TestParseMaterialPacketParsesStructuredSections(t *testing.T) {
	t.Parallel()

	packet, err := parseMaterialPacket(strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"ALLOWED_ACTIONS:",
		"- Propose features grounded in the current architecture.",
		"COMMITMENTS:",
		"- Keep the answer concrete.",
		"REFUSALS:",
		"- Do not invent missing subsystems.",
		"SCENE_CONSTRAINTS:",
		"- Sound direct and grounded.",
		"NOTES:",
		"- Emphasize the strongest opportunities first.",
	}, "\n"))
	if err != nil {
		t.Fatalf("parseMaterialPacket() err = %v", err)
	}
	if packet.Facts[0] != "The repo was inspected." {
		t.Fatalf("Facts = %#v, want parsed fact", packet.Facts)
	}
	if packet.SceneConstraints[0] != "Sound direct and grounded." {
		t.Fatalf("SceneConstraints = %#v, want parsed constraint", packet.SceneConstraints)
	}
}

func TestGovernorMaterialArtifactFallsBackToLegacyText(t *testing.T) {
	t.Parallel()

	packet, sidecar, structured := governorMaterialArtifact("plain old reply text", true)
	if structured {
		t.Fatal("structured = true, want legacy fallback")
	}
	if sidecar != "plain old reply text" {
		t.Fatalf("sidecar = %q, want legacy text", sidecar)
	}
	if len(packet.Notes) != 1 || packet.Notes[0] != "plain old reply text" {
		t.Fatalf("packet = %#v, want legacy notes packet", packet)
	}
}
