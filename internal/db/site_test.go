package db_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

func TestSiteIcon(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	q := db.New(pool)
	ctx := t.Context()

	asset := func(key string) int64 {
		t.Helper()
		a, err := q.CreateAsset(ctx, db.CreateAssetParams{Key: key, ContentType: "image/png", Sha256: []byte{0}, Width: 512, Height: 512})
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	set := func(iconID *int64) db.SiteSetting {
		t.Helper()
		s, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: iconID, IconBackground: "#ffffff"})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// mark returns the rev the icon replaced, or -1 if it changed nothing.
	mark := func(iconID int64, rev int32) int32 {
		t.Helper()
		prev, err := q.MarkSiteIconLive(ctx, db.MarkSiteIconLiveParams{IconID: iconID, Rev: rev})
		if errors.Is(err, pgx.ErrNoRows) {
			return -1
		}
		if err != nil {
			t.Fatal(err)
		}
		return prev
	}
	live := func() (*int64, int32) {
		t.Helper()
		s, err := q.GetSiteSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s.SiteSetting.LiveIconID, s.SiteSetting.LiveIconRev
	}

	s, err := q.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SiteSetting.IconID != nil || s.SiteSetting.LiveIconID != nil || s.SiteSetting.IconBackground != "#ffffff" {
		t.Fatalf("default settings = %+v", s.SiteSetting)
	}

	a, b := asset("aaaaaaaa"), asset("bbbbbbbb")
	revA := set(&a).IconRev
	if prev := mark(a, revA); prev != 0 {
		t.Fatalf("marking A live replaced rev %d, want 0", prev)
	}
	revB := set(&b).IconRev
	if id, rev := live(); id == nil || *id != a || rev != revA {
		t.Fatalf("while B is drawn, live = %v %d, want A at %d", id, rev, revA)
	}
	if mark(a, revA+1) != -1 {
		t.Fatal("a job for an icon no longer chosen made it live")
	}
	if prev := mark(b, revB); prev != revA {
		t.Fatalf("marking B live replaced rev %d, want %d", prev, revA)
	}
	if mark(b, revB-1) != -1 {
		t.Fatal("a late job for an earlier rev replaced the live one")
	}

	if _, err := pool.Exec(ctx, "DELETE FROM assets WHERE id = $1", b); err != nil {
		t.Fatal(err)
	}
	s, err = q.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.SiteSetting.IconID != nil || s.SiteSetting.LiveIconID != nil {
		t.Fatalf("after deleting the icon's asset, settings = %+v", s.SiteSetting)
	}

	revA = set(&a).IconRev
	mark(a, revA)
	if s := set(nil); s.LiveIconID != nil {
		t.Fatalf("clearing the icon left live icon %d", *s.LiveIconID)
	}

	if _, err := pool.Exec(ctx, "INSERT INTO site_settings (id) VALUES (false)"); err == nil {
		t.Fatal("a second settings row was accepted")
	}
	if _, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconBackground: "white"}); err == nil {
		t.Fatal("a background that isn't #rrggbb was accepted")
	}
}
