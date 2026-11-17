package seed_test

import (
	"errors"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/markdown"
	"github.com/riadafridishibly/quickstack/internal/seed"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

// TestRun keeps `make seed` working as the schema changes.
func TestRun(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	store, err := blob.OpenFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := func(string, ...any) {}
	if err := seed.Run(t.Context(), pool, store, false, log); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(t.Context(), pool, store, false, log); !errors.Is(err, seed.ErrSeeded) {
		t.Fatalf("second run without reset: %v", err)
	}
	if err := seed.Run(t.Context(), pool, store, true, log); err != nil {
		t.Fatalf("run with reset: %v", err)
	}

	// The sample posts show every feature, so each must render whole.
	q := db.New(pool)
	rows, err := q.ListPosts(t.Context(), db.ListPostsParams{Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	for state, want := range map[string]int{"": 4, "published": 2, "scheduled": 1, "draft": 1} {
		n, err := q.ListPosts(t.Context(), db.ListPostsParams{State: state, Lim: 100})
		if err != nil {
			t.Fatal(err)
		}
		if len(n) != want {
			t.Errorf("%d posts in state %q, want %d", len(n), state, want)
		}
	}
	for _, r := range rows {
		p, err := q.GetPost(t.Context(), r.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, problems, err := markdown.Render(t.Context(), p.Post.Body, q.ListAssetsByKeys)
		if err != nil {
			t.Fatal(err)
		}
		if len(problems) > 0 {
			t.Errorf("post %s: %v", r.Slug, problems)
		}
	}
	assets, err := q.ListAssets(t.Context(), db.ListAssetsParams{Lim: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 4 {
		t.Errorf("%d images after a reset, want 4", len(assets))
	}
}
