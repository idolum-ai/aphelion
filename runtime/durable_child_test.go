//go:build linux

package runtime

import (
	"path/filepath"
	"reflect"
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
	parent.Providers.FallbackChain = []string{"openrouter"}
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
	if child.Agent.UserWorkspaceRoot != scope.UserWorkspace {
		t.Fatalf("Agent.UserWorkspaceRoot = %q, want %q", child.Agent.UserWorkspaceRoot, scope.UserWorkspace)
	}
	if child.Agent.UserMemoryRoot != scope.UserMemory {
		t.Fatalf("Agent.UserMemoryRoot = %q, want %q", child.Agent.UserMemoryRoot, scope.UserMemory)
	}
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
	if child.Providers.Default != "openrouter" {
		t.Fatalf("Providers.Default = %q, want openrouter", child.Providers.Default)
	}
	if !reflect.DeepEqual(child.Providers.FallbackChain, []string{"anthropic"}) {
		t.Fatalf("Providers.FallbackChain = %#v, want []string{\"anthropic\"}", child.Providers.FallbackChain)
	}
	if child.Providers.Anthropic.APIKey != "sk-ant-parent" {
		t.Fatalf("Providers.Anthropic.APIKey = %q, want inherited parent fallback key", child.Providers.Anthropic.APIKey)
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

func TestDurableAgentChildConfigInheritsParentFallbackForNativePrimary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := config.Default()
	parent.Telegram.BotToken = "tg-parent"
	parent.Sessions.DBPath = filepath.Join(root, "sessions.db")
	parent.Governor.Backend = "native"
	parent.Governor.NativeProvider = "anthropic"
	parent.Face.Backend = "provider"
	parent.Providers.Default = "anthropic"
	parent.Providers.FallbackChain = []string{"openrouter"}
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
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-ant-child",
			Model:          "claude-child-model",
			MaxTokens:      999,
		},
	}

	child := durableAgentChildConfig(&parent, agent, scope)
	if child.Providers.Default != "anthropic" {
		t.Fatalf("Providers.Default = %q, want anthropic", child.Providers.Default)
	}
	if !reflect.DeepEqual(child.Providers.FallbackChain, []string{"openrouter"}) {
		t.Fatalf("Providers.FallbackChain = %#v, want []string{\"openrouter\"}", child.Providers.FallbackChain)
	}
	if child.Providers.Anthropic.APIKey != "sk-ant-child" {
		t.Fatalf("Providers.Anthropic.APIKey = %q, want child primary key override", child.Providers.Anthropic.APIKey)
	}
	if child.Providers.OpenRouter.APIKey != "sk-or-parent" {
		t.Fatalf("Providers.OpenRouter.APIKey = %q, want inherited parent fallback key", child.Providers.OpenRouter.APIKey)
	}
}

func TestDurableAgentChildConfigUsesOpenAINativeBootstrap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := config.Default()
	parent.Telegram.BotToken = "tg-parent"
	parent.Sessions.DBPath = filepath.Join(root, "sessions.db")
	parent.Governor.Backend = "native"
	parent.Governor.NativeProvider = "openai"
	parent.Face.Backend = "provider"
	parent.Providers.Default = "openai"
	parent.Providers.FallbackChain = []string{"anthropic"}
	parent.Providers.OpenAI.APIKey = "sk-openai-parent"
	parent.Providers.Anthropic.APIKey = "sk-ant-parent"
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
			NativeProvider: "openai",
			APIKey:         "sk-openai-child",
			BaseURL:        "https://api.openai.test/v1",
			Model:          "gpt-5.5",
			MaxTokens:      777,
		},
	}

	child := durableAgentChildConfig(&parent, agent, scope)
	if child.Governor.NativeProvider != "openai" {
		t.Fatalf("Governor.NativeProvider = %q, want openai", child.Governor.NativeProvider)
	}
	if child.Providers.Default != "openai" {
		t.Fatalf("Providers.Default = %q, want openai", child.Providers.Default)
	}
	if !reflect.DeepEqual(child.Providers.FallbackChain, []string{"anthropic"}) {
		t.Fatalf("Providers.FallbackChain = %#v, want []string{\"anthropic\"}", child.Providers.FallbackChain)
	}
	if child.Providers.OpenAI.APIKey != "sk-openai-child" {
		t.Fatalf("Providers.OpenAI.APIKey = %q, want child key", child.Providers.OpenAI.APIKey)
	}
	if child.Providers.OpenAI.BaseURL != "https://api.openai.test/v1" {
		t.Fatalf("Providers.OpenAI.BaseURL = %q, want child base url", child.Providers.OpenAI.BaseURL)
	}
	if child.Providers.OpenAI.Model != "gpt-5.5" {
		t.Fatalf("Providers.OpenAI.Model = %q, want gpt-5.5", child.Providers.OpenAI.Model)
	}
	if child.Providers.OpenAI.MaxTokens != 777 {
		t.Fatalf("Providers.OpenAI.MaxTokens = %d, want 777", child.Providers.OpenAI.MaxTokens)
	}
	if child.Providers.Anthropic.APIKey != "sk-ant-parent" {
		t.Fatalf("Providers.Anthropic.APIKey = %q, want inherited parent fallback key", child.Providers.Anthropic.APIKey)
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
