//go:build linux

package session

import (
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
	ReviewChecklistJSON string                        `json:"review_checklist_json,omitempty"`
	CreatedAt           time.Time                     `json:"created_at,omitempty"`
	UpdatedAt           time.Time                     `json:"updated_at,omitempty"`
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
	h.MemoryDigestJSON = strings.TrimSpace(h.MemoryDigestJSON)
	if h.MemoryDigestJSON == "" {
		h.MemoryDigestJSON = "[]"
	}
	h.ResourceReviewJSON = strings.TrimSpace(h.ResourceReviewJSON)
	if h.ResourceReviewJSON == "" {
		h.ResourceReviewJSON = "[]"
	}
	h.PolicyPatchJSON = strings.TrimSpace(h.PolicyPatchJSON)
	if h.PolicyPatchJSON == "" {
		h.PolicyPatchJSON = "{}"
	}
	h.ReviewChecklistJSON = strings.TrimSpace(h.ReviewChecklistJSON)
	if h.ReviewChecklistJSON == "" {
		h.ReviewChecklistJSON = "[]"
	}
	return h
}
