package db

import (
	"context"
	"runtime/trace"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tracer opens a runtime/trace region per query, named by sqlc's
// "-- name:" header, and one per pool acquire. It does nothing while no
// trace is being recorded.
type tracer struct{}

// Separate keys, so a query run inside an acquire (AfterConnect) can't end
// the acquire's region.
type (
	queryRegionKey   struct{}
	acquireRegionKey struct{}
)

func (tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.IsEnabled() {
		return ctx
	}
	return context.WithValue(ctx, queryRegionKey{}, trace.StartRegion(ctx, "sql "+queryName(data.SQL)))
}

func (tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	endRegion(ctx, queryRegionKey{})
}

func (tracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if !trace.IsEnabled() {
		return ctx
	}
	return context.WithValue(ctx, acquireRegionKey{}, trace.StartRegion(ctx, "pool acquire"))
}

func (tracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	endRegion(ctx, acquireRegionKey{})
}

func endRegion(ctx context.Context, key any) {
	if r, ok := ctx.Value(key).(*trace.Region); ok {
		r.End()
	}
}

// queryName is the name sqlc gives a query, or else the statement's first
// word, such as begin or commit.
func queryName(sql string) string {
	if rest, ok := strings.CutPrefix(sql, "-- name: "); ok {
		name, _, _ := strings.Cut(rest, " ")
		return name
	}
	sql = strings.TrimSpace(sql)
	word, _, _ := strings.Cut(sql, " ")
	return strings.ToLower(word)
}
