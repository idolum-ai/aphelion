//go:build linux

package maintenancecli

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

func RunSchemaMaintenanceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: schema verify --db <sessions.db>")
	}
	switch args[0] {
	case "verify":
		return runSchemaVerifyCommand(args[1:])
	default:
		return fmt.Errorf("unknown schema command %q", args[0])
	}
}

func runSchemaVerifyCommand(args []string) error {
	fs := flag.NewFlagSet("schema verify", flag.ContinueOnError)
	dbPath := fs.String("db", "", "path to copied sessions.db")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("schema verify requires --db <sessions.db>")
	}
	db, err := sql.Open("sqlite3", "file:"+*dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	version := scalarInt(db, `SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	openThreads := scalarInt(db, `SELECT COUNT(*) FROM telegram_threads WHERE status = 'open'`)
	closedThreads := scalarInt(db, `SELECT COUNT(*) FROM telegram_threads WHERE status != 'open'`)
	blankAutoScopes := scalarInt(db, `SELECT COUNT(*) FROM operator_auto_approvals WHERE chat_id != 0 AND (TRIM(scope_kind) = '' OR TRIM(scope_id) = '')`) + scalarInt(db, `SELECT COUNT(*) FROM operator_autonomy_overrides WHERE chat_id != 0 AND (TRIM(scope_kind) = '' OR TRIM(scope_id) = '')`)
	missingThreadSessions := scalarInt(db, `SELECT COUNT(*) FROM telegram_threads t WHERE NOT EXISTS (SELECT 1 FROM sessions s WHERE s.session_id = 'telegram_thread:' || t.chat_id || ':' || t.thread_id)`)
	badDisplaySlots := scalarInt(db, `SELECT COUNT(*) FROM telegram_threads WHERE status = 'open' AND display_slot <= 0`)
	fmt.Fprintf(os.Stdout, "schema version: %d\n", version)
	fmt.Fprintf(os.Stdout, "telegram_threads: %d open, %d non-open\n", openThreads, closedThreads)
	fmt.Fprintf(os.Stdout, "thread display slots: %s\n", okFail(badDisplaySlots == 0))
	fmt.Fprintf(os.Stdout, "thread sessions: %s\n", okFail(missingThreadSessions == 0))
	fmt.Fprintf(os.Stdout, "auto scope columns: %s\n", okFail(blankAutoScopes == 0))
	fmt.Fprintf(os.Stdout, "blank auto scopes: %d\n", blankAutoScopes)
	if version < 54 || badDisplaySlots != 0 || missingThreadSessions != 0 || blankAutoScopes != 0 {
		return fmt.Errorf("schema verify failed")
	}
	return nil
}

func scalarInt(db *sql.DB, query string) int64 {
	var out sql.NullInt64
	if err := db.QueryRow(query).Scan(&out); err != nil || !out.Valid {
		return -1
	}
	return out.Int64
}

func okFail(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}
