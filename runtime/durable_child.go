//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type DurableAgentChildBootstrap struct {
	Config config.Config `json:"config"`
}

type DurableGroupChildResult struct {
	TurnResult      core.TurnResult `json:"turn_result"`
	ReplyText       string          `json:"reply_text"`
	AllowLocalReply bool            `json:"allow_local_reply"`
	InboundWasVoice bool            `json:"inbound_was_voice"`
	TurnIndex       int             `json:"turn_index"`
}

type durableGroupChildExecutor interface {
	Supports(scope sandbox.Scope, agent core.DurableAgent) bool
	Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, msg core.InboundMessage) (*DurableGroupChildResult, error)
}

type sandboxDurableGroupChildExecutor struct {
	cfg        *config.Config
	binaryPath string
	runner     *sandbox.Runner
	store      *session.SQLiteStore
	supported  bool
}

func newSandboxDurableGroupChildExecutor(cfg *config.Config, store *session.SQLiteStore) durableGroupChildExecutor {
	if cfg == nil {
		return nil
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return nil
	}
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		return nil
	}
	return &sandboxDurableGroupChildExecutor{
		cfg:        cfg,
		binaryPath: binaryPath,
		runner:     sandbox.NewRunner(),
		store:      store,
		supported:  true,
	}
}

func (e *sandboxDurableGroupChildExecutor) Supports(scope sandbox.Scope, agent core.DurableAgent) bool {
	if e == nil || !e.supported || e.runner == nil {
		return false
	}
	if !core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
		return false
	}
	return e.runner.Supports(scope)
}

func (e *sandboxDurableGroupChildExecutor) Run(ctx context.Context, scope sandbox.Scope, agent core.DurableAgent, msg core.InboundMessage) (*DurableGroupChildResult, error) {
	if !e.Supports(scope, agent) {
		return nil, fmt.Errorf("durable child executor is unavailable for scope %q", scope.Principal.Role)
	}
	payloadRoot := filepath.Join(scope.SharedMemoryRoot, ".aphelion", "child-run")
	if err := os.MkdirAll(payloadRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create durable child payload root: %w", err)
	}

	bootstrapPath, err := writeJSONTemp(payloadRoot, "bootstrap-*.json", DurableAgentChildBootstrap{
		Config: *durableAgentChildConfig(e.cfg, agent, scope),
	})
	if err != nil {
		return nil, err
	}
	defer os.Remove(bootstrapPath)

	messagePath, err := writeJSONTemp(payloadRoot, "message-*.json", msg)
	if err != nil {
		return nil, err
	}
	defer os.Remove(messagePath)

	stateRoot := filepath.Dir(strings.TrimSpace(e.cfg.Sessions.DBPath))
	childAccess, err := durableChildSandboxAccessFor(e.binaryPath, agent, e.store)
	if err != nil {
		return nil, err
	}
	command := durableAgentChildCommand(e.binaryPath, bootstrapPath, messagePath)
	res, err := e.runner.Run(ctx, sandbox.ExecRequest{
		Scope:              scope,
		Command:            command,
		Workdir:            scope.WorkingRoot,
		ExtraReadonlyPaths: childAccess.readonlyPaths,
		ExtraReadonlyBinds: childAccess.readonlyBinds,
		ExtraWritablePaths: []string{stateRoot},
		ExtraEnv:           childAccess.env,
	})
	if err != nil {
		if strings.TrimSpace(res.Stderr) != "" {
			return nil, fmt.Errorf("durable child runner failed: %w: %s", err, strings.TrimSpace(res.Stderr))
		}
		return nil, fmt.Errorf("durable child runner failed: %w", err)
	}

	var out DurableGroupChildResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &out); err != nil {
		return nil, fmt.Errorf("decode durable child result: %w", err)
	}
	return &out, nil
}

func durableAgentChildConfig(parent *config.Config, agent core.DurableAgent, scope sandbox.Scope) *config.Config {
	if parent == nil {
		return &config.Config{}
	}
	bootstrap := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	copy := *parent
	copy.Telegram.BotToken = ""
	copy.Telegram.DurableGroups = nil
	copy.Principals = config.PrincipalsConfig{}
	copy.OpenAI.Files.Enabled = false
	copy.OpenAI.VectorStores.Enabled = false
	copy.Heartbeat.Enabled = false
	copy.Cron.Enabled = false
	copy.Voice = config.VoiceConfig{Mode: "off"}
	copy.Governor = config.GovernorConfig{}
	copy.Face = config.FaceConfig{Backend: string(faceBackendForChildBootstrap(bootstrap))}
	copy.Agent.ExecRoot = scope.WorkingRoot
	copy.Agent.SharedMemoryRoot = scope.SharedMemoryRoot
	copy.Agent.UserWorkspaceRoot = firstNonEmpty(strings.TrimSpace(scope.UserWorkspace), strings.TrimSpace(scope.WorkingRoot))
	copy.Agent.UserMemoryRoot = firstNonEmpty(strings.TrimSpace(scope.UserMemory), strings.TrimSpace(scope.SharedMemoryRoot))
	copy.Agent.PromptRoot = filepath.Join(scope.SharedMemoryRoot, "agent")
	if strings.TrimSpace(copy.Agent.PromptRoot) == "agent" {
		copy.Agent.PromptRoot = firstNonEmpty(strings.TrimSpace(scope.GlobalRoot), strings.TrimSpace(parent.Agent.PromptRoot))
	}
	if strings.TrimSpace(copy.Agent.SharedMemoryRoot) == "" {
		copy.Agent.SharedMemoryRoot = scope.SharedMemoryRoot
	}
	if strings.TrimSpace(copy.Agent.ExecRoot) == "" {
		copy.Agent.ExecRoot = scope.WorkingRoot
	}
	switch bootstrap.Backend {
	case "codex":
		copy.Governor.Backend = "codex"
		copy.Governor.Codex = config.GovernorCodexConfig{
			AuthSource: bootstrap.CodexAuthSource,
			CodexHome:  bootstrap.CodexHome,
			BaseURL:    bootstrap.CodexBaseURL,
		}
		copy.Providers = config.ProvidersConfig{}
	case "native":
		copy.Governor.Backend = "native"
		copy.Governor.NativeProvider = bootstrap.NativeProvider
		copy.Providers = durableChildProviders(parent, bootstrap)
	}
	return &copy
}

