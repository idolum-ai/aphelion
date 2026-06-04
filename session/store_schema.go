//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

const schemaVersion43 = 43
const schemaVersion44 = 44
const schemaVersion45 = 45
const schemaVersion46 = 46
const schemaVersion47 = 47
const schemaVersion48 = 48
const schemaVersion49 = 49
const schemaVersion50 = 50
const schemaVersion51 = 51
const schemaVersion52 = 52
const schemaVersion53 = 53
const schemaVersion54 = 54
const schemaVersion55 = 55
const schemaVersion56 = 56
const schemaVersion57 = 57
const schemaVersion58 = 58
const schemaVersion59 = 59
const schemaVersion60 = 60
const schemaVersion61 = 61
const schemaVersion62 = 62

var migratableSchemaVersions = map[int]struct{}{
	schemaVersion43: {},
	schemaVersion44: {},
	schemaVersion45: {},
	schemaVersion46: {},
	schemaVersion47: {},
	schemaVersion48: {},
	schemaVersion49: {},
	schemaVersion50: {},
	schemaVersion51: {},
	schemaVersion52: {},
	schemaVersion53: {},
	schemaVersion54: {},
	schemaVersion55: {},
	schemaVersion56: {},
	schemaVersion57: {},
	schemaVersion58: {},
	schemaVersion59: {},
	schemaVersion60: {},
	schemaVersion61: {},
	schemaVersion62: {},
}

func existingUserTableCount(tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
			AND name NOT LIKE 'sqlite_%'
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("query existing sqlite tables: %w", err)
	}
	return count, nil
}

func validateCurrentSchemaVersion(tx *sql.Tx, existingTables int) (int, error) {
	currentVersion, err := currentSchemaVersion(tx)
	if err != nil {
		return 0, err
	}
	if currentVersion == 0 {
		if existingTables == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("unsupported unversioned database schema; reinstall from a clean current state")
	}
	if currentVersion < schemaVersion {
		if _, ok := migratableSchemaVersions[currentVersion]; ok {
			return currentVersion, nil
		}
		return 0, fmt.Errorf("unsupported database schema version %d (current schema version is %d); reinstall from a clean current state", currentVersion, schemaVersion)
	}
	if currentVersion > schemaVersion {
		return 0, fmt.Errorf("unsupported database schema version %d (binary schema version is %d); install a matching or newer binary", currentVersion, schemaVersion)
	}
	return currentVersion, nil
}

func migrateCurrentSchemaVersion(tx *sql.Tx, currentVersion int) (int, error) {
	version := currentVersion
	if version == schemaVersion43 {
		if err := migrateSchemaV43ToV44(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion44); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion44, err)
		}
		version = schemaVersion44
	}
	if version == schemaVersion44 {
		if err := migrateSchemaV44ToV45(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion45); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion45, err)
		}
		version = schemaVersion45
	}
	if version == schemaVersion45 {
		if err := migrateSchemaV45ToV46(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion46); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion46, err)
		}
		version = schemaVersion46
	}
	if version == schemaVersion46 {
		if err := migrateSchemaV46ToV47(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion47); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion47, err)
		}
		version = schemaVersion47
	}
	if version == schemaVersion47 {
		if err := migrateSchemaV47ToV48(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion48); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion48, err)
		}
		version = schemaVersion48
	}
	if version == schemaVersion48 {
		if err := migrateSchemaV48ToV49(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion49); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion49, err)
		}
		version = schemaVersion49
	}
	if version == schemaVersion49 {
		if err := migrateSchemaV49ToV50(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion50); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion50, err)
		}
		version = schemaVersion50
	}
	if version == schemaVersion50 {
		if err := migrateSchemaV50ToV51(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion51); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion51, err)
		}
		version = schemaVersion51
	}
	if version == schemaVersion51 {
		if err := migrateSchemaV51ToV52(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion52); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion52, err)
		}
		version = schemaVersion52
	}
	if version == schemaVersion52 {
		if err := migrateSchemaV52ToV53(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion53); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion53, err)
		}
		version = schemaVersion53
	}
	if version == schemaVersion53 {
		if err := migrateSchemaV53ToV54(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion54); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion54, err)
		}
		version = schemaVersion54
	}
	if version == schemaVersion54 {
		if err := migrateSchemaV54ToV55(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion55); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion55, err)
		}
		version = schemaVersion55
	}
	if version == schemaVersion55 {
		if err := migrateSchemaV55ToV56(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion56); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion56, err)
		}
		version = schemaVersion56
	}
	if version == schemaVersion56 {
		if err := migrateSchemaV56ToV57(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion57); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion57, err)
		}
		version = schemaVersion57
	}
	if version == schemaVersion57 {
		if err := migrateSchemaV57ToV58(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion58); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion58, err)
		}
		version = schemaVersion58
	}
	if version == schemaVersion58 {
		if err := migrateSchemaV58ToV59(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion59); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion59, err)
		}
		version = schemaVersion59
	}
	if version == schemaVersion59 {
		if err := migrateSchemaV59ToV60(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion60); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion60, err)
		}
		version = schemaVersion60
	}
	if version == schemaVersion60 {
		if err := migrateSchemaV60ToV61(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion61); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion61, err)
		}
		version = schemaVersion61
	}
	if version == schemaVersion61 {
		if err := migrateSchemaV61ToV62(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion62); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion62, err)
		}
		version = schemaVersion62
	}
	if version == schemaVersion62 {
		if err := migrateSchemaV62ToV63(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion, err)
		}
		version = schemaVersion
	}
	return version, nil
}
