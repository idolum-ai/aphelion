//go:build linux

package turn

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

// Policy describes how one turn should move through the orchestration engine.
type Policy struct {
	Brokerage bool
	Proposal  bool
	Render    bool
	Reason    string
}

// DefaultPolicy decides whether a turn seeks face pressure before execution and
// whether it stages a visible scene afterward.
func DefaultPolicy(req Request) Policy {
	text := strings.TrimSpace(req.Inbound.Text)
	switch req.RunKind {
	case session.TurnRunKindInteractive, "":
		if text == "" || strings.HasPrefix(text, "/") {
			return Policy{Reason: "empty_or_command"}
		}
		if looksBrokerageTurn(text) {
			return Policy{Brokerage: true, Proposal: true, Render: true, Reason: "strategic_or_ambiguous"}
		}
		if looksSimpleFactualTurn(text) {
			return Policy{Render: true, Reason: "simple_interactive"}
		}
		return Policy{Proposal: true, Render: true, Reason: "interactive_default"}
	case session.TurnRunKindHeartbeat:
		return Policy{Reason: "heartbeat_default"}
	case session.TurnRunKindCron:
		return Policy{Reason: "cron_default"}
	case session.TurnRunKindRecovery:
		return Policy{Reason: "recovery_default"}
	default:
		return Policy{Reason: "noninteractive_default"}
	}
}

func looksBrokerageTurn(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	for _, marker := range []string{
		"brainstorm",
		"feature",
		"features",
		"roadmap",
		"direction",
		"what should we build",
		"what should i build",
		"how should we proceed",
		"help me think",
		"thinking this through",
		"inspect the repo",
		"inspect the repository",
		"look at the codebase",
		"review the project",
		"repository",
		"codebase",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return strings.Contains(trimmed, "not sure") || strings.Contains(trimmed, "unclear") || strings.Contains(trimmed, "torn between")
}

func looksSimpleFactualTurn(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return true
	}
	for _, greeting := range []string{"hello", "hi", "hey", "good morning", "good evening"} {
		if trimmed == greeting {
			return false
		}
	}
	return len(strings.Fields(trimmed)) <= 8 && len(trimmed) <= 64
}
