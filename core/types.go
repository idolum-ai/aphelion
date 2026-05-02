//go:build linux

package core

import (
	"encoding/json"
	"time"
)

type InboundOrigin string

const (
	InboundOriginUser              InboundOrigin = "user"
	InboundOriginTurnAuthorization InboundOrigin = "turn_authorization"
	InboundOriginStartupRecovery   InboundOrigin = "startup_recovery"
)

type InboundMessage struct {
	ChatID     int64
	ChatType   string
	ChatTitle  string
	SenderID   int64
	SenderName string
	// Text contains the authored user text/caption and may include a synthetic
	// appended "Reply context:" block during ingress normalization. Consumers that
	// need authored-only text should strip that augmentation.
	Text      string
	Artifacts []Artifact
	ReplyTo   *int64
	MessageID int64
	Reaction  *InboundReaction
	// IngressSeq is assigned by the router as a per-session, monotonic ingress order.
	IngressSeq     int64
	DurableAgentID string
	Origin         InboundOrigin
	OriginDetail   string
	Timestamp      time.Time
	Raw            json.RawMessage
}

type InboundReaction struct {
	MessageID int64
	Old       []string
	New       []string
}

type OutboundMessage struct {
	ChatID    int64
	Text      string
	Media     []Media
	ReplyTo   *int64
	ParseMode string
	Reactions []string
}

type Media struct {
	Type     string
	Data     []byte
	Path     string
	URL      string
	MimeType string
	Filename string
}

type Artifact struct {
	ID               string
	Channel          string
	RemoteID         string
	SourceType       string
	Kind             string
	Subtype          string
	Data             []byte
	Path             string
	URL              string
	MimeType         string
	Filename         string
	SizeBytes        int64
	Caption          string
	Scope            string
	PrincipalID      string
	Metadata         map[string]string
	Capabilities     []string
	DefaultRetention string
	RetentionCeiling string
}

// TurnResult is returned by the agent after one turn.
type TurnResult struct {
	Text            string
	Media           []Media
	ToolLog         []string
	TokenUsage      TokenUsage
	ProviderFailure string
	ProviderEvents  []ProviderEvent
}

type ProviderEvent struct {
	EventType    string
	Provider     string
	FromProvider string
	ToProvider   string
	Attempt      int
	MaxRetries   int
	Error        string
}

type TokenUsage struct {
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

type Budget struct {
	Max     int
	Used    int
	Caution float64
	Warning float64
}
