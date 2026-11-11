package seed_test

import (
	"errors"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/seed"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

// TestRun keeps `make seed` working as the schema changes.
func TestRun(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	log := func(string, ...any) {}
	if err := seed.Run(t.Context(), pool, false, log); err != nil {
		t.Fatal(err)
	}
	if err := seed.Run(t.Context(), pool, false, log); !errors.Is(err, seed.ErrSeeded) {
		t.Fatalf("second run without reset: %v", err)
	}
	if err := seed.Run(t.Context(), pool, true, log); err != nil {
		t.Fatalf("run with reset: %v", err)
	}
}
