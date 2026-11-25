package site_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

func staff(t *testing.T, s *apptest.Server, email string, role auth.PlatformRole) *http.Cookie {
	t.Helper()
	id, c := authtest.SignIn(t, s.Pool, email)
	authtest.SetPlatformRole(t, s.Pool, id, role)
	return c
}

// asset adds an asset with key, ready unless status says otherwise.
func asset(t *testing.T, s *apptest.Server, key, contentType string, w, h int32, status string) db.Asset {
	t.Helper()
	q := db.New(s.Pool)
	a, err := q.CreateAsset(t.Context(), db.CreateAssetParams{Key: key, ContentType: contentType, Sha256: []byte{0}, Width: w, Height: h})
	if err != nil {
		t.Fatal(err)
	}
	switch status {
	case media.Ready:
		_, err = q.MarkAssetReady(t.Context(), db.MarkAssetReadyParams{ID: a.ID, Width: w, Height: h, Widths: []int32{320}})
	case media.Failed:
		_, err = q.MarkAssetFailed(t.Context(), db.MarkAssetFailedParams{ID: a.ID, Error: "x"})
	}
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func settings(t *testing.T, s *apptest.Server) db.SiteSetting {
	t.Helper()
	row, err := db.New(s.Pool).GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return row.SiteSetting
}

// jobs lists the queued jobs as kind and args.
func jobs(t *testing.T, s *apptest.Server) []string {
	t.Helper()
	rows, err := s.Pool.Query(t.Context(), "SELECT kind || ' ' || args::text FROM river_job ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			t.Fatal(err)
		}
		out = append(out, j)
	}
	return out
}

func audit(t *testing.T, s *apptest.Server) []string {
	t.Helper()
	rows, err := s.Pool.Query(t.Context(), "SELECT action || ' ' || target FROM audit_log WHERE action LIKE 'site.%' ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSpace(a))
	}
	return out
}

func TestSiteIsForSuperadmins(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)
	if b := s.Get("/admin/site", admin).Body.String(); !strings.Contains(b, `href="/admin/site"`) || !strings.Contains(b, `name="icon"`) {
		t.Error("a superadmin doesn't see the Site section")
	}
	for role, page := range map[auth.PlatformRole]string{auth.Support: "/admin/accounts", auth.Editor: "/admin/blog"} {
		c := staff(t, s, string(role)+"@example.com", role)
		apptest.Status(t, s.Get("/admin/site", c), http.StatusForbidden)
		apptest.Status(t, s.Post("/admin/site/icon", c, url.Values{"background": {"#000000"}}), http.StatusForbidden)
		w := s.Get(page, c)
		apptest.Status(t, w, http.StatusOK)
		if strings.Contains(w.Body.String(), `href="/admin/site"`) {
			t.Errorf("%s sees the Site link", role)
		}
	}
	if got := settings(t, s).IconBackground; got != "#ffffff" {
		t.Errorf("background %q after refused posts", got)
	}
}

