//go:build linux

package session

import (
	"strings"
	"time"
)

const (
	ApprovalWindowOfferSourceDecision     = "decision"
	ApprovalWindowOfferSourceContinuation = "continuation"
	ApprovalWindowOfferSourceMission      = "mission_action"
)

type ApprovalWindowOffer struct {
	ID                 string
	ChatID             int64
	AdminUserID        int64
	SessionID          string
	ScopeKind          string
	ScopeID            string
	DurableAgentID     string
	SourceKind         string
	SourceID           string
	SourceDecisionKind string
	CreatedAt          time.Time
	ExpiresAt          time.Time
	UsedAt             time.Time
	ClosedAt           time.Time
	UpdatedAt          time.Time
}

func NormalizeApprovalWindowOffer(offer ApprovalWindowOffer) ApprovalWindowOffer {
	offer.ID = strings.TrimSpace(offer.ID)
	offer.SessionID = strings.TrimSpace(offer.SessionID)
	offer.ScopeKind = strings.TrimSpace(offer.ScopeKind)
	offer.ScopeID = strings.TrimSpace(offer.ScopeID)
	offer.DurableAgentID = strings.TrimSpace(offer.DurableAgentID)
	offer.SourceKind = normalizeEnumValue(offer.SourceKind)
	offer.SourceID = strings.TrimSpace(offer.SourceID)
	offer.SourceDecisionKind = strings.TrimSpace(offer.SourceDecisionKind)
	if !offer.CreatedAt.IsZero() {
		offer.CreatedAt = offer.CreatedAt.UTC()
	}
	if !offer.ExpiresAt.IsZero() {
		offer.ExpiresAt = offer.ExpiresAt.UTC()
	}
	if !offer.UsedAt.IsZero() {
		offer.UsedAt = offer.UsedAt.UTC()
	}
	if !offer.ClosedAt.IsZero() {
		offer.ClosedAt = offer.ClosedAt.UTC()
	}
	if !offer.UpdatedAt.IsZero() {
		offer.UpdatedAt = offer.UpdatedAt.UTC()
	}
	return offer
}

func (offer ApprovalWindowOffer) ActiveAt(now time.Time) bool {
	offer = NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || offer.ChatID == 0 || offer.ScopeKind == "" || offer.ScopeID == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !offer.ClosedAt.IsZero() {
		return false
	}
	return !offer.ExpiresAt.IsZero() && offer.ExpiresAt.After(now)
}

func (offer ApprovalWindowOffer) ScopeRef() ScopeRef {
	offer = NormalizeApprovalWindowOffer(offer)
	return NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(offer.ScopeKind),
		ID:             offer.ScopeID,
		DurableAgentID: offer.DurableAgentID,
	})
}
