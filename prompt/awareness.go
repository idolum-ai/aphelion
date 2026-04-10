//go:build linux

package prompt

import (
	"fmt"
	"strings"
)

type RuntimeAwareness struct {
	SessionKind          string
	RunKind              string
	Channel              string
	GovernorBackend      string
	GovernorProvider     string
	GovernorModel        string
	GovernorProviderPath []string
	ActiveProvider       string
	FallbackActive       bool
	ReasoningEffort      string
	ReasoningSummary     string
	FaceBackend          string
	FaceProvider         string
	DeliveryMode         string
	StreamReply          bool
	PromptRoot           string
	ExecRoot             string
	SharedMemoryRoot     string
	UserWorkspaceRoot    string
	UserMemoryRoot       string
	WorkingRoot          string
	SandboxMode          string
	NetworkPolicy        string
}

func renderGovernorRuntimeAwarenessBlock(aw RuntimeAwareness) string {
	lines := []string{"## Runtime Awareness"}
	lines = append(lines, nonEmptyAwarenessLine("session_kind", aw.SessionKind))
	lines = append(lines, nonEmptyAwarenessLine("run_kind", aw.RunKind))
	lines = append(lines, nonEmptyAwarenessLine("channel", aw.Channel))
	lines = append(lines, nonEmptyAwarenessLine("governor_backend", aw.GovernorBackend))
	lines = append(lines, nonEmptyAwarenessLine("governor_provider", aw.GovernorProvider))
	lines = append(lines, nonEmptyAwarenessLine("governor_model", aw.GovernorModel))
	if path := formatProviderPath(aw.GovernorProviderPath); path != "" {
		lines = append(lines, fmt.Sprintf("- configured_provider_path: %s", path))
	}
	lines = append(lines, nonEmptyAwarenessLine("active_provider", aw.ActiveProvider))
	lines = append(lines, fmt.Sprintf("- fallback_active: %t", aw.FallbackActive))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_effort", aw.ReasoningEffort))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_summary", aw.ReasoningSummary))
	lines = append(lines, nonEmptyAwarenessLine("prompt_root", aw.PromptRoot))
	lines = append(lines, nonEmptyAwarenessLine("exec_root", aw.ExecRoot))
	lines = append(lines, nonEmptyAwarenessLine("shared_memory_root", aw.SharedMemoryRoot))
	lines = append(lines, nonEmptyAwarenessLine("user_workspace_root", aw.UserWorkspaceRoot))
	lines = append(lines, nonEmptyAwarenessLine("user_memory_root", aw.UserMemoryRoot))
	lines = append(lines, nonEmptyAwarenessLine("working_root", aw.WorkingRoot))
	lines = append(lines, nonEmptyAwarenessLine("sandbox_mode", aw.SandboxMode))
	lines = append(lines, nonEmptyAwarenessLine("network_policy", aw.NetworkPolicy))
	return strings.Join(compactLines(lines), "\n")
}

func renderFaceAwarenessBlock(aw RuntimeAwareness, principalRole string, mode string) string {
	lines := []string{"## Delivery Awareness"}
	lines = append(lines, nonEmptyAwarenessLine("session_kind", aw.SessionKind))
	lines = append(lines, nonEmptyAwarenessLine("run_kind", aw.RunKind))
	lines = append(lines, nonEmptyAwarenessLine("channel", aw.Channel))
	lines = append(lines, nonEmptyAwarenessLine("principal_role", principalRole))
	lines = append(lines, nonEmptyAwarenessLine("mode", mode))
	lines = append(lines, nonEmptyAwarenessLine("governor_backend", aw.GovernorBackend))
	lines = append(lines, nonEmptyAwarenessLine("governor_provider", aw.GovernorProvider))
	lines = append(lines, nonEmptyAwarenessLine("governor_model", aw.GovernorModel))
	lines = append(lines, nonEmptyAwarenessLine("active_provider", aw.ActiveProvider))
	lines = append(lines, fmt.Sprintf("- fallback_active: %t", aw.FallbackActive))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_effort", aw.ReasoningEffort))
	lines = append(lines, nonEmptyAwarenessLine("reasoning_summary", aw.ReasoningSummary))
	lines = append(lines, nonEmptyAwarenessLine("face_backend", aw.FaceBackend))
	lines = append(lines, nonEmptyAwarenessLine("face_provider", aw.FaceProvider))
	lines = append(lines, nonEmptyAwarenessLine("delivery_mode", aw.DeliveryMode))
	lines = append(lines, fmt.Sprintf("- stream_reply: %t", aw.StreamReply))
	return strings.Join(compactLines(lines), "\n")
}

func nonEmptyAwarenessLine(key, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return fmt.Sprintf("- %s: %s", key, trimmed)
}

func compactLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func formatProviderPath(path []string) string {
	if len(path) == 0 {
		return ""
	}
	out := make([]string, 0, len(path))
	for _, segment := range path {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, " -> ")
}
