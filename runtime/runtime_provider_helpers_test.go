//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type toolRequestingProvider struct {
	mu             sync.Mutex
	callCount      int
	firstToolCount int
	lastToolOutput string
}

func (p *toolRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		p.firstToolCount = len(tools)
		if len(tools) == 0 {
			return &agent.Response{Content: "no tools"}, nil
		}
		return &agent.Response{
			ToolCalls: []agent.ToolCall{{
				ID:    "tool-call-1",
				Name:  tools[0].Name,
				Input: json.RawMessage(`{"command":"echo hi"}`),
			}},
		}, nil
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			p.lastToolOutput = messages[i].Content
			break
		}
	}
	return &agent.Response{Content: "done"}, nil
}

func (p *toolRequestingProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

type multiToolRequestingProvider struct {
	mu        sync.Mutex
	callCount int
}

func (p *multiToolRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		if len(tools) == 0 {
			return &agent.Response{Content: "no tools"}, nil
		}
		return &agent.Response{
			ToolCalls: []agent.ToolCall{
				{
					ID:    "tool-call-1",
					Name:  tools[0].Name,
					Input: json.RawMessage(`{"command":"rg first"}`),
				},
				{
					ID:    "tool-call-2",
					Name:  tools[0].Name,
					Input: json.RawMessage(`{"command":"rg second"}`),
				},
			},
		}, nil
	}
	return &agent.Response{Content: "done"}, nil
}

type durableAgentToolRequestingProvider struct {
	mu             sync.Mutex
	callCount      int
	lastToolOutput string
}

func (p *durableAgentToolRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if p.callCount == 1 {
		for _, def := range tools {
			if def.Name == "durable_agent" {
				return &agent.Response{
					ToolCalls: []agent.ToolCall{{
						ID:    "tool-call-1",
						Name:  "durable_agent",
						Input: json.RawMessage(`{"action":"policy_apply","agent_id":"family-group","policy_overrides":{"outbound_mode":"read_only"},"reason":"ratified from conversation"}`),
					}},
				}, nil
			}
		}
		return &agent.Response{Content: "durable-agent tool unavailable"}, nil
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			p.lastToolOutput = messages[i].Content
			break
		}
	}
	return &agent.Response{Content: "Policy updated through conversation."}, nil
}

type directRecordingTools struct {
	defs         []agent.ToolDef
	executeCalls int
}

func (t *directRecordingTools) Definitions() []agent.ToolDef {
	return append([]agent.ToolDef(nil), t.defs...)
}

func (t *directRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	t.executeCalls++
	return "direct execution", nil
}

type principalRecordingTools struct {
	defs                     []agent.ToolDef
	executeCalls             int
	executeForPrincipalCalls int
	supportsPrincipal        bool
	lastPrincipal            principal.Principal
	output                   string
	err                      error
}

func (t *principalRecordingTools) Definitions() []agent.ToolDef {
	return append([]agent.ToolDef(nil), t.defs...)
}

func (t *principalRecordingTools) Execute(_ context.Context, _ string, _ json.RawMessage) (string, error) {
	t.executeCalls++
	if strings.TrimSpace(t.output) != "" {
		return t.output, t.err
	}
	if t.err != nil {
		return "", t.err
	}
	return "direct execution", nil
}

func (t *principalRecordingTools) ExecuteForPrincipal(_ context.Context, p principal.Principal, _ string, _ json.RawMessage) (string, error) {
	t.executeForPrincipalCalls++
	t.lastPrincipal = p
	if strings.TrimSpace(t.output) != "" {
		return t.output, t.err
	}
	if t.err != nil {
		return "", t.err
	}
	return "principal execution", nil
}

func newPrincipalRecordingToolError(message string) error {
	return errors.New(message)
}

func (t *principalRecordingTools) SupportsPrincipal(_ principal.Principal) bool {
	return t.supportsPrincipal
}

