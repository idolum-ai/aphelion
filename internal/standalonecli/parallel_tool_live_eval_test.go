//go:build linux

package standalonecli

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/provider"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestLiveParallelNativeFileToolAffordance(t *testing.T) {
	if os.Getenv("APHELION_LIVE_PARALLEL_TOOL_EVAL") != "1" {
		t.Skip("set APHELION_LIVE_PARALLEL_TOOL_EVAL=1 to run live native parallel tool affordance eval")
	}

	cfg, configPath, err := loadConfigForCommand(os.Getenv("APHELION_CONFIG"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
		t.Skipf("providers.openai.api_key is not configured in %s", configPath)
	}
	model := firstAgencyEvalNonEmpty(os.Getenv("APHELION_LIVE_PARALLEL_TOOL_EVAL_MODEL"), cfg.Providers.OpenAI.Model)
	if strings.TrimSpace(model) == "" {
		t.Skipf("providers.openai.model is not configured in %s", configPath)
	}

	subject, err := provider.NewOpenAI(provider.OpenAIOptions{
		APIKey:     cfg.Providers.OpenAI.APIKey,
		BaseURL:    cfg.Providers.OpenAI.BaseURL,
		Model:      model,
		MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
		UserAgent:  config.EffectiveUserAgent(cfg, ""),
	})
	if err != nil {
		t.Fatalf("new OpenAI provider: %v", err)
	}

	tools := selectParallelAffordanceEvalTools(t, toolpkg.NewRegistry(".", 2*time.Second).Definitions())
	blocks := prompt.BuildGovernorPromptBlocks(prompt.GovernorRequest{
		GovernorName:  prompt.DefaultGovernorName,
		PrincipalRole: "admin",
		ToolManifest:  "exec, read_file, list_dir, search",
		ToolCapabilities: prompt.ToolCapabilities{
			Exec:     true,
			ReadFile: true,
			ListDir:  true,
			Search:   true,
		},
		Runtime: prompt.RuntimeAwareness{
			SessionKind:           "interactive",
			RunKind:               "interactive",
			Channel:               "telegram",
			TurnAuthorizationKind: "admin_dm",
			SandboxMode:           "trusted",
			NetworkPolicy:         "deny",
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	resp, err := subject.CompleteWithOptions(ctx, []agent.Message{
		{Role: "system", SystemBlocks: blocks},
		{Role: "user", Content: strings.Join([]string{
			"Inspect these independent files before answering:",
			"- README.md",
			"- docs/architecture/transparent-execution-sequence.md",
			"- docs/guides/operator-setup.md",
			"Do not use shell commands for this first evidence step. Use native file tools and issue the independent file reads/searches together.",
		}, "\n")},
	}, tools, agent.CompleteOptions{Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortLow}})
	if err != nil {
		t.Fatalf("live parallel affordance completion: %v", err)
	}
	if len(resp.ToolCalls) < 2 {
		t.Fatalf("tool calls = %#v, want at least two native file calls in one response", resp.ToolCalls)
	}
	for _, call := range resp.ToolCalls {
		switch strings.TrimSpace(call.Name) {
		case "read_file", "list_dir", "search":
		default:
			t.Fatalf("tool calls = %#v, want only native parallel-safe file tools", resp.ToolCalls)
		}
	}
}

func selectParallelAffordanceEvalTools(t *testing.T, defs []agent.ToolDef) []agent.ToolDef {
	t.Helper()
	wanted := map[string]bool{
		"exec":      true,
		"read_file": true,
		"list_dir":  true,
		"search":    true,
	}
	out := make([]agent.ToolDef, 0, len(wanted))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if wanted[name] {
			out = append(out, def)
			delete(wanted, name)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("missing eval tool definitions: %#v", wanted)
	}
	return out
}
