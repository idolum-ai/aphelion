//go:build linux

package config

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func validateTelegramChildBots(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	seenChats := make(map[int64]string, len(cfg.Telegram.ChildBots))
	seenAgents := make(map[string]int64, len(cfg.Telegram.ChildBots))
	durableGroupChats := make(map[int64]string, len(cfg.Telegram.DurableGroups))
	for _, group := range cfg.Telegram.DurableGroups {
		if group.ChatID != 0 && strings.TrimSpace(group.AgentID) != "" {
			durableGroupChats[group.ChatID] = strings.TrimSpace(group.AgentID)
		}
	}
	defaultReviewTarget := int64(0)
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 {
		defaultReviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
	}
	for i, bot := range cfg.Telegram.ChildBots {
		agentID := strings.TrimSpace(bot.AgentID)
		if agentID == "" {
			return fmt.Errorf("telegram.child_bots[%d].agent_id is required", i)
		}
		if !isSafeDurableAgentID(agentID) {
			return fmt.Errorf("telegram.child_bots[%d].agent_id must contain only letters, digits, _, or -", i)
		}
		if strings.TrimSpace(bot.TokenFile) == "" {
			return fmt.Errorf("telegram.child_bots[%d].token_file is required", i)
		}
		if bot.ChatID == 0 {
			return fmt.Errorf("telegram.child_bots[%d].chat_id is required", i)
		}
		if existing, ok := seenChats[bot.ChatID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].chat_id duplicates child bot %q", i, existing)
		}
		if existing, ok := durableGroupChats[bot.ChatID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].chat_id duplicates telegram.durable_groups route %q", i, existing)
		}
		if existing, ok := seenAgents[agentID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].agent_id duplicates chat_id %d", i, existing)
		}
		switch normalizeTelegramDurableGroupRespondOn(bot.RespondOn) {
		case "all", "mentions":
		default:
			return fmt.Errorf("telegram.child_bots[%d].respond_on must be one of all|mentions", i)
		}
		if bot.ReviewTargetChatID == 0 && defaultReviewTarget == 0 {
			return fmt.Errorf("telegram.child_bots[%d].review_target_chat_id is required when no admin_user_ids are configured", i)
		}
		if bot.ReviewTargetChatID < 0 {
			return fmt.Errorf("telegram.child_bots[%d].review_target_chat_id must be positive", i)
		}
		seenChats[bot.ChatID] = agentID
		seenAgents[agentID] = bot.ChatID
	}
	return nil
}

func validateTelegramDurableGroups(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	seenChats := make(map[int64]string, len(cfg.Telegram.DurableGroups))
	seenAgents := make(map[string]int64, len(cfg.Telegram.DurableGroups))
	defaultReviewTarget := int64(0)
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 {
		defaultReviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
	}
	for i, group := range cfg.Telegram.DurableGroups {
		if group.ChatID == 0 {
			return fmt.Errorf("telegram.durable_groups[%d].chat_id is required", i)
		}
		if existing, ok := seenChats[group.ChatID]; ok {
			return fmt.Errorf("telegram.durable_groups[%d].chat_id duplicates durable group %q", i, existing)
		}
		agentID := strings.TrimSpace(group.AgentID)
		if agentID == "" {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id is required", i)
		}
		if !isSafeDurableAgentID(agentID) {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id must contain only letters, digits, _, or -", i)
		}
		if existing, ok := seenAgents[agentID]; ok {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id duplicates chat_id %d", i, existing)
		}
		if strings.TrimSpace(group.Charter) == "" {
			return fmt.Errorf("telegram.durable_groups[%d].charter is required", i)
		}
		switch normalizeTelegramDurableGroupRespondOn(group.RespondOn) {
		case "all", "mentions":
		default:
			return fmt.Errorf("telegram.durable_groups[%d].respond_on must be one of all|mentions", i)
		}
		if group.ReviewTargetChatID == 0 && defaultReviewTarget == 0 {
			return fmt.Errorf("telegram.durable_groups[%d].review_target_chat_id is required when no admin_user_ids are configured", i)
		}
		if group.ReviewTargetChatID < 0 {
			return fmt.Errorf("telegram.durable_groups[%d].review_target_chat_id must be positive", i)
		}
		switch group.LLMBackend {
		case "native":
			if !isNativeProviderName(group.LLMProvider) {
				return fmt.Errorf("telegram.durable_groups[%d].llm_provider must be one of anthropic|openai|openrouter|gemini|ollama for native backend", i)
			}
			if group.LLMProvider != "ollama" && strings.TrimSpace(group.LLMAPIKey) == "" {
				return fmt.Errorf("telegram.durable_groups[%d].llm_api_key is required for native backend", i)
			}
			if strings.TrimSpace(group.LLMCodexAuthSource) != "" || strings.TrimSpace(group.LLMCodexHome) != "" || strings.TrimSpace(group.LLMCodexBaseURL) != "" {
				return fmt.Errorf("telegram.durable_groups[%d] mixes native llm settings with codex bootstrap settings", i)
			}
		case "codex":
			if strings.TrimSpace(group.LLMCodexHome) == "" {
				return fmt.Errorf("telegram.durable_groups[%d].llm_codex_home is required for codex backend", i)
			}
			if strings.TrimSpace(group.LLMProvider) != "" || strings.TrimSpace(group.LLMAPIKey) != "" || strings.TrimSpace(group.LLMBaseURL) != "" || strings.TrimSpace(group.LLMModel) != "" || group.LLMMaxTokens > 0 {
				return fmt.Errorf("telegram.durable_groups[%d] mixes codex llm settings with native provider bootstrap settings", i)
			}
		default:
			return fmt.Errorf("telegram.durable_groups[%d].llm_backend must be one of native|codex", i)
		}
		seenChats[group.ChatID] = agentID
		seenAgents[agentID] = group.ChatID
	}
	return nil
}

func isSafeDurableAgentID(value string) bool {
	return core.ValidateDurableAgentID(value) == nil
}
