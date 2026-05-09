//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) SearchArtifacts(query string, limit int, scope *SessionKey) ([]ArtifactRecord, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("artifact search query is required")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	pattern := "%" + query + "%"
	base := `
		SELECT artifact_id, session_id, chat_id, user_id, turn_index, source_type, kind, summary,
			handling, retention, fetch_state, materialized_path, created_at, updated_at
		FROM artifact_index
		WHERE (
			LOWER(summary) LIKE LOWER(?)
			OR LOWER(kind) LIKE LOWER(?)
			OR LOWER(source_type) LIKE LOWER(?)
			OR LOWER(materialized_path) LIKE LOWER(?)
		)
	`
	args := []any{pattern, pattern, pattern, pattern}
	if scope != nil {
		base += ` AND session_id = ?`
		args = append(args, SessionIDForKey(*scope))
	}
	base += ` ORDER BY updated_at DESC, turn_index DESC, artifact_id DESC, session_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("search artifacts: %w", err)
	}
	defer rows.Close()

	hits := make([]ArtifactRecord, 0, limit)
	for rows.Next() {
		var (
			hit          ArtifactRecord
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&hit.ArtifactID, &hit.SessionID, &hit.ChatID, &hit.UserID, &hit.TurnIndex,
			&hit.SourceType, &hit.Kind, &hit.Summary, &hit.Handling, &hit.Retention,
			&hit.FetchState, &hit.MaterializedPath, &createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan artifact search hit: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse artifact search created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse artifact search updated_at: %w", err)
		}
		hit.CreatedAt = createdAt
		hit.UpdatedAt = updatedAt
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact search hits: %w", err)
	}
	return hits, nil
}

func (s *SQLiteStore) RecordRhizomeEvent(scope string, source string, salience float64, concepts []string) error {
	scope = strings.TrimSpace(scope)
	source = strings.TrimSpace(source)
	normalized := normalizeRhizomeConcepts(concepts)
	if scope == "" {
		return fmt.Errorf("rhizome scope is required")
	}
	if source == "" {
		source = "reflection"
	}
	if len(normalized) < 2 {
		return nil
	}
	if salience <= 0 {
		salience = 1
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin rhizome event tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, concept := range normalized {
		if _, err := tx.Exec(`
			INSERT INTO rhizome_nodes(scope, name, event_count, last_seen_at)
			VALUES (?, ?, 1, ?)
			ON CONFLICT(scope, name) DO UPDATE SET
				event_count = event_count + 1,
				last_seen_at = excluded.last_seen_at
		`, scope, concept, now); err != nil {
			return fmt.Errorf("upsert rhizome node %q: %w", concept, err)
		}
	}

	conceptsJSON, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal rhizome concepts: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO rhizome_events(scope, source, salience, concepts_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, scope, source, salience, string(conceptsJSON), now); err != nil {
		return fmt.Errorf("insert rhizome event: %w", err)
	}

	for i := 0; i < len(normalized); i++ {
		for j := i + 1; j < len(normalized); j++ {
			left, right := orderedPair(normalized[i], normalized[j])
			if _, err := tx.Exec(`
				INSERT INTO rhizome_edges(
					scope, left_concept, right_concept, strength, recurrence_count, last_reinforced_at, decay_state, last_source
				) VALUES (?, ?, ?, ?, 1, ?, ?, ?)
				ON CONFLICT(scope, left_concept, right_concept) DO UPDATE SET
					strength = rhizome_edges.strength + excluded.strength,
					recurrence_count = rhizome_edges.recurrence_count + 1,
					last_reinforced_at = excluded.last_reinforced_at,
					decay_state = CASE
						WHEN rhizome_edges.recurrence_count + 1 >= 8 THEN 'frozen'
						WHEN rhizome_edges.recurrence_count + 1 >= 5 THEN 'cold'
						WHEN rhizome_edges.recurrence_count + 1 >= 3 THEN 'warm'
						ELSE 'hot'
					END,
					last_source = excluded.last_source
			`, scope, left, right, salience, now, classifyRhizomeDecayState(1), source); err != nil {
				return fmt.Errorf("upsert rhizome edge %q/%q: %w", left, right, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rhizome event tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TopRhizomeEdges(scope string, limit int) ([]RhizomeEdge, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("rhizome scope is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, scope, left_concept, right_concept, strength, recurrence_count, last_reinforced_at, decay_state, COALESCE(last_source, '')
		FROM rhizome_edges
		WHERE scope = ?
		ORDER BY strength DESC, recurrence_count DESC, last_reinforced_at DESC
		LIMIT ?
	`, scope, limit)
	if err != nil {
		return nil, fmt.Errorf("query rhizome edges: %w", err)
	}
	defer rows.Close()

	edges := make([]RhizomeEdge, 0, limit)
	for rows.Next() {
		var edge RhizomeEdge
		var ts string
		if err := rows.Scan(&edge.ID, &edge.Scope, &edge.LeftConcept, &edge.RightConcept, &edge.Strength, &edge.RecurrenceCount, &ts, &edge.DecayState, &edge.LastSource); err != nil {
			return nil, fmt.Errorf("scan rhizome edge: %w", err)
		}
		tm, err := parseSQLiteTime(ts)
		if err != nil {
			return nil, fmt.Errorf("parse rhizome edge ts: %w", err)
		}
		edge.LastReinforcedAt = tm
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rhizome edges: %w", err)
	}
	return edges, nil
}

