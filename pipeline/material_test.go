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
		"CONTINUITY_CONTEXT:",
		"- kind=recovery; visibility=internal; reason=token rollover finished; text=Budget recovery completed before delivery.",
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
	if got := packet.ContinuityContext[0].Kind; got != core.MaterialContinuityKindRecovery {
		t.Fatalf("ContinuityContext[0].Kind = %q, want recovery", got)
	}
	if got := packet.ContinuityContext[0].Visibility; got != core.MaterialContinuityVisibilityInternal {
		t.Fatalf("ContinuityContext[0].Visibility = %q, want internal", got)
	}
	if packet.ContinuityContext[0].Reason != "token rollover finished" || packet.ContinuityContext[0].Text != "Budget recovery completed before delivery." {
		t.Fatalf("ContinuityContext = %#v, want parsed continuity context", packet.ContinuityContext)
	}
	if !strings.Contains(packet.Text(), "CONTINUITY_CONTEXT:") {
		t.Fatalf("packet.Text() = %q, want continuity context preserved in canonical floor", packet.Text())
	}
}

func TestParseMaterialPacketDefaultsPlainContinuityContextToInternal(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"CONTINUITY_CONTEXT:",
		"- Legacy recovery sentence from an older governor.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	if got := packet.ContinuityContext[0].Visibility; got != core.MaterialContinuityVisibilityInternal {
		t.Fatalf("Visibility = %q, want internal fail-closed default", got)
	}
	if packet.ContinuityContext[0].Text != "Legacy recovery sentence from an older governor." {
		t.Fatalf("ContinuityContext = %#v, want preserved legacy text", packet.ContinuityContext)
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
