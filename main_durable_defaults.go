//go:build linux

package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const defaultDailyReviewRecipePath = "recipes/durable-children/daily-review.toml"

func syncRuntimeDurableAgentsAtStartup(cfg *config.Config, store *session.SQLiteStore) error {
	if err := syncConfiguredTelegramDurableGroups(cfg, store); err != nil {
		return err
	}
	if err := syncDurableAgentBootstrapInheritance(cfg, store); err != nil {
		return err
	}
	return nil
}

type installDailyReviewRecipeOptions struct {
	Disabled bool
	Source   string
}

type dailyReviewRecipeInstallResult struct {
	AgentID       string
	RecipeID      string
	RecipeVersion string
	Installed     bool
	Existing      bool
	Skipped       bool
	SkipReason    string
	InstallSource string
}

type durableChildRecipe struct {
	ID          string `toml:"id"`
	Version     string `toml:"version"`
	AgentID     string `toml:"agent_id"`
	ChannelKind string `toml:"channel_kind"`
	WakeupMode  string `toml:"wakeup_mode"`
	Status      string `toml:"status"`
	Policy      struct {
		Charter      string   `toml:"charter"`
		OutboundMode string   `toml:"outbound_mode"`
		Visibility   string   `toml:"visibility"`
		Capabilities []string `toml:"capabilities"`
		DriftPolicy  string   `toml:"drift_policy"`
	} `toml:"policy"`
	Schedule struct {
		Kind    string `toml:"kind"`
		TimeUTC string `toml:"time_utc"`
	} `toml:"schedule"`
	Review struct {
		Title            string `toml:"title"`
		Window           string `toml:"window"`
		MaxMessages      int    `toml:"max_messages"`
		Artifact         string `toml:"artifact"`
		TranscriptDir    string `toml:"transcript_dir"`
		PromptTemplate   string `toml:"prompt_template"`
		GuidanceQuestion string `toml:"guidance_question"`
	} `toml:"review"`
}

func installDailyReviewRecipeForConfig(cfg *config.Config, opts installDailyReviewRecipeOptions) (dailyReviewRecipeInstallResult, error) {
	recipe, err := loadBundledDailyReviewRecipe()
	if err != nil {
		result := dailyReviewRecipeResult(opts, durableChildRecipe{})
		result.Skipped = true
		result.SkipReason = "recipe_unavailable"
		return result, err
	}
	result := dailyReviewRecipeResult(opts, recipe)
	if cfg == nil {
		result.Skipped = true
		result.SkipReason = "config_nil"
		return result, fmt.Errorf("config is nil")
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		return result, err
	}
	defer store.Close()
	return installDailyReviewRecipe(cfg, store, opts)
}

func installDailyReviewRecipe(cfg *config.Config, store *session.SQLiteStore, opts installDailyReviewRecipeOptions) (dailyReviewRecipeInstallResult, error) {
	recipe, err := loadBundledDailyReviewRecipe()
	if err != nil {
		result := dailyReviewRecipeResult(opts, durableChildRecipe{})
		result.Skipped = true
		result.SkipReason = "recipe_unavailable"
		return result, err
	}
	result := dailyReviewRecipeResult(opts, recipe)
	if opts.Disabled {
		result.Skipped = true
		result.SkipReason = "disabled"
		return result, nil
	}
	if cfg == nil || store == nil {
		result.Skipped = true
		result.SkipReason = "runtime_unavailable"
		return result, nil
	}
	if len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
		result.Skipped = true
		result.SkipReason = "missing_admin_review_target"
		return result, nil
	}

	existing, err := store.DurableAgent(recipe.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("load daily review recipe durable agent: %w", err)
	}
	if existing != nil {
		result.Existing = true
		return result, nil
	}

	workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, recipe.AgentID)
	for _, root := range []string{workspaceRoot, memoryRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return result, fmt.Errorf("create daily review recipe root %s: %w", root, err)
		}
	}
	if _, err := sandbox.DurableAgentScope(recipe.AgentID, cfg.Agent.PromptRoot, workspaceRoot, memoryRoot, "default"); err != nil {
		return result, fmt.Errorf("validate daily review recipe scope: %w", err)
	}

	livePolicy := durableChildRecipeLivePolicy(recipe)
	bootstrapLLM := defaultDurableAgentBootstrapFromConfig(cfg)
	if !core.NormalizeNodeLLMBootstrap(bootstrapLLM).Configured() {
		return result, fmt.Errorf("daily review recipe requires a configured llm bootstrap")
	}

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            recipe.AgentID,
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: cfg.Principals.Telegram.AdminUserIDs[0],
		ChannelKind:        recipe.ChannelKind,
		ChannelConfig:      durableChildRecipeChannelConfig(recipe),
		LivePolicy:         livePolicy,
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling(recipe.ChannelKind, livePolicy),
		BootstrapLLM:       bootstrapLLM,
		PolicyVersion:      1,
		LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
		NetworkPolicy:      "default",
		WakeupMode:         firstNonEmpty(strings.TrimSpace(recipe.WakeupMode), "poll"),
		Status:             firstNonEmpty(strings.TrimSpace(recipe.Status), "active"),
	}); err != nil {
		return result, fmt.Errorf("install daily review recipe durable agent: %w", err)
	}
	result.Installed = true
	return result, nil
}

