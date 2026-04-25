//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestRemoteDurableAgentChildConfigClearsTelegramAndPrincipals(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID: "external-child",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-ant-child",
		},
		LocalStorageRoots: []string{
			filepath.Join(root, "workspace"),
			filepath.Join(root, "memory"),
		},
	}
	agent := core.DurableAgent{AgentID: "external-child"}

	cfg := remoteDurableAgentChildConfig(filepath.Join(root, "sessions.db"), bootstrap, agent)
	if cfg.Telegram.BotToken != "" {
		t.Fatalf("Telegram.BotToken = %q, want empty", cfg.Telegram.BotToken)
	}
	if len(cfg.Telegram.DurableGroups) > 0 {
		t.Fatalf("Telegram.DurableGroups = %d, want 0", len(cfg.Telegram.DurableGroups))
	}
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 || len(cfg.Principals.Telegram.ApprovedUserIDs) > 0 {
		t.Fatalf("Principals.Telegram = %#v, want empty", cfg.Principals.Telegram)
	}
}