func TestSetIcon(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)
	a := asset(t, s, "pngk2345", "image/png", 600, 600, media.Ready)
	if b := s.Get("/admin/site", admin).Body.String(); !strings.Contains(b, "No icon is set") {
		t.Error("the page doesn't say no icon is set")
	}

	w := s.Post("/admin/site/icon", admin, url.Values{"icon": {"/media/pngk2345"}, "background": {"#FFFFFF"}})
	apptest.Redirect(t, w, "/admin/site?done=saved")
	set := settings(t, s)
	if set.IconID == nil || *set.IconID != a.ID || set.IconBackground != "#ffffff" || set.IconRev != 1 {
		t.Fatalf("settings %+v", set)
	}
	if got := jobs(t, s); !slices.Equal(got, []string{`media_icon {"rev": 1}`}) {
		t.Errorf("jobs %v", got)
	}
	if got := audit(t, s); !slices.Equal(got, []string{"site.icon_set /media/pngk2345"}) {
		t.Errorf("audit %v", got)
	}
	b := s.Get("/admin/site?done=saved", admin).Body.String()
	if !strings.Contains(b, "Icon saved.") || !strings.Contains(b, "Drawing the icon’s files") || !strings.Contains(b, `hx-trigger="every 2s"`) {
		t.Errorf("page while drawing:\n%s", b)
	}

	// The same values again change nothing.
	apptest.Redirect(t, s.Post("/admin/site/icon", admin, url.Values{"icon": {"/media/pngk2345"}, "background": {"#ffffff"}}), "/admin/site?done=saved")
	if got := settings(t, s).IconRev; got != 1 {
		t.Errorf("rev %d after saving the same values", got)
	}

	if _, err := db.New(s.Pool).MarkSiteIconLive(t.Context(), db.MarkSiteIconLiveParams{IconID: a.ID, Rev: 1}); err != nil {
		t.Fatal(err)
	}
	w = s.Do(apptest.Req{Path: "/admin/site", Cookie: admin, Target: "icon-status"})
	if b := w.Body.String(); strings.Contains(b, "<html") || strings.Contains(b, "hx-trigger") || !strings.Contains(b, "Pages show this icon.") {
		t.Errorf("status fragment once live:\n%s", b)
	}

	w = s.Do(apptest.Req{Path: "/admin/site/icon", Cookie: admin, Form: url.Values{"icon": {"/media/pngk2345"}, "background": {"#102030"}}, Target: "icon-form"})
	if got := w.Header().Get("HX-Redirect"); got != "/admin/site?done=saved" {
		t.Errorf("htmx save: HX-Redirect %q", got)
	}
	if set := settings(t, s); set.IconBackground != "#102030" || set.IconRev != 2 || set.LiveIconRev != 1 {
		t.Errorf("after changing the background: %+v", set)
	}

	apptest.Redirect(t, s.Post("/admin/site/icon", admin, url.Values{"icon": {""}, "background": {"#102030"}}), "/admin/site?done=saved")
	if set := settings(t, s); set.IconID != nil || set.LiveIconID != nil {
		t.Errorf("after removing the icon: %+v", set)
	}
	want := []string{`media_icon {"rev": 1}`, `media_icon {"rev": 2}`, `media_delete_files {"key": "site/icon/1"}`}
	if got := jobs(t, s); !slices.Equal(got, want) {
		t.Errorf("jobs %v, want %v", got, want)
	}
	want = []string{"site.icon_set /media/pngk2345", "site.icon_background_set #102030", "site.icon_removed"}
	if got := audit(t, s); !slices.Equal(got, want) {
		t.Errorf("audit %v, want %v", got, want)
	}
}

func TestIconValidation(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)
	asset(t, s, "prcs2345", "image/png", 600, 600, media.Processing)
	asset(t, s, "fadd2345", "image/png", 600, 600, media.Failed)
	asset(t, s, "smaw2345", "image/png", 800, 511, media.Ready)
	asset(t, s, "svgv2345", "image/svg+xml", 16, 16, media.Ready)

	cases := []struct {
		name, msg string
		form      url.Values
	}{
		{"bad address", "Enter an image’s address from Media", url.Values{"icon": {"https://example.com/x.png"}}},
		{"not in media", "There is no image /media/ab12cd34 in Media.", url.Values{"icon": {"/media/ab12cd34"}}},
		{"processing", "still being processed", url.Values{"icon": {"/media/prcs2345"}}},
		{"failed", "couldn’t be processed", url.Values{"icon": {"/media/fadd2345"}}},
		{"too small", "This image is 800×511 pixels. An icon needs at least 512 on its shorter side.", url.Values{"icon": {"/media/smaw2345"}}},
		{"bad colour", "Enter a colour as # and six hex digits", url.Values{"background": {"white"}}},
	}
	if !media.SVGRaster {
		cases = append(cases, struct {
			name, msg string
			form      url.Values
		}{"svg", "This build can’t make the PNG icons", url.Values{"icon": {"/media/svgv2345"}}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := s.Post("/admin/site/icon", admin, tc.form)
			apptest.Status(t, w, http.StatusUnprocessableEntity)
			if b := w.Body.String(); !strings.Contains(b, tc.msg) || !strings.Contains(b, "<html") {
				t.Errorf("plain post:\n%s", b)
			}
			w = s.Do(apptest.Req{Path: "/admin/site/icon", Cookie: admin, Form: tc.form, Target: "icon-form"})
			apptest.Status(t, w, http.StatusUnprocessableEntity)
			if b := w.Body.String(); !strings.Contains(b, tc.msg) || strings.Contains(b, "<html") {
				t.Errorf("htmx post:\n%s", b)
			}
		})
	}
	if set := settings(t, s); set.IconID != nil || set.IconRev != 0 {
		t.Errorf("settings changed by refused posts: %+v", set)
	}

	if media.SVGRaster {
		apptest.Redirect(t, s.Post("/admin/site/icon", admin, url.Values{"icon": {"/media/svgv2345"}}), "/admin/site?done=saved")
	}
}
