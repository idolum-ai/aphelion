//go:build linux

package turn

import (
	"strings"

	"github.com/idolum-ai/aphelion/pipeline"
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
		if pipeline.ParseExecutionContract(text) != nil {
			return Policy{Brokerage: true, Proposal: true, Render: true, Reason: "user_execution_contract"}
		}
		return Policy{Proposal: true, Render: true, Reason: "interactive_conversational_default"}
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
