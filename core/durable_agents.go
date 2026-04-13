//go:build linux

package core

import "time"

type DurableAgent struct {
	AgentID            string
	ParentAgentID      string
	ParentScopeKind    string
	ParentScopeID      string
	ReviewTargetChatID int64
	ChannelKind        string
	Charter            string
	CapabilityEnvelope []string
	LocalStorageRoots  []string
	NetworkPolicy      string
	WakeupMode         string
	OutboundMode       string
	DriftPolicy        string
	SecretScopes       []string
	Status             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type DurableAgentState struct {
	AgentID      string
	Cursor       string
	Status       string
	StateJSON    string
	LastWakeAt   time.Time
	LastReviewAt time.Time
	DormantAt    time.Time
	UpdatedAt    time.Time
}

type DurableReviewArtifact struct {
	AgentID       string
	Summary       string
	IntervalLabel string
	LocalActions  []string
	Questions     []string
	RiskFlags     []string
	ArtifactRefs  []string
	Metadata      map[string]string
}
