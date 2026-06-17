//go:build linux

package runtime

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/workspace"
)

func TestToolHistoryMayHaveMutatedStablePromptFiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		history []agent.Message
		want    bool
	}{
		{
			name:    "read only tool does not invalidate",
			history: []agent.Message{{Role: "tool", ToolName: "read_file", Content: "ok"}},
		},
		{
			name:    "exec invalidates conservatively",
			history: []agent.Message{{Role: "tool", ToolName: "exec", Content: "ok"}},
			want:    true,
		},
		{
			name:    "write file invalidates",
			history: []agent.Message{{Role: "tool", ToolName: "write_file", Content: "ok"}},
			want:    true,
		},
		{
			name:    "assistant tool request alone does not invalidate",
			history: []agent.Message{{Role: "assistant", ToolCalls: []agent.ToolCall{{Name: "write_file"}}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toolHistoryMayHaveMutatedStablePromptFiles(tc.history); got != tc.want {
				t.Fatalf("toolHistoryMayHaveMutatedStablePromptFiles() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMaybeInvalidateStablePromptCacheForToolHistoryInvalidatesGovernorAndFaceCaches(t *testing.T) {
	t.Parallel()

	cfg, _, _, _ := buildRuntimeFixtures(t)
	root := cfg.Agent.PromptRoot
	cfg.Agent.Workspace = root
	cache := newPromptStableContextCache()
	key := promptStableContextCacheKey(cfg.Agent)
	cache.entries[key] = promptStableContextCacheEntry{
		fingerprint: "cached",
		stable:      []workspace.LoadedFile{{Path: "AGENTS.md", Content: "old"}},
	}
	primaryFace := &invalidationTrackingFaceRenderer{}
	secondaryFace := &invalidationTrackingFaceRenderer{}
	rt := &Runtime{
		promptStableCache: cache,
		faceModel:         primaryFace,
		faceModels: map[string]face.Renderer{
			"secondary": secondaryFace,
			"duplicate": primaryFace,
		},
	}

	rt.maybeInvalidateStablePromptCacheForToolHistory(sandbox.Scope{GlobalRoot: root}, []agent.Message{
		{Role: "tool", ToolName: "write_file", Content: "ok"},
	})

	if len(cache.entries) != 0 {
		t.Fatalf("stable prompt cache entries = %#v, want invalidated", cache.entries)
	}
	if primaryFace.invalidations == 0 || primaryFace.lastRoot != root {
		t.Fatalf("primary face invalidations/root = %d/%q, want >=1/%q", primaryFace.invalidations, primaryFace.lastRoot, root)
	}
	if secondaryFace.invalidations != 1 || secondaryFace.lastRoot != root {
		t.Fatalf("secondary face invalidations/root = %d/%q, want 1/%q", secondaryFace.invalidations, secondaryFace.lastRoot, root)
	}
}

type invalidationTrackingFaceRenderer struct {
	invalidations int
	lastRoot      string
}

func (r *invalidationTrackingFaceRenderer) Render(context.Context, face.RenderRequest) (string, error) {
	return "ok", nil
}

func (r *invalidationTrackingFaceRenderer) InvalidatePromptCache(workspaceRoot string) {
	r.invalidations++
	r.lastRoot = workspaceRoot
}
