package tenant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestWorkspacesDoNotLeak(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	tdb := tenant.NewDB(pool)
	a, b := testdb.CreateWorkspace(t, pool, "a"), testdb.CreateWorkspace(t, pool, "b")
	ctxA := tenant.WithWorkspace(t.Context(), a)
	ctxB := tenant.WithWorkspace(t.Context(), b)

	create := func(ctx context.Context, name string) {
		t.Helper()
		err := tdb.Tx(ctx, func(tx tenant.Tx) error {
			_, err := tx.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: tx.WorkspaceID, Name: name, Status: "active"})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	create(ctxA, "in a")
	create(ctxB, "in b")

	// A query that asks for b's rows inside a's transaction gets nothing:
	// row-level security is the backstop when the filter is wrong.
	err := tdb.Tx(ctxA, func(tx tenant.Tx) error {
		rows, err := tx.ListProjects(ctxA, db.ListProjectsParams{WorkspaceID: b.ID, Lim: 10})
		if len(rows) != 0 {
			t.Errorf("workspace a sees %d of b's projects", len(rows))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writing another workspace's id fails the policy's check.
	err = tdb.Tx(ctxA, func(tx tenant.Tx) error {
		_, err := tx.CreateProject(ctxA, db.CreateProjectParams{WorkspaceID: b.ID, Name: "sneaky", Status: "active"})
		return err
	})
	if err == nil {
		t.Error("workspace a wrote a project into b")
	}
	// Outside Tx there is no workspace, and the query fails.
	if _, err := db.New(pool).ListProjects(t.Context(), db.ListProjectsParams{WorkspaceID: a.ID, Lim: 10}); err == nil {
		t.Error("query outside Tx succeeded")
	}
	if err := tdb.Tx(t.Context(), func(tenant.Tx) error { return nil }); !errors.Is(err, tenant.ErrNoWorkspace) {
		t.Errorf("Tx without a workspace: %v", err)
	}
}
