package site

import (
	"context"
	"testing"
	"time"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestCacheListens(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := db.New(pool)
	ctx := t.Context()
	c := NewCache(pool)

	listenCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		c.Listen(listenCtx)
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})

	// eventually waits for the cache to be listening and to hold settings
	// for which ok is true.
	eventually := func(what string, ok func(Settings) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			s, err := c.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			c.mu.Lock()
			cached := c.cached != nil
			c.mu.Unlock()
			if cached && ok(s) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: settings %+v, cached %v", what, s, cached)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	eventually("no icon", func(s Settings) bool { return s.Icon == nil })

	a, err := q.CreateAsset(ctx, db.CreateAssetParams{Key: "abcd2345", ContentType: "image/png", Sha256: []byte{0}, Width: 512, Height: 512})
	if err != nil {
		t.Fatal(err)
	}
	set, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: &a.ID, IconBackground: "#000000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkSiteIconLive(ctx, db.MarkSiteIconLiveParams{IconID: a.ID, Rev: set.IconRev}); err != nil {
		t.Fatal(err)
	}
	eventually("icon live", func(s Settings) bool { return s.Icon != nil && s.Icon.Rev == set.IconRev && s.Background == "#000000" })

	// Deleting the asset clears the icon without an UPDATE of our own.
	if _, err := q.DeleteAsset(ctx, a.Key); err != nil {
		t.Fatal(err)
	}
	eventually("asset deleted", func(s Settings) bool { return s.Icon == nil })

	stop()
	<-done
	if s, err := c.Get(ctx); err != nil || s.Icon != nil || c.cached != nil {
		t.Errorf("after Listen stopped: %+v, %v, cached %v", s, err, c.cached != nil)
	}
}
