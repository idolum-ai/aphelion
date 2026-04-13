//go:build linux

package session

import (
	"fmt"
	"strings"
)

const DefaultReviewSummaryMaxChars = 600

type ReviewSummaryInput struct {
	SourceChatID int64
	SourceUserID int64
	SourceRole   string
	SourceScope  ScopeRef
	TurnIndex    int
	UserText     string
	SceneText    string
	ToolLog      []string
}

// BuildReviewSummary creates a compact, bounded, provenance-labeled digest.
func BuildReviewSummary(in ReviewSummaryInput, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultReviewSummaryMaxChars
	}

	role := strings.TrimSpace(in.SourceRole)
	if role == "" {
		role = "unknown"
	}
	provenance := fmt.Sprintf(
		"provenance chat=%d user=%d role=%s turn=%d",
		in.SourceChatID,
		in.SourceUserID,
		role,
		in.TurnIndex,
	)
	if scope := NormalizeScopeRef(in.SourceScope); !scope.IsZero() {
		provenance += " scope=" + scope.String()
		if scope.DurableAgentID != "" {
			provenance += " agent=" + scope.DurableAgentID
		}
		if scope.ParentScopeKind != "" || scope.ParentScopeID != "" {
			parent := NormalizeScopeRef(ScopeRef{
				Kind: scope.ParentScopeKind,
				ID:   scope.ParentScopeID,
			})
			if !parent.IsZero() {
				provenance += " parent=" + parent.String()
			}
		}
	}

	userText := clampChars(normalizeWhitespace(in.UserText), maxChars/3)
	if userText == "" {
		userText = "(empty)"
	}
	replyText := clampChars(normalizeWhitespace(in.SceneText), maxChars/3)
	if replyText == "" {
		replyText = "(empty)"
	}

	var b strings.Builder
	b.WriteString(provenance)
	b.WriteString("\nuser: ")
	b.WriteString(userText)
	b.WriteString("\nreply: ")
	b.WriteString(replyText)
	if len(in.ToolLog) > 0 {
		b.WriteString("\ntools: ")
		b.WriteString(clampChars(normalizeWhitespace(strings.Join(in.ToolLog, ", ")), maxChars/3))
	}

	return clampChars(b.String(), maxChars)
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func clampChars(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 3 {
		return string(r[:limit])
	}
	return string(r[:limit-3]) + "..."
}
