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
	for _, needle := range []string{"Mission Control Proposal", "Runtime recovery", "Adding this only creates a candidate"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("FormatReviewEventMessage() = %q, want substring %q", text, needle)
		}
	}
	rows := ReviewEventInlineRows(event)
	if len(rows) != 2 || len(rows[0]) != 2 || len(rows[1]) != 2 {
		t.Fatalf("rows = %#v, want two rows with four mission proposal buttons", rows)
	}
	if rows[0][0].Text != "Add to Mission Control" || rows[0][1].Text != "Ask edit" || rows[1][0].Text != "Park" || rows[1][1].Text != "Reject" {
		t.Fatalf("rows = %#v, want Add/Ask edit/Park/Reject", rows)
	}
}
