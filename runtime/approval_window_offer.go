//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const approvalWindowOfferTTL = 24 * time.Hour

func (r *Runtime) CreateApprovalWindowOfferForKey(ctx context.Context, key session.SessionKey, adminUserID int64, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.ApprovalWindowOffer{}, false, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return session.ApprovalWindowOffer{}, false, nil
	}
	sourceKind = strings.TrimSpace(sourceKind)
	sourceID = strings.TrimSpace(sourceID)
	if sourceKind == "" || sourceID == "" || key.ChatID == 0 {
		return session.ApprovalWindowOffer{}, false, nil
	}
	scope := session.NormalizeScopeRef(key.Scope)
	if scope.IsZero() && key.ChatID != 0 {
		scope = telegramDMScopeRef(key.ChatID)
	}
	scopeKind, scopeID := session.OperatorAutoScopeForRef(scope)
	if strings.TrimSpace(scopeKind) == "" || strings.TrimSpace(scopeID) == "" {
		return session.ApprovalWindowOffer{}, false, nil
	}
	now := time.Now().UTC()
	if existing, ok, err := r.store.ActiveApprovalWindowOfferForSource(key.ChatID, sourceKind, sourceID, now); err != nil {
		return session.ApprovalWindowOffer{}, false, err
	} else if ok {
		return existing, true, nil
	}
	offer := session.ApprovalWindowOffer{
		ID:                 newApprovalWindowOfferID(key.ChatID, now),
		ChatID:             key.ChatID,
		AdminUserID:        adminUserID,
		SessionID:          session.SessionIDForKey(session.SessionKey{ChatID: key.ChatID, UserID: key.UserID, Scope: scope}),
		ScopeKind:          scopeKind,
		ScopeID:            scopeID,
		DurableAgentID:     scope.DurableAgentID,
		SourceKind:         sourceKind,
		SourceID:           sourceID,
		SourceDecisionKind: strings.TrimSpace(sourceDecisionKind),
		CreatedAt:          now,
		ExpiresAt:          now.Add(approvalWindowOfferTTL),
		UpdatedAt:          now,
	}
	created, err := r.store.CreateApprovalWindowOffer(offer)
	if err != nil {
		return session.ApprovalWindowOffer{}, false, err
	}
	return created, true, nil
}

func (r *Runtime) EnableApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64, duration time.Duration) (string, error) {
	offer, ok, err := r.activeApprovalWindowOffer(offerID, false)
	if err != nil || !ok {
		return "", err
	}
	text, err := r.EnableApprovalWindowForKey(ctx, approvalWindowOfferSessionKey(offer), adminUserID, duration)
	if err != nil {
		return "", err
	}
	if _, ok, err := r.store.MarkApprovalWindowOfferUsed(offer.ID, time.Now().UTC()); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("approval window offer is no longer active")
	}
	return text, nil
}

func (r *Runtime) DoubleApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64) (string, error) {
	offer, ok, err := r.activeApprovalWindowOffer(offerID, true)
	if err != nil || !ok {
		return "", err
	}
	return r.DoubleApprovalWindowForKey(ctx, approvalWindowOfferSessionKey(offer), adminUserID)
}

func (r *Runtime) CancelApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64) (string, error) {
	offer, ok, err := r.activeApprovalWindowOffer(offerID, true)
	if err != nil || !ok {
		return "", err
	}
	text, err := r.CancelApprovalWindowForKey(ctx, approvalWindowOfferSessionKey(offer), adminUserID)
	if err != nil {
		return "", err
	}
	if _, _, closeErr := r.store.CloseApprovalWindowOffer(offer.ID, time.Now().UTC()); closeErr != nil {
		return "", closeErr
	}
	return text, nil
}

func (r *Runtime) CloseApprovalWindowOffer(ctx context.Context, offerID string) error {
	_ = ctx
	if r == nil || r.store == nil {
		return nil
	}
	_, _, err := r.store.CloseApprovalWindowOffer(offerID, time.Now().UTC())
	return err
}

func (r *Runtime) activeApprovalWindowOffer(offerID string, allowUsed bool) (session.ApprovalWindowOffer, bool, error) {
	if r == nil || r.store == nil {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval windows are unavailable")
	}
	offer, ok, err := r.store.ApprovalWindowOffer(offerID)
	if err != nil {
		return session.ApprovalWindowOffer{}, false, err
	}
	if !ok || !offer.ActiveAt(time.Now().UTC()) {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer is no longer active")
	}
	if !allowUsed && !offer.UsedAt.IsZero() {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer was already used")
	}
	return offer, true, nil
}

func approvalWindowOfferSessionKey(offer session.ApprovalWindowOffer) session.SessionKey {
	offer = session.NormalizeApprovalWindowOffer(offer)
	return session.SessionKey{ChatID: offer.ChatID, Scope: offer.ScopeRef()}
}

func newApprovalWindowOfferID(chatID int64, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return "awo-" + strconv.FormatInt(chatID, 36) + "-" + strconv.FormatInt(now.UnixNano(), 36)
}
