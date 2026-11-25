package media_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

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
		if err := media.DrawIcon(ctx, s.Pool, s.Blob, s.Jobs.Client, rev); err != nil {
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
	// Pages and manifests cached before the change still link rev1.
	if !has(rev1, media.FaviconFile) {
		t.Error("the replaced icon's files went at once")
	}
	if got := iconDeletions(t, s); len(got) != 1 || got[0].key != "site/icon/1" || got[0].in < media.IconGrace-time.Minute {
		t.Errorf("deletions %+v, want site/icon/1 in %v", got, media.IconGrace)
	}
	draw(rev3)
	if !has(rev3, media.FaviconFile) || len(iconDeletions(t, s)) != 1 {
		t.Error("running the live rev's job again changed something")
	}
}

type deletion struct {
	key   string
	queue string
	// in is how long from now the deletion is scheduled.
	in time.Duration
}

// iconDeletions lists the queued deletions of icon files.
func iconDeletions(t *testing.T, s *apptest.Server) []deletion {
	t.Helper()
	rows, err := s.Pool.Query(t.Context(), `
		SELECT args->>'key', queue, extract(epoch FROM scheduled_at - now())::float8
		FROM river_job WHERE kind = 'media_delete_files' AND args->>'key' LIKE 'site/icon/%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []deletion
	for rows.Next() {
		var d deletion
		var secs float64
		if err := rows.Scan(&d.key, &d.queue, &secs); err != nil {
			t.Fatal(err)
		}
		d.in = time.Duration(secs * float64(time.Second))
		if d.queue != "media" {
			t.Errorf("deletion of %s on queue %q", d.key, d.queue)
		}
		out = append(out, d)
	}
	return out
}

func TestDrawIconFails(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	ctx := t.Context()
	q := db.New(s.Pool)
	for _, tc := range []struct {
		name, key, contentType, msg string
		skip                        bool
	}{
		{"unreadable", "badp2345", "image/png", "This image couldn’t be drawn as an icon.", false},
		{"svg without resvg", "bads2345", "image/svg+xml", "This build can’t draw an SVG icon.", media.SVGRaster},
	} {
		if tc.skip {
			continue
		}
		a, err := q.CreateAsset(ctx, db.CreateAssetParams{Key: tc.key, ContentType: tc.contentType, Sha256: []byte{0}, Width: 600, Height: 600})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.MarkAssetReady(ctx, db.MarkAssetReadyParams{ID: a.ID, Width: 600, Height: 600, Widths: []int32{}}); err != nil {
			t.Fatal(err)
		}
		name := "original.png"
		if tc.contentType == "image/svg+xml" {
			name = media.SVGFile
		}
		data := `<svg xmlns="http://www.w3.org/2000/svg" width="9" height="9"/>`
		if err := s.Blob.Put(ctx, a.Key+"/"+name, strings.NewReader(data), int64(len(data)), tc.contentType); err != nil {
			t.Fatal(err)
		}
		set, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: &a.ID, IconBackground: "#ffffff"})
		if err != nil {
			t.Fatal(err)
		}
		err = media.DrawIcon(ctx, s.Pool, s.Blob, s.Jobs.Client, set.IconRev)
		var cancel *river.JobCancelError
		if !errors.As(err, &cancel) {
			t.Errorf("%s: DrawIcon = %v, want a cancelled job", tc.name, err)
		}
		row, err := q.GetSiteSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := row.SiteSetting.IconError; !strings.HasPrefix(got, tc.msg) || row.SiteSetting.LiveIconID != nil {
			t.Errorf("%s: settings %+v", tc.name, row.SiteSetting)
		}
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
	if err := media.DrawIcon(ctx, s.Pool, s.Blob, s.Jobs.Client, set.IconRev); err != nil {
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
