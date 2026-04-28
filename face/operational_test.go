//go:build linux

package face

import (
	"strings"
	"testing"
)

func TestRenderReviewDigestFormatsDurableSections(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceRole:  "durable_agent",
		SourceScope: "durable_agent:idolum-email",
		SourceAgent: "idolum-email",
		ParentScope: "telegram_dm:6313146",
		Summary: strings.Join([]string{
			"durable_agent=idolum-email channel=email parent=telegram_dm:6313146 interval=2026-04-26T22:33:00Z",
			"summary: cannot verify live inbox access until gog_cli grants materialize.",
			"local: Read profile/growth.md.; Ran connection_test.",
			"questions: Can the parent materialize gog_cli?",
			"risks: parent_conversation_sync",
		}, "\n"),
	})

	for _, needle := range []string{
		"**Email child review**",
		"Email • 2026-04-26T22:33:00Z",
		"**Summary**",
		"cannot verify live inbox access",
		"**Checked**",
		"- Read profile/growth.md.",
		"- Ran connection_test.",
		"**Needs attention**",
		"- Can the parent materialize gog_cli?",
		"**Risks**",
		"- parent_conversation_sync",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
	for _, unwanted := range []string{"Source Chat:", "Source User:", "Source Role:", "durable_agent=", "parent=telegram_dm:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("RenderReviewDigest() = %q, should not contain raw label %q", out, unwanted)
		}
	}
}

func TestRenderReviewDigestExtractsInlineSummaryHighlights(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceRole:  "durable_agent",
		SourceScope: "durable_agent:idolum-daily-review",
		SourceAgent: "idolum-daily-review",
		ParentScope: "heartbeat:admin-house",
		Summary: strings.Join([]string{
			"durable_agent=idolum-daily-review channel=daily_review parent=heartbeat:admin-house interval=2026-04-26",
			"summary: Scheduled check-in from child for 2026-04-26. What matters: - Read the 2026-04-26 transcript: 420 staged entries. - Read the daily-review child profile files requested by parent guidance: - profile/growth.md - profile/capability-ledger.md - profile/scorecard.md - Current daily-review child capability ledger says no active grants. - What worked yesterday: - Daily review recovered: the 2026-04-25 review ran and summarized the prior day. - Semantic/memory cleanup made strong progress: - generated reports/semantic-quarantine-review.pdf",
			"local: Reviewed staged transcript for 2026-04-26 and drafted next-day actions.",
			"questions: What guidance should I apply before the next daily check-in?",
			"risks: scheduled_check_in",
		}, "\n"),
	})

	for _, needle := range []string{
		"**Daily review**",
		"Daily review • 2026-04-26",
		"**Summary**",
		"Scheduled check-in from child for 2026-04-26.",
		"**Highlights**",
		"- Read the 2026-04-26 transcript: 420 staged entries.",
		"- Current daily-review child capability ledger says no active grants.",
		"- What worked yesterday: Daily review recovered",
		"**Checked**",
		"- Reviewed staged transcript for 2026-04-26 and drafted next-day actions.",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
	for _, unwanted := range []string{
		"durable_agent=",
		"parent=heartbeat:",
		"What matters: -",
		"- profile/growth.md",
		"- profile/capability-ledger.md",
		"- profile/scorecard.md",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("RenderReviewDigest() = %q, should not contain %q", out, unwanted)
		}
	}
}

func TestRenderReviewDigestKeepsSimpleReviewCompact(t *testing.T) {
	out := RenderReviewDigest(ReviewDigestNotice{
		SourceChatID: 7001,
		SourceUserID: 44,
		SourceRole:   "approved_user",
		TurnRange:    "1-3",
		Summary:      "user requested package install in isolated workspace",
	})

	for _, needle := range []string{
		"**Review: approved user**",
		"`turns=1-3 chat=7001 user=44 role=approved_user`",
		"**Summary**",
		"user requested package install in isolated workspace",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("RenderReviewDigest() = %q, want substring %q", out, needle)
		}
	}
}
