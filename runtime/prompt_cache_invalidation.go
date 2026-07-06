//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type promptCacheInvalidatingFaceRenderer interface {
	InvalidatePromptCache(workspaceRoot string)
}

func (r *Runtime) maybeInvalidateStablePromptCacheForToolHistory(scope sandbox.Scope, history []agent.Message) {
	if r == nil || !toolHistoryMayHaveMutatedStablePromptFiles(history) {
		return
	}
	root := strings.TrimSpace(scope.GlobalRoot)
	if root == "" {
		return
	}
	if r.promptStableCache != nil {
		r.promptStableCache.invalidateWorkspace(root)
	}
	r.invalidateFacePromptCaches(root)
}

func (r *Runtime) invalidateFacePromptCaches(workspaceRoot string) {
	if r == nil {
		return
	}
	renderers := []face.Renderer{}
	if r.faceModel != nil {
		renderers = append(renderers, r.faceModel)
	}
	r.faceModelsMu.Lock()
	for _, renderer := range r.faceModels {
		if renderer != nil {
			renderers = append(renderers, renderer)
		}
	}
	r.faceModelsMu.Unlock()

	for _, renderer := range renderers {
		invalidator, ok := renderer.(promptCacheInvalidatingFaceRenderer)
		if !ok || invalidator == nil {
			continue
		}
		invalidator.InvalidatePromptCache(workspaceRoot)
	}
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
