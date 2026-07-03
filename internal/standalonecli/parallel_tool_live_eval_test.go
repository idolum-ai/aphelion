//go:build linux

package standalonecli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/provider"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

type parallelToolLiveEvalCase struct {
	ID           string
	User         string
	Options      agent.CompleteOptions
	MinCalls     int
	RequireBatch bool
}

type parallelToolLiveEvalReport struct {
	Model   string                           `json:"model"`
	Passed  bool                             `json:"passed"`
	Results []parallelToolLiveEvalCaseResult `json:"results"`
}

type parallelToolLiveEvalCaseResult struct {
	ID        string   `json:"id"`
	Passed    bool     `json:"passed"`
	ToolCalls []string `json:"tool_calls"`
	Native    int      `json:"native"`
	Exec      int      `json:"exec"`
	Failure   string   `json:"failure,omitempty"`
}

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
	model := firstAgencyEvalNonEmpty(os.Getenv("APHELION_LIVE_PARALLEL_TOOL_EVAL_MODEL"), os.Getenv("APHELION_LIVE_EVAL_MODEL"), cfg.Providers.OpenAI.Model)
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
	toolManifest := toolpkg.RenderManifest(tools, nil, nil)
	blocks := prompt.BuildGovernorPromptBlocks(prompt.GovernorRequest{
		GovernorName:     prompt.DefaultGovernorName,
		PrincipalRole:    "admin",
		ToolManifest:     toolManifest,
		ToolCapabilities: prompt.ToolCapabilitiesFromDefs(tools),
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

	report := parallelToolLiveEvalReport{Model: model, Passed: true}
	for _, tc := range parallelToolLiveEvalCases() {
		resp, err := subject.CompleteWithOptions(ctx, []agent.Message{
			{Role: "system", SystemBlocks: blocks},
			{Role: "user", Content: tc.User},
		}, tools, tc.Options)
		result := evaluateParallelToolLiveEvalCase(tc, resp, err)
		report.Results = append(report.Results, result)
		if !result.Passed {
			report.Passed = false
		}
	}
	writeParallelToolLiveEvalReportIfRequested(t, report)
	if !report.Passed {
		t.Fatalf("parallel native tool live eval failed:\n%s", mustParallelToolLiveEvalJSON(report))
	}
}

func parallelToolLiveEvalCases() []parallelToolLiveEvalCase {
	return []parallelToolLiveEvalCase{
		{
			ID: "explicit_parallel_native_batch",
			User: strings.Join([]string{
				"Inspect these independent files before answering:",
				"- README.md",
				"- docs/architecture/transparent-execution-sequence.md",
				"- docs/guides/operator-setup.md",
				"Do not use shell commands for this first evidence step. Use native file tools and issue the independent file reads/searches together.",
			}, "\n"),
			Options: agent.CompleteOptions{
				Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortLow},
				Verbosity: agent.VerbosityLow,
				MaxTokens: 1024,
			},
			MinCalls:     2,
			RequireBatch: true,
		},
		{
			ID: "natural_ordinary_inspection_routes_native",
			User: strings.Join([]string{
				"I am reviewing the local repository. Start by gathering first-pass evidence; wait for tool results before answering.",
				"Please inspect README.md and docs/architecture/transparent-execution-sequence.md, and search the repository for parallel_missed_opportunity.",
				"These evidence requests are independent.",
			}, "\n"),
			Options: agent.CompleteOptions{
				Reasoning: agent.ReasoningConfig{Effort: agent.ReasoningEffortMedium},
				Verbosity: agent.VerbosityLow,
				MaxTokens: 1536,
			},
			MinCalls:     2,
			RequireBatch: true,
		},
	}
}

func evaluateParallelToolLiveEvalCase(tc parallelToolLiveEvalCase, resp *agent.Response, err error) parallelToolLiveEvalCaseResult {
	result := parallelToolLiveEvalCaseResult{ID: tc.ID, Passed: true}
	if err != nil {
		result.Passed = false
		result.Failure = err.Error()
		return result
	}
	if resp == nil {
		result.Passed = false
		result.Failure = "nil response"
		return result
	}
	for _, call := range resp.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "(empty)"
		}
		result.ToolCalls = append(result.ToolCalls, name)
		switch name {
		case "read_file", "list_dir", "search":
			result.Native++
		case "exec":
			result.Exec++
		}
	}
	if result.Exec > 0 {
		result.Passed = false
		result.Failure = "ordinary inspection routed through exec"
		return result
	}
	if result.Native != len(result.ToolCalls) {
		result.Passed = false
		result.Failure = "unexpected non-native tool call"
		return result
	}
	if len(result.ToolCalls) < tc.MinCalls {
		result.Passed = false
		result.Failure = "too few native tool calls"
		return result
	}
	if tc.RequireBatch && len(result.ToolCalls) < 2 {
		result.Passed = false
		result.Failure = "did not batch independent native tool calls"
	}
	return result
}

func writeParallelToolLiveEvalReportIfRequested(t *testing.T, report parallelToolLiveEvalReport) {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("APHELION_LIVE_EVAL_REPORT"))
	if path == "" {
		return
	}
	path = liveEvalReportPath(path, "parallel-tool")
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal parallel tool live eval report: %v", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create live eval report dir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write live eval report %s: %v", path, err)
	}
	t.Logf("wrote parallel tool live eval report: %s", path)
}

func mustParallelToolLiveEvalJSON(report parallelToolLiveEvalReport) string {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
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
