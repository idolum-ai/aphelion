//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) ReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, bool, error) {
	_ = ctx
	if r == nil || r.store == nil || r.resolver == nil {
		return session.ReentryRecommendation{}, false, fmt.Errorf("re-entry recommendation is unavailable")
	}
	if _, ok := r.resolver.ResolveTelegramUser(senderID); !ok {
		return session.ReentryRecommendation{}, false, ErrPrincipalDenied
	}
	return r.store.ReentryRecommendation(strings.TrimSpace(recommendationID))
}

func (r *Runtime) IgnoreReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, error) {
	_ = ctx
	if r == nil || r.store == nil || r.resolver == nil {
		return session.ReentryRecommendation{}, fmt.Errorf("re-entry recommendation is unavailable")
	}
	if _, ok := r.resolver.ResolveTelegramUser(senderID); !ok {
		return session.ReentryRecommendation{}, ErrPrincipalDenied
	}
	record, ok, err := r.store.MarkReentryRecommendationIgnored(strings.TrimSpace(recommendationID), "operator ignored re-entry recommendation", time.Now().UTC())
	if err != nil {
		return session.ReentryRecommendation{}, err
	}
	if !ok {
		return session.ReentryRecommendation{}, fmt.Errorf("re-entry recommendation %q not found", strings.TrimSpace(recommendationID))
	}
	return record, nil
}

func (r *Runtime) PrepareReentryRecommendationSelection(ctx context.Context, senderID int64, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error) {
	_ = ctx
	if r == nil || r.store == nil || r.resolver == nil {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, fmt.Errorf("re-entry recommendation is unavailable")
	}
	if _, ok := r.resolver.ResolveTelegramUser(senderID); !ok {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, ErrPrincipalDenied
	}
	record, ok, err := r.store.ReentryRecommendation(strings.TrimSpace(recommendationID))
	if err != nil || !ok {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, err
	}
	candidate, ok := record.Candidate(candidateID)
	if !ok {
		return record, session.ReentryRecommendationCandidate{}, false, nil
	}
	if session.ReentryRecommendationStatusTerminal(record.Status) {
		return record, candidate, false, nil
	}
	key := session.SessionKey{ChatID: record.ChatID, UserID: 0, Scope: record.Scope}
	fingerprint, current, err := r.CurrentReentryRecommendationFingerprint(key)
	if err != nil {
		return record, candidate, false, err
	}
	if !current || fingerprint != record.TerminalFingerprint {
		stale, _, staleErr := r.store.MarkReentryRecommendationStale(record.ID, "terminal state changed before operator selection", time.Now().UTC())
		if staleErr != nil {
			return record, candidate, false, staleErr
		}
		return stale, candidate, false, nil
	}
	return record, candidate, true, nil
}

func (r *Runtime) ConfirmReentryRecommendationSelection(ctx context.Context, senderID int64, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error) {
	_ = ctx
	if r == nil || r.store == nil || r.resolver == nil {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, fmt.Errorf("re-entry recommendation is unavailable")
	}
	if _, ok := r.resolver.ResolveTelegramUser(senderID); !ok {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, ErrPrincipalDenied
	}
	record, ok, err := r.store.ReentryRecommendation(strings.TrimSpace(recommendationID))
	if err != nil || !ok {
		return session.ReentryRecommendation{}, session.ReentryRecommendationCandidate{}, false, err
	}
	candidate, ok := record.Candidate(candidateID)
	if !ok {
		return record, session.ReentryRecommendationCandidate{}, false, nil
	}
	if session.ReentryRecommendationStatusTerminal(record.Status) {
		return record, candidate, false, nil
	}
	selected, ok, err := r.store.MarkReentryRecommendationSelected(record.ID, candidate.ID, "operator selected re-entry candidate", time.Now().UTC())
	if err != nil || !ok {
		return selected, candidate, false, err
	}
	return selected, candidate, true, nil
}
