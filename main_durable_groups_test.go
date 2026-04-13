//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestSyncConfiguredTelegramDurableGroupsPreservesExistingLivePolicy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	existing := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Existing ratified charter.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
			PublicSurfaceMode:  "explicit_parent_relay_only",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"group_reply", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"read_only", "reply_with_parent_review"},
			AllowedPublicSurfaceModes:    []string{"none", "explicit_parent_relay_only"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-existing",
			Model:          "openrouter/existing-model",
		},
		PolicyVersion: 4,
		LocalStorageRoots: []string{
			filepath.Join(root, "existing", "workspace"),
			filepath.Join(root, "existing", "memory"),
		},
		Status: "active",
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
		Telegram: config.TelegramConfig{
			DurableGroups: []config.TelegramDurableGroupConfig{{
				ChatID:       -100200,
				AgentID:      "family-group",
				Charter:      "New bootstrap charter should not clobber live policy.",
				RespondOn:    "mentions",
				LLMProvider:  "openrouter",
				LLMAPIKey:    "sk-or-group",
				LLMModel:     "openrouter/group-model",
				LLMMaxTokens: 321,
			}},
		},
	}

	if err := syncConfiguredTelegramDurableGroups(cfg, store); err != nil {
		t.Fatalf("syncConfiguredTelegramDurableGroups() err = %v", err)
	}

	got, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.LivePolicy.Charter != "Existing ratified charter." {
		t.Fatalf("LivePolicy.Charter = %q, want preserved existing charter", got.LivePolicy.Charter)
	}
	if got.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want preserved read_only", got.LivePolicy.OutboundMode)
	}
	if got.PolicyVersion != 4 {
		t.Fatalf("PolicyVersion = %d, want preserved 4", got.PolicyVersion)
	}
	if len(got.BootstrapCeiling.AllowedOutboundModes) != 2 || got.BootstrapCeiling.AllowedOutboundModes[1] != "reply_with_parent_review" {
		t.Fatalf("BootstrapCeiling.AllowedOutboundModes = %#v, want preserved existing ceiling", got.BootstrapCeiling.AllowedOutboundModes)
	}
	if got.ReviewTargetChatID != 1001 {
		t.Fatalf("ReviewTargetChatID = %d, want 1001", got.ReviewTargetChatID)
	}
	if len(got.LocalStorageRoots) != 2 {
		t.Fatalf("LocalStorageRoots = %#v, want synced storage roots", got.LocalStorageRoots)
	}
	if got.BootstrapLLM.Backend != "native" {
		t.Fatalf("BootstrapLLM.Backend = %q, want native", got.BootstrapLLM.Backend)
	}
	if got.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("BootstrapLLM.NativeProvider = %q, want openrouter", got.BootstrapLLM.NativeProvider)
	}
	if got.BootstrapLLM.APIKey != "sk-or-group" {
		t.Fatalf("BootstrapLLM.APIKey = %q, want sk-or-group", got.BootstrapLLM.APIKey)
	}
	if got.BootstrapLLM.Model != "openrouter/group-model" {
		t.Fatalf("BootstrapLLM.Model = %q, want openrouter/group-model", got.BootstrapLLM.Model)
	}
	if got.BootstrapLLM.MaxTokens != 321 {
		t.Fatalf("BootstrapLLM.MaxTokens = %d, want 321", got.BootstrapLLM.MaxTokens)
	}
}

func TestSyncConfiguredTelegramDurableGroupsRejectsMissingPromptRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		Sessions: config.SessionsConfig{DBPath: dbPath},
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{AdminUserIDs: []int64{1001}},
		},
		Telegram: config.TelegramConfig{
			DurableGroups: []config.TelegramDurableGroupConfig{{
				ChatID:      -100200,
				AgentID:     "family-group",
				Charter:     "Bootstrap charter.",
				RespondOn:   "mentions",
				LLMProvider: "anthropic",
				LLMAPIKey:   "sk-ant-group",
			}},
		},
	}

	if err := syncConfiguredTelegramDurableGroups(cfg, store); err == nil {
		t.Fatal("syncConfiguredTelegramDurableGroups() err = nil, want prompt root validation")
	}
}
