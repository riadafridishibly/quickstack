package db_test

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

// Platform tables with a workspace_id: read before a workspace is known
// (invitations by token) or across workspaces (memberships, the audit log).
var noRLS = []string{"audit_log", "invitations", "memberships"}

func TestEveryWorkspaceTableHasRLS(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	rows, err := pool.Query(t.Context(), `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN pg_class pc ON pc.relname = c.table_name
		JOIN pg_namespace n ON n.oid = pc.relnamespace AND n.nspname = c.table_schema
		WHERE c.table_schema = 'public' AND c.column_name = 'workspace_id' AND pc.relkind = 'r'
		  AND NOT (pc.relrowsecurity AND pc.relforcerowsecurity
		           AND EXISTS (SELECT FROM pg_policies p WHERE p.schemaname = 'public' AND p.tablename = c.table_name))
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	missing = slices.DeleteFunc(missing, func(s string) bool { return slices.Contains(noRLS, s) })
	if len(missing) > 0 {
		t.Fatalf("tables with workspace_id but no forced RLS policy: %v", missing)
	}
}

func TestEscapeLike(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	pattern := "%" + db.EscapeLike("50%_") + "%"
	var exact, other bool
	err := pool.QueryRow(t.Context(), "SELECT '50%_ off' ILIKE $1, '5000 off' ILIKE $1", pattern).Scan(&exact, &other)
	if err != nil {
		t.Fatal(err)
	}
	if !exact || other {
		t.Errorf("pattern %q: matches itself %v, matches 5000 %v", pattern, exact, other)
	}
}
