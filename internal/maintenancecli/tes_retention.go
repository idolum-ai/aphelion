//go:build linux

package maintenancecli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func tesRetentionConfigSafety(cfg *config.Config) (config.SessionsTESRetentionConfig, time.Duration, string, error) {
	if cfg == nil {
		return config.SessionsTESRetentionConfig{}, 0, "", fmt.Errorf("config is nil")
	}
	policy := cfg.Sessions.TESRetention
	defaultPolicy := config.Default().Sessions.TESRetention
	if strings.TrimSpace(policy.MaxAge) == "" {
		policy.MaxAge = defaultPolicy.MaxAge
	}
	if policy.MinRetainedRows <= 0 {
		policy.MinRetainedRows = defaultPolicy.MinRetainedRows
	}
	if policy.MaxDeletePerGC <= 0 {
		policy.MaxDeletePerGC = defaultPolicy.MaxDeletePerGC
	}
	maxAge, err := time.ParseDuration(strings.TrimSpace(policy.MaxAge))
	if err != nil {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.max_age must be a valid duration: %w", err)
	}
	if maxAge < 24*time.Hour {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.max_age must be >= 24h")
	}
	if policy.MinRetainedRows < 100 {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.min_retained_rows must be >= 100")
	}
	if policy.MaxDeletePerGC <= 0 {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be > 0")
	}
	if policy.MaxDeletePerGC > policy.MinRetainedRows {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be <= min_retained_rows")
	}
	policy.ExportDir = strings.TrimSpace(policy.ExportDir)
	if policy.Enabled && policy.ExportDir == "" {
		return policy, 0, "", fmt.Errorf("sessions.tes_retention.export_dir is required when retention is enabled")
	}
	mode := "disabled"
	if policy.Enabled {
		mode = "enabled"
	}
	exportDir := "-"
	if policy.ExportDir != "" {
		exportDir = policy.ExportDir
	}
	summary := fmt.Sprintf("%s max_age=%s min_retained_rows=%d max_delete_per_gc=%d export_dir=%s",
		mode,
		maxAge.String(),
		policy.MinRetainedRows,
		policy.MaxDeletePerGC,
		exportDir,
	)
	return policy, maxAge, summary, nil
}

func pruneExecutionEventsForRetention(cfg *config.Config, now time.Time) (int, string, error) {
	policy, maxAge, summary, err := tesRetentionConfigSafety(cfg)
	if err != nil {
		return 0, "", err
	}
	if !policy.Enabled {
		return 0, summary, nil
	}
	dbPath := strings.TrimSpace(cfg.Sessions.DBPath)
	if dbPath == "" {
		return 0, "", fmt.Errorf("sessions.db_path is required when sessions.tes_retention.enabled = true")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, summary + " db=missing", nil
		}
		return 0, "", fmt.Errorf("stat sessions db %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return 0, "", fmt.Errorf("open sqlite for tes retention: %w", err)
	}
	defer db.Close()

	cutoffAt := now.UTC().Add(-maxAge)
	cutoff := cutoffAt.Format(time.RFC3339Nano)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, "", fmt.Errorf("begin tes retention prune tx: %w", err)
	}
	defer tx.Rollback()

	candidates, err := selectExecutionEventsForRetentionPrune(tx, policy.MinRetainedRows, cutoff, policy.MaxDeletePerGC)
	if err != nil {
		return 0, "", err
	}

	if len(candidates) == 0 {
		if err := tx.Commit(); err != nil {
			return 0, "", fmt.Errorf("commit tes retention no-op tx: %w", err)
		}
		status := fmt.Sprintf("%s cutoff=%s result=no-op", summary, cutoffAt.Format(time.RFC3339))
		return 0, status, nil
	}

	exportPath, err := writeExecutionEventsRetentionExport(policy.ExportDir, now.UTC(), cutoffAt, candidates)
	if err != nil {
		return 0, "", err
	}

	placeholders := make([]string, 0, len(candidates))
	args := make([]any, 0, len(candidates))
	for _, event := range candidates {
		placeholders = append(placeholders, "?")
		args = append(args, event.ID)
	}
	res, err := tx.Exec(
		`DELETE FROM execution_events WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return 0, "", fmt.Errorf("prune execution events by retention policy: %w", err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, "", fmt.Errorf("inspect pruned execution events rows affected: %w", err)
	}
	if removed != int64(len(candidates)) {
		return 0, "", fmt.Errorf("retention prune removed %d rows; expected %d exported rows", removed, len(candidates))
	}
	if err := tx.Commit(); err != nil {
		return 0, "", fmt.Errorf("commit tes retention prune tx: %w", err)
	}

	status := fmt.Sprintf("%s cutoff=%s export=%s", summary, cutoffAt.Format(time.RFC3339), exportPath)
	return int(removed), status, nil
}

type tesRetentionExportEvent struct {
	ID           int64  `json:"id"`
	SessionID    string `json:"session_id"`
	ChatID       int64  `json:"chat_id"`
	UserID       int64  `json:"user_id"`
	ScopeKind    string `json:"scope_kind"`
	ScopeID      string `json:"scope_id"`
	DurableAgent string `json:"durable_agent_id"`
	Seq          int64  `json:"seq"`
	EventType    string `json:"event_type"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	CausedBySeq  int64  `json:"caused_by_seq"`
	PayloadJSON  string `json:"payload_json"`
	CreatedAt    string `json:"created_at"`
}

