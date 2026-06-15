//go:build linux

package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type Doer interface {
	Do(ctx context.Context, req Request) (Result, error)
}

type Request struct {
	Agent           core.DurableAgent
	Address         string
	MemoryRoot      string
	ThreadID        string
	Prompt          string
	Now             time.Time
	StatusSchema    string
	ApprovalHandler ApprovalHandler
}

type Result struct {
	ThreadID       string
	TurnID         string
	Text           string
	EnvelopeRaw    json.RawMessage
	Envelope       core.DurableChildStatusEnvelope
	PayloadHash    string
	ApprovalLog    []ApprovalDecision
	Notifications  int
	CodexEvents    []session.WorkCodexEvent
	PatchPreview   string
	Completed      bool
	ArtifactRel    string
	ArtifactSHA256 string
}

type ApprovalDecision struct {
	Method   string `json:"method"`
	Command  string `json:"command,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Decision string `json:"decision"`
	Effect   string `json:"effect,omitempty"`
}

type ApprovalHandler func(method string, params map[string]any) ApprovalDecision

type StreamOptions struct {
	FirstNotificationTimeout time.Duration
}

type WorkMode string

const (
	WorkModeReadOnly       WorkMode = "read_only"
	WorkModeWorkspaceWrite WorkMode = "workspace_write"
	WorkModeCommit         WorkMode = "commit"
	WorkModeDeploy         WorkMode = "deploy"
)

type WorkRequest struct {
	OperationID string
	RepoRoot    string
	Workdir     string
	Prompt      string
	Mode        WorkMode
	LeaseID     string
	State       session.ContinuationState
}

type WorkResult struct {
	ExecutorName     string
	TurnRunID        int64
	ThreadID         string
	TurnID           string
	Summary          string
	RecoveryKind     string
	RecoverySummary  string
	RecoveryDelivery string
	ProviderFailure  string
	ProviderEvents   []core.ProviderEvent
	Recovery         *core.TurnRecovery
	ChangedFiles     []string
	Commands         []string
	CodexEvents      []session.WorkCodexEvent
	PatchPreview     string
	CommitLaneStatus string
	ApprovalLog      []ApprovalDecision
	CompletionKind   string
	SideEffects      bool
	ToolSuccesses    int
	ToolFailures     int
	ToolFailure      string
	ToolFailureTexts []string
}
