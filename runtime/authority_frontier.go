//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const authorityFrontierRecentWindow = 24 * time.Hour
const authorityFrontierRecentLimit = 12

func (r *Runtime) AuthorityFrontierStatus(ctx context.Context, adminChatID int64) (core.AuthorityFrontierStatusSnapshot, error) {
	_ = ctx
	if r == nil {
		snapshot := core.AuthorityFrontierStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
			AdminChatID: adminChatID,
			Budget:      MaxOutstandingLookaheadApprovalFrontiers,
		}
		appendAuthorityFrontierEmptySlots(&snapshot)
		return snapshot, nil
	}
	now := r.authorityDiscoveryNow()
	snapshot := core.AuthorityFrontierStatusSnapshot{
		GeneratedAt: now,
		AdminChatID: adminChatID,
		Budget:      MaxOutstandingLookaheadApprovalFrontiers,
	}
	if r.store == nil || adminChatID == 0 {
		appendAuthorityFrontierEmptySlots(&snapshot)
		return snapshot, nil
	}
	if err := r.expireLookaheadAllowancesWithSilence(adminChatID, now); err != nil {
		return core.AuthorityFrontierStatusSnapshot{}, err
	}
	records, err := r.store.LookaheadAllowancesForAdmin(adminChatID, MaxOutstandingLookaheadApprovalFrontiers)
	if err != nil {
		return core.AuthorityFrontierStatusSnapshot{}, err
	}
	for _, record := range records {
		if len(snapshot.Slots) >= snapshot.Budget {
			break
		}
		slot := authorityFrontierSlotFromAllowance(len(snapshot.Slots)+1, record, now)
		switch slot.Status {
		case string(session.LookaheadAllowanceReserved):
			snapshot.Used++
			snapshot.Reserved++
		case string(session.LookaheadAllowanceOpen):
			snapshot.Used++
			snapshot.Open++
		case string(session.LookaheadAllowanceExpired):
			snapshot.Expired++
		}
		snapshot.Slots = append(snapshot.Slots, slot)
	}
	recent, err := r.authorityFrontierRecentEvents(adminChatID, now)
	if err != nil {
		return core.AuthorityFrontierStatusSnapshot{}, err
	}
	snapshot.Recent = recent
	appendAuthorityFrontierEmptySlots(&snapshot)
	return snapshot, nil
}

