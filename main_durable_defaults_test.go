//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSyncDefaultDailyReviewDurableAgentCreatesDefaultAgent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		Agent:    config.AgentConfig{PromptRoot: filepath.Join(root, "prompt")},
		Sessions: config.SessionsConfig{DBPath: dbPath},
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{AdminUserIDs: []int64{1001}},
		},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.AnthropicConfig{
				APIKey:    "sk-ant-main",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 4096,
			},
		},
	}

	if err := syncDefaultDailyReviewDurableAgent(cfg, store); err != nil {
		t.Fatalf("syncDefaultDailyReviewDurableAgent() err = %v", err)
	}

	agent, err := store.DurableAgent(defaultDailyReviewDurableAgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if agent.ChannelKind != dailyReviewDurableChannelKind {
		t.Fatalf("ChannelKind = %q, want %q", agent.ChannelKind, dailyReviewDurableChannelKind)
	}
	if agent.ReviewTargetChatID != 1001 {
		t.Fatalf("ReviewTargetChatID = %d, want 1001", agent.ReviewTargetChatID)
	}
	if agent.WakeupMode != "poll" {
		t.Fatalf("WakeupMode = %q, want poll", agent.WakeupMode)
	}
	if agent.Status != "active" {
		t.Fatalf("Status = %q, want active", agent.Status)
	}
	if agent.BootstrapLLM.Backend != "native" || agent.BootstrapLLM.NativeProvider != "anthropic" {
		t.Fatalf("BootstrapLLM = %#v, want native anthropic", agent.BootstrapLLM)
	}
}

func TestSyncDefaultDailyReviewDurableAgentPreservesExistingPolicy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	existing := core.DurableAgent{
		AgentID:            defaultDailyReviewDurableAgentID,
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        dailyReviewDurableChannelKind,
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Existing charter should remain.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling(dailyReviewDurableChannelKind, core.DurableAgentLivePolicy{
			Charter:            "Existing charter should remain.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "sk-ant-existing",
			Model:          "claude-opus-4-6",
		},
		PolicyVersion: 5,
		Status:        "active",
	}
	if err := store.UpsertDurableAgent(existing); err != nil {
		t.Fatalf("UpsertDurableAgent(existing) err = %v", err)
	}

	cfg := &config.Config{
		Agent:    config.AgentConfig{PromptRoot: filepath.Join(root, "prompt")},
		Sessions: config.SessionsConfig{DBPath: dbPath},
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{AdminUserIDs: []int64{1001}},
		},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.AnthropicConfig{
				APIKey:    "sk-ant-main",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 4096,
			},
		},
	}

	if err := syncDefaultDailyReviewDurableAgent(cfg, store); err != nil {
		t.Fatalf("syncDefaultDailyReviewDurableAgent() err = %v", err)
	}

	agent, err := store.DurableAgent(defaultDailyReviewDurableAgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if agent.LivePolicy.Charter != "Existing charter should remain." {
		t.Fatalf("LivePolicy.Charter = %q, want preserved existing charter", agent.LivePolicy.Charter)
	}
	if agent.PolicyVersion != 5 {
		t.Fatalf("PolicyVersion = %d, want preserved 5", agent.PolicyVersion)
	}
}
