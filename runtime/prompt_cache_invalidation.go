//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func (r *Runtime) maybeInvalidateStablePromptCacheForToolHistory(scope sandbox.Scope, history []agent.Message) {
	if r == nil || r.promptStableCache == nil || !toolHistoryMayHaveMutatedStablePromptFiles(history) {
		return
	}
	r.promptStableCache.invalidateWorkspace(scope.GlobalRoot)
}

func toolHistoryMayHaveMutatedStablePromptFiles(history []agent.Message) bool {
	for _, msg := range history {
		if strings.TrimSpace(msg.Role) != "tool" {
			continue
		}
		switch strings.TrimSpace(msg.ToolName) {
		case "exec", "write_file":
			return true
		}
	}
	return false
}
