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

func TestSerializeFloorFallbackDoesNotLeakSceneConstraintsOnlyFloor(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		SceneConstraints: []string{"Keep internal recovery mechanics out of the visible headline."},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	if got != "(no response)" {
		t.Fatalf("SerializeFloorFallback() = %q, want no public response for scene-constraints-only floor", got)
	}
}

func TestSerializeFloorFallbackOmitsContinuityContext(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		Facts: []string{
			"Completed the clean replacement release-PR route.",
			"Pushed `release/v0.2.5` with `--force-with-lease`.",
		},
		ContinuityContext: []core.MaterialContinuityContext{{
			Kind:        core.MaterialContinuityKindRecovery,
			Visibility:  core.MaterialContinuityVisibilityInternal,
			Reason:      "token budget rollover succeeded",
			EvidenceRef: "execution_event:budget_recovery_resumed",
		}},
		Commitments: []string{"Stopped before merge/publication."},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	for _, blocked := range []string{"Recovery hop", "CONTINUITY_CONTEXT"} {
		if strings.Contains(got, blocked) {
			t.Fatalf("fallback leaked continuity context %q: %q", blocked, got)
		}
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
}

func TestSerializeFloorFallbackKeepsUserRelevantRecoveryFacts(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind:  core.MaterialPacketKindStatusReport,
		Facts: []string{"I ran out of token room before finishing, so I need one narrower approved step."},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	if !strings.Contains(got, "I ran out of token room before finishing") {
		t.Fatalf("fallback omitted user-relevant recovery fact: %q", got)
	}
}

func TestSerializeFloorFallbackSurfacesMustSurfaceContinuityBlocker(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		ContinuityContext: []core.MaterialContinuityContext{{
			Kind:        core.MaterialContinuityKindWarning,
			Visibility:  core.MaterialContinuityVisibilityMustSurface,
			Reason:      "The prior approval expired before the next action could run, so fresh approval is required.",
			EvidenceRef: "continuation:lease_expired",
		}},
		AllowedActions: []string{"Ask for a fresh bounded approval before retrying."},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	for _, want := range []string{
		"Continuity:",
		"The prior approval expired before the next action could run, so fresh approval is required.",
		"Ask for a fresh bounded approval before retrying.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback missing %q: %q", want, got)
		}
	}
}

func TestSerializeFloorFallbackSurfacesMustSurfaceContinuityWithoutReason(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		ContinuityContext: []core.MaterialContinuityContext{{
			Kind:        core.MaterialContinuityKindWarning,
			Visibility:  core.MaterialContinuityVisibilityMustSurface,
			EvidenceRef: "continuation:missing_approval",
		}},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	if !strings.Contains(got, "Continuity requires attention before proceeding.") {
		t.Fatalf("fallback omitted generic must-surface blocker: %q", got)
	}
}

func TestSerializeFloorFallbackDoesNotLeakInternalOnlyFloor(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		ContinuityContext: []core.MaterialContinuityContext{{
			Kind:        core.MaterialContinuityKindRecovery,
			Visibility:  core.MaterialContinuityVisibilityInternal,
			Reason:      "token budget rollover succeeded",
			EvidenceRef: "execution_event:budget_recovery_resumed",
		}},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram"})
	if got != "(no response)" {
		t.Fatalf("SerializeFloorFallback() = %q, want no public response for internal-only floor", got)
	}
}

func TestSerializeFloorFallbackSurfacesUserRelevantContinuityRecap(t *testing.T) {
	t.Parallel()

	packet := core.MaterialPacket{
		Kind: core.MaterialPacketKindStatusReport,
		ContinuityContext: []core.MaterialContinuityContext{{
			Kind:        core.MaterialContinuityKindContinuation,
			Visibility:  core.MaterialContinuityVisibilityInternal,
			Reason:      "We were preparing the release fix PR and had stopped before merge or publication.",
			EvidenceRef: "operation:release_fix_pr",
		}},
	}
	got := SerializeFloorFallback(packet, packet.Text(), FallbackOptions{Channel: "telegram", UserIntent: IntentContinuityQuestion})
	if !strings.Contains(got, "Continuity:") ||
		!strings.Contains(got, "We were preparing the release fix PR and had stopped before merge or publication.") {
		t.Fatalf("fallback omitted user-relevant continuity recap: %q", got)
	}
}

func TestContinuityPresentationPolicySeparatesMaterialFromVisibility(t *testing.T) {
	t.Parallel()

	ctx := []core.MaterialContinuityContext{
		{Kind: core.MaterialContinuityKindRecovery, Visibility: core.MaterialContinuityVisibilityInternal, Reason: "recovery completed", EvidenceRef: "event:recovery"},
		{Kind: core.MaterialContinuityKindWarning, Visibility: core.MaterialContinuityVisibilityMustSurface, Reason: "approval is stale", EvidenceRef: "lease:expired"},
	}
	decision := ContinuityPresentationPolicy(ctx, IntentUnspecified)
	if len(decision.Background) != 1 || decision.Background[0].Kind != core.MaterialContinuityKindRecovery {
		t.Fatalf("Background = %#v, want internal recovery kept in background", decision.Background)
	}
	if len(decision.Visible) != 1 || decision.Visible[0].Kind != core.MaterialContinuityKindWarning {
		t.Fatalf("Visible = %#v, want must-surface warning", decision.Visible)
	}

	continuityQuestion := ContinuityPresentationPolicy(ctx[:1], IntentContinuityQuestion)
	if len(continuityQuestion.Visible) != 1 || len(continuityQuestion.Background) != 0 {
		t.Fatalf("continuity question decision = %#v, want internal continuity visible on explicit continuity intent", continuityQuestion)
	}
}

func TestInferFallbackUserIntentDetectsContinuityQuestions(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"Where were we?",
		"Can you catch me up on this?",
		"What did you recover from?",
	} {
		if got := InferFallbackUserIntent(text); got != IntentContinuityQuestion {
			t.Fatalf("InferFallbackUserIntent(%q) = %q, want continuity_question", text, got)
		}
	}
	if got := InferFallbackUserIntent("please run the tests"); got != IntentUnspecified {
		t.Fatalf("InferFallbackUserIntent(non-continuity) = %q, want unspecified", got)
	}
}
