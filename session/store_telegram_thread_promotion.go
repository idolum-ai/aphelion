//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var defaultTelegramThreadPromotionChecklistJSON = `[
  "distill source thread context",
  "review memory candidates before child write",
  "review filesystem/tool/network/resource requests",
  "review policy/autonomy/wake defaults",
  "approve supervised first run separately"
]`

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
	if handoff, ok, err := latestTelegramThreadPromotionHandoffTx(tx, chatID, threadID, TelegramThreadPromotionStatusDraft); err != nil {
		return TelegramThreadPromotionHandoff{}, false, err
	} else if ok {
		if err := tx.Commit(); err != nil {
			return TelegramThreadPromotionHandoff{}, false, fmt.Errorf("commit telegram thread promotion draft lookup: %w", err)
		}
		return handoff, false, nil
	}

	sessionID := SessionIDForKey(SessionKey{ChatID: chatID, UserID: 0, Scope: TelegramThreadScopeRef(chatID, threadID)})
	handoffID := telegramThreadPromotionHandoffID(chatID, threadID, now)
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
		ReviewChecklistJSON: defaultTelegramThreadPromotionChecklistJSON,
		CreatedAt:           now,
		UpdatedAt:           now,
	})
	if _, err := tx.Exec(`
		INSERT INTO telegram_thread_promotion_handoffs(
			handoff_id, chat_id, thread_id, display_slot, status, created_by_sender_id,
			source_session_id, source_thread_status, source_preview, context_summary,
			memory_digest_json, resource_review_json, policy_patch_json, review_checklist_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, handoff.HandoffID, handoff.ChatID, handoff.ThreadID, handoff.DisplaySlot, string(handoff.Status), handoff.CreatedBySenderID,
		handoff.SourceSessionID, handoff.SourceThreadStatus, handoff.SourcePreview, handoff.ContextSummary,
		handoff.MemoryDigestJSON, handoff.ResourceReviewJSON, handoff.PolicyPatchJSON, handoff.ReviewChecklistJSON,
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
		memory_digest_json, resource_review_json, policy_patch_json, review_checklist_json,
		created_at, updated_at
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
