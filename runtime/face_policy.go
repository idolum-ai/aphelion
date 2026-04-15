//go:build linux

package runtime

import (
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/session"
)

type faceTurnPolicy = pipeline.FacePolicy

func decideInteractiveFacePolicy(sess *session.Session, userText string) faceTurnPolicy {
	return pipeline.DecideInteractiveFacePolicy(sess, userText)
}

func shouldRenderIdolumReply(policy faceTurnPolicy, userText string, floorText string, toolLog []string, generated []agent.Message) bool {
	return pipeline.ShouldRenderInteractiveIdolumReply(policy, pipeline.RenderDecisionInput{
		UserText:          userText,
		FloorText:         floorText,
		ToolLog:           toolLog,
		GeneratedMessages: generated,
	})
}
