package media_test

import (
	"io"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

func TestDrawIcon(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ctx := t.Context()
	q := db.New(s.Pool)
	id, c := authtest.SignIn(t, s.Pool, "ada@example.com")
	authtest.SetPlatformRole(t, s.Pool, id, auth.Superadmin)

	upload(s, c, pngOf(t, 600, 600), "", nil)
	a := onlyAsset(t, s)
	if err := media.Process(ctx, s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}

	has := func(rev int32, name string) bool {
		t.Helper()
		f, err := s.Blob.Open(ctx, media.IconDir(rev)+name)
		if err != nil {
			return false
		}
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		return true
	}
	choose := func(bg string) int32 {
		t.Helper()
		set, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: &a.ID, IconBackground: bg})
		if err != nil {
			t.Fatal(err)
		}
		return set.IconRev
	}
	draw := func(rev int32) {
		t.Helper()
		if err := media.DrawIcon(ctx, s.Pool, s.Blob, rev); err != nil {
			t.Fatal(err)
		}
	}
	live := func() int32 {
		t.Helper()
		set, err := q.GetSiteSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return set.SiteSetting.LiveIconRev
	}

	rev1 := choose("#ffffff")
	draw(rev1)
	if live() != rev1 {
		t.Fatalf("live rev %d, want %d", live(), rev1)
	}
	for _, name := range []string{media.FaviconFile, media.AppleIconFile, media.AppIconFile(192), media.AppIconFile(512)} {
		if !has(rev1, name) {
			t.Errorf("no %s for rev %d", name, rev1)
		}
	}
	if has(rev1, media.IconSVGFile) {
		t.Errorf("%s drawn from a PNG", media.IconSVGFile)
	}

	rev2 := choose("#000000")
	rev3 := choose("#112233")
	draw(rev2)
	if live() != rev1 || has(rev2, media.FaviconFile) {
		t.Fatal("a job for a rev that is no longer the latest drew it")
	}
	draw(rev3)
	if live() != rev3 || !has(rev3, media.FaviconFile) {
		t.Fatalf("live rev %d, want %d", live(), rev3)
	}
	if has(rev1, media.FaviconFile) {
		t.Error("the replaced icon's files are still there")
	}
	draw(rev3)
	if !has(rev3, media.FaviconFile) {
		t.Error("running the live rev's job again deleted its files")
	}
}

func TestDrawSVGIcon(t *testing.T) {
	t.Parallel()
	if !media.SVGRaster {
		t.Skip("drawing an SVG icon needs the resvg tag")
	}
	s := apptest.New(t)
	ctx := t.Context()
	upload(s, editor(t, s), []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 24"><script>alert(1)</script><rect width="48" height="24" fill="#c00"/></svg>`), "", nil)
	a := onlyAsset(t, s)
	if err := media.Process(ctx, s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}
	set, err := db.New(s.Pool).UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: &a.ID, IconBackground: "#ffffff"})
	if err != nil {
		t.Fatal(err)
	}
	if err := media.DrawIcon(ctx, s.Pool, s.Blob, set.IconRev); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{media.FaviconFile, media.AppleIconFile, media.IconSVGFile} {
		f, err := s.Blob.Open(ctx, media.IconDir(set.IconRev)+name)
		if err != nil {
			t.Fatalf("no %s: %v", name, err)
		}
		b, _ := io.ReadAll(f)
		_ = f.Close()
		if name == media.IconSVGFile && (strings.Contains(string(b), "script") || !strings.Contains(string(b), "<rect")) {
			t.Errorf("%s isn't the sanitized SVG:\n%s", name, b)
		}
	}
}
