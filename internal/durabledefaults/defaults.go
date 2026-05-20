//go:build linux

package durabledefaults

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const DefaultDailyReviewRecipePath = "recipes/durable-children/daily-review.toml"

type Deps struct {
	RecipeFS         fs.FS
	DefaultBootstrap func(*config.Config) core.NodeLLMBootstrap
}

func (d Deps) recipeFS() (fs.FS, error) {
	if d.RecipeFS == nil {
		return nil, fmt.Errorf("daily review recipe fs is required")
	}
	return d.RecipeFS, nil
}

func (d Deps) defaultBootstrap(cfg *config.Config) core.NodeLLMBootstrap {
	if d.DefaultBootstrap != nil {
		return d.DefaultBootstrap(cfg)
	}
	return DefaultDurableAgentBootstrapFromConfig(cfg)
}

func SyncRuntimeDurableAgentsAtStartup(cfg *config.Config, store *session.SQLiteStore, deps Deps) error {
	if err := SyncConfiguredTelegramDurableGroups(cfg, store); err != nil {
		return err
	}
	if err := SyncDurableAgentBootstrapInheritance(cfg, store, deps); err != nil {
		return err
	}
	return nil
}

type InstallDailyReviewRecipeOptions struct {
	Disabled bool
	Source   string
}

type DailyReviewRecipeInstallResult struct {
	AgentID       string
	RecipeID      string
	RecipeVersion string
	RecipeSource  string
	Installed     bool
	Existing      bool
	Skipped       bool
	SkipReason    string
	InstallSource string
	DriftReasons  []string
}

type durableChildRecipe struct {
	ID          string `toml:"id"`
	Version     string `toml:"version"`
	AgentID     string `toml:"agent_id"`
	ChannelKind string `toml:"channel_kind"`
	WakeupMode  string `toml:"wakeup_mode"`
	Status      string `toml:"status"`
	Install     struct {
		ParentScopeKind string `toml:"parent_scope_kind"`
		ParentScopeID   string `toml:"parent_scope_id"`
		ReviewTarget    string `toml:"review_target"`
		Bootstrap       string `toml:"bootstrap"`
		StorageRoots    string `toml:"storage_roots"`
		NetworkPolicy   string `toml:"network_policy"`
		PolicyVersion   int64  `toml:"policy_version"`
		RecipeSource    string `toml:"recipe_source"`
	} `toml:"install"`
	Policy struct {
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

func InstallDailyReviewRecipeForConfig(cfg *config.Config, opts InstallDailyReviewRecipeOptions, deps Deps) (DailyReviewRecipeInstallResult, error) {
	recipe, err := loadBundledDailyReviewRecipe(deps)
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
	return InstallDailyReviewRecipe(cfg, store, opts, deps)
}

func InstallDailyReviewRecipe(cfg *config.Config, store *session.SQLiteStore, opts InstallDailyReviewRecipeOptions, deps Deps) (DailyReviewRecipeInstallResult, error) {
	recipe, err := loadBundledDailyReviewRecipe(deps)
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
	if tombstoned, err := store.DurableAgentTombstoned(recipe.AgentID); err != nil {
		return result, err
	} else if tombstoned {
		if existing, existingErr := store.DurableAgent(recipe.AgentID); errors.Is(existingErr, sql.ErrNoRows) || existing == nil {
			result.Skipped = true
			result.SkipReason = "removed_by_operator"
			return result, nil
		} else if existingErr != nil {
			return result, fmt.Errorf("load tombstoned daily review recipe durable agent: %w", existingErr)
		}
	}

	install, err := durableChildRecipeInstallConfigForHost(recipe, cfg)
	if err != nil {
		return result, err
	}
	livePolicy := durableChildRecipeLivePolicy(recipe)
	channelConfig := durableChildRecipeChannelConfig(recipe)

	existing, err := store.DurableAgent(recipe.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("load daily review recipe durable agent: %w", err)
	}
	if existing != nil {
		result.Existing = true
		result.SkipReason = "preserved_existing"
		result.DriftReasons = dailyReviewRecipeDriftReasons(*existing, recipe, install, livePolicy, channelConfig)
		return result, nil
	}

	workspaceRoot, memoryRoot, err := durableChildRecipeLocalRoots(recipe, cfg)
	if err != nil {
		return result, err
	}
	for _, root := range []string{workspaceRoot, memoryRoot} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return result, fmt.Errorf("create daily review recipe root %s: %w", root, err)
		}
	}
	if _, err := sandbox.DurableAgentScope(recipe.AgentID, cfg.Agent.PromptRoot, workspaceRoot, memoryRoot, install.NetworkPolicy); err != nil {
		return result, fmt.Errorf("validate daily review recipe scope: %w", err)
	}

	bootstrapLLM, err := durableChildRecipeBootstrap(recipe, cfg, deps)
	if err != nil {
		return result, err
	}

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            recipe.AgentID,
		ParentScopeKind:    install.ParentScopeKind,
		ParentScopeID:      install.ParentScopeID,
		ReviewTargetChatID: install.ReviewTargetChatID,
		ChannelKind:        recipe.ChannelKind,
		ChannelConfig:      channelConfig,
		LivePolicy:         livePolicy,
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling(recipe.ChannelKind, livePolicy),
		BootstrapLLM:       bootstrapLLM,
		PolicyVersion:      install.PolicyVersion,
		LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
		NetworkPolicy:      install.NetworkPolicy,
		WakeupMode:         firstNonEmpty(strings.TrimSpace(recipe.WakeupMode), "poll"),
		Status:             firstNonEmpty(strings.TrimSpace(recipe.Status), "active"),
	}); err != nil {
		return result, fmt.Errorf("install daily review recipe durable agent: %w", err)
	}
	result.Installed = true
	return result, nil
}

