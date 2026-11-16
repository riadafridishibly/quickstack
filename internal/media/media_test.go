package media_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	vips "github.com/cshum/vipsgen/vips816"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func webpSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	img, err := vips.NewWebploadBuffer(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	return img.Width(), img.Height()
}

// upload posts file to /admin/media as a multipart form.
func upload(s *apptest.Server, c *http.Cookie, file []byte, target string, header http.Header) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "photo")
	_, _ = fw.Write(file)
	_ = mw.Close()
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", mw.FormDataContentType())
	return s.Do(apptest.Req{Path: "/admin/media", Cookie: c, Body: &body, Target: target, Header: header})
}

func editor(t *testing.T, s *apptest.Server) *http.Cookie {
	t.Helper()
	id, c := authtest.SignIn(t, s.Pool, "eve@example.com")
	authtest.SetPlatformRole(t, s.Pool, id, auth.Editor)
	return c
}

func onlyAsset(t *testing.T, s *apptest.Server) db.Asset {
	t.Helper()
	rows, err := db.New(s.Pool).ListAssets(t.Context(), db.ListAssetsParams{Lim: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("assets %+v, %v", rows, err)
	}
	a, err := db.New(s.Pool).GetAsset(t.Context(), rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func jobKinds(t *testing.T, s *apptest.Server) []string {
	t.Helper()
	rows, err := s.Pool.Query(t.Context(), "SELECT kind FROM river_job ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	return kinds
}

func TestUploadAndProcess(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)

	w := upload(s, c, pngOf(t, 800, 600), "", nil)
	a := onlyAsset(t, s)
	apptest.Redirect(t, w, "/admin/media/"+a.Key)
	if !media.ValidKey(a.Key) || a.Status != media.Processing || a.ContentType != "image/png" || a.Width != 800 || a.Height != 600 {
		t.Fatalf("asset %+v", a)
	}
	if k := jobKinds(t, s); !slices.Equal(k, []string{"media_process"}) {
		t.Errorf("jobs %v", k)
	}
	if b := s.Get("/admin/media/"+a.Key, c).Body.String(); !strings.Contains(b, `hx-trigger="every 2s"`) {
		t.Error("the page of a processing image doesn't poll")
	}

	if err := media.Process(t.Context(), s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}
	a = onlyAsset(t, s)
	if a.Status != media.Ready || !slices.Equal(a.Widths, []int32{320, 640, 800}) {
		t.Fatalf("after processing: %+v", a)
	}

	for _, wd := range []int{320, 800} {
		w := s.Get(media.FileURL(a.Key, strconv.Itoa(wd)+".webp"), nil)
		apptest.Status(t, w, http.StatusOK)
		if ct, cc := w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"); ct != "image/webp" || cc != "public, max-age=31536000, immutable" {
			t.Errorf("%d.webp: Content-Type %q, Cache-Control %q", wd, ct, cc)
		}
		if gw, gh := webpSize(t, w.Body.Bytes()); gw != wd || gh != wd*3/4 {
			t.Errorf("%d.webp is %d×%d", wd, gw, gh)
		}
	}
	w = s.Get(media.FileURL(a.Key, media.CardFile), nil)
	apptest.Status(t, w, http.StatusOK)
	if cfg, err := jpeg.DecodeConfig(w.Body); err != nil || cfg.Width != media.CardWidth || cfg.Height != media.CardHeight {
		t.Errorf("og.jpg: %+v, %v", cfg, err)
	}
	for _, p := range []string{"original.png", "1024.webp", "0320.webp", "x.webp", "og.png"} {
		apptest.Status(t, s.Get(media.FileURL(a.Key, p), nil), http.StatusNotFound)
	}
	apptest.Status(t, s.Get("/media/ABCDEFGH/320.webp", nil), http.StatusNotFound)
	apptest.Status(t, s.Get("/media/zzzzzzzz/320.webp", nil), http.StatusNotFound)

	if b := s.Get("/admin/media", c).Body.String(); !strings.Contains(b, media.FileURL(a.Key, "320.webp")) {
		t.Error("the list doesn't show the thumbnail")
	}
	w = s.Do(apptest.Req{Path: "/admin/media/" + a.Key, Cookie: c, Target: "asset-preview"})
	if b := w.Body.String(); strings.Contains(b, "<html") || strings.Contains(b, "hx-trigger") || !strings.Contains(b, "srcset") {
		t.Errorf("preview fragment once ready:\n%s", b)
	}
}

func TestTurnedJPEG(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)
	// EXIF orientation 6: stored 400×200, shown turned a quarter.
	jpg := jpegOf(t, 400, 200)
	exif := []byte("\xff\xe1\x00\x22Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x06\x00\x00\x00\x00\x00\x00")
	jpg = slices.Concat(jpg[:2], exif, jpg[2:])

	upload(s, c, jpg, "", nil)
	a := onlyAsset(t, s)
	if err := media.Process(t.Context(), s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}
	a = onlyAsset(t, s)
	if a.Width != 200 || a.Height != 400 || !slices.Equal(a.Widths, []int32{200}) {
		t.Fatalf("asset %+v", a)
	}
	w := s.Get(media.FileURL(a.Key, "200.webp"), nil)
	if gw, gh := webpSize(t, w.Body.Bytes()); gw != 200 || gh != 400 {
		t.Errorf("200.webp is %d×%d", gw, gh)
	}
}

func TestUnreadableImage(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)
	jpg := jpegOf(t, 400, 300)
	upload(s, c, jpg[:len(jpg)/2], "", nil)
	a := onlyAsset(t, s)
	if err := media.Process(t.Context(), s.Pool, s.Blob, a.ID); err != nil {
		t.Fatal(err)
	}
	a = onlyAsset(t, s)
	if a.Status != media.Failed || !strings.Contains(a.Error, "couldn’t be read") {
		t.Fatalf("asset %+v", a)
	}
	if b := s.Get("/admin/media/"+a.Key, c).Body.String(); !strings.Contains(b, "couldn’t be read") {
		t.Error("the page doesn't show the error")
	}
}

