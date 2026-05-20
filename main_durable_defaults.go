//go:build linux

package main

import (
	"io"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/durabledefaults"
	"github.com/idolum-ai/aphelion/session"
)

const defaultDailyReviewRecipePath = durabledefaults.DefaultDailyReviewRecipePath

type installDailyReviewRecipeOptions = durabledefaults.InstallDailyReviewRecipeOptions
type dailyReviewRecipeInstallResult = durabledefaults.DailyReviewRecipeInstallResult

func durableDefaultsDeps() durabledefaults.Deps {
	return durabledefaults.Deps{
		RecipeFS:         durableChildRecipeFilesFS,
		DefaultBootstrap: defaultDurableAgentBootstrapFromConfig,
	}
}

func syncRuntimeDurableAgentsAtStartup(cfg *config.Config, store *session.SQLiteStore) error {
	return durabledefaults.SyncRuntimeDurableAgentsAtStartup(cfg, store, durableDefaultsDeps())
}

func installDailyReviewRecipeForConfig(cfg *config.Config, opts installDailyReviewRecipeOptions) (dailyReviewRecipeInstallResult, error) {
	return durabledefaults.InstallDailyReviewRecipeForConfig(cfg, opts, durableDefaultsDeps())
}

func installDailyReviewRecipe(cfg *config.Config, store *session.SQLiteStore, opts installDailyReviewRecipeOptions) (dailyReviewRecipeInstallResult, error) {
	return durabledefaults.InstallDailyReviewRecipe(cfg, store, opts, durableDefaultsDeps())
}

func printDailyReviewRecipeInstallResult(w io.Writer, result dailyReviewRecipeInstallResult) {
	durabledefaults.PrintDailyReviewRecipeInstallResult(w, result)
}

func syncDurableAgentBootstrapInheritance(cfg *config.Config, store *session.SQLiteStore) error {
	return durabledefaults.SyncDurableAgentBootstrapInheritance(cfg, store, durableDefaultsDeps())
}

func defaultDurableAgentBootstrapFromConfig(cfg *config.Config) core.NodeLLMBootstrap {
	return durabledefaults.DefaultDurableAgentBootstrapFromConfig(cfg)
}
