//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/session"
)

type faceTurnPolicy struct {
	Proposal bool
	Render   bool
}

func decideInteractiveFacePolicy(sess *session.Session, userText string) faceTurnPolicy {
	_ = sess
	trimmed := strings.TrimSpace(userText)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return faceTurnPolicy{}
	}
	return faceTurnPolicy{Proposal: true, Render: true}
}

func shouldRenderIdolumReply(policy faceTurnPolicy, userText string, floorText string, toolLog []string, generated []agent.Message) bool {
	_ = userText
	_ = floorText
	_ = toolLog
	_ = generated
	return policy.Render
}

func lastTurnHadToolMessages(messages []session.Message) bool {
	lastTurn := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Compacted {
			continue
		}
		lastTurn = messages[i].TurnIndex
		break
	}
	if lastTurn < 0 {
		return false
	}
	for _, msg := range messages {
		if msg.Compacted || msg.TurnIndex != lastTurn {
			continue
		}
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}

func generatedHasToolMessages(messages []agent.Message) bool {
	for _, msg := range messages {
		if msg.Role == "tool" {
			return true
		}
	}
	return false
}
