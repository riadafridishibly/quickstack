// Package tenant holds the workspace a request acts in, and the
// transactions that scope workspace-owned tables to it with row-level
// security.
package tenant

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/db"
)

var ErrNoWorkspace = errors.New("tenant: no workspace in context")

type Workspace struct {
	ID     int64
	Slug   string
	Name   string
	Public bool
}

// URL is path inside the workspace's dashboard, such as URL("/projects").
func (w *Workspace) URL(path string) string { return "/w/" + w.Slug + path }

type ctxKey struct{}

func WithWorkspace(ctx context.Context, w *Workspace) context.Context {
	return context.WithValue(ctx, ctxKey{}, w)
}

// From returns the workspace in ctx, or nil.
func From(ctx context.Context) *Workspace {
	w, _ := ctx.Value(ctxKey{}).(*Workspace)
	return w
}

type DB struct{ pool *pgxpool.Pool }

func NewDB(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

type Tx struct {
	*db.Queries
	Raw         pgx.Tx
	WorkspaceID int64
}

// Tx runs fn in a transaction scoped to the workspace in ctx. The workspace
// id is set transaction-locally, so it can't leak to the next user of the
// connection, and a query outside Tx fails instead of returning rows of
// another workspace.
func (d *DB) Tx(ctx context.Context, fn func(tx Tx) error) error {
	w := From(ctx)
	if w == nil {
		return ErrNoWorkspace
	}
	return pgx.BeginTxFunc(ctx, d.pool, pgx.TxOptions{}, func(t pgx.Tx) error {
		_, err := t.Exec(ctx,
			"SELECT set_config('app.workspace_id', $1, true), set_config('statement_timeout', '5s', true)",
			strconv.FormatInt(w.ID, 10))
		if err != nil {
			return err
		}
		return fn(Tx{Queries: db.New(t), Raw: t, WorkspaceID: w.ID})
	})
}
