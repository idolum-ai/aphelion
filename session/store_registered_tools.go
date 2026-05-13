//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertRegisteredTool(record RegisteredTool) (RegisteredTool, error) {
	record = NormalizeRegisteredTool(record)
	if record.ToolName == "" {
		return RegisteredTool{}, fmt.Errorf("registered tool name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO registered_tools(tool_name, implementation_ref, registered, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			implementation_ref = excluded.implementation_ref,
			registered = excluded.registered,
			updated_at = excluded.updated_at
	`,
		record.ToolName,
		record.ImplementationRef,
		boolToInt(record.Registered),
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return RegisteredTool{}, fmt.Errorf("upsert registered tool: %w", err)
	}
	stored, ok, err := s.RegisteredTool(record.ToolName)
	if err != nil {
		return RegisteredTool{}, err
	}
	if !ok {
		return RegisteredTool{}, fmt.Errorf("registered tool %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) RegisteredTool(toolName string) (RegisteredTool, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return RegisteredTool{}, false, nil
	}
	var (
		record       RegisteredTool
		registered   int
		createdAtRaw string
		updatedAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		WHERE tool_name = ?
	`, toolName).Scan(
		&record.ToolName,
		&record.ImplementationRef,
		&registered,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredTool{}, false, nil
	}
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("load registered tool: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool updated_at: %w", err)
	}
	record.Registered = registered != 0
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeRegisteredTool(record), true, nil
}

func (s *SQLiteStore) RegisteredTools(limit int) ([]RegisteredTool, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		ORDER BY updated_at DESC, tool_name ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query registered tools: %w", err)
	}
	defer rows.Close()

	out := make([]RegisteredTool, 0, limit)
	for rows.Next() {
		var (
			record       RegisteredTool
			registered   int
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&record.ToolName,
			&record.ImplementationRef,
			&registered,
			&createdAtRaw,
			&updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan registered tool: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool updated_at: %w", err)
		}
		record.Registered = registered != 0
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		out = append(out, NormalizeRegisteredTool(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registered tools: %w", err)
	}
	return out, nil
}
