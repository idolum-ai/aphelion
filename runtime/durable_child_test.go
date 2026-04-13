//go:build linux

package runtime

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDurableAgentChildConfigUsesNativeBootstrapWithoutParentCredentials(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := config.Default()
	parent.Telegram.BotToken = "tg-parent"
	parent.Sessions.DBPath = filepath.Join(root, "sessions.db")
	parent.Governor.Backend = "native"
	parent.Governor.NativeProvider = "anthropic"
	parent.Face.Backend = "provider"
	parent.Providers.Default = "anthropic"
	parent.Providers.Anthropic.APIKey = "sk-ant-parent"
	parent.Providers.OpenRouter.APIKey = "sk-or-parent"
	parent.Providers.OpenAI.APIKey = "sk-openai-parent"
	parent.Agent.PromptRoot = filepath.Join(root, "prompt")

	scope, err := sandbox.DurableAgentScope(
		"family-group",
		parent.Agent.PromptRoot,
		filepath.Join(root, "workspace"),
		filepath.Join(root, "memory"),
		"default",
	)
	if err != nil {
		t.Fatalf("DurableAgentScope() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID: "family-group",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-child",
			BaseURL:        "https://openrouter.child.test",
			Model:          "openrouter/child-model",
			MaxTokens:      321,
		},
	}

	child := durableAgentChildConfig(&parent, agent, scope)
	if child.Telegram.BotToken != "" {
		t.Fatalf("Telegram.BotToken = %q, want empty", child.Telegram.BotToken)
	}
	if child.Governor.Backend != "native" {
		t.Fatalf("Governor.Backend = %q, want native", child.Governor.Backend)
	}
	if child.Governor.NativeProvider != "openrouter" {
		t.Fatalf("Governor.NativeProvider = %q, want openrouter", child.Governor.NativeProvider)
	}
	if child.Face.Backend != "provider" {
		t.Fatalf("Face.Backend = %q, want provider", child.Face.Backend)
	}
	if child.Providers.Anthropic.APIKey != "" {
		t.Fatalf("Providers.Anthropic.APIKey = %q, want cleared parent key", child.Providers.Anthropic.APIKey)
	}
	if child.Providers.OpenAI.APIKey != "" {
		t.Fatalf("Providers.OpenAI.APIKey = %q, want cleared parent key", child.Providers.OpenAI.APIKey)
	}
	if child.Providers.OpenRouter.APIKey != "sk-or-child" {
		t.Fatalf("Providers.OpenRouter.APIKey = %q, want child key", child.Providers.OpenRouter.APIKey)
	}
	if child.Providers.OpenRouter.BaseURL != "https://openrouter.child.test" {
		t.Fatalf("Providers.OpenRouter.BaseURL = %q, want child base url", child.Providers.OpenRouter.BaseURL)
	}
	if child.Providers.OpenRouter.Model != "openrouter/child-model" {
		t.Fatalf("Providers.OpenRouter.Model = %q, want child model", child.Providers.OpenRouter.Model)
	}
	if child.Providers.OpenRouter.MaxTokens != 321 {
		t.Fatalf("Providers.OpenRouter.MaxTokens = %d, want 321", child.Providers.OpenRouter.MaxTokens)
	}
}

func TestDurableAgentChildConfigUsesCodexBootstrapWithoutParentCredentials(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := config.Default()
	parent.Telegram.BotToken = "tg-parent"
	parent.Sessions.DBPath = filepath.Join(root, "sessions.db")
	parent.Governor.Backend = "native"
	parent.Governor.NativeProvider = "anthropic"
	parent.Face.Backend = "provider"
	parent.Providers.Default = "anthropic"
	parent.Providers.Anthropic.APIKey = "sk-ant-parent"
	parent.Providers.OpenRouter.APIKey = "sk-or-parent"
	parent.Agent.PromptRoot = filepath.Join(root, "prompt")

	scope, err := sandbox.DurableAgentScope(
		"family-group",
		parent.Agent.PromptRoot,
		filepath.Join(root, "workspace"),
		filepath.Join(root, "memory"),
		"default",
	)
	if err != nil {
		t.Fatalf("DurableAgentScope() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID: "family-group",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:         "codex",
			CodexAuthSource: "codex_cli",
			CodexHome:       "/srv/family-group/.codex",
			CodexBaseURL:    "https://chatgpt.example.test/backend-api",
		},
	}

	child := durableAgentChildConfig(&parent, agent, scope)
	if child.Telegram.BotToken != "" {
		t.Fatalf("Telegram.BotToken = %q, want empty", child.Telegram.BotToken)
	}
	if child.Governor.Backend != "codex" {
		t.Fatalf("Governor.Backend = %q, want codex", child.Governor.Backend)
	}
	if child.Governor.NativeProvider != "" {
		t.Fatalf("Governor.NativeProvider = %q, want empty", child.Governor.NativeProvider)
	}
	if child.Governor.Codex.AuthSource != "codex_cli" {
		t.Fatalf("Governor.Codex.AuthSource = %q, want codex_cli", child.Governor.Codex.AuthSource)
	}
	if child.Governor.Codex.CodexHome != "/srv/family-group/.codex" {
		t.Fatalf("Governor.Codex.CodexHome = %q, want /srv/family-group/.codex", child.Governor.Codex.CodexHome)
	}
	if child.Governor.Codex.BaseURL != "https://chatgpt.example.test/backend-api" {
		t.Fatalf("Governor.Codex.BaseURL = %q, want child codex base url", child.Governor.Codex.BaseURL)
	}
	if child.Face.Backend != "floor_fallback" {
		t.Fatalf("Face.Backend = %q, want floor_fallback", child.Face.Backend)
	}
	if child.Providers.Anthropic.APIKey != "" || child.Providers.OpenRouter.APIKey != "" || child.Providers.OpenAI.APIKey != "" {
		t.Fatalf("Providers = %#v, want cleared parent/native credentials", child.Providers)
	}
}