func loadBundledDailyReviewRecipe(deps Deps) (durableChildRecipe, error) {
	recipeFS, err := deps.recipeFS()
	if err != nil {
		return durableChildRecipe{}, err
	}
	raw, err := fs.ReadFile(recipeFS, DefaultDailyReviewRecipePath)
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
	recipe.Install.ParentScopeKind = strings.TrimSpace(recipe.Install.ParentScopeKind)
	recipe.Install.ParentScopeID = strings.TrimSpace(recipe.Install.ParentScopeID)
	recipe.Install.ReviewTarget = strings.TrimSpace(recipe.Install.ReviewTarget)
	recipe.Install.Bootstrap = strings.TrimSpace(recipe.Install.Bootstrap)
	recipe.Install.StorageRoots = strings.TrimSpace(recipe.Install.StorageRoots)
	recipe.Install.NetworkPolicy = strings.TrimSpace(recipe.Install.NetworkPolicy)
	recipe.Install.RecipeSource = strings.TrimSpace(recipe.Install.RecipeSource)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
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
		RecipeID:         recipe.ID,
		RecipeVersion:    recipe.Version,
		RecipeSource:     durableChildRecipeSource(recipe),
	}})
}

type durableChildRecipeInstallConfig struct {
	ParentScopeKind    string
	ParentScopeID      string
	ReviewTargetChatID int64
	NetworkPolicy      string
	PolicyVersion      int64
}

func durableChildRecipeInstallConfigForHost(recipe durableChildRecipe, cfg *config.Config) (durableChildRecipeInstallConfig, error) {
	if cfg == nil {
		return durableChildRecipeInstallConfig{}, fmt.Errorf("config is nil")
	}
	reviewTarget, err := durableChildRecipeReviewTargetChatID(recipe, cfg)
	if err != nil {
		return durableChildRecipeInstallConfig{}, err
	}
	return durableChildRecipeInstallConfig{
		ParentScopeKind:    firstNonEmpty(strings.TrimSpace(recipe.Install.ParentScopeKind), string(session.ScopeKindHeartbeat)),
		ParentScopeID:      firstNonEmpty(strings.TrimSpace(recipe.Install.ParentScopeID), "admin-house"),
		ReviewTargetChatID: reviewTarget,
		NetworkPolicy:      firstNonEmpty(strings.TrimSpace(recipe.Install.NetworkPolicy), "default"),
		PolicyVersion:      durableChildRecipePolicyVersion(recipe),
	}, nil
}

