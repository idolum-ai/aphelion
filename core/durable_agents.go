//go:build linux

package core

import (
	"time"
)

type DurableAgent struct {
	AgentID                string
	ParentAgentID          string
	ParentScopeKind        string
	ParentScopeID          string
	ReviewTargetChatID     int64
	ChannelKind            string
	LivePolicy             DurableAgentLivePolicy
	ChannelConfig          DurableAgentChannelConfig
	BootstrapCeiling       DurableAgentBootstrapCeiling
	BootstrapLLM           NodeLLMBootstrap
	ControlPlaneSecret     string
	PolicyVersion          int64
	PolicyHash             string
	PolicyIssuedAt         time.Time
	LocalStorageRoots      []string
	NetworkPolicy          string
	WakeupMode             string
	SecretScopes           []string
	AllowedTelegramUserIDs []int64
	Status                 string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}
