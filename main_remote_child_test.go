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

func TestRemoteDurableAgentChildConfigCodexBootstrapIsComplete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bootstrap := core.DurableAgentRemoteBootstrap{
		AgentID: "remote-codex-child",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:         "codex",
			CodexAuthSource: "auto",
			CodexHome:       filepath.Join(root, ".codex"),
			CodexBaseURL:    "https://chatgpt.example.test/backend-api",
		},
		LocalStorageRoots: []string{
			filepath.Join(root, "workspace"),
			filepath.Join(root, "memory"),
		},
	}
	agent := core.DurableAgent{AgentID: "remote-codex-child"}

	cfg := remoteDurableAgentChildConfig(filepath.Join(root, "sessions.db"), bootstrap, agent)
	if cfg.Governor.Backend != "codex" {
		t.Fatalf("Governor.Backend = %q, want codex", cfg.Governor.Backend)
	}
	if cfg.Governor.Codex.AuthSource != "auto" {
		t.Fatalf("Governor.Codex.AuthSource = %q, want auto", cfg.Governor.Codex.AuthSource)
	}
	if cfg.Governor.Codex.CodexHome != filepath.Join(root, ".codex") {
		t.Fatalf("Governor.Codex.CodexHome = %q, want child codex home", cfg.Governor.Codex.CodexHome)
	}
	if cfg.Governor.Codex.BaseURL != "https://chatgpt.example.test/backend-api" {
		t.Fatalf("Governor.Codex.BaseURL = %q, want child codex base URL", cfg.Governor.Codex.BaseURL)
	}
	if cfg.Governor.Codex.Model == "" {
		t.Fatal("Governor.Codex.Model is empty; want hydrated default")
	}
	if cfg.Governor.Codex.ContextWindow <= 0 {
		t.Fatalf("Governor.Codex.ContextWindow = %d, want hydrated default", cfg.Governor.Codex.ContextWindow)
	}
	if cfg.Governor.Codex.MaxContinuations <= 0 {
		t.Fatalf("Governor.Codex.MaxContinuations = %d, want hydrated default", cfg.Governor.Codex.MaxContinuations)
	}
	if cfg.Governor.Codex.TransportRetries < 0 {
		t.Fatalf("Governor.Codex.TransportRetries = %d, want hydrated non-negative default", cfg.Governor.Codex.TransportRetries)
	}
	if cfg.Governor.Codex.ResponseHeaderTimeout == "" {
		t.Fatal("Governor.Codex.ResponseHeaderTimeout is empty; want hydrated default")
	}
}