func durableChildProviders(parent *config.Config, bootstrap core.NodeLLMBootstrap) config.ProvidersConfig {
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	providers := config.ProvidersConfig{}
	if parent != nil {
		providers = parent.Providers
	}

	primary := normalizeNativeProviderName(bootstrap.NativeProvider)
	providers.Default = primary
	providers.FallbackChain = durableChildFallbackChain(parent, primary)
	providers.OpenAI = config.OpenAIProviderConfig{}

	defaults := config.Default().Providers
	switch primary {
	case "anthropic":
		anthropic := providers.Anthropic
		anthropic.APIKey = firstNonEmpty(strings.TrimSpace(bootstrap.APIKey), strings.TrimSpace(anthropic.APIKey))
		anthropic.Model = firstNonEmpty(strings.TrimSpace(bootstrap.Model), strings.TrimSpace(anthropic.Model), defaults.Anthropic.Model)
		anthropic.MaxTokens = firstPositive(bootstrap.MaxTokens, anthropic.MaxTokens, defaults.Anthropic.MaxTokens)
		anthropic.ContextWindow = firstPositive(anthropic.ContextWindow, defaults.Anthropic.ContextWindow)
		providers.Anthropic = anthropic
	case "openai":
		openAI := providers.OpenAI
		openAI.APIKey = firstNonEmpty(strings.TrimSpace(bootstrap.APIKey), strings.TrimSpace(openAI.APIKey))
		openAI.BaseURL = firstNonEmpty(strings.TrimSpace(bootstrap.BaseURL), strings.TrimSpace(openAI.BaseURL), defaults.OpenAI.BaseURL)
		openAI.Model = firstNonEmpty(strings.TrimSpace(bootstrap.Model), strings.TrimSpace(openAI.Model), defaults.OpenAI.Model)
		openAI.MaxTokens = firstPositive(bootstrap.MaxTokens, openAI.MaxTokens, defaults.OpenAI.MaxTokens)
		openAI.ContextWindow = firstPositive(openAI.ContextWindow, defaults.OpenAI.ContextWindow)
		providers.OpenAI = openAI
	case "openrouter":
		openRouter := providers.OpenRouter
		openRouter.APIKey = firstNonEmpty(strings.TrimSpace(bootstrap.APIKey), strings.TrimSpace(openRouter.APIKey))
		openRouter.BaseURL = firstNonEmpty(strings.TrimSpace(bootstrap.BaseURL), strings.TrimSpace(openRouter.BaseURL), defaults.OpenRouter.BaseURL)
		openRouter.Model = firstNonEmpty(strings.TrimSpace(bootstrap.Model), strings.TrimSpace(openRouter.Model), defaults.OpenRouter.Model)
		openRouter.MaxTokens = firstPositive(bootstrap.MaxTokens, openRouter.MaxTokens, defaults.OpenRouter.MaxTokens)
		openRouter.ContextWindow = firstPositive(openRouter.ContextWindow, defaults.OpenRouter.ContextWindow)
		providers.OpenRouter = openRouter
	}
	return providers
}

func durableChildFallbackChain(parent *config.Config, primary string) []string {
	primary = normalizeNativeProviderName(primary)
	ordered := durableChildOrderedNativeProviders(parent)
	if len(ordered) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ordered))
	out := make([]string, 0, len(ordered))
	for _, candidate := range ordered {
		candidate = normalizeNativeProviderName(candidate)
		if candidate == "" || candidate == primary {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func durableChildOrderedNativeProviders(parent *config.Config) []string {
	if parent == nil {
		return nil
	}
	candidates := append(
		[]string{
			strings.TrimSpace(parent.Governor.NativeProvider),
			strings.TrimSpace(parent.Providers.Default),
		},
		parent.Providers.FallbackChain...,
	)
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		name := normalizeNativeProviderName(candidate)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeNativeProviderName(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "anthropic":
		return "anthropic"
	case "openai":
		return "openai"
	case "openrouter":
		return "openrouter"
	default:
		return ""
	}
}

func faceBackendForChildBootstrap(bootstrap core.NodeLLMBootstrap) string {
	bootstrap = core.NormalizeNodeLLMBootstrap(bootstrap)
	switch bootstrap.Backend {
	case "native":
		return config.NormalizeFaceBackendValue("provider")
	default:
		return config.NormalizeFaceBackendValue("floor_fallback")
	}
}

func durableAgentChildCommand(binaryPath string, bootstrapPath string, messagePath string) string {
	return strings.Join([]string{
		shellQuote(binaryPath),
		"durable-agent",
		"child-run",
		"--bootstrap",
		shellQuote(bootstrapPath),
		"--message",
		shellQuote(messagePath),
	}, " ")
}

func shellQuote(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func writeJSONTemp(root string, pattern string, value any) (string, error) {
	tmp, err := os.CreateTemp(root, pattern)
	if err != nil {
		return "", fmt.Errorf("create temp payload: %w", err)
	}
	path := filepath.Clean(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("encode temp payload %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp payload %s: %w", path, err)
	}
	return path, nil
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
