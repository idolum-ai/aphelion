//go:build linux

package core

import (
	"encoding/json"
	"strings"
	"time"
)

type DurableAgentState struct {
	AgentID                       string
	Cursor                        string
	Status                        string
	StateJSON                     string
	LastOfferedPolicyVersion      int64
	LastOfferedPolicyHash         string
	LastOfferedPolicyAt           time.Time
	LastAcknowledgedPolicyVersion int64
	LastAcknowledgedPolicyHash    string
	LastAcknowledgedPolicyAt      time.Time
	LastAppliedPolicyVersion      int64
	LastAppliedPolicyHash         string
	LastAppliedPolicyAt           time.Time
	LastApplyStatus               string
	LastApplyError                string
	LastWakeAt                    time.Time
	LastReviewAt                  time.Time
	DormantAt                     time.Time
	UpdatedAt                     time.Time
}

// DurableAgentIdentityState stores durable policy-handshake identity that
// should remain canonical and independent from runtime wake posture.
type DurableAgentIdentityState struct {
	AgentID                       string
	LastOfferedPolicyVersion      int64
	LastOfferedPolicyHash         string
	LastOfferedPolicyAt           time.Time
	LastAcknowledgedPolicyVersion int64
	LastAcknowledgedPolicyHash    string
	LastAcknowledgedPolicyAt      time.Time
	LastAppliedPolicyVersion      int64
	LastAppliedPolicyHash         string
	LastAppliedPolicyAt           time.Time
	UpdatedAt                     time.Time
}

// DurableAgentRuntimeState stores runtime/apply posture and continuity data.
type DurableAgentRuntimeState struct {
	AgentID         string
	Cursor          string
	Status          string
	StateJSON       string
	LastApplyStatus string
	LastApplyError  string
	LastWakeAt      time.Time
	LastReviewAt    time.Time
	DormantAt       time.Time
	UpdatedAt       time.Time
}

func DurableAgentIdentityStateFrom(state DurableAgentState) DurableAgentIdentityState {
	return DurableAgentIdentityState{
		AgentID:                       state.AgentID,
		LastOfferedPolicyVersion:      state.LastOfferedPolicyVersion,
		LastOfferedPolicyHash:         state.LastOfferedPolicyHash,
		LastOfferedPolicyAt:           state.LastOfferedPolicyAt,
		LastAcknowledgedPolicyVersion: state.LastAcknowledgedPolicyVersion,
		LastAcknowledgedPolicyHash:    state.LastAcknowledgedPolicyHash,
		LastAcknowledgedPolicyAt:      state.LastAcknowledgedPolicyAt,
		LastAppliedPolicyVersion:      state.LastAppliedPolicyVersion,
		LastAppliedPolicyHash:         state.LastAppliedPolicyHash,
		LastAppliedPolicyAt:           state.LastAppliedPolicyAt,
		UpdatedAt:                     state.UpdatedAt,
	}
}

func DurableAgentRuntimeStateFrom(state DurableAgentState) DurableAgentRuntimeState {
	return DurableAgentRuntimeState{
		AgentID:         state.AgentID,
		Cursor:          state.Cursor,
		Status:          state.Status,
		StateJSON:       state.StateJSON,
		LastApplyStatus: state.LastApplyStatus,
		LastApplyError:  state.LastApplyError,
		LastWakeAt:      state.LastWakeAt,
		LastReviewAt:    state.LastReviewAt,
		DormantAt:       state.DormantAt,
		UpdatedAt:       state.UpdatedAt,
	}
}

// DurableAgentExternalChannelRuntimeState stores generic external-channel
// continuity for a durable child. It carries transport lifecycle state shared by
// adapters: cursor/session references, last command/attempt/success, artifact
// pointers, and failure backoff. Protocol-specific residue belongs in
// AdapterState, not as a top-level core field.
type DurableAgentExternalChannelRuntimeState struct {
	Adapter       string          `json:"adapter,omitempty"`
	Cursor        string          `json:"cursor,omitempty"`
	SessionRef    string          `json:"session_ref,omitempty"`
	LastCommand   string          `json:"last_command,omitempty"`
	LastAttemptAt time.Time       `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time       `json:"last_success_at,omitempty"`
	LastArtifact  string          `json:"last_artifact,omitempty"`
	LastStatus    string          `json:"last_status,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	LastErrorAt   time.Time       `json:"last_error_at,omitempty"`
	BackoffUntil  time.Time       `json:"backoff_until,omitempty"`
	FailureCount  int             `json:"failure_count,omitempty"`
	AdapterState  json.RawMessage `json:"adapter_state,omitempty"`
}

