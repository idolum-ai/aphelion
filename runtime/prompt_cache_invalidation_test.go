//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/agent"
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
