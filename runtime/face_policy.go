//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

func decideInteractiveFacePolicy(sess *session.Session, userText string) pipeline.FacePolicy {
	return pipeline.DecideInteractiveFacePolicy(sess, userText)
}

func shouldRenderIdolumReply(policy pipeline.FacePolicy, userText string, floorText string, toolLog []string, generated []agent.Message) bool {
	return pipeline.ShouldRenderInteractiveIdolumReply(policy, pipeline.RenderDecisionInput{
		UserText:          userText,
		FloorText:         floorText,
		ToolLog:           toolLog,
		GeneratedMessages: generated,
	})
}