func loadBundledDailyReviewRecipe() (durableChildRecipe, error) {
	raw, err := durableChildRecipeFilesFS.ReadFile(defaultDailyReviewRecipePath)
	if err != nil {
		return durableChildRecipe{}, fmt.Errorf("read bundled daily review recipe: %w", err)
	}
	var recipe durableChildRecipe
	if _, err := toml.Decode(string(raw), &recipe); err != nil {
		return durableChildRecipe{}, fmt.Errorf("decode bundled daily review recipe: %w", err)
	}
	recipe = normalizeDurableChildRecipe(recipe)
	if strings.TrimSpace(recipe.AgentID) == "" || strings.TrimSpace(recipe.ID) == "" || strings.TrimSpace(recipe.ChannelKind) == "" {
		return durableChildRecipe{}, fmt.Errorf("daily review recipe is missing id, agent_id, or channel_kind")
	}
	return recipe, nil
}

func normalizeDurableChildRecipe(recipe durableChildRecipe) durableChildRecipe {
	recipe.ID = strings.TrimSpace(recipe.ID)
	recipe.Version = strings.TrimSpace(recipe.Version)
	recipe.AgentID = strings.TrimSpace(recipe.AgentID)
	recipe.ChannelKind = strings.TrimSpace(recipe.ChannelKind)
	recipe.WakeupMode = strings.TrimSpace(recipe.WakeupMode)
	recipe.Status = strings.TrimSpace(recipe.Status)
	recipe.Policy.Charter = strings.TrimSpace(recipe.Policy.Charter)
	recipe.Policy.OutboundMode = strings.TrimSpace(recipe.Policy.OutboundMode)
	recipe.Policy.Visibility = strings.TrimSpace(recipe.Policy.Visibility)
	recipe.Policy.Capabilities = normalizeStringSet(recipe.Policy.Capabilities)
	recipe.Policy.DriftPolicy = strings.TrimSpace(recipe.Policy.DriftPolicy)
	recipe.Schedule.Kind = strings.TrimSpace(recipe.Schedule.Kind)
	recipe.Schedule.TimeUTC = strings.TrimSpace(recipe.Schedule.TimeUTC)
	recipe.Review.Title = strings.TrimSpace(recipe.Review.Title)
	recipe.Review.Window = strings.TrimSpace(recipe.Review.Window)
	recipe.Review.Artifact = strings.TrimSpace(recipe.Review.Artifact)
	recipe.Review.TranscriptDir = strings.TrimSpace(recipe.Review.TranscriptDir)
	recipe.Review.PromptTemplate = strings.TrimSpace(recipe.Review.PromptTemplate)
	recipe.Review.GuidanceQuestion = strings.TrimSpace(recipe.Review.GuidanceQuestion)
	return recipe
}

func normalizeStringSet(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func durableChildRecipeLivePolicy(recipe durableChildRecipe) core.DurableAgentLivePolicy {
	return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
		Charter:                   recipe.Policy.Charter,
		CapabilityEnvelope:        append([]string(nil), recipe.Policy.Capabilities...),
		OutboundMode:              recipe.Policy.OutboundMode,
		DriftPolicy:               recipe.Policy.DriftPolicy,
		PublicSurfaceMode:         recipe.Policy.Visibility,
		SharedInferenceReuse:      "disabled",
		SharedInferenceReuseScope: "public_prefix_only",
	})
}

func durableChildRecipeChannelConfig(recipe durableChildRecipe) core.DurableAgentChannelConfig {
	if strings.TrimSpace(recipe.ChannelKind) != "scheduled_review" {
		return core.DurableAgentChannelConfig{}
	}
	return core.NormalizeDurableAgentChannelConfig(core.DurableAgentChannelConfig{ScheduledReview: &core.DurableAgentScheduledReviewChannelConfig{
		Title:            recipe.Review.Title,
		ScheduleKind:     recipe.Schedule.Kind,
		TimeUTC:          recipe.Schedule.TimeUTC,
		Window:           recipe.Review.Window,
		MaxMessages:      recipe.Review.MaxMessages,
		ArtifactKind:     recipe.Review.Artifact,
		TranscriptDir:    recipe.Review.TranscriptDir,
		PromptTemplate:   recipe.Review.PromptTemplate,
		GuidanceQuestion: recipe.Review.GuidanceQuestion,
	}})
}

func dailyReviewRecipeResult(opts installDailyReviewRecipeOptions, recipe durableChildRecipe) dailyReviewRecipeInstallResult {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = "init"
	}
	return dailyReviewRecipeInstallResult{
		AgentID:       strings.TrimSpace(recipe.AgentID),
		RecipeID:      strings.TrimSpace(recipe.ID),
		RecipeVersion: strings.TrimSpace(recipe.Version),
		InstallSource: source,
	}
}

func printDailyReviewRecipeInstallResult(w io.Writer, result dailyReviewRecipeInstallResult) {
	if w == nil {
		return
	}
	status := "skipped"
	switch {
	case result.Installed:
		status = "installed"
	case result.Existing:
		status = "existing"
	case result.Skipped:
		status = "skipped"
	}
	fmt.Fprintf(w, "daily_review_recipe: %s\n", status)
	fmt.Fprintf(w, "daily_review_agent_id: %s\n", strings.TrimSpace(result.AgentID))
	fmt.Fprintf(w, "daily_review_recipe_id: %s\n", strings.TrimSpace(result.RecipeID))
	fmt.Fprintf(w, "daily_review_recipe_version: %s\n", strings.TrimSpace(result.RecipeVersion))
	if source := strings.TrimSpace(result.InstallSource); source != "" {
		fmt.Fprintf(w, "daily_review_recipe_source: %s\n", source)
	}
	if reason := strings.TrimSpace(result.SkipReason); reason != "" {
		fmt.Fprintf(w, "daily_review_recipe_reason: %s\n", reason)
	}
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
