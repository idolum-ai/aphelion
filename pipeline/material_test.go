//go:build linux

package pipeline

import (
	"strings"
	"testing"
)

func TestParseMaterialPacketParsesStructuredSections(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"FACTS:",
		"- The repo was inspected.",
		"ALLOWED_ACTIONS:",
		"- Propose grounded actions.",
		"COMMITMENTS:",
		"- Keep replies concrete.",
		"REFUSALS:",
		"- Avoid unsupported claims.",
		"SCENE_CONSTRAINTS:",
		"- Sound direct and grounded.",
		"NOTES:",
		"- Show the highest-confidence action first.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	if packet.Facts[0] != "The repo was inspected." {
		t.Fatalf("Facts = %#v, want parsed fact", packet.Facts)
	}
	if packet.SceneConstraints[0] != "Sound direct and grounded." {
		t.Fatalf("SceneConstraints = %#v, want parsed constraint", packet.SceneConstraints)
	}
}

func TestBuildFloorFromGovernorFallsBackToLegacyText(t *testing.T) {
	t.Parallel()

	packet, sidecar, structured := BuildFloorFromGovernor("plain old reply text", true)
	if structured {
		t.Fatal("structured = true, want legacy fallback")
	}
	if sidecar == "" {
		t.Fatal("sidecar = empty, want fallback floor text")
	}
	if len(packet.Notes) == 0 || packet.Notes[0] != "plain old reply text" {
		t.Fatalf("packet = %#v, want legacy notes packet", packet)
	}
}
