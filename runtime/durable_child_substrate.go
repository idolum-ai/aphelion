//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type durableChildSubstrate struct {
	ReadonlyPaths []string
	Labels        []string
}

func durableChildSubstrateFor(binaryPath string, agent core.DurableAgent) durableChildSubstrate {
	out := durableChildSubstrate{}
	if binaryPath = strings.TrimSpace(binaryPath); binaryPath != "" {
		out.ReadonlyPaths = append(out.ReadonlyPaths, binaryPath)
		out.Labels = append(out.Labels, "parent_binary")
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if bootstrap.Backend == "codex" && strings.TrimSpace(bootstrap.CodexHome) != "" {
		out.ReadonlyPaths = append(out.ReadonlyPaths, strings.TrimSpace(bootstrap.CodexHome))
		out.Labels = append(out.Labels, "codex_home")
	}
	return out
}