func durableChildRecipeReviewTargetChatID(recipe durableChildRecipe, cfg *config.Config) (int64, error) {
	target := firstNonEmpty(strings.TrimSpace(recipe.Install.ReviewTarget), "first_admin")
	if strings.EqualFold(target, "first_admin") {
		if cfg == nil || len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
			return 0, fmt.Errorf("daily review recipe requires at least one admin review target")
		}
		return cfg.Principals.Telegram.AdminUserIDs[0], nil
	}
	chatID, err := strconv.ParseInt(target, 10, 64)
	if err != nil || chatID == 0 {
		return 0, fmt.Errorf("daily review recipe install.review_target %q is unsupported", target)
	}
	return chatID, nil
}

func durableChildRecipeLocalRoots(recipe durableChildRecipe, cfg *config.Config) (string, string, error) {
	mode := firstNonEmpty(strings.TrimSpace(recipe.Install.StorageRoots), "default")
	switch strings.ToLower(mode) {
	case "default":
		workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, recipe.AgentID)
		return workspaceRoot, memoryRoot, nil
	default:
		return "", "", fmt.Errorf("daily review recipe install.storage_roots %q is unsupported", mode)
	}
}

func durableChildRecipeBootstrap(recipe durableChildRecipe, cfg *config.Config, deps Deps) (core.NodeLLMBootstrap, error) {
	mode := firstNonEmpty(strings.TrimSpace(recipe.Install.Bootstrap), "inherit_parent")
	if !strings.EqualFold(mode, "inherit_parent") {
		return core.NodeLLMBootstrap{}, fmt.Errorf("daily review recipe install.bootstrap %q is unsupported", mode)
	}
	bootstrap := deps.defaultBootstrap(cfg)
	if !core.NormalizeNodeLLMBootstrap(bootstrap).Configured() {
		return core.NodeLLMBootstrap{}, fmt.Errorf("daily review recipe requires a configured llm bootstrap")
	}
	return bootstrap, nil
}

func durableChildRecipePolicyVersion(recipe durableChildRecipe) int64 {
	if recipe.Install.PolicyVersion > 0 {
		return recipe.Install.PolicyVersion
	}
	return 1
}

func durableChildRecipeSource(recipe durableChildRecipe) string {
	return firstNonEmpty(strings.TrimSpace(recipe.Install.RecipeSource), "bundled")
}

func dailyReviewRecipeDriftReasons(agent core.DurableAgent, recipe durableChildRecipe, install durableChildRecipeInstallConfig, livePolicy core.DurableAgentLivePolicy, channelConfig core.DurableAgentChannelConfig) []string {
	var reasons []string
	if strings.TrimSpace(agent.ChannelKind) != strings.TrimSpace(recipe.ChannelKind) {
		reasons = append(reasons, "channel_kind")
	}
	if strings.TrimSpace(agent.ParentScopeKind) != strings.TrimSpace(install.ParentScopeKind) || strings.TrimSpace(agent.ParentScopeID) != strings.TrimSpace(install.ParentScopeID) {
		reasons = append(reasons, "parent_scope")
	}
	if agent.ReviewTargetChatID != install.ReviewTargetChatID {
		reasons = append(reasons, "review_target")
	}
	if strings.TrimSpace(agent.NetworkPolicy) != strings.TrimSpace(install.NetworkPolicy) {
		reasons = append(reasons, "network_policy")
	}
	if agent.PolicyVersion != install.PolicyVersion {
		reasons = append(reasons, "policy_version")
	}
	if strings.TrimSpace(agent.WakeupMode) != firstNonEmpty(strings.TrimSpace(recipe.WakeupMode), "poll") {
		reasons = append(reasons, "wakeup_mode")
	}
	if strings.TrimSpace(agent.Status) != firstNonEmpty(strings.TrimSpace(recipe.Status), "active") {
		reasons = append(reasons, "status")
	}
	if core.NormalizeDurableAgentLivePolicy(agent.LivePolicy).PublicSurfaceMode != livePolicy.PublicSurfaceMode {
		reasons = append(reasons, "policy.visibility")
	}
	installedScheduled := agent.ChannelConfig.ScheduledReviewConfig()
	expectedScheduled := channelConfig.ScheduledReviewConfig()
	if installedScheduled == nil || expectedScheduled == nil {
		reasons = append(reasons, "scheduled_review_config")
	} else {
		if installedScheduled.RecipeID != expectedScheduled.RecipeID || installedScheduled.RecipeVersion != expectedScheduled.RecipeVersion || installedScheduled.RecipeSource != expectedScheduled.RecipeSource {
			reasons = append(reasons, "recipe_metadata")
		}
		if installedScheduled.TimeUTC != expectedScheduled.TimeUTC || installedScheduled.Window != expectedScheduled.Window || installedScheduled.ArtifactKind != expectedScheduled.ArtifactKind {
			reasons = append(reasons, "scheduled_review_contract")
		}
	}
	return normalizeStringSet(reasons)
}