// DurableAgentScheduledReviewRuntimeState stores generic scheduled-review wake
// lifecycle state. ReviewDate scopes backoff to one scheduled interval so a new
// review date can still attempt even when the prior date is backed off.
type DurableAgentScheduledReviewRuntimeState struct {
	ReviewDate    string    `json:"review_date,omitempty"`
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	LastStatus    string    `json:"last_status,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	LastErrorAt   time.Time `json:"last_error_at,omitempty"`
	BackoffUntil  time.Time `json:"backoff_until,omitempty"`
	FailureCount  int       `json:"failure_count,omitempty"`
}

type DurableAgentEmailPendingState struct {
	Threads []DurableAgentEmailPendingThread `json:"threads,omitempty"`
}

type DurableAgentEmailPendingThread struct {
	ThreadID        string    `json:"thread_id,omitempty"`
	MessageID       string    `json:"message_id,omitempty"`
	From            string    `json:"from,omitempty"`
	Subject         string    `json:"subject,omitempty"`
	Snippet         string    `json:"snippet,omitempty"`
	Body            string    `json:"body,omitempty"`
	ReceivedAt      time.Time `json:"received_at,omitempty"`
	Labels          []string  `json:"labels,omitempty"`
	AttachmentNames []string  `json:"attachment_names,omitempty"`
}

const durableAgentEmailPendingMaxItems = 200

func normalizeDurableAgentExternalChannelRuntimeState(state *DurableAgentExternalChannelRuntimeState) *DurableAgentExternalChannelRuntimeState {
	if state == nil {
		return nil
	}
	normalized := *state
	normalized.Adapter = strings.ToLower(strings.TrimSpace(normalized.Adapter))
	normalized.Cursor = strings.TrimSpace(normalized.Cursor)
	normalized.SessionRef = strings.TrimSpace(normalized.SessionRef)
	normalized.LastCommand = strings.TrimSpace(normalized.LastCommand)
	normalized.LastArtifact = strings.TrimSpace(normalized.LastArtifact)
	normalized.LastStatus = strings.TrimSpace(normalized.LastStatus)
	normalized.LastError = strings.TrimSpace(normalized.LastError)
	if normalized.FailureCount < 0 {
		normalized.FailureCount = 0
	}
	normalized.AdapterState = normalizeDurableAgentRawJSON(normalized.AdapterState)
	if normalized.Adapter == "" && normalized.Cursor == "" && normalized.SessionRef == "" && normalized.LastCommand == "" &&
		normalized.LastAttemptAt.IsZero() && normalized.LastSuccessAt.IsZero() && normalized.LastArtifact == "" &&
		normalized.LastStatus == "" && normalized.LastError == "" && normalized.LastErrorAt.IsZero() &&
		normalized.BackoffUntil.IsZero() && normalized.FailureCount == 0 && len(normalized.AdapterState) == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentScheduledReviewRuntimeState(state *DurableAgentScheduledReviewRuntimeState) *DurableAgentScheduledReviewRuntimeState {
	if state == nil {
		return nil
	}
	normalized := *state
	normalized.ReviewDate = strings.TrimSpace(normalized.ReviewDate)
	normalized.LastStatus = strings.TrimSpace(normalized.LastStatus)
	normalized.LastError = strings.TrimSpace(normalized.LastError)
	if normalized.FailureCount < 0 {
		normalized.FailureCount = 0
	}
	if normalized.ReviewDate == "" && normalized.LastAttemptAt.IsZero() && normalized.LastSuccessAt.IsZero() &&
		normalized.LastStatus == "" && normalized.LastError == "" && normalized.LastErrorAt.IsZero() &&
		normalized.BackoffUntil.IsZero() && normalized.FailureCount == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentRawJSON(raw json.RawMessage) json.RawMessage {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return append(json.RawMessage(nil), raw...)
	}
	compact, err := json.Marshal(v)
	if err != nil || string(compact) == "null" || string(compact) == "{}" {
		return nil
	}
	return json.RawMessage(compact)
}

func normalizeDurableAgentEmailPendingState(state *DurableAgentEmailPendingState) *DurableAgentEmailPendingState {
	if state == nil {
		return nil
	}
	normalized := DurableAgentEmailPendingState{
		Threads: normalizeDurableAgentEmailPendingThreads(state.Threads),
	}
	if len(normalized.Threads) == 0 {
		return nil
	}
	return &normalized
}

func normalizeDurableAgentEmailPendingThreads(values []DurableAgentEmailPendingThread) []DurableAgentEmailPendingThread {
	if len(values) == 0 {
		return nil
	}
	out := make([]DurableAgentEmailPendingThread, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.ThreadID = strings.TrimSpace(value.ThreadID)
		value.MessageID = strings.TrimSpace(value.MessageID)
		value.From = clampDurableAgentField(value.From, 512)
		value.Subject = clampDurableAgentField(value.Subject, 512)
		value.Snippet = clampDurableAgentField(value.Snippet, 1200)
		value.Body = clampDurableAgentField(value.Body, 2400)
		value.Labels = normalizeDurableAgentStringSet(value.Labels)
		value.AttachmentNames = normalizeDurableAgentStringSet(value.AttachmentNames)
		if value.ThreadID == "" {
			continue
		}
		key := firstNonEmptyDurableAgent(value.ThreadID, value.MessageID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
		if len(out) == durableAgentEmailPendingMaxItems {
			break
		}
	}
	return out
}
