//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
)

func syncConfiguredTelegramDurableGroups(cfg *config.Config, store *session.SQLiteStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	for _, group := range cfg.Telegram.DurableGroups {
		reviewTarget := group.ReviewTargetChatID
		if reviewTarget == 0 && len(cfg.Principals.Telegram.AdminUserIDs) > 0 {
			reviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
		}
		workspaceRoot, memoryRoot := durableagent.DefaultLocalRoots(cfg.Sessions.DBPath, group.AgentID)
		for _, root := range []string{workspaceRoot, memoryRoot} {
			if err := os.MkdirAll(root, 0o755); err != nil {
				return fmt.Errorf("create durable group root %s: %w", root, err)
			}
		}
		if err := store.UpsertDurableAgent(core.DurableAgent{
			AgentID:            strings.TrimSpace(group.AgentID),
			ParentScopeKind:    string(session.ScopeKindHeartbeat),
			ParentScopeID:      "admin-house",
			ReviewTargetChatID: reviewTarget,
			ChannelKind:        "telegram_group",
			Charter:            strings.TrimSpace(group.Charter),
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
			NetworkPolicy:      "default",
			WakeupMode:         "telegram_update",
			OutboundMode:       "reply_within_charter",
			DriftPolicy:        "admin_review",
			Status:             "active",
		}); err != nil {
			return fmt.Errorf("upsert durable telegram group %s: %w", group.AgentID, err)
		}
	}
	return nil
}

func durableGroupsNeedBotIdentity(groups []config.TelegramDurableGroupConfig) bool {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(group.RespondOn), "all") {
			continue
		}
		return true
	}
	return false
}

func durableGroupsConfigured(cfg *config.Config) bool {
	return cfg != nil && len(cfg.Telegram.DurableGroups) > 0
}
