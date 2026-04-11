//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/session"
)

type faceTurnPolicy struct {
	Brokerage bool
	Proposal  bool
	Render    bool
}

func decideInteractiveFacePolicy(sess *session.Session, userText string) faceTurnPolicy {
	trimmed := strings.TrimSpace(userText)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") {
		return faceTurnPolicy{}
	}
	if looksBrokerageTurn(trimmed) {
		return faceTurnPolicy{Brokerage: true, Render: true}
	}
	if looksToolHeavyRequest(trimmed) || lastTurnHadToolMessages(sess.Messages) {
		return faceTurnPolicy{Render: true}
	}
	if isSimpleFactualTurn(trimmed) {
		return faceTurnPolicy{Render: false}
	}
	return faceTurnPolicy{Proposal: true, Render: true}
}

func shouldRenderIdolumReply(policy faceTurnPolicy, userText string, floorText string, toolLog []string, generated []agent.Message) bool {
	if !policy.Render {
		return false
	}
	if len(toolLog) > 0 || generatedHasToolMessages(generated) {
		return false
	}
	if looksMechanicalReply(floorText) {
		return false
	}
	if isSimpleFactualTurn(userText) && countWords(floorText) <= 24 {
		return false
	}
	return true
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

func isSimpleFactualTurn(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return true
	}
	for _, greeting := range []string{"hello", "hi", "hey", "good morning", "good evening"} {
		if trimmed == greeting {
			return false
		}
	}
	if countWords(trimmed) > 8 || len(trimmed) > 64 {
		return false
	}
	if strings.Contains(trimmed, "how are you") || strings.Contains(trimmed, "i feel") || strings.Contains(trimmed, "i'm feeling") {
		return false
	}
	if strings.Contains(trimmed, "please") && (strings.Contains(trimmed, "look") || strings.Contains(trimmed, "explore") || strings.Contains(trimmed, "investigate")) {
		return false
	}
	return true
}

func looksToolHeavyRequest(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	for _, marker := range []string{
		"```",
		"stack trace",
		"traceback",
		"panic:",
		"test failed",
		"compile error",
		"build failed",
		".go",
		".py",
		".ts",
		"error:",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	return false
}

func looksBrokerageTurn(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if trimmed == "" {
		return false
	}
	for _, marker := range []string{
		"come up with",
		"brainstorm",
		"feature",
		"features",
		"roadmap",
		"direction",
		"plan",
		"what should we build",
		"what should i build",
		"how should we proceed",
		"how should i proceed",
		"help me think",
		"thinking this through",
		"look into the codebase",
		"look at the codebase",
		"inspect the repo",
		"inspect the repository",
		"review the project",
		"explore the repo",
		"repository",
		"codebase",
	} {
		if strings.Contains(trimmed, marker) {
			return true
		}
	}
	if strings.Contains(trimmed, "not sure") || strings.Contains(trimmed, "unclear") || strings.Contains(trimmed, "torn between") {
		return true
	}
	return false
}

func looksMechanicalReply(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "```") {
		return true
	}
	if strings.Count(trimmed, "\n") >= 6 {
		return true
	}
	if countWords(trimmed) > 160 {
		return true
	}
	return false
}

func countWords(text string) int {
	return len(strings.Fields(strings.TrimSpace(text)))
}
