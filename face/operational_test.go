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
		"**Review: idolum-email**",
		"`durable_agent=idolum-email channel=email parent=telegram_dm:6313146 interval=2026-04-26T22:33:00Z`",
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
	for _, unwanted := range []string{"Source Chat:", "Source User:", "Source Role:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("RenderReviewDigest() = %q, should not contain raw label %q", out, unwanted)
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
