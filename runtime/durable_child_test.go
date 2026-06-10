//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestDurableChildSandboxAccessDoesNotSpecialCaseChannelAdapter(t *testing.T) {
	t.Setenv("CHILD_ADAPTER_TOKEN", "secret-for-test")
	configHome := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(filepath.Join(configHome, "child-adapter"), 0o700); err != nil {
		t.Fatalf("MkdirAll(child-adapter) err = %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", configHome)

	access, err := durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{
		AgentID:     "child-alpha",
		ChannelKind: "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter: "child_adapter",
		}},
		BootstrapLLM: core.NodeLLMBootstrap{Backend: "codex", CodexHome: "/srv/codex"},
	}, nil)
	if err != nil {
		t.Fatalf("durableChildSandboxAccessFor() err = %v", err)
	}

	if !containsString(access.readonlyPaths, "/srv/aphelion/bin/aphelion") || !containsString(access.readonlyPaths, "/srv/codex") {
		t.Fatalf("readonlyPaths = %#v, want binary and codex home", access.readonlyPaths)
	}
	if containsString(access.readonlyPaths, filepath.Join(configHome, "child-adapter")) {
		t.Fatalf("readonlyPaths = %#v, did not expect implicit child-adapter config", access.readonlyPaths)
	}
	if len(access.env) != 0 {
		t.Fatalf("env = %#v, want no implicit child adapter env", access.env)
	}
}

func TestDurableChildSandboxAccessMaterializesGrantedRuntimeCapability(t *testing.T) {
	t.Setenv("MAIL_TOOL_TOKEN", "secret-for-test")
	root := t.TempDir()
	exe := filepath.Join(root, "mail-reader")
	if err := os.WriteFile(exe, []byte("#!/usr/bin/env bash\necho ok\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(exe) err = %v", err)
	}
	configDir := filepath.Join(root, "mail-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(configDir) err = %v", err)
	}
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-mail-reader",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract: `{
			"child_runtime": {
				"executable": "` + exe + `",
				"readonly_paths": ["` + configDir + `"],
				"env_from_parent": ["MAIL_TOOL_TOKEN"]
			}
		}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	access, err := durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{
		AgentID:      "child-alpha",
		BootstrapLLM: core.NodeLLMBootstrap{Backend: "codex", CodexHome: "/srv/codex"},
	}, store)
	if err != nil {
		t.Fatalf("durableChildSandboxAccessFor() err = %v", err)
	}

	if !containsString(access.readonlyPaths, configDir) {
		t.Fatalf("readonlyPaths = %#v, want granted config dir", access.readonlyPaths)
	}
	if access.env["MAIL_TOOL_TOKEN"] != "secret-for-test" {
		t.Fatalf("env = %#v, want granted env inherited", access.env)
	}
	if !containsBind(access.readonlyBinds, exe, "/usr/local/bin/mail-reader") {
		t.Fatalf("readonlyBinds = %#v, want executable bind", access.readonlyBinds)
	}
}