func dailyReviewRecipeResult(opts InstallDailyReviewRecipeOptions, recipe durableChildRecipe) DailyReviewRecipeInstallResult {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = "init"
	}
	return DailyReviewRecipeInstallResult{
		AgentID:       strings.TrimSpace(recipe.AgentID),
		RecipeID:      strings.TrimSpace(recipe.ID),
		RecipeVersion: strings.TrimSpace(recipe.Version),
		RecipeSource:  durableChildRecipeSource(recipe),
		InstallSource: source,
	}
}

func PrintDailyReviewRecipeInstallResult(w io.Writer, result DailyReviewRecipeInstallResult) {
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
	fmt.Fprintf(w, "scheduled_review_recipe: %s\n", status)
	fmt.Fprintf(w, "scheduled_review_agent_id: %s\n", strings.TrimSpace(result.AgentID))
	fmt.Fprintf(w, "scheduled_review_recipe_id: %s\n", strings.TrimSpace(result.RecipeID))
	fmt.Fprintf(w, "scheduled_review_recipe_version: %s\n", strings.TrimSpace(result.RecipeVersion))
	if source := strings.TrimSpace(result.RecipeSource); source != "" {
		fmt.Fprintf(w, "scheduled_review_recipe_source: %s\n", source)
	}
	if source := strings.TrimSpace(result.InstallSource); source != "" {
		fmt.Fprintf(w, "scheduled_review_recipe_install_source: %s\n", source)
	}
	if result.Existing {
		fmt.Fprintf(w, "scheduled_review_recipe_preserved: true\n")
	}
	if len(result.DriftReasons) == 0 {
		fmt.Fprintf(w, "scheduled_review_recipe_drift: none\n")
	} else {
		fmt.Fprintf(w, "scheduled_review_recipe_drift:\n")
		for _, reason := range result.DriftReasons {
			fmt.Fprintf(w, "  - %s\n", reason)
		}
	}
	if reason := strings.TrimSpace(result.SkipReason); reason != "" {
		fmt.Fprintf(w, "scheduled_review_recipe_reason: %s\n", reason)
	}
}

func SyncDurableAgentBootstrapInheritance(cfg *config.Config, store *session.SQLiteStore, deps Deps) error {
	if cfg == nil || store == nil {
		return nil
	}
	inherited := core.NormalizeNodeLLMBootstrap(deps.defaultBootstrap(cfg))
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

func DefaultDurableAgentBootstrapFromConfig(cfg *config.Config) core.NodeLLMBootstrap {
	if cfg == nil {
		return core.NodeLLMBootstrap{}
	}
	if shouldUseCodexDurableAgentBootstrap(cfg) {
		codex := durableAgentCodexBootstrapFromConfig(cfg)
		if codex.Configured() {
			return codex
		}
	}
	for _, name := range config.EffectiveProviderChain(cfg) {
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