func (s *SQLiteStore) ResetRhizome(scope string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return fmt.Errorf("rhizome scope is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset rhizome tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM rhizome_edges WHERE scope = ?`, []any{scope}},
		{`DELETE FROM rhizome_events WHERE scope = ?`, []any{scope}},
		{`DELETE FROM rhizome_nodes WHERE scope = ?`, []any{scope}},
	} {
		if _, err := tx.Exec(stmt.query, stmt.args...); err != nil {
			return fmt.Errorf("reset rhizome scope %s: %w", scope, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset rhizome tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ResetAllRhizome() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset all rhizome tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM rhizome_edges`,
		`DELETE FROM rhizome_events`,
		`DELETE FROM rhizome_nodes`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("reset all rhizome with %q: %w", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset all rhizome tx: %w", err)
	}
	return nil
}

func upsertArtifactIndexRecords(tx *sql.Tx, session *Session) error {
	if tx == nil || session == nil {
		return nil
	}
	return upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(session.SessionID), session.ChatID, session.UserID, session.TurnCount, session.LastFloorMetadata)
}

func upsertArtifactIndexFloorMetadata(tx *sql.Tx, sessionID string, chatID int64, userID int64, turnIndex int, metadata string) error {
	if tx == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	metadata = strings.TrimSpace(metadata)
	if sessionID == "" || metadata == "" {
		return nil
	}
	var floor core.FloorMetadata
	if err := json.Unmarshal([]byte(metadata), &floor); err != nil {
		return nil
	}
	for _, ref := range floor.Artifacts {
		retention := strings.TrimSpace(ref.Retention)
		if retention != "session_reference" && retention != "child_local" {
			continue
		}
		artifactID := strings.TrimSpace(ref.ArtifactID)
		if artifactID == "" {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`
			INSERT INTO artifact_index(
				session_id, turn_index, artifact_id, chat_id, user_id, source_type, kind, summary,
				handling, retention, fetch_state, materialized_path, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, turn_index, artifact_id) DO UPDATE SET
				chat_id = excluded.chat_id,
				user_id = excluded.user_id,
				source_type = excluded.source_type,
				kind = excluded.kind,
				summary = excluded.summary,
				handling = excluded.handling,
				retention = excluded.retention,
				fetch_state = excluded.fetch_state,
				materialized_path = excluded.materialized_path,
				updated_at = excluded.updated_at
		`, sessionID, turnIndex, artifactID, chatID, userID,
			strings.TrimSpace(ref.SourceType), strings.TrimSpace(ref.Kind), strings.TrimSpace(ref.Summary),
			strings.TrimSpace(ref.Handling), retention, strings.TrimSpace(ref.FetchState), strings.TrimSpace(ref.MaterializedPath), now, now); err != nil {
			return fmt.Errorf("upsert artifact index record %s/%d/%s: %w", sessionID, turnIndex, artifactID, err)
		}
	}
	return nil
}

func normalizeRhizomeConcepts(concepts []string) []string {
	seen := make(map[string]struct{}, len(concepts))
	out := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		normalized := strings.ToLower(strings.TrimSpace(concept))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func orderedPair(a string, b string) (string, string) {
	if a <= b {
		return a, b
	}
	return b, a
}

func classifyRhizomeDecayState(recurrence int) string {
	switch {
	case recurrence >= 8:
		return "frozen"
	case recurrence >= 5:
		return "cold"
	case recurrence >= 3:
		return "warm"
	default:
		return "hot"
	}
}