func setFakeBubblewrapRunnerForRegistry(t *testing.T, registry *toolpkg.Registry) {
	t.Helper()

	dir := t.TempDir()
	fakeBwrapPath := filepath.Join(dir, "bwrap")
	script := `#!/usr/bin/env bash
set -euo pipefail
workdir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --chdir)
      shift
      workdir="$1"
      ;;
    --)
      shift
      break
      ;;
  esac
  shift
done
if [[ -n "$workdir" ]]; then
  cd "$workdir"
fi
exec "$@"
`
	if err := os.WriteFile(fakeBwrapPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake bwrap: %v", err)
	}

	registry.WithRunner(sandbox.NewRunnerWithLookPath(func(_ string) (string, error) {
		return fakeBwrapPath, nil
	}))
}

func testExecToolDef() agent.ToolDef {
	return agent.ToolDef{
		Name:        "exec",
		Description: "test exec",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}
}

func buildRuntimeFixtures(t *testing.T) (*config.Config, *session.SQLiteStore, *fakeProvider, *fakeSender) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")

	cfg := &config.Config{
		Telegram: config.TelegramConfig{
			Media: config.TelegramMediaConfig{
				DownloadMaxSize:  "20MB",
				AutoVisionPhotos: true,
				AutoVisionDocs:   true,
				ExtractPDFText:   true,
				MaxPDFBytes:      "8MB",
			},
		},
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs:    []int64{1001},
				ApprovedUserIDs: []int64{1002},
			},
		},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
			Codex: config.GovernorCodexConfig{
				AuthSource:     "auto",
				BaseURL:        "https://chatgpt.com/backend-api",
				ContextWindow:  200000,
				StoreResponses: true,
			},
		},
		Sessions: config.SessionsConfig{
			DBPath:             dbPath,
			IdleExpiry:         "24h",
			MaxContextRatio:    0.75,
			CompactionRatio:    0.55,
			CompactionStrategy: "summarize",
		},
		Autonomy: config.AutonomyConfig{
			DefaultMode:         "ask_first",
			Ceiling:             "leased",
			AllowLiveOverrides:  true,
			MaxOverrideDuration: "4h",
		},
		Agent: config.AgentConfig{
			PromptRoot:             root,
			ExecRoot:               root,
			SharedMemoryRoot:       root,
			UserWorkspaceRoot:      filepath.Join(root, "isolated", "workspaces"),
			UserMemoryRoot:         filepath.Join(root, "isolated", "memory"),
			MaxIterations:          10,
			ToolTimeout:            10,
			BootstrapFiles:         []string{"AGENTS.md"},
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
			DailyNotes:             false,
			DailyNotesDir:          "memory/daily",
		},
		Memory: config.MemoryConfig{
			Reflection: config.MemoryReflectionConfig{
				Enabled: true,
				Every:   "6h",
			},
			Decay: config.MemoryDecayConfig{
				Enabled:  true,
				HotDays:  3,
				WarmDays: 14,
				ColdDays: 30,
			},
			Identity: config.MemoryIdentityConfig{
				Preserve: []string{"SOUL.md", "IDENTITY.md", "IDOLUM.md", "MEMORY.md"},
			},
		},
	}

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agent rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	provider := &fakeProvider{
		replyText: "ok",
		responseUsage: core.TokenUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}
	sender := &fakeSender{}
	return cfg, store, provider, sender
}

func useTrustedDurableAgentSandboxForWakeTest(t *testing.T, cfg *config.Config) {
	t.Helper()
	if cfg == nil {
		t.Fatal("test config is nil")
	}
	// These wake tests exercise parent/generic adapter behavior, not the host's
	// isolated sandbox installation. CI may not have bubblewrap, so keep the
	// durable-agent sandbox trusted for these in-process fixtures.
	cfg.Sandbox.Profiles.DurableAgent.Mode = "trusted"
	cfg.Sandbox.Profiles.DurableAgent.Network = "allowlist"
}
