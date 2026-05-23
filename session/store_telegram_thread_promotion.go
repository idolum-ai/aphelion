//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var defaultTelegramThreadPromotionChecklistJSON = EncodeTelegramThreadPromotionJSON([]string{
	"distill source thread context",
	"review proposed child label, charter, autonomy, wake mode, and first task",
	"review memory candidates before child write",
	"review filesystem/tool/network/resource requests before grants",
	"review policy/autonomy/wake defaults",
	"approve supervised first run separately",
}, `[]`)

func (s *SQLiteStore) CreateTelegramThreadPromotionDraft(chatID int64, threadID int64, createdBySenderID int64, now time.Time) (TelegramThreadPromotionHandoff, bool, error) {
	if chatID == 0 || threadID <= 0 {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("telegram thread promotion requires chat and thread id")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("begin telegram thread promotion draft: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	thread, ok, err := telegramThreadTx(tx, chatID, threadID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, err
	}
	if !ok {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("telegram thread %d/%d does not exist", chatID, threadID)
	}
	if !thread.Open() {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("telegram thread %d/%d is not open", chatID, threadID)
	}
	if handoff, ok, err := latestTelegramThreadPromotionHandoffTx(tx, chatID, threadID, ""); err != nil {
		return TelegramThreadPromotionHandoff{}, false, err
	} else if ok && telegramThreadPromotionStatusActive(handoff.Status) {
		if err := tx.Commit(); err != nil {
			return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("commit telegram thread promotion draft lookup: %w", err)
		}
		return handoff, false, nil
	}

	sessionID := SessionIDForKey(SessionKey{ChatID: chatID, UserID: 0, Scope: TelegramThreadScopeRef(chatID, threadID)})
	handoffID := telegramThreadPromotionHandoffID(chatID, threadID, now)
	firstTask := telegramThreadPromotionDefaultFirstTask(thread)
	handoff := NormalizeTelegramThreadPromotionHandoff(TelegramThreadPromotionHandoff{
		HandoffID:           handoffID,
		ChatID:              chatID,
		ThreadID:            threadID,
		DisplaySlot:         thread.DisplaySlot,
		Status:              TelegramThreadPromotionStatusDraft,
		CreatedBySenderID:   createdBySenderID,
		SourceSessionID:     sessionID,
		SourceThreadStatus:  string(thread.Status),
		SourcePreview:       thread.CreatedText,
		ContextSummary:      telegramThreadPromotionDefaultContextSummary(thread),
		MemoryDigestJSON:    "[]",
		ResourceReviewJSON:  "[]",
		PolicyPatchJSON:     "{}",
		ProposedChildJSON:   "{}",
		FirstTask:           firstTask,
		ValidationJSON:      EncodeTelegramThreadPromotionJSON(defaultTelegramThreadPromotionValidation(), `[]`),
		ReviewChecklistJSON: defaultTelegramThreadPromotionChecklistJSON,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if _, err := tx.Exec(`
		INSERT INTO telegram_thread_promotion_handoffs(
			handoff_id, chat_id, thread_id, display_slot, status, created_by_sender_id,
			source_session_id, source_thread_status, source_preview, context_summary,
			memory_digest_json, resource_review_json, policy_patch_json, proposed_child_json,
			first_task, validation_json, review_checklist_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, handoff.HandoffID, handoff.ChatID, handoff.ThreadID, handoff.DisplaySlot, string(handoff.Status), handoff.CreatedBySenderID,
		handoff.SourceSessionID, handoff.SourceThreadStatus, handoff.SourcePreview, handoff.ContextSummary,
		handoff.MemoryDigestJSON, handoff.ResourceReviewJSON, handoff.PolicyPatchJSON, handoff.ProposedChildJSON,
		handoff.FirstTask, handoff.ValidationJSON, handoff.ReviewChecklistJSON,
		handoff.CreatedAt.Format(time.RFC3339Nano), handoff.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("insert telegram thread promotion draft: %w", err)
	}
	reloaded, ok, err := telegramThreadPromotionHandoffTx(tx, handoff.HandoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, err
	}
	if !ok {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("telegram thread promotion handoff %q missing after insert", handoff.HandoffID)
	}
	if err := tx.Commit(); err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("commit telegram thread promotion draft: %w", err)
	}
	return reloaded, true, nil
}

func (s *SQLiteStore) UpdateTelegramThreadPromotionReviewPackage(handoff TelegramThreadPromotionHandoff, now time.Time) (TelegramThreadPromotionHandoff, error) {
	handoff = NormalizeTelegramThreadPromotionHandoff(handoff)
	if handoff.HandoffID == "" {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	_, err := s.db.Exec(`
		UPDATE telegram_thread_promotion_handoffs
		SET context_summary = ?, memory_digest_json = ?, resource_review_json = ?, policy_patch_json = ?,
			proposed_child_json = ?, first_task = ?, validation_json = ?, review_checklist_json = ?, updated_at = ?
		WHERE handoff_id = ?
	`, handoff.ContextSummary, handoff.MemoryDigestJSON, handoff.ResourceReviewJSON, handoff.PolicyPatchJSON,
		handoff.ProposedChildJSON, handoff.FirstTask, handoff.ValidationJSON, handoff.ReviewChecklistJSON,
		now.Format(time.RFC3339Nano), handoff.HandoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("update telegram thread promotion review package: %w", err)
	}
	updated, ok, err := s.TelegramThreadPromotionHandoff(handoff.HandoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, err
	}
	if !ok {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff %q not found after update", handoff.HandoffID)
	}
	return updated, nil
}

func (s *SQLiteStore) MarkTelegramThreadPromotionReady(handoffID string, now time.Time) (TelegramThreadPromotionHandoff, error) {
	return s.updateTelegramThreadPromotionStatus(handoffID, TelegramThreadPromotionStatusReady, now, TelegramThreadPromotionStatusDraft, TelegramThreadPromotionStatusReady)
}

func (s *SQLiteStore) CancelTelegramThreadPromotion(handoffID string, now time.Time) (TelegramThreadPromotionHandoff, error) {
	return s.updateTelegramThreadPromotionStatus(handoffID, TelegramThreadPromotionStatusCancelled, now, TelegramThreadPromotionStatusDraft, TelegramThreadPromotionStatusReady, TelegramThreadPromotionStatusCancelled)
}

func (s *SQLiteStore) SupersedeTelegramThreadPromotion(handoffID string, now time.Time) (TelegramThreadPromotionHandoff, error) {
	return s.updateTelegramThreadPromotionStatus(handoffID, TelegramThreadPromotionStatusSuperseded, now, TelegramThreadPromotionStatusDraft, TelegramThreadPromotionStatusReady, TelegramThreadPromotionStatusSuperseded)
}

func (s *SQLiteStore) updateTelegramThreadPromotionStatus(handoffID string, next TelegramThreadPromotionStatus, now time.Time, allowed ...TelegramThreadPromotionStatus) (TelegramThreadPromotionHandoff, error) {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff id is required")
	}
	current, ok, err := s.TelegramThreadPromotionHandoff(handoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, err
	}
	if !ok {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff %q was not found", handoffID)
	}
	allowedSet := map[TelegramThreadPromotionStatus]struct{}{}
	for _, status := range allowed {
		allowedSet[NormalizeTelegramThreadPromotionStatus(status)] = struct{}{}
	}
	if _, ok := allowedSet[current.Status]; !ok {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff %q is %s and cannot transition to %s", handoffID, current.Status, next)
	}
	if current.Status == next {
		return current, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	_, err = s.db.Exec(`
		UPDATE telegram_thread_promotion_handoffs
		SET status = ?, updated_at = ?
		WHERE handoff_id = ?
	`, string(next), now.Format(time.RFC3339Nano), handoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("update telegram thread promotion status: %w", err)
	}
	updated, ok, err := s.TelegramThreadPromotionHandoff(handoffID)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, err
	}
	if !ok {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("telegram thread promotion handoff %q not found after status update", handoffID)
	}
	return updated, nil
}

func (s *SQLiteStore) TelegramThreadPromotionHandoff(handoffID string) (TelegramThreadPromotionHandoff, bool, error) {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	return telegramThreadPromotionHandoffDB(s.db, handoffID)
}

func (s *SQLiteStore) LatestTelegramThreadPromotionHandoff(chatID int64, threadID int64) (TelegramThreadPromotionHandoff, bool, error) {
	if chatID == 0 || threadID <= 0 {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	return latestTelegramThreadPromotionHandoffDB(s.db, chatID, threadID, "")
}

func (s *SQLiteStore) ListTelegramThreadPromotionHandoffs(chatID int64, limit int) ([]TelegramThreadPromotionHandoff, error) {
	if chatID == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(telegramThreadPromotionHandoffSelectSQL()+`
		WHERE chat_id = ?
		ORDER BY updated_at DESC, handoff_id DESC
		LIMIT ?
	`, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list telegram thread promotion handoffs: %w", err)
	}
	defer rows.Close()
	return scanTelegramThreadPromotionHandoffRows(rows)
}

func telegramThreadPromotionHandoffID(chatID int64, threadID int64, now time.Time) string {
	return "thread-promotion:" + strconv.FormatInt(chatID, 10) + ":" + strconv.FormatInt(threadID, 10) + ":" + strconv.FormatInt(now.UnixNano(), 10)
}

func telegramThreadPromotionDefaultContextSummary(thread TelegramThread) string {
	label := strconv.FormatInt(firstNonZeroInt(thread.DisplaySlot, thread.ThreadID), 10)
	preview := strings.Join(strings.Fields(strings.TrimSpace(thread.CreatedText)), " ")
	if preview == "" {
		preview = "No thread prompt captured yet. Review the source thread transcript before promotion."
	}
	return "Draft promotion handoff for thread " + label + ". Context, memory candidates, resources, policy, and first run still require explicit review. Source preview: " + clampStoreText(preview, 500)
}

func telegramThreadPromotionDefaultFirstTask(thread TelegramThread) string {
	label := strconv.FormatInt(firstNonZeroInt(thread.DisplaySlot, thread.ThreadID), 10)
	preview := strings.Join(strings.Fields(strings.TrimSpace(thread.CreatedText)), " ")
	if preview == "" {
		preview = "review the source thread context"
	}
	return "Read the approved promotion handoff for source thread " + label + ", restate the charter, boundaries, memory/resource decisions, and first useful next step, then stop for parent review before taking external action. Source intent: " + clampStoreText(preview, 500)
}

func defaultTelegramThreadPromotionValidation() []string {
	return []string{
		"source thread digest present",
		"proposed child spec reviewed",
		"memory candidates reviewed without writes",
		"resource/capability candidates reviewed without grants",
		"first supervised task requires separate approval before live run",
	}
}

func telegramThreadPromotionStatusActive(status TelegramThreadPromotionStatus) bool {
	switch NormalizeTelegramThreadPromotionStatus(status) {
	case TelegramThreadPromotionStatusDraft, TelegramThreadPromotionStatusReady, TelegramThreadPromotionStatusApproved:
		return true
	default:
		return false
	}
}

func latestTelegramThreadPromotionHandoffDB(db *sql.DB, chatID int64, threadID int64, status TelegramThreadPromotionStatus) (TelegramThreadPromotionHandoff, bool, error) {
	where := "WHERE chat_id = ? AND thread_id = ?"
	args := []any{chatID, threadID}
	if normalized := NormalizeTelegramThreadPromotionStatus(status); normalized != "" {
		where += " AND status = ?"
		args = append(args, string(normalized))
	}
	row := db.QueryRow(telegramThreadPromotionHandoffSelectSQL()+where+` ORDER BY updated_at DESC, handoff_id DESC LIMIT 1`, args...)
	handoff, err := scanTelegramThreadPromotionHandoff(row)
	if err == sql.ErrNoRows {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("lookup telegram thread promotion handoff: %w", err)
	}
	return handoff, true, nil
}

func latestTelegramThreadPromotionHandoffTx(tx *sql.Tx, chatID int64, threadID int64, status TelegramThreadPromotionStatus) (TelegramThreadPromotionHandoff, bool, error) {
	where := "WHERE chat_id = ? AND thread_id = ?"
	args := []any{chatID, threadID}
	if normalized := NormalizeTelegramThreadPromotionStatus(status); normalized != "" {
		where += " AND status = ?"
		args = append(args, string(normalized))
	}
	row := tx.QueryRow(telegramThreadPromotionHandoffSelectSQL()+where+` ORDER BY updated_at DESC, handoff_id DESC LIMIT 1`, args...)
	handoff, err := scanTelegramThreadPromotionHandoff(row)
	if err == sql.ErrNoRows {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("lookup telegram thread promotion handoff: %w", err)
	}
	return handoff, true, nil
}

func telegramThreadPromotionHandoffDB(db *sql.DB, handoffID string) (TelegramThreadPromotionHandoff, bool, error) {
	row := db.QueryRow(telegramThreadPromotionHandoffSelectSQL()+`WHERE handoff_id = ?`, strings.TrimSpace(handoffID))
	handoff, err := scanTelegramThreadPromotionHandoff(row)
	if err == sql.ErrNoRows {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("lookup telegram thread promotion handoff: %w", err)
	}
	return handoff, true, nil
}

func telegramThreadPromotionHandoffTx(tx *sql.Tx, handoffID string) (TelegramThreadPromotionHandoff, bool, error) {
	row := tx.QueryRow(telegramThreadPromotionHandoffSelectSQL()+`WHERE handoff_id = ?`, strings.TrimSpace(handoffID))
	handoff, err := scanTelegramThreadPromotionHandoff(row)
	if err == sql.ErrNoRows {
		return TelegramThreadPromotionHandoff{}, false, nil
	}
	if err != nil {
		return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("lookup telegram thread promotion handoff: %w", err)
	}
	return handoff, true, nil
}

func telegramThreadPromotionHandoffSelectSQL() string {
	return `SELECT handoff_id, chat_id, thread_id, display_slot, status, created_by_sender_id,
		source_session_id, source_thread_status, source_preview, context_summary,
		memory_digest_json, resource_review_json, policy_patch_json, proposed_child_json,
		first_task, validation_json, review_checklist_json, created_at, updated_at
		FROM telegram_thread_promotion_handoffs `
}

func scanTelegramThreadPromotionHandoffRows(rows *sql.Rows) ([]TelegramThreadPromotionHandoff, error) {
	out := []TelegramThreadPromotionHandoff{}
	for rows.Next() {
		handoff, err := scanTelegramThreadPromotionHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, handoff)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram thread promotion handoffs: %w", err)
	}
	return out, nil
}

func scanTelegramThreadPromotionHandoff(scanner interface{ Scan(dest ...any) error }) (TelegramThreadPromotionHandoff, error) {
	var h TelegramThreadPromotionHandoff
	var statusRaw string
	var createdRaw string
	var updatedRaw string
	if err := scanner.Scan(
		&h.HandoffID,
		&h.ChatID,
		&h.ThreadID,
		&h.DisplaySlot,
		&statusRaw,
		&h.CreatedBySenderID,
		&h.SourceSessionID,
		&h.SourceThreadStatus,
		&h.SourcePreview,
		&h.ContextSummary,
		&h.MemoryDigestJSON,
		&h.ResourceReviewJSON,
		&h.PolicyPatchJSON,
		&h.ProposedChildJSON,
		&h.FirstTask,
		&h.ValidationJSON,
		&h.ReviewChecklistJSON,
		&createdRaw,
		&updatedRaw,
	); err != nil {
		return TelegramThreadPromotionHandoff{}, err
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("parse telegram thread promotion created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return TelegramThreadPromotionHandoff{}, fmt.Errorf("parse telegram thread promotion updated_at: %w", err)
	}
	h.Status = NormalizeTelegramThreadPromotionStatus(TelegramThreadPromotionStatus(statusRaw))
	h.CreatedAt = createdAt
	h.UpdatedAt = updatedAt
	return NormalizeTelegramThreadPromotionHandoff(h), nil
}
