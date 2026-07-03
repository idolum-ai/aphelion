//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) markAuthorityDiscoveryContinuationRecoveryContractStatus(key session.SessionKey, state session.ContinuationState, status session.IdentificationLedgerEntryStatus, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	contractID := strings.TrimSpace(state.ContinuationLease.RecoveryContractID)
	if contractID == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	expiresAt := time.Time{}
	if status == session.IdentificationLedgerStatusApproved {
		expiresAt = state.ContinuationLease.ExpiresAt
	}
	return r.markAuthorityDiscoveryLabelStatus(key, contractID, status, expiresAt, now)
}

func (r *Runtime) markAuthorityDiscoveryLabelStatus(key session.SessionKey, labelRef string, status session.IdentificationLedgerEntryStatus, expiresAt time.Time, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	labelRef = strings.TrimSpace(labelRef)
	if labelRef == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	sessionID := session.SessionIDForKey(key)
	entries, err := r.store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(sessionID),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   sessionID,
		LabelRef:    labelRef,
		Limit:       500,
	})
	if err != nil {
		return fmt.Errorf("load authority discovery entries for %s: %w", labelRef, err)
	}
	for _, projection := range entries {
		entry := session.NormalizeIdentificationLedgerEntry(projection.Entry)
		if entry.LabelRef != labelRef {
			continue
		}
		if entry.Status == status {
			continue
		}
		if authorityDiscoveryLedgerStatusTerminal(entry.Status) {
			continue
		}
		input := session.IdentificationLedgerEntryInput{
			EntryID:     entry.EntryID,
			PlanID:      entry.PlanID,
			PlanVersion: entry.PlanVersion,
			SessionID:   entry.SessionID,
			StepRef:     entry.StepRef,
			ShapeHash:   entry.ShapeHash,
			LabelRef:    entry.LabelRef,
			Status:      status,
			ExpiresAt:   expiresAt,
			UpdatedAt:   now,
		}
		if _, err := r.store.RecordIdentificationLedgerEntry(input); err != nil {
			return fmt.Errorf("mark authority discovery entry %s as %s: %w", entry.EntryID, status, err)
		}
	}
	return nil
}

func authorityDiscoveryLedgerStatusTerminal(status session.IdentificationLedgerEntryStatus) bool {
	switch session.NormalizeIdentificationLedgerEntryStatus(status) {
	case session.IdentificationLedgerStatusConsumed, session.IdentificationLedgerStatusExpired, session.IdentificationLedgerStatusInvalidated:
		return true
	default:
		return false
	}
}
