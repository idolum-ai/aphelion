//go:build linux

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const (
	defaultDailyReviewDurableAgentID = "idolum-daily-review"
	dailyReviewDurableChannelKind    = "daily_review"
)

func syncDefaultDailyReviewDurableAgent(cfg *config.Config, store *session.SQLiteStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	if len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
		return nil
	}

	existing, err := store.DurableAgent(defaultDailyReviewDurableAgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load default daily review durable agent: %w", err)
	}

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, defaultDailyReviewDurableAgentID)
	for _, root := range []string{workspaceRoot, memoryRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return fmt.Errorf("create default daily review root %s: %w", root, err)
		}
	}
	if _, err := sandbox.DurableAgentScope(defaultDailyReviewDurableAgentID, cfg.Agent.PromptRoot, workspaceRoot, memoryRoot, "default"); err != nil {
		return fmt.Errorf("validate default daily review scope: %w", err)
	}

	reviewTarget := cfg.Principals.Telegram.AdminUserIDs[0]
	livePolicy := defaultDailyReviewLivePolicy()
	bootstrapCeiling := core.DefaultDurableAgentBootstrapCeiling(dailyReviewDurableChannelKind, livePolicy)
	policyVersion := int64(1)
	policyHash := ""
	policyIssuedAt := time.Time{}
	bootstrapLLM := core.NodeLLMBootstrap{}
	if existing != nil {
		reviewTarget = existing.ReviewTargetChatID
		if reviewTarget == 0 {
			reviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
		}
		livePolicy = existing.LivePolicy
		bootstrapCeiling = existing.BootstrapCeiling
		policyVersion = existing.PolicyVersion
		policyHash = existing.PolicyHash
		policyIssuedAt = existing.PolicyIssuedAt
		bootstrapLLM = existing.BootstrapLLM
	}
	if !core.NormalizeNodeLLMBootstrap(bootstrapLLM).Configured() {
		bootstrapLLM = defaultDurableAgentBootstrapFromConfig(cfg)
	}
	if !core.NormalizeNodeLLMBootstrap(bootstrapLLM).Configured() {
		return fmt.Errorf("default daily review durable agent requires a configured llm bootstrap")
	}

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            defaultDailyReviewDurableAgentID,
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: reviewTarget,
		ChannelKind:        dailyReviewDurableChannelKind,
		LivePolicy:         livePolicy,
		BootstrapCeiling:   bootstrapCeiling,
		BootstrapLLM:       bootstrapLLM,
		PolicyVersion:      policyVersion,
		PolicyHash:         policyHash,
		PolicyIssuedAt:     policyIssuedAt,
		LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
		NetworkPolicy:      "default",
		WakeupMode:         "poll",
		Status:             "active",
	}); err != nil {
		return fmt.Errorf("upsert default daily review durable agent: %w", err)
	}
	return nil
}

func syncDurableAgentBootstrapInheritance(cfg *config.Config, store *session.SQLiteStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	inherited := core.NormalizeNodeLLMBootstrap(defaultDurableAgentBootstrapFromConfig(cfg))
	if !inherited.Configured() {
		return nil
	}
	agents, err := store.ListDurableAgents()
	if err != nil {
		return fmt.Errorf("list durable agents for bootstrap inheritance: %w", err)
	}
	for _, agent := range agents {
		if core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM).Configured() {
			continue
		}
		agent.BootstrapLLM = inherited
		if err := store.UpsertDurableAgent(agent); err != nil {
			return fmt.Errorf("backfill durable agent bootstrap inheritance agent=%s: %w", strings.TrimSpace(agent.AgentID), err)
		}
	}
	return nil
}

func shouldUseCodexDurableAgentBootstrap(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Governor.Backend)) {
	case "codex":
		return true
	case "auto", "":
		bundle, err := governorauth.ResolveFromConfig(cfg.Governor)
		return err == nil && strings.EqualFold(strings.TrimSpace(bundle.Backend), governorauth.BackendCodex)
	default:
		return false
	}
}

func durableAgentCodexBootstrapFromConfig(cfg *config.Config) core.NodeLLMBootstrap {
	if cfg == nil {
		return core.NodeLLMBootstrap{}
	}
	return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{
		Backend:         "codex",
		CodexAuthSource: cfg.Governor.Codex.AuthSource,
		CodexHome:       durableAgentCodexHomeFromConfig(cfg),
		CodexBaseURL:    cfg.Governor.Codex.BaseURL,
	})
}

func durableAgentCodexHomeFromConfig(cfg *config.Config) string {
	if cfg != nil {
		if home := strings.TrimSpace(cfg.Governor.Codex.CodexHome); home != "" {
			return home
		}
	}
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return ""
	}
	return filepath.Join(userHome, ".codex")
}

func defaultDailyReviewLivePolicy() core.DurableAgentLivePolicy {
	return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
		Charter: "Run one scheduled daily review of yesterday's transcript and open a plain parent-child check-in with concise action items for tomorrow.",
		CapabilityEnvelope: []string{
			"bounded_review_artifact",
			"session_recall",
		},
		OutboundMode:      "read_only",
		DriftPolicy:       "admin_review",
		PublicSurfaceMode: "explicit_parent_relay_only",
	})
}

func defaultDurableAgentBootstrapFromConfig(cfg *config.Config) core.NodeLLMBootstrap {
	if cfg == nil {
		return core.NodeLLMBootstrap{}
	}
	if shouldUseCodexDurableAgentBootstrap(cfg) {
		codex := durableAgentCodexBootstrapFromConfig(cfg)
		if codex.Configured() {
			return codex
		}
	}
	for _, name := range orderedNativeProviderNames(cfg) {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "anthropic":
			if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
				continue
			}
			return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "anthropic",
				APIKey:         cfg.Providers.Anthropic.APIKey,
				Model:          cfg.Providers.Anthropic.Model,
				MaxTokens:      cfg.Providers.Anthropic.MaxTokens,
			})
		case "openrouter":
			if strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) == "" {
				continue
			}
			return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{
				Backend:        "native",
				NativeProvider: "openrouter",
				APIKey:         cfg.Providers.OpenRouter.APIKey,
				BaseURL:        cfg.Providers.OpenRouter.BaseURL,
				Model:          cfg.Providers.OpenRouter.Model,
				MaxTokens:      cfg.Providers.OpenRouter.MaxTokens,
			})
		}
	}
	return core.NodeLLMBootstrap{}
}
