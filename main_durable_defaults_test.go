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

func TestSyncDurableAgentBootstrapInheritanceBackfillsMissingBootstrap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "draft-missing-bootstrap",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_dm",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_dm", core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage.")),
		Status:             "draft",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(draft missing bootstrap) err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "existing-bootstrap",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_dm",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_dm", core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage.")),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-existing",
			Model:          "openrouter/child-model",
		},
		Status: "draft",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(existing bootstrap) err = %v", err)
	}

	cfg := &config.Config{
		Sessions: config.SessionsConfig{DBPath: dbPath},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.AnthropicConfig{
				APIKey:    "sk-parent-default",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 4096,
			},
		},
	}

	if err := syncDurableAgentBootstrapInheritance(cfg, store); err != nil {
		t.Fatalf("syncDurableAgentBootstrapInheritance() err = %v", err)
	}

	missing, err := store.DurableAgent("draft-missing-bootstrap")
	if err != nil {
		t.Fatalf("DurableAgent(draft-missing-bootstrap) err = %v", err)
	}
	if got := core.NormalizeNodeLLMBootstrap(missing.BootstrapLLM); !got.Configured() {
		t.Fatalf("missing BootstrapLLM = %#v, want inherited configured bootstrap", missing.BootstrapLLM)
	}
	if missing.BootstrapLLM.NativeProvider != "anthropic" || missing.BootstrapLLM.APIKey != "sk-parent-default" {
		t.Fatalf("missing BootstrapLLM = %#v, want inherited anthropic bootstrap", missing.BootstrapLLM)
	}

	existing, err := store.DurableAgent("existing-bootstrap")
	if err != nil {
		t.Fatalf("DurableAgent(existing-bootstrap) err = %v", err)
	}
	if existing.BootstrapLLM.NativeProvider != "openrouter" || existing.BootstrapLLM.APIKey != "sk-existing" {
		t.Fatalf("existing BootstrapLLM = %#v, want preserved existing bootstrap", existing.BootstrapLLM)
	}
}

func TestSyncDurableAgentBootstrapInheritanceNoopWithoutParentBootstrap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "draft-missing-bootstrap",
		ParentScopeKind:    string(session.ScopeKindTelegramDM),
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_dm",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_dm", core.DefaultTelegramGroupLivePolicy("Handle delegated DM triage.")),
		Status:             "draft",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(draft missing bootstrap) err = %v", err)
	}

	cfg := &config.Config{
		Sessions: config.SessionsConfig{DBPath: dbPath},
	}
	if err := syncDurableAgentBootstrapInheritance(cfg, store); err != nil {
		t.Fatalf("syncDurableAgentBootstrapInheritance() err = %v", err)
	}

	agent, err := store.DurableAgent("draft-missing-bootstrap")
	if err != nil {
		t.Fatalf("DurableAgent(draft-missing-bootstrap) err = %v", err)
	}
	if got := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM); got.Configured() {
		t.Fatalf("BootstrapLLM = %#v, want no inherited bootstrap when parent has none", agent.BootstrapLLM)
	}
}
