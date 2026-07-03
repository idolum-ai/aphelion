//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type IdentificationLedgerQuery struct {
	PlanID      string
	PlanVersion string
	SessionID   string
	Status      IdentificationLedgerEntryStatus
	Limit       int
}

func (s *SQLiteStore) RecordIdentificationLedgerEntry(input IdentificationLedgerEntryInput) (IdentificationLedgerEntry, error) {
	if s == nil || s.db == nil {
		return IdentificationLedgerEntry{}, fmt.Errorf("identification ledger store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return IdentificationLedgerEntry{}, fmt.Errorf("begin identification ledger tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := recordIdentificationLedgerEntryTx(tx, input)
	if err != nil {
		return IdentificationLedgerEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentificationLedgerEntry{}, fmt.Errorf("commit identification ledger tx: %w", err)
	}
	return entry, nil
}

func (s *SQLiteStore) RecordIdentificationLedgerObservation(entryInput IdentificationLedgerEntryInput, observationInput IdentificationLedgerObservationInput) (IdentificationLedgerEntry, IdentificationLedgerObservation, error) {
	if s == nil || s.db == nil {
		return IdentificationLedgerEntry{}, IdentificationLedgerObservation{}, fmt.Errorf("identification ledger store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return IdentificationLedgerEntry{}, IdentificationLedgerObservation{}, fmt.Errorf("begin identification ledger tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := recordIdentificationLedgerEntryTx(tx, entryInput)
	if err != nil {
		return IdentificationLedgerEntry{}, IdentificationLedgerObservation{}, err
	}
	observationInput.EntryID = entry.EntryID
	observation, err := recordIdentificationLedgerObservationTx(tx, observationInput)
	if err != nil {
		return IdentificationLedgerEntry{}, IdentificationLedgerObservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return IdentificationLedgerEntry{}, IdentificationLedgerObservation{}, fmt.Errorf("commit identification ledger tx: %w", err)
	}
	return entry, observation, nil
}

func (s *SQLiteStore) RecordIdentificationLedgerObservations(entryInput IdentificationLedgerEntryInput, observationInputs []IdentificationLedgerObservationInput) (IdentificationLedgerEntry, []IdentificationLedgerObservation, error) {
	if s == nil || s.db == nil {
		return IdentificationLedgerEntry{}, nil, fmt.Errorf("identification ledger store unavailable")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return IdentificationLedgerEntry{}, nil, fmt.Errorf("begin identification ledger tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	entry, err := recordIdentificationLedgerEntryTx(tx, entryInput)
	if err != nil {
		return IdentificationLedgerEntry{}, nil, err
	}
	observations := make([]IdentificationLedgerObservation, 0, len(observationInputs))
	for _, observationInput := range observationInputs {
		observationInput.EntryID = entry.EntryID
		observation, err := recordIdentificationLedgerObservationTx(tx, observationInput)
		if err != nil {
			return IdentificationLedgerEntry{}, nil, err
		}
		observations = append(observations, observation)
	}
	if err := tx.Commit(); err != nil {
		return IdentificationLedgerEntry{}, nil, fmt.Errorf("commit identification ledger tx: %w", err)
	}
	return entry, observations, nil
}

func (s *SQLiteStore) IdentificationLedgerEntries(query IdentificationLedgerQuery) ([]IdentificationLedgerProjection, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("identification ledger store unavailable")
	}
	query.PlanID = strings.TrimSpace(query.PlanID)
	query.PlanVersion = strings.TrimSpace(query.PlanVersion)
	query.SessionID = strings.TrimSpace(query.SessionID)
	query.Status = NormalizeIdentificationLedgerEntryStatus(query.Status)
	if query.PlanVersion == "" {
		query.PlanVersion = IdentificationDefaultPlanVersion
	}
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := []string{"1 = 1"}
	args := []any{}
	if query.PlanID != "" {
		where = append(where, "plan_id = ?")
		args = append(args, query.PlanID)
	}
	if query.PlanVersion != "" {
		where = append(where, "plan_version = ?")
		args = append(args, query.PlanVersion)
	}
	if query.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, query.SessionID)
	}
	if query.Status != "" && query.Status != IdentificationLedgerStatusUnidentified {
		where = append(where, "status = ?")
		args = append(args, string(query.Status))
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT entry_id, plan_id, plan_version, session_id, step_ref, shape_hash,
			label_ref, status, expires_at, created_at, updated_at
		FROM identification_ledger_entries
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY updated_at DESC, entry_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query identification ledger entries: %w", err)
	}
	defer rows.Close()
	entries := []IdentificationLedgerEntry{}
	for rows.Next() {
		entry, _, err := scanIdentificationLedgerEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate identification ledger entries: %w", err)
	}
	projections := make([]IdentificationLedgerProjection, 0, len(entries))
	for _, entry := range entries {
		observations, err := s.identificationLedgerObservations(entry.EntryID)
		if err != nil {
			return nil, err
		}
		properties := map[IdentificationObservationProperty][]IdentificationLedgerObservation{}
		for _, observation := range observations {
			properties[observation.Property] = append(properties[observation.Property], observation)
		}
		projections = append(projections, IdentificationLedgerProjection{
			Entry:        entry,
			Observations: observations,
			Properties:   properties,
		})
	}
	return projections, nil
}

func recordIdentificationLedgerEntryTx(tx *sql.Tx, input IdentificationLedgerEntryInput) (IdentificationLedgerEntry, error) {
	statusExplicit := identificationLedgerEntryStatusExplicit(input)
	if err := ValidateIdentificationLedgerEntryEnums(input); err != nil {
		return IdentificationLedgerEntry{}, err
	}
	input = NormalizeIdentificationLedgerEntryInput(input)
	if err := ValidateIdentificationLedgerEntryInput(input); err != nil {
		return IdentificationLedgerEntry{}, err
	}
	now := time.Now().UTC()
	if input.CreatedAt.IsZero() {
		input.CreatedAt = now
	}
	if input.UpdatedAt.IsZero() {
		input.UpdatedAt = input.CreatedAt
	}
	if existing, ok, err := identificationLedgerEntryByIDTx(tx, input.EntryID); err != nil {
		return IdentificationLedgerEntry{}, err
	} else if ok {
		if existing.PlanID != input.PlanID || existing.PlanVersion != input.PlanVersion || existing.SessionID != input.SessionID || existing.StepRef != input.StepRef || existing.ShapeHash != input.ShapeHash {
			return IdentificationLedgerEntry{}, fmt.Errorf("identification ledger entry %s idempotency conflict", input.EntryID)
		}
		updatedAt := input.UpdatedAt
		if updatedAt.Before(existing.UpdatedAt) {
			updatedAt = existing.UpdatedAt
		}
		labelRef := firstNonEmptyStore(input.LabelRef, existing.LabelRef)
		status := existing.Status
		if statusExplicit {
			status = input.Status
			if !identificationLedgerEntryStatusTransitionAllowed(existing.Status, status) {
				return IdentificationLedgerEntry{}, fmt.Errorf("identification ledger entry %s invalid status transition %s -> %s", input.EntryID, existing.Status, status)
			}
		}
		expiresAt, err := identificationLedgerEntryMergedExpiry(existing, input, statusExplicit)
		if err != nil {
			return IdentificationLedgerEntry{}, err
		}
		if _, err := tx.Exec(`
			UPDATE identification_ledger_entries
			SET label_ref = ?, status = ?, expires_at = ?, updated_at = ?
			WHERE entry_id = ?
		`, labelRef, string(status), nullableTime(expiresAt), updatedAt.Format(time.RFC3339Nano), input.EntryID); err != nil {
			return IdentificationLedgerEntry{}, fmt.Errorf("update identification ledger entry %s: %w", input.EntryID, err)
		}
		existing.LabelRef = labelRef
		existing.Status = status
		existing.ExpiresAt = expiresAt
		existing.UpdatedAt = updatedAt
		return existing, nil
	}
	if _, err := tx.Exec(`
		INSERT INTO identification_ledger_entries(
			entry_id, plan_id, plan_version, session_id, step_ref, shape_hash,
			label_ref, status, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.EntryID, input.PlanID, input.PlanVersion, input.SessionID, input.StepRef, input.ShapeHash,
		input.LabelRef, string(input.Status), nullableTime(input.ExpiresAt),
		input.CreatedAt.Format(time.RFC3339Nano), input.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		return IdentificationLedgerEntry{}, fmt.Errorf("insert identification ledger entry %s: %w", input.EntryID, err)
	}
	return IdentificationLedgerEntry{
		EntryID:     input.EntryID,
		PlanID:      input.PlanID,
		PlanVersion: input.PlanVersion,
		SessionID:   input.SessionID,
		StepRef:     input.StepRef,
		ShapeHash:   input.ShapeHash,
		LabelRef:    input.LabelRef,
		Status:      input.Status,
		ExpiresAt:   input.ExpiresAt,
		CreatedAt:   input.CreatedAt,
		UpdatedAt:   input.UpdatedAt,
	}, nil
}

func recordIdentificationLedgerObservationTx(tx *sql.Tx, input IdentificationLedgerObservationInput) (IdentificationLedgerObservation, error) {
	if err := ValidateIdentificationLedgerObservationEnums(input); err != nil {
		return IdentificationLedgerObservation{}, err
	}
	input = NormalizeIdentificationLedgerObservationInput(input)
	if err := ValidateIdentificationLedgerObservationInput(input); err != nil {
		return IdentificationLedgerObservation{}, err
	}
	if _, ok, err := identificationLedgerEntryByIDTx(tx, input.EntryID); err != nil {
		return IdentificationLedgerObservation{}, err
	} else if !ok {
		return IdentificationLedgerObservation{}, fmt.Errorf("identification ledger entry %s not found", input.EntryID)
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = time.Now().UTC()
	}
	if input.LastObservedAt.IsZero() {
		input.LastObservedAt = input.ObservedAt
	}
	existing, ok, err := identificationLedgerObservationByIDTx(tx, input.ObservationID)
	if err != nil {
		return IdentificationLedgerObservation{}, err
	}
	if ok {
		expiresAt := identificationObservationMergedExpiry(existing.ExpiresAt, input.ExpiresAt)
		lastObservedAt := input.LastObservedAt
		if lastObservedAt.Before(existing.LastObservedAt) {
			lastObservedAt = existing.LastObservedAt
		}
		count := existing.OccurrenceCount + input.OccurrenceCount
		if _, err := tx.Exec(`
			UPDATE identification_ledger_observations
			SET expires_at = ?, last_observed_at = ?, occurrence_count = ?
			WHERE observation_id = ?
		`, nullableTime(expiresAt), lastObservedAt.Format(time.RFC3339Nano), count, input.ObservationID); err != nil {
			return IdentificationLedgerObservation{}, fmt.Errorf("update identification ledger observation %s: %w", input.ObservationID, err)
		}
		existing.ExpiresAt = expiresAt
		existing.LastObservedAt = lastObservedAt
		existing.OccurrenceCount = count
		return existing, nil
	}
	if _, err := tx.Exec(`
		INSERT INTO identification_ledger_observations(
			observation_id, entry_id, method, property, value, evidence_ref,
			expires_at, observed_at, last_observed_at, occurrence_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ObservationID, input.EntryID, string(input.Method), string(input.Property), input.Value, input.EvidenceRef,
		nullableTime(input.ExpiresAt), input.ObservedAt.Format(time.RFC3339Nano), input.LastObservedAt.Format(time.RFC3339Nano), input.OccurrenceCount); err != nil {
		return IdentificationLedgerObservation{}, fmt.Errorf("insert identification ledger observation %s: %w", input.ObservationID, err)
	}
	return IdentificationLedgerObservation{
		ObservationID:   input.ObservationID,
		EntryID:         input.EntryID,
		Method:          input.Method,
		Property:        input.Property,
		Value:           input.Value,
		EvidenceRef:     input.EvidenceRef,
		ExpiresAt:       input.ExpiresAt,
		ObservedAt:      input.ObservedAt,
		LastObservedAt:  input.LastObservedAt,
		OccurrenceCount: input.OccurrenceCount,
	}, nil
}

func identificationObservationMergedExpiry(existing time.Time, requested time.Time) time.Time {
	if requested.IsZero() {
		return existing
	}
	requested = requested.UTC()
	if existing.IsZero() || requested.Before(existing) {
		return requested
	}
	return existing
}

func identificationLedgerEntryStatusExplicit(input IdentificationLedgerEntryInput) bool {
	return strings.TrimSpace(string(input.Status)) != ""
}

func identificationLedgerEntryMergedExpiry(existing IdentificationLedgerEntry, input IdentificationLedgerEntryInput, statusExplicit bool) (time.Time, error) {
	if input.ExpiresAt.IsZero() {
		return existing.ExpiresAt, nil
	}
	requested := input.ExpiresAt.UTC()
	if existing.ExpiresAt.IsZero() {
		return requested, nil
	}
	if requested.After(existing.ExpiresAt) {
		if identificationLedgerEntryTerminalStatus(existing.Status) {
			return time.Time{}, fmt.Errorf("identification ledger entry %s cannot extend expiry after terminal status %s", existing.EntryID, existing.Status)
		}
		if !statusExplicit {
			return time.Time{}, fmt.Errorf("identification ledger entry %s expiry extension requires explicit status", existing.EntryID)
		}
	}
	return requested, nil
}

func identificationLedgerEntryStatusTransitionAllowed(from IdentificationLedgerEntryStatus, to IdentificationLedgerEntryStatus) bool {
	from = NormalizeIdentificationLedgerEntryStatus(from)
	to = NormalizeIdentificationLedgerEntryStatus(to)
	if from == to {
		return true
	}
	if to == "" || to == IdentificationLedgerStatusUnidentified {
		return false
	}
	switch from {
	case "", IdentificationLedgerStatusUnidentified:
		return true
	case IdentificationLedgerStatusPartial:
		return true
	case IdentificationLedgerStatusProposed:
		return to == IdentificationLedgerStatusApproved || identificationLedgerEntryTerminalStatus(to)
	case IdentificationLedgerStatusApproved:
		return identificationLedgerEntryTerminalStatus(to)
	case IdentificationLedgerStatusConsumed, IdentificationLedgerStatusExpired, IdentificationLedgerStatusInvalidated:
		return false
	default:
		return false
	}
}

func identificationLedgerEntryTerminalStatus(status IdentificationLedgerEntryStatus) bool {
	switch NormalizeIdentificationLedgerEntryStatus(status) {
	case IdentificationLedgerStatusConsumed, IdentificationLedgerStatusExpired, IdentificationLedgerStatusInvalidated:
		return true
	default:
		return false
	}
}

func identificationLedgerEntryByIDTx(tx *sql.Tx, entryID string) (IdentificationLedgerEntry, bool, error) {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return IdentificationLedgerEntry{}, false, nil
	}
	row := tx.QueryRow(`
		SELECT entry_id, plan_id, plan_version, session_id, step_ref, shape_hash,
			label_ref, status, expires_at, created_at, updated_at
		FROM identification_ledger_entries
		WHERE entry_id = ?
	`, entryID)
	return scanIdentificationLedgerEntry(row)
}

func identificationLedgerObservationByIDTx(tx *sql.Tx, observationID string) (IdentificationLedgerObservation, bool, error) {
	observationID = strings.TrimSpace(observationID)
	if observationID == "" {
		return IdentificationLedgerObservation{}, false, nil
	}
	row := tx.QueryRow(`
		SELECT observation_id, entry_id, method, property, value, evidence_ref,
			expires_at, observed_at, last_observed_at, occurrence_count
		FROM identification_ledger_observations
		WHERE observation_id = ?
	`, observationID)
	return scanIdentificationLedgerObservation(row)
}

func (s *SQLiteStore) identificationLedgerObservations(entryID string) ([]IdentificationLedgerObservation, error) {
	rows, err := s.db.Query(`
		SELECT observation_id, entry_id, method, property, value, evidence_ref,
			expires_at, observed_at, last_observed_at, occurrence_count
		FROM identification_ledger_observations
		WHERE entry_id = ?
		ORDER BY observed_at ASC, observation_id ASC
	`, strings.TrimSpace(entryID))
	if err != nil {
		return nil, fmt.Errorf("query identification ledger observations: %w", err)
	}
	defer rows.Close()
	var observations []IdentificationLedgerObservation
	for rows.Next() {
		observation, err := scanIdentificationLedgerObservationRows(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate identification ledger observations: %w", err)
	}
	return observations, nil
}

func scanIdentificationLedgerEntry(scanner interface{ Scan(dest ...any) error }) (IdentificationLedgerEntry, bool, error) {
	var (
		entry        IdentificationLedgerEntry
		statusRaw    string
		expiresAtRaw sql.NullString
		createdAtRaw string
		updatedAtRaw string
	)
	if err := scanner.Scan(
		&entry.EntryID, &entry.PlanID, &entry.PlanVersion, &entry.SessionID, &entry.StepRef, &entry.ShapeHash,
		&entry.LabelRef, &statusRaw, &expiresAtRaw, &createdAtRaw, &updatedAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return IdentificationLedgerEntry{}, false, nil
		}
		return IdentificationLedgerEntry{}, false, fmt.Errorf("scan identification ledger entry: %w", err)
	}
	entry.Status = NormalizeIdentificationLedgerEntryStatus(IdentificationLedgerEntryStatus(statusRaw))
	if expiresAtRaw.Valid && strings.TrimSpace(expiresAtRaw.String) != "" {
		expiresAt, err := parseSQLiteTime(expiresAtRaw.String)
		if err != nil {
			return IdentificationLedgerEntry{}, false, fmt.Errorf("parse identification ledger expires_at: %w", err)
		}
		entry.ExpiresAt = expiresAt
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return IdentificationLedgerEntry{}, false, fmt.Errorf("parse identification ledger created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return IdentificationLedgerEntry{}, false, fmt.Errorf("parse identification ledger updated_at: %w", err)
	}
	entry.CreatedAt = createdAt
	entry.UpdatedAt = updatedAt
	entry = NormalizeIdentificationLedgerEntry(entry)
	return entry, true, nil
}

func scanIdentificationLedgerObservationRows(scanner interface{ Scan(dest ...any) error }) (IdentificationLedgerObservation, error) {
	observation, _, err := scanIdentificationLedgerObservation(scanner)
	return observation, err
}

func scanIdentificationLedgerObservation(scanner interface{ Scan(dest ...any) error }) (IdentificationLedgerObservation, bool, error) {
	var (
		observation     IdentificationLedgerObservation
		methodRaw       string
		propertyRaw     string
		expiresAtRaw    sql.NullString
		observedRaw     string
		lastObservedRaw string
		occurrenceCount int
	)
	if err := scanner.Scan(
		&observation.ObservationID, &observation.EntryID, &methodRaw, &propertyRaw, &observation.Value, &observation.EvidenceRef,
		&expiresAtRaw, &observedRaw, &lastObservedRaw, &occurrenceCount,
	); err != nil {
		if err == sql.ErrNoRows {
			return IdentificationLedgerObservation{}, false, nil
		}
		return IdentificationLedgerObservation{}, false, fmt.Errorf("scan identification ledger observation: %w", err)
	}
	observation.Method = NormalizeIdentificationObservationMethod(IdentificationObservationMethod(methodRaw))
	observation.Property = NormalizeIdentificationObservationProperty(IdentificationObservationProperty(propertyRaw))
	if expiresAtRaw.Valid && strings.TrimSpace(expiresAtRaw.String) != "" {
		expiresAt, err := parseSQLiteTime(expiresAtRaw.String)
		if err != nil {
			return IdentificationLedgerObservation{}, false, fmt.Errorf("parse identification ledger observation expires_at: %w", err)
		}
		observation.ExpiresAt = expiresAt
	}
	observedAt, err := parseSQLiteTime(observedRaw)
	if err != nil {
		return IdentificationLedgerObservation{}, false, fmt.Errorf("parse identification ledger observed_at: %w", err)
	}
	lastObservedAt, err := parseSQLiteTime(lastObservedRaw)
	if err != nil {
		return IdentificationLedgerObservation{}, false, fmt.Errorf("parse identification ledger last_observed_at: %w", err)
	}
	observation.ObservedAt = observedAt
	observation.LastObservedAt = lastObservedAt
	observation.OccurrenceCount = occurrenceCount
	observation = NormalizeIdentificationLedgerObservation(observation)
	return observation, true, nil
}
