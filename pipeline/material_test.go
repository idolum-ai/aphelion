//go:build linux

package pipeline

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestParseMaterialPacketParsesStructuredSections(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"KIND: status_report",
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
	if packet.Kind != core.MaterialPacketKindStatusReport {
		t.Fatalf("Kind = %q, want status_report", packet.Kind)
	}
	if packet.SceneConstraints[0] != "Sound direct and grounded." {
		t.Fatalf("SceneConstraints = %#v, want parsed constraint", packet.SceneConstraints)
	}
}

func TestParseMaterialPacketParsesKindSection(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"KIND:",
		"- relational",
		"FACTS:",
		"- The reply needs visible care.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	if packet.Kind != core.MaterialPacketKindRelational {
		t.Fatalf("Kind = %q, want relational", packet.Kind)
	}
	if strings.Contains(packet.Text(), "KIND") || strings.Contains(packet.Text(), "relational") {
		t.Fatalf("packet.Text() leaked metadata kind: %q", packet.Text())
	}
}

func TestBuildFloorFromGovernorFallsBackToPlainText(t *testing.T) {
	t.Parallel()

	packet, sidecar, structured := BuildFloorFromGovernor("plain reply text", true)
	if structured {
		t.Fatal("structured = true, want plain-text fallback")
	}
	if sidecar == "" {
		t.Fatal("sidecar = empty, want fallback floor text")
	}
	if len(packet.Notes) == 0 || packet.Notes[0] != "plain reply text" {
		t.Fatalf("packet = %#v, want plain-text notes packet", packet)
	}
}

func TestBuildFloorFromGovernorUsesNoResponseFallback(t *testing.T) {
	t.Parallel()

	_, sidecarContract, structured := BuildFloorFromGovernor("", true)
	if structured {
		t.Fatal("structured = true, want plain-text fallback on empty input")
	}
	if sidecarContract != "(no response)" {
		t.Fatalf("sidecar = %q, want %q", sidecarContract, "(no response)")
	}

	_, sidecarPlain, structured := BuildFloorFromGovernor("", false)
	if structured {
		t.Fatal("structured = true, want plain-text floor when contract disabled")
	}
	if sidecarPlain != "(no response)" {
		t.Fatalf("sidecar = %q, want %q", sidecarPlain, "(no response)")
	}
}
