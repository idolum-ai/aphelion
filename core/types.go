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
	IngressSeq int64
	// IngressQueuedAt records when the message entered Aphelion's in-process ingress
	// queue. It is transport-local telemetry used to measure queue wait without
	// changing turn ordering or authority.
	IngressQueuedAt time.Time
	// IngressSurface names the durable transport ledger surface that accepted the
	// message before transport acknowledgement.
	IngressSurface string
	// IngressUpdateID is the transport update id on IngressSurface.
	IngressUpdateID int64
	// TelegramThreadID routes an ordinary Telegram chat message into a per-chat
	// side thread while preserving ChatID as the transport destination.
	TelegramThreadID int64
	DurableAgentID   string
	Origin           InboundOrigin
	OriginDetail     string
	Timestamp        time.Time
	Raw              json.RawMessage
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
	ButtonRows [][]OutboundButton
	Delivery  *OutboundDelivery
}

type OutboundButton struct {
	Text         string
	CallbackData string
	URL          string
}

// OutboundDelivery records transport-local delivery details for multi-message
// sends. Single-message callers can ignore it and keep using SendMessage's
// canonical first returned message id.
type OutboundDelivery struct {
	MessageIDs []int64
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
	Recovery        *TurnRecovery
}

type TurnRecoveryKind string

const (
	TurnRecoveryTokenBudgetExhausted     TurnRecoveryKind = "token_budget_exhausted"
	TurnRecoveryToolBudgetExhausted      TurnRecoveryKind = "tool_budget_exhausted"
	TurnRecoveryIterationBudgetExhausted TurnRecoveryKind = "iteration_budget_exhausted"
)

type TurnRecovery struct {
	Kind           TurnRecoveryKind
	Recoverable    bool
	ReplanRequired bool
	Summary        string
	MaxAutoHops    int
}

type ProviderEvent struct {
	EventType           string
	ObservedAt          time.Time
	Provider            string
	FromProvider        string
	ToProvider          string
	Attempt             int
	MaxRetries          int
	Error               string
	FailureKind         string
	Retryable           bool
	FailoverEligible    bool
	Reason              string
	ResponseID          string
	PartialContentChars int
	PartialToolCalls    int
}

type TokenUsage struct {
	InputTokens      int64
	OutputTokens     int64
	TotalTokens      int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	// CacheCreationTokens preserves provider-native cache creation/write accounting.
	// Anthropic reports this as cache_creation_input_tokens; OpenAI/OpenRouter may
	// surface cache_write_tokens instead. CacheWriteTokens remains the normalized
	// cross-provider write/create total used by existing aggregation paths.
	CacheCreationTokens int64
}

type Budget struct {
	Max     int
	Used    int
	Caution float64
	Warning float64
}
