//go:build linux

package provider

import "github.com/idolum-ai/aphelion/agent"

func resolveMaxTokens(defaultMaxTokens int, opts agent.CompleteOptions) int {
	if opts.MaxTokens > 0 {
		return opts.MaxTokens
	}
	return defaultMaxTokens
}
