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
		"- kind=recovery; visibility=internal; reason=token rollover finished; evidence_ref=execution_event:budget_recovery_resumed",
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
	if packet.ContinuityContext[0].Reason != "token rollover finished" || packet.ContinuityContext[0].EvidenceRef != "execution_event:budget_recovery_resumed" {
		t.Fatalf("ContinuityContext = %#v, want parsed continuity context", packet.ContinuityContext)
	}
	if !strings.Contains(packet.Text(), "CONTINUITY_CONTEXT:") {
		t.Fatalf("packet.Text() = %q, want continuity context preserved in canonical floor", packet.Text())
	}
}

func TestParseMaterialPacketQuarantinesLegacyContinuityProse(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"CONTINUITY_CONTEXT:",
		"- Recovered cleanly. Completed the release PR preparation.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	if got := packet.ContinuityContext[0].Kind; got != core.MaterialContinuityKindRecovery {
		t.Fatalf("Kind = %q, want legacy recovery quarantine", got)
	}
	if got := packet.ContinuityContext[0].Visibility; got != core.MaterialContinuityVisibilityInternal {
		t.Fatalf("Visibility = %q, want internal fail-closed default", got)
	}
	if packet.ContinuityContext[0].EvidenceRef != "legacy_recovery_prose" ||
		!strings.Contains(packet.ContinuityContext[0].Reason, "legacy recovery prose") {
		t.Fatalf("ContinuityContext = %#v, want legacy recovery prose quarantined", packet.ContinuityContext)
	}
}

func TestParseMaterialPacketQuarantinesUnknownContinuityProse(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"CONTINUITY_CONTEXT:",
		"- This is prose-shaped continuity and should not become presentation copy.",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	got := packet.ContinuityContext[0]
	if got.Kind != core.MaterialContinuityKindEvidence ||
		got.Visibility != core.MaterialContinuityVisibilityInternal ||
		got.EvidenceRef != "legacy_continuity_context_prose" ||
		!strings.Contains(got.Reason, "quarantined") {
		t.Fatalf("ContinuityContext = %#v, want unknown prose quarantined as internal evidence", got)
	}
}

func TestParseMaterialPacketParsesHandoffContinuityKind(t *testing.T) {
	t.Parallel()

	packet, err := ParseMaterialPacket(strings.Join([]string{
		"CONTINUITY_CONTEXT:",
		"- kind=handoff; visibility=user_relevant; reason=thread handoff is relevant to the current question; evidence_ref=thread:3",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMaterialPacket() err = %v", err)
	}
	if got := packet.ContinuityContext[0].Kind; got != core.MaterialContinuityKindHandoff {
		t.Fatalf("Kind = %q, want handoff", got)
	}
	if got := packet.ContinuityContext[0].Visibility; got != core.MaterialContinuityVisibilityUserRelevant {
		t.Fatalf("Visibility = %q, want user_relevant", got)
	}
	if got := packet.ContinuityContext[0].EvidenceRef; got != "thread:3" {
		t.Fatalf("EvidenceRef = %q, want thread:3", got)
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