func TestUploadRejected(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)
	const msg = "isn’t an image we can use"

	w := upload(s, c, []byte("not an image"), "", nil)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if b := w.Body.String(); !strings.Contains(b, "<html") || !strings.Contains(b, msg) {
		t.Errorf("plain rejection:\n%.2000s", b)
	}
	w = upload(s, c, []byte("not an image"), "upload-form", nil)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if b := w.Body.String(); strings.Contains(b, "<html") || !strings.Contains(b, msg) || !strings.Contains(b, `aria-invalid="true"`) {
		t.Errorf("htmx rejection:\n%s", b)
	}
	w = upload(s, c, nil, "", http.Header{"Accept": {"application/json"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	var e map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || !strings.Contains(e["error"], "empty") {
		t.Errorf("JSON rejection: %s", w.Body)
	}
	w = upload(s, c, make([]byte, media.MaxBytes+2<<20), "", nil)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "larger than 20 MB") {
		t.Error("no message for a file that is too large")
	}
	apptest.Status(t, s.Post("/admin/media", c, url.Values{}), http.StatusUnprocessableEntity)

	if rows, _ := db.New(s.Pool).ListAssets(t.Context(), db.ListAssetsParams{Lim: 10}); len(rows) != 0 {
		t.Errorf("rejected uploads were stored: %+v", rows)
	}
	if k := jobKinds(t, s); len(k) != 0 {
		t.Errorf("jobs %v", k)
	}
}

func TestUploadJSON(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)
	w := upload(s, c, pngOf(t, 10, 10), "", http.Header{"Accept": {"application/json"}})
	apptest.Status(t, w, http.StatusCreated)
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	key := got["key"]
	if !media.ValidKey(key) || got["url"] != "/media/"+key || got["markdown"] != "![](/media/"+key+")" || got["status"] != "processing" {
		t.Errorf("response %v", got)
	}
}

func TestEditAndDelete(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	c := editor(t, s)
	upload(s, c, pngOf(t, 10, 10), "", nil)
	a := onlyAsset(t, s)
	path := "/admin/media/" + a.Key

	apptest.Redirect(t, s.Post(path, c, url.Values{"alt": {"  A red\n door "}, "credit": {"Ana"}}), path+"?done=saved")
	if a := onlyAsset(t, s); a.Alt != "A red door" || a.Credit != "Ana" {
		t.Errorf("alt %q, credit %q", a.Alt, a.Credit)
	}
	if b := s.Get(path, c).Body.String(); !strings.Contains(b, "![A red door](/media/"+a.Key+")") {
		t.Error("the page doesn't show the Markdown with the alt text")
	}
	long := url.Values{"alt": {strings.Repeat("a", 501)}}
	w := s.Post(path, c, long)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("plain validation failure isn't a full page")
	}
	w = s.Do(apptest.Req{Path: path, Cookie: c, Form: long, Target: "asset-text"})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if b := w.Body.String(); strings.Contains(b, "<html") || !strings.Contains(b, "at most 500") {
		t.Errorf("htmx validation failure:\n%s", b)
	}
	apptest.Status(t, s.Post("/admin/media/zzzzzzzz", c, url.Values{"alt": {"x"}}), http.StatusNotFound)
	apptest.Status(t, s.Get("/admin/media/zzzzzzzz", c), http.StatusNotFound)

	apptest.Redirect(t, s.Post(path+"/delete", c, url.Values{}), "/admin/media?done=deleted")
	apptest.Status(t, s.Get(path, c), http.StatusNotFound)
	apptest.Status(t, s.Post(path+"/delete", c, url.Values{}), http.StatusNotFound)
	if k := jobKinds(t, s); !slices.Equal(k, []string{"media_process", "media_delete_files"}) {
		t.Errorf("jobs %v", k)
	}
	rows, err := db.New(s.Pool).ListAudit(t.Context(), db.ListAuditParams{Lim: 10})
	if err != nil || len(rows) != 2 || rows[0].Action != "media.deleted" || rows[1].Action != "media.uploaded" {
		t.Errorf("audit %+v, %v", rows, err)
	}
}

func TestMediaForbidden(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	supportID, support := authtest.SignIn(t, s.Pool, "sam@example.com")
	authtest.SetPlatformRole(t, s.Pool, supportID, auth.Support)
	adminID, admin := authtest.SignIn(t, s.Pool, "ada@example.com")
	authtest.SetPlatformRole(t, s.Pool, adminID, auth.Superadmin)

	apptest.Status(t, s.Get("/admin/media", support), http.StatusForbidden)
	apptest.Status(t, upload(s, support, pngOf(t, 10, 10), "", nil), http.StatusForbidden)
	apptest.Status(t, s.Get("/admin/media", admin), http.StatusOK)
	apptest.Redirect(t, s.Get("/admin/media", nil), "/login?next=%2Fadmin%2Fmedia")
	if b := s.Get("/admin/accounts", admin).Body.String(); !strings.Contains(b, `href="/admin/media"`) {
		t.Error("no Media link in a superadmin's sidebar")
	}
}
