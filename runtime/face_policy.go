//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

type faceTurnPolicy struct {
	Proposal bool
	Render   bool
}

func decideInteractiveFacePolicy(sess *session.Session, userText string) faceTurnPolicy {
	policy := pipeline.DecideInteractiveFacePolicy(sess, userText)
	return faceTurnPolicy(policy)
}

func shouldRenderIdolumReply(policy faceTurnPolicy, userText string, floorText string, toolLog []string, generated []agent.Message) bool {
	return pipeline.ShouldRenderIdolumReply(pipeline.FacePolicy(policy), userText, floorText, toolLog, generated)
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
