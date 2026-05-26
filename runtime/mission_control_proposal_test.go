//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestMissionControlProposalReviewEventRendersButtons(t *testing.T) {
	t.Parallel()

	metadata, err := core.MissionControlProposalMetadataJSON(core.MissionControlProposal{
		MissionID:   "mission-runtime-noise",
		Title:       "Runtime recovery and restart noise cleanup",
		Objective:   "Clean shutdown warning noise.",
		WhyProposed: "Restart now works but shutdown emits database-closed warnings.",
		NotIncluded: []string{"no self-continuation", "no tool execution"},
	})
	if err != nil {
		t.Fatalf("MissionControlProposalMetadataJSON() err = %v", err)
	}
	event := session.ReviewEvent{ID: 88, Summary: "proposal", MetadataJSON: metadata}
	text := FormatReviewEventMessage(event)
	for _, needle := range []string{"Mission Proposal", "Runtime recovery", "review-only candidate"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("FormatReviewEventMessage() = %q, want substring %q", text, needle)
		}
	}
	rows := ReviewEventInlineRows(event)
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 2 {
		t.Fatalf("rows = %#v, want two rows with four mission proposal buttons", rows)
	}
	labels := []string{rows[0][0].Text, rows[0][1].Text, rows[1][0].Text, rows[1][1].Text}
	if want := []string{"Reject", "Add mission", "Park", "Change"}; !equalStringSlices(labels, want) {
		t.Fatalf("rows = %#v, want Reject/Add mission/Park/Change", rows)
	}
	for _, label := range labels {
		if words := strings.Fields(label); len(words) > 2 {
			t.Fatalf("button label %q has %d words, want at most 2", label, len(words))
		}
	}
}