type tesRetentionExportBundle struct {
	SchemaVersion string                    `json:"schema_version"`
	GeneratedAt   time.Time                 `json:"generated_at"`
	CutoffAt      time.Time                 `json:"cutoff_at"`
	Count         int                       `json:"count"`
	FirstID       int64                     `json:"first_id"`
	LastID        int64                     `json:"last_id"`
	Events        []tesRetentionExportEvent `json:"events"`
}

func selectExecutionEventsForRetentionPrune(tx *sql.Tx, minRetainedRows int, cutoff string, maxDeletePerGC int) ([]tesRetentionExportEvent, error) {
	rows, err := tx.Query(`
WITH protected AS (
	SELECT id
	FROM execution_events
	ORDER BY created_at DESC, id DESC
	LIMIT ?
),
candidates AS (
	SELECT id
	FROM execution_events
	WHERE created_at < ?
	  AND id NOT IN (SELECT id FROM protected)
	ORDER BY created_at ASC, id ASC
	LIMIT ?
)
SELECT
	id,
	session_id,
	chat_id,
	user_id,
	scope_kind,
	scope_id,
	durable_agent_id,
	seq,
	event_type,
	stage,
	status,
	caused_by_seq,
	payload_json,
	created_at
FROM execution_events
WHERE id IN (SELECT id FROM candidates)
ORDER BY created_at ASC, id ASC
`, minRetainedRows, cutoff, maxDeletePerGC)
	if err != nil {
		return nil, fmt.Errorf("select execution events for retention prune: %w", err)
	}
	defer rows.Close()

	events := make([]tesRetentionExportEvent, 0, maxDeletePerGC)
	for rows.Next() {
		var row tesRetentionExportEvent
		if err := rows.Scan(
			&row.ID,
			&row.SessionID,
			&row.ChatID,
			&row.UserID,
			&row.ScopeKind,
			&row.ScopeID,
			&row.DurableAgent,
			&row.Seq,
			&row.EventType,
			&row.Stage,
			&row.Status,
			&row.CausedBySeq,
			&row.PayloadJSON,
			&row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan retention candidate execution event: %w", err)
		}
		events = append(events, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retention candidate execution events: %w", err)
	}
	return events, nil
}

func writeExecutionEventsRetentionExport(exportRoot string, generatedAt time.Time, cutoffAt time.Time, events []tesRetentionExportEvent) (string, error) {
	exportRoot = strings.TrimSpace(exportRoot)
	if exportRoot == "" {
		return "", fmt.Errorf("sessions.tes_retention.export_dir is required when retention is enabled")
	}
	if len(events) == 0 {
		return "", fmt.Errorf("retention export requires at least one execution event row")
	}
	dir := filepath.Join(exportRoot, generatedAt.UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create tes retention export dir %s: %w", dir, err)
	}

	firstID := events[0].ID
	lastID := events[len(events)-1].ID
	fileName := fmt.Sprintf(
		"execution-events-cutoff-%s-first-%d-last-%d-count-%d-at-%s.json",
		cutoffAt.UTC().Format("20060102T150405Z"),
		firstID,
		lastID,
		len(events),
		generatedAt.UTC().Format("20060102T150405Z"),
	)
	path := filepath.Join(dir, fileName)
	payload := tesRetentionExportBundle{
		SchemaVersion: "tes_retention_export.v1",
		GeneratedAt:   generatedAt.UTC(),
		CutoffAt:      cutoffAt.UTC(),
		Count:         len(events),
		FirstID:       firstID,
		LastID:        lastID,
		Events:        events,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tes retention export payload: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("write tes retention export %s: %w", path, err)
	}
	return path, nil
}
