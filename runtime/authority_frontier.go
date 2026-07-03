//go:build linux

package runtime

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

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
	appendAuthorityFrontierEmptySlots(&snapshot)
	return snapshot, nil
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
