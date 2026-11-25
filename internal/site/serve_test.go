package site_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

// setIcon uploads data, makes it the site icon, and draws its files. It
// returns the live rev.
func setIcon(t *testing.T, s *apptest.Server, c *http.Cookie, data []byte) int32 {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "icon")
	_, _ = fw.Write(data)
	_ = mw.Close()
	w := s.Do(apptest.Req{Path: "/admin/media", Cookie: c, Body: &body, Header: http.Header{"Content-Type": {mw.FormDataContentType()}}})
	key := strings.TrimPrefix(w.Header().Get("Location"), "/admin/media/")
	a, err := db.New(s.Pool).GetAssetByKey(t.Context(), key)
	if err != nil {
		t.Fatalf("upload: %d %q: %v", w.Code, key, err)
	}
	if err := media.Process(t.Context(), s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}
	apptest.Redirect(t, s.Post("/admin/site/icon", c, url.Values{"icon": {media.URL(key)}, "background": {"#203040"}}), "/admin/site?done=saved")
	rev := settings(t, s).IconRev
	if err := media.DrawIcon(t.Context(), s.Pool, s.Blob, rev); err != nil {
		t.Fatal(err)
	}
	return rev
}

func squarePNG(t *testing.T, size int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func manifestOf(t *testing.T, s *apptest.Server) (m struct {
	Name       string `json:"name"`
	ThemeColor string `json:"theme_color"`
	Icons      []struct{ Src, Sizes, Type string }
},
) {
	t.Helper()
	w := s.Get("/manifest.webmanifest", nil)
	apptest.Status(t, w, http.StatusOK)
	if ct := w.Header().Get("Content-Type"); ct != "application/manifest+json" {
		t.Errorf("manifest Content-Type %q", ct)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestServeIcon(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)

	b := s.Get("/login", nil).Body.String()
	if !strings.Contains(b, `<link rel="icon" href="data:,">`) || strings.Contains(b, "apple-touch-icon") || strings.Contains(b, "manifest") {
		t.Errorf("head links with no icon set:\n%s", b)
	}
	for _, p := range []string{"/favicon.ico", "/apple-touch-icon.png"} {
		w := s.Get(p, nil)
		apptest.Status(t, w, http.StatusNotFound)
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s with no icon: Content-Type %q, want a plain 404", p, ct)
		}
	}
	if m := manifestOf(t, s); m.Name != "Quickstack" || m.ThemeColor != "#ffffff" || len(m.Icons) != 0 {
		t.Errorf("manifest with no icon: %+v", m)
	}

	rev := setIcon(t, s, admin, squarePNG(t, 600))
	icon := func(file string) string { return "/site/icon/" + strconv.Itoa(int(rev)) + "/" + file }
	b = s.Get("/login", nil).Body.String()
	for _, link := range []string{
		`<link rel="icon" href="` + icon("favicon.ico") + `" sizes="32x32">`,
		`<link rel="apple-touch-icon" href="` + icon("apple-touch-icon.png") + `">`,
		`<link rel="manifest" href="/manifest.webmanifest">`,
	} {
		if !strings.Contains(b, link) {
			t.Errorf("no %s", link)
		}
	}
	if strings.Contains(b, "data:,") || strings.Contains(b, "image/svg+xml") {
		t.Error("the page still has the empty icon, or an SVG icon from a PNG")
	}

	for _, tc := range []struct{ path, contentType, cache string }{
		{"/favicon.ico", "image/x-icon", "public, max-age=3600"},
		{"/apple-touch-icon.png", "image/png", "public, max-age=3600"},
		{icon("favicon.ico"), "image/x-icon", "public, max-age=31536000, immutable"},
		{icon("icon-512.png"), "image/png", "public, max-age=31536000, immutable"},
	} {
		w := s.Get(tc.path, nil)
		apptest.Status(t, w, http.StatusOK)
		if ct, cc := w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"); ct != tc.contentType || cc != tc.cache {
			t.Errorf("%s: Content-Type %q, Cache-Control %q", tc.path, ct, cc)
		}
	}
	ico := s.Get("/favicon.ico", nil).Body.Bytes()
	if len(ico) < 22 || binary.LittleEndian.Uint16(ico[4:]) != 1 || ico[6] != 32 {
		t.Errorf("/favicon.ico isn't a 32×32 ICO: % x", ico[:min(len(ico), 22)])
	} else if cfg, err := png.DecodeConfig(bytes.NewReader(ico[22:])); err != nil || cfg.Width != 32 {
		t.Errorf("the ICO's image: %+v, %v", cfg, err)
	}
	apple, _ := io.ReadAll(s.Get("/apple-touch-icon.png", nil).Body)
	if cfg, err := png.DecodeConfig(bytes.NewReader(apple)); err != nil || cfg.Width != 180 {
		t.Errorf("/apple-touch-icon.png: %+v, %v", cfg, err)
	}
	for _, p := range []string{icon("original.png"), icon("icon.svg"), "/site/icon/0" + strconv.Itoa(int(rev)) + "/favicon.ico", "/site/icon/x/favicon.ico", "/site/icon/999/favicon.ico"} {
		apptest.Status(t, s.Get(p, nil), http.StatusNotFound)
	}

	m := manifestOf(t, s)
	var srcs []string
	for _, i := range m.Icons {
		srcs = append(srcs, i.Src+" "+i.Sizes+" "+i.Type)
	}
	want := []string{icon("icon-192.png") + " 192x192 image/png", icon("icon-512.png") + " 512x512 image/png"}
	if !slices.Equal(srcs, want) || m.ThemeColor != "#203040" {
		t.Errorf("manifest icons %v, theme %q; want %v", srcs, m.ThemeColor, want)
	}

	apptest.Redirect(t, s.Post("/admin/site/icon", admin, url.Values{"icon": {""}}), "/admin/site?done=saved")
	if b := s.Get("/login", nil).Body.String(); strings.Contains(b, "apple-touch-icon") {
		t.Error("the page links the icon after it was removed")
	}
	apptest.Status(t, s.Get("/favicon.ico", nil), http.StatusNotFound)
}

func TestServeSVGIcon(t *testing.T) {
	t.Parallel()
	if !media.SVGRaster {
		t.Skip("an SVG icon needs the resvg tag")
	}
	s := apptest.New(t)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)
	rev := setIcon(t, s, admin, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#c00"/></svg>`))
	svg := media.IconURL(rev, media.IconSVGFile)
	if b := s.Get("/login", nil).Body.String(); !strings.Contains(b, `<link rel="icon" href="`+svg+`" type="image/svg+xml">`) {
		t.Errorf("no SVG icon link:\n%s", b)
	}
	w := s.Get(svg, nil)
	apptest.Status(t, w, http.StatusOK)
	if ct, csp := w.Header().Get("Content-Type"), w.Header().Get("Content-Security-Policy"); ct != "image/svg+xml" || !strings.HasSuffix(csp, "; sandbox") {
		t.Errorf("icon.svg: Content-Type %q, CSP %q", ct, csp)
	}
}
