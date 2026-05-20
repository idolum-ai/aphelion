//go:build linux

package durabledefaults

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func SyncConfiguredTelegramDurableGroups(cfg *config.Config, store *session.SQLiteStore) error {
	if cfg == nil || store == nil {
		return nil
	}
	for _, group := range cfg.Telegram.DurableGroups {
		existing, err := store.DurableAgent(strings.TrimSpace(group.AgentID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("load durable telegram group %s: %w", group.AgentID, err)
		}
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
		if _, err := sandbox.DurableAgentScope(strings.TrimSpace(group.AgentID), cfg.Agent.PromptRoot, workspaceRoot, memoryRoot, "default"); err != nil {
			return fmt.Errorf("validate durable group scope %s: %w", group.AgentID, err)
		}
		livePolicy := core.DefaultTelegramGroupLivePolicy(strings.TrimSpace(group.Charter))
		bootstrapCeiling := core.DefaultDurableAgentBootstrapCeiling("telegram_group", livePolicy)
		bootstrapLLM := DurableGroupLLMBootstrap(group)
		if !bootstrapLLM.Configured() {
			return fmt.Errorf("telegram durable group %s requires a configured llm bootstrap", strings.TrimSpace(group.AgentID))
		}
		policyVersion := int64(1)
		policyHash := ""
		policyIssuedAt := time.Time{}
		if existing != nil {
			livePolicy = existing.LivePolicy
			bootstrapCeiling = existing.BootstrapCeiling
			policyVersion = existing.PolicyVersion
			policyHash = existing.PolicyHash
			policyIssuedAt = existing.PolicyIssuedAt
		}
		if err := store.UpsertDurableAgent(core.DurableAgent{
			AgentID:            strings.TrimSpace(group.AgentID),
			ParentScopeKind:    string(session.ScopeKindHeartbeat),
			ParentScopeID:      "admin-house",
			ReviewTargetChatID: reviewTarget,
			ChannelKind:        "telegram_group",
			LivePolicy:         livePolicy,
			BootstrapCeiling:   bootstrapCeiling,
			BootstrapLLM:       bootstrapLLM,
			PolicyVersion:      policyVersion,
			PolicyHash:         policyHash,
			PolicyIssuedAt:     policyIssuedAt,
			LocalStorageRoots:  []string{workspaceRoot, memoryRoot},
			NetworkPolicy:      "default",
			WakeupMode:         "telegram_update",
			Status:             "active",
		}); err != nil {
			return fmt.Errorf("upsert durable telegram group %s: %w", group.AgentID, err)
		}
	}
	return nil
}

func DurableGroupLLMBootstrap(group config.TelegramDurableGroupConfig) core.NodeLLMBootstrap {
	return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{
		Backend:         group.LLMBackend,
		NativeProvider:  group.LLMProvider,
		APIKey:          group.LLMAPIKey,
		BaseURL:         group.LLMBaseURL,
		Model:           group.LLMModel,
		MaxTokens:       group.LLMMaxTokens,
		CodexAuthSource: group.LLMCodexAuthSource,
		CodexHome:       group.LLMCodexHome,
		CodexBaseURL:    group.LLMCodexBaseURL,
	})
}

func DurableGroupsNeedBotIdentity(groups []config.TelegramDurableGroupConfig) bool {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(group.RespondOn), "all") {
			continue
		}
		return true
	}
	return false
}

func DurableGroupsConfigured(cfg *config.Config) bool {
	return cfg != nil && len(cfg.Telegram.DurableGroups) > 0
}
