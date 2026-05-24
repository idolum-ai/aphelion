//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
)

func schemaTableExists(tx *sql.Tx, tableName string) (bool, error) {
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return false, fmt.Errorf("schema table lookup requires table")
	}
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, tableName).Scan(&count); err != nil {
		return false, fmt.Errorf("query schema table %s: %w", tableName, err)
	}
	return count > 0, nil
}

type schemaColumnMigration struct {
	table     string
	column    string
	statement string
}

func addSchemaColumnIfMissing(tx *sql.Tx, migration schemaColumnMigration) error {
	exists, err := schemaColumnExists(tx, migration.table, migration.column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(migration.statement); err != nil {
		return fmt.Errorf("migrate schema v43 to v44 add %s.%s: %w", migration.table, migration.column, err)
	}
	return nil
}

func schemaColumnExists(tx *sql.Tx, tableName string, columnName string) (bool, error) {
	tableName = strings.TrimSpace(tableName)
	columnName = strings.TrimSpace(columnName)
	if tableName == "" || columnName == "" {
		return false, fmt.Errorf("schema column lookup requires table and column")
	}
	var count int
	query := fmt.Sprintf("SELECT COUNT(1) FROM pragma_table_info(%s) WHERE name = ?", sqliteStringLiteral(tableName))
	if err := tx.QueryRow(query, columnName).Scan(&count); err != nil {
		return false, fmt.Errorf("query schema column %s.%s: %w", tableName, columnName, err)
	}
	return count > 0, nil
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func recordCurrentSchemaVersion(tx *sql.Tx, currentVersion int) error {
	if currentVersion == schemaVersion {
		return nil
	}
	if currentVersion != 0 {
		return fmt.Errorf("unsupported database schema version %d (current schema version is %d); reinstall from a clean current state", currentVersion, schemaVersion)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
		return fmt.Errorf("insert schema version %d: %w", schemaVersion, err)
	}
	return nil
}

func currentSchemaVersion(tx *sql.Tx) (int, error) {
	var maxVersion sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&maxVersion); err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	if !maxVersion.Valid {
		return 0, nil
	}
	return int(maxVersion.Int64), nil
}
