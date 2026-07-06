//go:build linux

package session

import "testing"

func TestFreshSchemaIncludesScopedAutoIndexes(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	assertSQLiteIndex(t, store.db, "idx_operator_auto_approvals_scope_active")
	assertSQLiteIndex(t, store.db, "idx_operator_autonomy_overrides_scope_active")
	assertSQLiteIndex(t, store.db, "idx_judgment_uses_session_updated")
	assertSQLiteIndex(t, store.db, "idx_judgment_uses_result")
}
