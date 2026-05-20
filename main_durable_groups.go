//go:build linux

package main

import (
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/durabledefaults"
	"github.com/idolum-ai/aphelion/session"
)

func syncConfiguredTelegramDurableGroups(cfg *config.Config, store *session.SQLiteStore) error {
	return durabledefaults.SyncConfiguredTelegramDurableGroups(cfg, store)
}

func durableGroupLLMBootstrap(group config.TelegramDurableGroupConfig) core.NodeLLMBootstrap {
	return durabledefaults.DurableGroupLLMBootstrap(group)
}

func durableGroupsNeedBotIdentity(groups []config.TelegramDurableGroupConfig) bool {
	return durabledefaults.DurableGroupsNeedBotIdentity(groups)
}

func durableGroupsConfigured(cfg *config.Config) bool {
	return durabledefaults.DurableGroupsConfigured(cfg)
}
