//go:build linux

package session

import (
	"encoding/json"
	"strings"
	"time"
)

type TelegramThreadPromotionStatus string

const (
	TelegramThreadPromotionStatusDraft      TelegramThreadPromotionStatus = "draft"
	TelegramThreadPromotionStatusReady      TelegramThreadPromotionStatus = "ready"
	TelegramThreadPromotionStatusApproved   TelegramThreadPromotionStatus = "approved"
	TelegramThreadPromotionStatusCancelled  TelegramThreadPromotionStatus = "cancelled"
	TelegramThreadPromotionStatusSuperseded TelegramThreadPromotionStatus = "superseded"
)

type TelegramThreadPromotionResult struct {
	Text      string                        `json:"text"`
	HandoffID string                        `json:"handoff_id"`
	ThreadID  int64                         `json:"thread_id"`
	Status    TelegramThreadPromotionStatus `json:"status"`
}

type TelegramThreadPromotionHandoff struct {
	HandoffID           string                        `json:"handoff_id"`
	ChatID              int64                         `json:"chat_id"`
	ThreadID            int64                         `json:"thread_id"`
	DisplaySlot         int64                         `json:"display_slot,omitempty"`
	Status              TelegramThreadPromotionStatus `json:"status"`
	CreatedBySenderID   int64                         `json:"created_by_sender_id,omitempty"`
	SourceSessionID     string                        `json:"source_session_id,omitempty"`
	SourceThreadStatus  string                        `json:"source_thread_status,omitempty"`
	SourcePreview       string                        `json:"source_preview,omitempty"`
	ContextSummary      string                        `json:"context_summary,omitempty"`
	MemoryDigestJSON    string                        `json:"memory_digest_json,omitempty"`
	ResourceReviewJSON  string                        `json:"resource_review_json,omitempty"`
	PolicyPatchJSON     string                        `json:"policy_patch_json,omitempty"`
	ProposedChildJSON   string                        `json:"proposed_child_json,omitempty"`
	FirstTask           string                        `json:"first_task,omitempty"`
	ValidationJSON      string                        `json:"validation_json,omitempty"`
	ReviewChecklistJSON string                        `json:"review_checklist_json,omitempty"`
	CreatedAt           time.Time                     `json:"created_at,omitempty"`
	UpdatedAt           time.Time                     `json:"updated_at,omitempty"`
}

type TelegramThreadPromotionContextDigest struct {
	Summary              string                                  `json:"summary"`
	SourceSessionID      string                                  `json:"source_session_id"`
	SourceThreadStatus   string                                  `json:"source_thread_status"`
	SourcePreview        string                                  `json:"source_preview,omitempty"`
	MessageCount         int                                     `json:"message_count"`
	IncludedMessageCount int                                     `json:"included_message_count"`
	RecentMessages       []TelegramThreadPromotionContextMessage `json:"recent_messages,omitempty"`
	SourceRefs           []RecordReference                       `json:"source_refs,omitempty"`
	GeneratedAt          time.Time                               `json:"generated_at,omitempty"`
}

type TelegramThreadPromotionContextMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	TurnIndex int       `json:"turn_index,omitempty"`
	Excerpt   string    `json:"excerpt"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type TelegramThreadPromotionProposedChild struct {
	AgentID     string   `json:"agent_id"`
	Label       string   `json:"label"`
	ChannelKind string   `json:"channel_kind"`
	Charter     string   `json:"charter"`
	Autonomy    string   `json:"autonomy"`
	Visibility  string   `json:"visibility"`
	WakeupMode  string   `json:"wakeup_mode"`
	FirstTask   string   `json:"first_task"`
	NonGoals    []string `json:"non_goals,omitempty"`
}

type TelegramThreadPromotionMemoryCandidate struct {
	CandidateID string `json:"candidate_id"`
	Source      string `json:"source"`
	Label       string `json:"label"`
	Excerpt     string `json:"excerpt"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason,omitempty"`
}

