//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) authorityDiscoveryNow() time.Time {
	if r != nil && r.authorityDiscoveryClock != nil {
		return r.authorityDiscoveryClock().UTC()
	}
	return time.Now().UTC()
}

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
		r.recordAuthorityDiscoveryLifecycleEvent(key, input, status, now)
	}
	return nil
}

func (r *Runtime) recordAuthorityDiscoveryLifecycleEvent(key session.SessionKey, entry session.IdentificationLedgerEntryInput, status session.IdentificationLedgerEntryStatus, at time.Time) {
	if r == nil {
		return
	}
	if at.IsZero() {
		at = r.authorityDiscoveryNow()
	}
	r.recordExecutionEvent(key, core.ExecutionEventAuthorityDiscoveryLifecycle, "authority_discovery", string(status), map[string]any{
		"entry_id":     strings.TrimSpace(entry.EntryID),
		"plan_id":      strings.TrimSpace(entry.PlanID),
		"plan_version": strings.TrimSpace(entry.PlanVersion),
		"session_id":   strings.TrimSpace(entry.SessionID),
		"step_ref":     strings.TrimSpace(entry.StepRef),
		"shape_hash":   strings.TrimSpace(entry.ShapeHash),
		"label_ref":    strings.TrimSpace(entry.LabelRef),
	}, at.UTC())
}

func (r *Runtime) recordAuthorityDiscoveryOperatorObservationForLabel(key session.SessionKey, labelRef string, actorPrincipal string, actorAction string, at time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	labelRef = strings.TrimSpace(labelRef)
	if labelRef == "" {
		return nil
	}
	if at.IsZero() {
		at = r.authorityDiscoveryNow()
	}
	sessionID := session.SessionIDForKey(key)
	entries, err := r.store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(sessionID),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   sessionID,
		LabelRef:    labelRef,
		Limit:       500,
	})
	if err != nil {
		return fmt.Errorf("load authority discovery entries for operator observation %s: %w", labelRef, err)
	}
	for _, projection := range entries {
		entry := session.NormalizeIdentificationLedgerEntry(projection.Entry)
		if entry.EntryID == "" || entry.LabelRef != labelRef {
			continue
		}
		if _, _, err := r.store.RecordIdentificationLedgerObservation(session.IdentificationLedgerEntryInput{
			EntryID:     entry.EntryID,
			PlanID:      entry.PlanID,
			PlanVersion: entry.PlanVersion,
			SessionID:   entry.SessionID,
			StepRef:     entry.StepRef,
			ShapeHash:   entry.ShapeHash,
			LabelRef:    entry.LabelRef,
			UpdatedAt:   at,
		}, session.IdentificationLedgerObservationInput{
			Method:         session.IdentificationObservationOperator,
			Property:       session.IdentificationPropertyOperatorAction,
			Value:          strings.TrimSpace(actorAction),
			EvidenceRef:    "label:" + labelRef,
			ActorKind:      "operator",
			ActorPrincipal: strings.TrimSpace(actorPrincipal),
			ActorAction:    strings.TrimSpace(actorAction),
			ObservedAt:     at,
		}); err != nil {
			return fmt.Errorf("record authority discovery operator observation: %w", err)
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
