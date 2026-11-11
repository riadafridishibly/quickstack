// Package testdb gives each test its own PostgreSQL database, copied from a
// migrated template and connected as the app role so RLS applies.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
)

const envURL = "QUICKSTACK_TEST_DATABASE_URL"

var (
	mu       sync.Mutex
	template string
)

// New returns a pool for a fresh database with every migration applied.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin := adminURL(t)

	tpl := migratedTemplate(t, admin)
	name := "qst_test_" + randHex(8)
	exec(t, admin, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s OWNER %s", name, tpl, db.OwnerRole))

	pool, err := db.ConnectAs(ctx, withDB(admin, name).String(), db.AppRole)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		exec(t, admin, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
	})
	return pool
}

// CreateWorkspace creates a workspace with no members and returns it.
func CreateWorkspace(t testing.TB, pool *pgxpool.Pool, slug string) *tenant.Workspace {
	t.Helper()
	w, err := db.New(pool).CreateWorkspace(context.Background(), db.CreateWorkspaceParams{Slug: slug, Name: slug})
	if err != nil {
		t.Fatal(err)
	}
	return &tenant.Workspace{ID: w.ID, Slug: w.Slug, Name: w.Name}
}

func migratedTemplate(t testing.TB, admin *url.URL) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if template != "" {
		return template
	}

	ctx := context.Background()
	conn := connect(t, admin)
	defer conn.Close(ctx)
	// Test binaries for different packages run in parallel.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('quickstack_testdb'))"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, db.RolesSQL); err != nil {
		t.Fatal(err)
	}

	tpl := "qst_tpl_" + fingerprint(t)
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT FROM pg_database WHERE datname = $1)", tpl).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		tmp := tpl + "_tmp"
		mustExec(t, conn, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", tmp))
		mustExec(t, conn, fmt.Sprintf("CREATE DATABASE %s OWNER %s", tmp, db.OwnerRole))
		if err := db.Migrate(ctx, withDB(admin, tmp).String()); err != nil {
			t.Fatal(err)
		}
		mustExec(t, conn, fmt.Sprintf("ALTER DATABASE %s RENAME TO %s", tmp, tpl))

		rows, err := conn.Query(ctx, "SELECT datname FROM pg_database WHERE datname ~ '^qst_tpl_[0-9a-f]{12}$' AND datname <> $1", tpl)
		if err != nil {
			t.Fatal(err)
		}
		stale, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stale {
			mustExec(t, conn, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", s))
		}
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext('quickstack_testdb'))"); err != nil {
		t.Fatal(err)
	}
	template = tpl
	return tpl
}

// fingerprint names the template after the migrations, so a changed
// migration builds a new one.
func fingerprint(t testing.TB) string {
	t.Helper()
	h := sha256.New()
	h.Write([]byte(db.RolesSQL))
	err := fs.WalkDir(db.MigrationsFS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(db.MigrationsFS(), path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\n", path)
		h.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := rivermigrate.New(riverpgxv5.New(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range m.AllVersions() {
		fmt.Fprintf(h, "river/%d\n%s", v.Version, v.SQLUp)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func adminURL(t testing.TB) *url.URL {
	t.Helper()
	raw := os.Getenv(envURL)
	if raw == "" {
		raw = "postgres://localhost:5432/postgres?sslmode=disable"
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", envURL, err)
	}
	return u
}

func withDB(u *url.URL, name string) *url.URL {
	c := *u
	c.Path = "/" + name
	return &c
}

func connect(t testing.TB, u *url.URL) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), u.String())
	if err != nil {
		t.Fatalf("connect to test postgres (set %s to a superuser URL; `make db-up` starts one): %v", envURL, err)
	}
	return conn
}

func exec(t testing.TB, u *url.URL, sql string) {
	t.Helper()
	conn := connect(t, u)
	defer conn.Close(context.Background())
	mustExec(t, conn, sql)
}

func mustExec(t testing.TB, conn *pgx.Conn, sql string) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