func TestDurableChildSandboxAccessIgnoresExpiredGrantWithoutRuntimeMaterial(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "mail-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(configDir) err = %v", err)
	}
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-mail-reader-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + configDir + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(runtime) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-expired-public-web-trial",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindPublicWeb,
		TargetResource: "public job pages",
		AllowedActions: []string{"fetch", "read"},
		Status:         session.CapabilityGrantStatusActive,
		ExpiresAt:      time.Now().UTC().Add(-time.Hour),
		Contract:       `{"allowed_sources":["public pages"]}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(public_web) err = %v", err)
	}

	access, err := durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{AgentID: "child-alpha"}, store)
	if err != nil {
		t.Fatalf("durableChildSandboxAccessFor() err = %v, want expired non-runtime grant ignored", err)
	}
	if !containsString(access.readonlyPaths, configDir) {
		t.Fatalf("readonlyPaths = %#v, want runtime grant materialized", access.readonlyPaths)
	}
}

func TestDurableChildSandboxAccessBlocksExpiredRuntimeGrant(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-expired-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		ExpiresAt:      time.Now().UTC().Add(-time.Hour),
		Contract:       `{"child_runtime":{"readonly_paths":["/srv/mail"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	_, err = durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{AgentID: "child-alpha"}, store)
	if err == nil || !strings.Contains(err.Error(), "child_runtime_blocked: grant_expired grant_id=capg-expired-runtime") {
		t.Fatalf("durableChildSandboxAccessFor() err = %v, want expired child_runtime block", err)
	}
}

func TestDurableChildSandboxAccessBlocksStoredExpiredRuntimeGrantWhenNoActiveRuntimeGrant(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-stored-expired-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusExpired,
		Contract:       `{"child_runtime":{"readonly_paths":["/srv/mail"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	_, err = durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{AgentID: "child-alpha"}, store)
	if err == nil || !strings.Contains(err.Error(), "child_runtime_blocked: grant_expired grant_id=capg-stored-expired-runtime") {
		t.Fatalf("durableChildSandboxAccessFor() err = %v, want stored expired child_runtime block", err)
	}
}

func TestDurableChildSandboxAccessIgnoresStoredExpiredRuntimeGrantWhenActiveReplacementExists(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "mail-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(configDir) err = %v", err)
	}
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-stored-expired-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusExpired,
		Contract:       `{"child_runtime":{"readonly_paths":["/srv/old-mail"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(expired) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-active-runtime-replacement",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + configDir + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(active) err = %v", err)
	}

	access, err := durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{AgentID: "child-alpha"}, store)
	if err != nil {
		t.Fatalf("durableChildSandboxAccessFor() err = %v, want active runtime replacement to win", err)
	}
	if !containsString(access.readonlyPaths, configDir) {
		t.Fatalf("readonlyPaths = %#v, want active runtime replacement materialized", access.readonlyPaths)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsBind(values []sandbox.BindPath, source string, target string) bool {
	for _, value := range values {
		if value.Source == source && value.Target == target {
			return true
		}
	}
	return false
}

func TestDurableAgentProfileContextLoadsExternalProfileFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	profileRoot := filepath.Join(root, "memory", "profile")
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll(profileRoot) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "persona.md"), []byte("Speak as a careful external child."), 0o600); err != nil {
		t.Fatalf("WriteFile(persona) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileRoot, "policy.md"), []byte("Ask parent before new tool access."), 0o600); err != nil {
		t.Fatalf("WriteFile(policy) err = %v", err)
	}

	ctx := durableAgentProfileContext(sandbox.Scope{SharedMemoryRoot: filepath.Join(root, "memory")}, core.DurableAgent{AgentID: "child-alpha"})
	if !strings.Contains(ctx, "External durable child profile files") || !strings.Contains(ctx, "profile/persona.md") || !strings.Contains(ctx, "Ask parent before new tool access") {
		t.Fatalf("profile context = %q, want external profile file content", ctx)
	}
}

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
	parent.Governor.Codex.AuthSource = "aphelion"
	parent.Governor.Codex.AuthPath = "/parent/codex-auth.json"
	parent.Governor.Codex.CodexHome = "/parent/.codex"
	parent.Governor.Codex.Model = "gpt-parent-codex"
	parent.Governor.Codex.ContextWindow = 123456
	parent.Governor.Codex.StoreResponses = false
	parent.Governor.Codex.MaxContinuations = 7
	parent.Governor.Codex.TransportRetries = 0
	parent.Governor.Codex.ResponseHeaderTimeout = "45s"
	parent.Governor.Brokerage.MaxRounds = 5
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
	if child.Governor.Codex.AuthPath != "" {
		t.Fatalf("Governor.Codex.AuthPath = %q, want empty child auth path", child.Governor.Codex.AuthPath)
	}
	if child.Governor.Codex.Model != "gpt-parent-codex" {
		t.Fatalf("Governor.Codex.Model = %q, want inherited parent codex model", child.Governor.Codex.Model)
	}
	if child.Governor.Codex.ContextWindow != 123456 {
		t.Fatalf("Governor.Codex.ContextWindow = %d, want inherited parent context window", child.Governor.Codex.ContextWindow)
	}
	if child.Governor.Codex.StoreResponses {
		t.Fatalf("Governor.Codex.StoreResponses = true, want inherited false")
	}
	if child.Governor.Codex.MaxContinuations != 7 {
		t.Fatalf("Governor.Codex.MaxContinuations = %d, want inherited parent max continuations", child.Governor.Codex.MaxContinuations)
	}
	if child.Governor.Codex.TransportRetries != 0 {
		t.Fatalf("Governor.Codex.TransportRetries = %d, want inherited zero retries", child.Governor.Codex.TransportRetries)
	}
	if child.Governor.Codex.ResponseHeaderTimeout != "45s" {
		t.Fatalf("Governor.Codex.ResponseHeaderTimeout = %q, want inherited parent timeout", child.Governor.Codex.ResponseHeaderTimeout)
	}
	if child.Governor.Brokerage.MaxRounds != 5 {
		t.Fatalf("Governor.Brokerage.MaxRounds = %d, want inherited parent brokerage", child.Governor.Brokerage.MaxRounds)
	}
	if child.Face.Backend != "floor_fallback" {
		t.Fatalf("Face.Backend = %q, want floor_fallback", child.Face.Backend)
	}
	if child.Providers.Anthropic.APIKey != "" || child.Providers.OpenRouter.APIKey != "" || child.Providers.OpenAI.APIKey != "" {
		t.Fatalf("Providers = %#v, want cleared parent/native credentials", child.Providers)
	}
}

