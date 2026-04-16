//go:build linux

package core

import (
	"encoding/json"
	"time"
)

type InboundMessage struct {
	ChatID         int64
	ChatType       string
	ChatTitle      string
	SenderID       int64
	SenderName     string
	Text           string
	Artifacts      []Artifact
	ReplyTo        *int64
	MessageID      int64
	DurableAgentID string
	Timestamp      time.Time
	Raw            json.RawMessage
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
	Text       string
	Media      []Media
	ToolLog    []string
	TokenUsage TokenUsage
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
