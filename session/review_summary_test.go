//go:build linux

package session

import (
	"strings"
	"testing"
)

func TestBuildReviewSummaryIncludesProvenance(t *testing.T) {
	t.Parallel()

	out := BuildReviewSummary(ReviewSummaryInput{
		SourceChatID: 77,
		SourceUserID: 88,
		SourceRole:   "approved_user",
		TurnIndex:    4,
		UserText:     "hello",
		SceneText:    "hi there",
		ToolLog:      []string{"exec:ok"},
	}, 300)

	if !strings.Contains(out, "provenance chat=77 user=88 role=approved_user turn=4") {
		t.Fatalf("summary missing provenance: %q", out)
	}
	if !strings.Contains(out, "user: hello") {
		t.Fatalf("summary missing user text: %q", out)
	}
	if !strings.Contains(out, "reply: hi there") {
		t.Fatalf("summary missing reply text: %q", out)
	}
	if !strings.Contains(out, "tools: exec:ok") {
		t.Fatalf("summary missing tool log: %q", out)
	}
}

func TestBuildReviewSummaryIncludesScopeProvenanceWhenPresent(t *testing.T) {
	t.Parallel()

	out := BuildReviewSummary(ReviewSummaryInput{
		SourceChatID: 77,
		SourceRole:   "durable_agent",
		SourceScope: ScopeRef{
			Kind:            ScopeKindDurableAgent,
			ID:              "family-group",
			DurableAgentID:  "family-group",
			ParentScopeKind: ScopeKindTelegramDM,
			ParentScopeID:   "1001",
		},
		TurnIndex: 2,
		UserText:  "the group is pushing for a standing tone change",
		SceneText: "surfacing for parent review",
	}, 400)

	if !strings.Contains(out, "scope=durable_agent:family-group") {
		t.Fatalf("summary missing source scope: %q", out)
	}
	if !strings.Contains(out, "agent=family-group") {
		t.Fatalf("summary missing durable agent id: %q", out)
	}
	if !strings.Contains(out, "parent=telegram_dm:1001") {
		t.Fatalf("summary missing parent scope: %q", out)
	}
}

func TestBuildReviewSummaryIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 500)
	out := BuildReviewSummary(ReviewSummaryInput{
		SourceChatID: 1,
		SourceUserID: 2,
		SourceRole:   "approved_user",
		TurnIndex:    1,
		UserText:     long,
		SceneText:    long,
		ToolLog:      []string{long},
	}, 120)

	if got := len([]rune(out)); got > 120 {
		t.Fatalf("summary length = %d, want <= 120", got)
	}
}