func TestDurableAgentChildConfigDefaultsCodexOperationalFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parent := config.Config{}
	parent.Sessions.DBPath = filepath.Join(root, "sessions.db")
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
			Backend:   "codex",
			CodexHome: "/srv/family-group/.codex",
		},
	}

	child := durableAgentChildConfig(&parent, agent, scope)
	defaults := config.Default().Governor.Codex
	if child.Governor.Codex.BaseURL != defaults.BaseURL {
		t.Fatalf("Governor.Codex.BaseURL = %q, want default %q", child.Governor.Codex.BaseURL, defaults.BaseURL)
	}
	if child.Governor.Codex.Model != defaults.Model {
		t.Fatalf("Governor.Codex.Model = %q, want default %q", child.Governor.Codex.Model, defaults.Model)
	}
	if child.Governor.Codex.ContextWindow != defaults.ContextWindow {
		t.Fatalf("Governor.Codex.ContextWindow = %d, want default %d", child.Governor.Codex.ContextWindow, defaults.ContextWindow)
	}
	if child.Governor.Codex.MaxContinuations != defaults.MaxContinuations {
		t.Fatalf("Governor.Codex.MaxContinuations = %d, want default %d", child.Governor.Codex.MaxContinuations, defaults.MaxContinuations)
	}
	if child.Governor.Codex.TransportRetries != defaults.TransportRetries {
		t.Fatalf("Governor.Codex.TransportRetries = %d, want default %d", child.Governor.Codex.TransportRetries, defaults.TransportRetries)
	}
	if child.Governor.Codex.ResponseHeaderTimeout != defaults.ResponseHeaderTimeout {
		t.Fatalf("Governor.Codex.ResponseHeaderTimeout = %q, want default %q", child.Governor.Codex.ResponseHeaderTimeout, defaults.ResponseHeaderTimeout)
	}
}

func TestDurableChildSandboxAccessBlocksStaleRuntimeGrant(t *testing.T) {
	root := t.TempDir()
	store, err := session.NewSQLiteStore(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-stale-runtime",
		GrantedTo:      core.DurableAgentPrincipal("child-alpha"),
		Kind:           session.CapabilityKindTool,
		TargetResource: "mail-reader",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		StaleReason:    "manifest_drift",
		Contract:       `{"child_runtime":{"readonly_paths":["/srv/mail"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	_, err = durableChildSandboxAccessFor("/srv/aphelion/bin/aphelion", core.DurableAgent{AgentID: "child-alpha"}, store)
	if err == nil || !strings.Contains(err.Error(), "child_runtime_blocked: grant_stale_manifest_drift") {
		t.Fatalf("durableChildSandboxAccessFor() err = %v, want stale child_runtime block", err)
	}
}
