package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// OwnerRole owns the tables and runs migrations; it bypasses row-level
// security. AppRole is what the server connects as, so RLS applies to it.
const (
	OwnerRole = "quickstack_owner"
	AppRole   = "quickstack_app"
)

//go:embed roles.sql
var RolesSQL string

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationsFS is the migrations directory.
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = 25
	}
	return open(ctx, cfg)
}

// ConnectAs connects with url and switches every connection to role.
func ConnectAs(ctx context.Context, url, role string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize())
		return err
	}
	return open(ctx, cfg)
}

func open(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	cfg.ConnConfig.Tracer = tracer{}
	// Queries turn timestamps into dates, which must not depend on the server's zone.
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	return pool, nil
}

// Migrate applies the migrations, then River's, as the owner role and under
// an advisory lock so concurrent boots don't race.
func Migrate(ctx context.Context, url string) error {
	pool, err := ConnectAs(ctx, url, OwnerRole)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer pool.Close()

	lock, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(hashtext('quickstack_migrate'))"); err != nil {
		return err
	}
	defer func() {
		_, _ = lock.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(hashtext('quickstack_migrate'))")
	}()

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, MigrationsFS())
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, r := range results {
		slog.InfoContext(ctx, "migration applied", "file", r.Source.Path, "duration", r.Duration)
	}

	m, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	res, err := m.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate river: %w", err)
	}
	for _, v := range res.Versions {
		slog.InfoContext(ctx, "migration applied", "set", "river", "version", v.Version, "duration", v.Duration)
	}
	return nil
}