func (r *Runtime) expireLookaheadAllowancesWithSilence(adminChatID int64, now time.Time) error {
	if r == nil || r.store == nil || adminChatID == 0 {
		return nil
	}
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	expired, err := r.store.ExpireLookaheadAllowancesForAdmin(adminChatID, now)
	if err != nil {
		return err
	}
	key := session.SessionKey{ChatID: adminChatID, UserID: 0, Scope: telegramDMScopeRef(adminChatID)}
	for _, allowance := range expired {
		r.recordAuthorityFrontierDelta(key, "expired_unreviewed", map[string]any{
			"allowance_id":          allowance.AllowanceID,
			"review_event_id":       allowance.ReviewEventID,
			"target_admin_chat_id":  adminChatID,
			"next_action_record_id": allowance.NextActionRecordID,
			"entry_id":              allowance.EntryID,
			"reason":                "expired_unreviewed",
		}, now)
		if err := r.recordLookaheadSilenceObservation(key, allowance, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) sweepExpiredLookaheadAllowancesWithSilence(now time.Time) error {
	if r == nil || r.store == nil || r.cfg == nil {
		return nil
	}
	if now.IsZero() {
		now = r.authorityDiscoveryNow()
	}
	for _, adminChatID := range uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs) {
		if err := r.expireLookaheadAllowancesWithSilence(adminChatID, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) recordLookaheadSilenceObservation(key session.SessionKey, allowance session.LookaheadAllowance, now time.Time) error {
	if r == nil || r.store == nil || strings.TrimSpace(allowance.EntryID) == "" {
		return nil
	}
	entry, ok, err := r.store.IdentificationLedgerEntryByID(allowance.EntryID)
	if err != nil || !ok {
		return err
	}
	status := entry.Status
	expiresAt := entry.ExpiresAt
	if !identificationLedgerRuntimeTerminalStatus(status) {
		status = session.IdentificationLedgerStatusExpired
		if expiresAt.IsZero() || (!allowance.ExpiresAt.IsZero() && allowance.ExpiresAt.Before(expiresAt)) {
			expiresAt = allowance.ExpiresAt
		}
	}
	entryInput := session.IdentificationLedgerEntryInput{
		EntryID:     entry.EntryID,
		PlanID:      entry.PlanID,
		PlanVersion: entry.PlanVersion,
		SessionID:   entry.SessionID,
		StepRef:     entry.StepRef,
		ShapeHash:   entry.ShapeHash,
		LabelRef:    entry.LabelRef,
		Status:      status,
		ExpiresAt:   expiresAt,
		CreatedAt:   entry.CreatedAt,
		UpdatedAt:   now,
	}
	_, _, err = r.store.RecordIdentificationLedgerObservation(entryInput, session.IdentificationLedgerObservationInput{
		Method:         session.IdentificationObservationOperator,
		Property:       session.IdentificationPropertyOperatorAction,
		Value:          "expired_unreviewed",
		EvidenceRef:    "lookahead_allowance:" + strings.TrimSpace(allowance.AllowanceID),
		ActorKind:      "operator_absence",
		ActorPrincipal: "silence",
		ActorAction:    "expired_unreviewed",
		ObservedAt:     now,
	})
	return err
}

func identificationLedgerRuntimeTerminalStatus(status session.IdentificationLedgerEntryStatus) bool {
	switch session.NormalizeIdentificationLedgerEntryStatus(status) {
	case session.IdentificationLedgerStatusConsumed, session.IdentificationLedgerStatusExpired, session.IdentificationLedgerStatusInvalidated:
		return true
	default:
		return false
	}
}

func authorityFrontierSlotFromAllowance(index int, record session.LookaheadAllowance, now time.Time) core.AuthorityFrontierSlot {
	record = session.NormalizeLookaheadAllowance(record)
	status := string(record.Status)
	ttl := int64(0)
	if !record.ExpiresAt.IsZero() {
		if record.ExpiresAt.After(now) {
			ttl = int64(record.ExpiresAt.Sub(now).Round(time.Second).Seconds())
		} else if record.Status == session.LookaheadAllowanceReserved || record.Status == session.LookaheadAllowanceOpen {
			status = string(session.LookaheadAllowanceExpired)
		}
	}
	return core.AuthorityFrontierSlot{
		Index:              index,
		Status:             status,
		AllowanceID:        record.AllowanceID,
		ReviewEventID:      record.ReviewEventID,
		SourceSessionID:    record.SourceSessionID,
		TargetSessionID:    record.TargetSessionID,
		NextActionRecordID: record.NextActionRecordID,
		EntryID:            record.EntryID,
		Reason:             record.Reason,
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
		ExpiresAt:          record.ExpiresAt,
		ReleasedAt:         record.ReleasedAt,
		TTLSeconds:         ttl,
	}
}

func (r *Runtime) authorityFrontierRecentEvents(adminChatID int64, now time.Time) ([]core.AuthorityFrontierEvent, error) {
	if r == nil || r.store == nil || adminChatID == 0 {
		return nil, nil
	}
	events, err := r.store.ExecutionEventsByTypes([]string{core.ExecutionEventAuthorityFrontierDelta}, now.Add(-authorityFrontierRecentWindow), 100)
	if err != nil {
		return nil, err
	}
	projected := make([]core.AuthorityFrontierEvent, 0, len(events))
	for _, event := range events {
		delta, ok := authorityFrontierEventFromExecutionEvent(event)
		if !ok || delta.AdminChatID != adminChatID {
			continue
		}
		projected = append(projected, delta)
	}
	sort.SliceStable(projected, func(i, j int) bool {
		return projected[i].ObservedAt.Before(projected[j].ObservedAt)
	})
	lastByShape := map[string]time.Time{}
	countByShape := map[string]int{}
	for i := range projected {
		shapeHash := projected[i].ShapeHash
		if shapeHash == "" {
			continue
		}
		countByShape[shapeHash]++
		projected[i].RepeatedShapeOrdinal = countByShape[shapeHash]
		if previous, ok := lastByShape[shapeHash]; ok {
			projected[i].InterArrivalSeconds = int64(projected[i].ObservedAt.Sub(previous).Round(time.Second).Seconds())
		}
		lastByShape[shapeHash] = projected[i].ObservedAt
	}
	if len(projected) > authorityFrontierRecentLimit {
		projected = projected[len(projected)-authorityFrontierRecentLimit:]
	}
	sort.SliceStable(projected, func(i, j int) bool {
		return projected[i].ObservedAt.After(projected[j].ObservedAt)
	})
	return projected, nil
}

func authorityFrontierEventFromExecutionEvent(event session.ExecutionEvent) (core.AuthorityFrontierEvent, bool) {
	if event.EventType != core.ExecutionEventAuthorityFrontierDelta {
		return core.AuthorityFrontierEvent{}, false
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
		return core.AuthorityFrontierEvent{}, false
	}
	delta := core.AuthorityFrontierEvent{
		ObservedAt:         event.CreatedAt,
		Status:             event.Status,
		AdminChatID:        int64FromAuthorityFrontierPayload(payload, "target_admin_chat_id"),
		AllowanceID:        stringFromAuthorityFrontierPayload(payload, "allowance_id"),
		ReviewEventID:      int64FromAuthorityFrontierPayload(payload, "review_event_id"),
		NextActionRecordID: stringFromAuthorityFrontierPayload(payload, "next_action_record_id"),
		EntryID:            stringFromAuthorityFrontierPayload(payload, "entry_id"),
		ShapeHash:          stringFromAuthorityFrontierPayload(payload, "shape_hash"),
		StepRef:            stringFromAuthorityFrontierPayload(payload, "step_ref"),
		LabelRef:           stringFromAuthorityFrontierPayload(payload, "label_ref"),
		Reason:             stringFromAuthorityFrontierPayload(payload, "reason"),
	}
	return delta, delta.AdminChatID != 0
}

func stringFromAuthorityFrontierPayload(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key].(string); ok {
		return value
	}
	return ""
}

func int64FromAuthorityFrontierPayload(payload map[string]any, key string) int64 {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	default:
		return 0
	}
}

func appendAuthorityFrontierEmptySlots(snapshot *core.AuthorityFrontierStatusSnapshot) {
	if snapshot == nil {
		return
	}
	for len(snapshot.Slots) < snapshot.Budget {
		snapshot.Slots = append(snapshot.Slots, core.AuthorityFrontierSlot{
			Index:  len(snapshot.Slots) + 1,
			Status: "empty",
		})
		snapshot.Empty++
	}
}
