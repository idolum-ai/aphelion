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
	return pipeline.ShouldRenderInteractiveIdolumReply(pipeline.FacePolicy(policy), pipeline.RenderDecisionInput{
		UserText:          userText,
		FloorText:         floorText,
		ToolLog:           toolLog,
		GeneratedMessages: generated,
	})
}