type TelegramThreadPromotionResourceCandidate struct {
	CandidateID    string `json:"candidate_id"`
	Kind           string `json:"kind"`
	TargetResource string `json:"target_resource"`
	Purpose        string `json:"purpose"`
	RiskClass      string `json:"risk_class,omitempty"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
}

type TelegramThreadPromotionPolicyReview struct {
	Mode           string   `json:"mode"`
	Autonomy       string   `json:"autonomy"`
	Visibility     string   `json:"visibility"`
	SharedContext  string   `json:"shared_context"`
	DriftPolicy    string   `json:"drift_policy"`
	Capabilities   []string `json:"capabilities,omitempty"`
	StopConditions []string `json:"stop_conditions,omitempty"`
}

func NormalizeTelegramThreadPromotionStatus(status TelegramThreadPromotionStatus) TelegramThreadPromotionStatus {
	switch TelegramThreadPromotionStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case TelegramThreadPromotionStatusDraft:
		return TelegramThreadPromotionStatusDraft
	case TelegramThreadPromotionStatusReady:
		return TelegramThreadPromotionStatusReady
	case TelegramThreadPromotionStatusApproved:
		return TelegramThreadPromotionStatusApproved
	case TelegramThreadPromotionStatusCancelled:
		return TelegramThreadPromotionStatusCancelled
	case TelegramThreadPromotionStatusSuperseded:
		return TelegramThreadPromotionStatusSuperseded
	default:
		return ""
	}
}

func NormalizeTelegramThreadPromotionHandoff(h TelegramThreadPromotionHandoff) TelegramThreadPromotionHandoff {
	h.HandoffID = strings.TrimSpace(h.HandoffID)
	h.Status = NormalizeTelegramThreadPromotionStatus(h.Status)
	if h.Status == "" {
		h.Status = TelegramThreadPromotionStatusDraft
	}
	h.SourceSessionID = strings.TrimSpace(h.SourceSessionID)
	h.SourceThreadStatus = strings.TrimSpace(h.SourceThreadStatus)
	h.SourcePreview = clampStoreText(strings.Join(strings.Fields(strings.TrimSpace(h.SourcePreview)), " "), 500)
	h.ContextSummary = clampStoreText(strings.TrimSpace(h.ContextSummary), 4000)
	h.MemoryDigestJSON = normalizePromotionJSON(h.MemoryDigestJSON, "[]")
	h.ResourceReviewJSON = normalizePromotionJSON(h.ResourceReviewJSON, "[]")
	h.PolicyPatchJSON = normalizePromotionJSON(h.PolicyPatchJSON, "{}")
	h.ProposedChildJSON = normalizePromotionJSON(h.ProposedChildJSON, "{}")
	h.FirstTask = clampStoreText(strings.TrimSpace(h.FirstTask), 1200)
	h.ValidationJSON = normalizePromotionJSON(h.ValidationJSON, "[]")
	h.ReviewChecklistJSON = normalizePromotionJSON(h.ReviewChecklistJSON, "[]")
	return h
}

func EncodeTelegramThreadPromotionJSON(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	encoded := strings.TrimSpace(string(raw))
	if encoded == "" || encoded == "null" {
		return fallback
	}
	return encoded
}

func DecodeTelegramThreadPromotionContextDigest(raw string) (TelegramThreadPromotionContextDigest, bool) {
	var digest TelegramThreadPromotionContextDigest
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &digest); err != nil {
		return TelegramThreadPromotionContextDigest{}, false
	}
	digest.Summary = clampStoreText(strings.TrimSpace(digest.Summary), 4000)
	digest.SourceSessionID = strings.TrimSpace(digest.SourceSessionID)
	digest.SourceThreadStatus = strings.TrimSpace(digest.SourceThreadStatus)
	digest.SourcePreview = clampStoreText(strings.Join(strings.Fields(strings.TrimSpace(digest.SourcePreview)), " "), 500)
	return digest, true
}

func DecodeTelegramThreadPromotionProposedChild(raw string) (TelegramThreadPromotionProposedChild, bool) {
	var child TelegramThreadPromotionProposedChild
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &child); err != nil {
		return TelegramThreadPromotionProposedChild{}, false
	}
	child.AgentID = strings.TrimSpace(child.AgentID)
	child.Label = strings.TrimSpace(child.Label)
	child.ChannelKind = strings.TrimSpace(child.ChannelKind)
	child.Charter = clampStoreText(strings.TrimSpace(child.Charter), 1000)
	child.Autonomy = strings.TrimSpace(child.Autonomy)
	child.Visibility = strings.TrimSpace(child.Visibility)
	child.WakeupMode = strings.TrimSpace(child.WakeupMode)
	child.FirstTask = clampStoreText(strings.TrimSpace(child.FirstTask), 1200)
	return child, child.AgentID != "" || child.Label != "" || child.Charter != ""
}

func DecodeTelegramThreadPromotionMemoryCandidates(raw string) ([]TelegramThreadPromotionMemoryCandidate, bool) {
	var candidates []TelegramThreadPromotionMemoryCandidate
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &candidates); err != nil {
		return nil, false
	}
	return candidates, true
}

func DecodeTelegramThreadPromotionResourceCandidates(raw string) ([]TelegramThreadPromotionResourceCandidate, bool) {
	var candidates []TelegramThreadPromotionResourceCandidate
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &candidates); err != nil {
		return nil, false
	}
	return candidates, true
}

func DecodeTelegramThreadPromotionPolicyReview(raw string) (TelegramThreadPromotionPolicyReview, bool) {
	var review TelegramThreadPromotionPolicyReview
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &review); err != nil {
		return TelegramThreadPromotionPolicyReview{}, false
	}
	return review, true
}

func normalizePromotionJSON(raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if !json.Valid([]byte(raw)) {
		return fallback
	}
	return raw
}
