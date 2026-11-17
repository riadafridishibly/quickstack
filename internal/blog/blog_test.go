package blog_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
)

func staff(t *testing.T, s *apptest.Server, email string, role auth.PlatformRole) *http.Cookie {
	t.Helper()
	id, c := authtest.SignIn(t, s.Pool, email)
	authtest.SetPlatformRole(t, s.Pool, id, role)
	return c
}

// create posts a new post and returns its id.
func create(t *testing.T, s *apptest.Server, c *http.Cookie, form url.Values) int64 {
	t.Helper()
	w := s.Post("/admin/blog", c, form)
	apptest.Status(t, w, http.StatusSeeOther)
	loc, _, _ := strings.Cut(strings.TrimPrefix(w.Header().Get("Location"), "/admin/blog/"), "?")
	id, err := strconv.ParseInt(loc, 10, 64)
	if err != nil {
		t.Fatalf("redirect to %q", w.Header().Get("Location"))
	}
	return id
}

func get(t *testing.T, s *apptest.Server, id int64) db.Post {
	t.Helper()
	row, err := db.New(s.Pool).GetPost(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row.Post
}

// edit posts form over the post's current fields.
func edit(s *apptest.Server, c *http.Cookie, p db.Post, form url.Values, target string) *httptest.ResponseRecorder {
	base := url.Values{
		"title":      {p.Title},
		"slug":       {p.Slug},
		"summary":    {p.Summary},
		"body":       {p.Body},
		"tags":       {strings.Join(p.Tags, ",")},
		"updated_at": {p.UpdatedAt.Format(time.RFC3339Nano)},
	}
	if p.PublishedAt != nil {
		base.Set("published_at", p.PublishedAt.UTC().Format("2006-01-02T15:04"))
	}
	for k, v := range form {
		base[k] = v
	}
	return s.Do(apptest.Req{Path: "/admin/blog/" + strconv.FormatInt(p.ID, 10), Cookie: c, Form: base, Target: target})
}

func audit(t *testing.T, s *apptest.Server) []string {
	t.Helper()
	rows, err := db.New(s.Pool).ListAudit(t.Context(), db.ListAuditParams{Lim: 50})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i].Action+" "+rows[i].Target)
	}
	return out
}

func TestWritePost(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)

	apptest.Status(t, s.Get("/admin/blog", editor), http.StatusOK)
	apptest.Status(t, s.Get("/admin/blog/new", editor), http.StatusOK)
	id := create(t, s, editor, url.Values{"title": {"Hello, world!"}, "body": {"First *post*."}, "tags": {"News, Go, news"}})
	p := get(t, s, id)
	if p.Slug != "hello-world" || p.Status != "draft" || p.PublishedAt != nil || strings.Join(p.Tags, ",") != "news,go" {
		t.Fatalf("created %+v", p)
	}
	apptest.Status(t, s.Get("/blog/hello-world", nil), http.StatusNotFound)
	apptest.Status(t, s.Get("/blog/hello-world", editor), http.StatusNotFound)
	if b := s.Get("/admin/blog/"+strconv.FormatInt(id, 10)+"/preview", editor).Body.String(); !strings.Contains(b, "<em>post</em>") || !strings.Contains(b, `name="robots" content="noindex"`) {
		t.Errorf("preview:\n%s", b)
	}

	apptest.Redirect(t, edit(s, editor, p, url.Values{"title": {"Hello again"}}, ""), "/admin/blog/"+strconv.FormatInt(id, 10)+"?done=saved")
	p = get(t, s, id)
	if p.Title != "Hello again" || p.Slug != "hello-world" {
		t.Fatalf("edited %+v", p)
	}

	w := edit(s, editor, p, url.Values{"action": {"publish"}}, "")
	apptest.Status(t, w, http.StatusSeeOther)
	p = get(t, s, id)
	if p.Status != "published" || p.PublishedAt == nil || time.Since(*p.PublishedAt) > time.Minute {
		t.Fatalf("published %+v", p)
	}
	w = s.Get("/blog/hello-world", nil)
	apptest.Status(t, w, http.StatusOK)
	if b := w.Body.String(); !strings.Contains(b, "Hello again") || !strings.Contains(b, "<em>post</em>") || !strings.Contains(b, `<link rel="canonical" href="http://quickstack.test/blog/hello-world">`) {
		t.Errorf("post page:\n%s", b)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control %q", cc)
	}
	apptest.Status(t, s.Get("/blog/tags/go", nil), http.StatusOK)
	apptest.Status(t, s.Get("/blog/tags/rust", nil), http.StatusNotFound)

	apptest.Status(t, edit(s, editor, p, url.Values{"action": {"unpublish"}}, ""), http.StatusSeeOther)
	if p = get(t, s, id); p.Status != "draft" {
		t.Fatalf("unpublished %+v", p)
	}
	apptest.Status(t, s.Get("/blog/hello-world", nil), http.StatusNotFound)

	apptest.Redirect(t, s.Post("/admin/blog/"+strconv.FormatInt(id, 10)+"/delete", editor, url.Values{}), "/admin/blog?done=deleted")
	apptest.Status(t, s.Get("/admin/blog/"+strconv.FormatInt(id, 10), editor), http.StatusNotFound)
	apptest.Status(t, s.Post("/admin/blog/"+strconv.FormatInt(id, 10)+"/delete", editor, url.Values{}), http.StatusNotFound)

	want := []string{"blog.published hello-world", "blog.unpublished hello-world", "blog.deleted hello-world"}
	if got := audit(t, s); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit %q, want %q", got, want)
	}
}

func TestBlogAdminIsForEditors(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	admin := staff(t, s, "ada@example.com", auth.Superadmin)
	support := staff(t, s, "sam@example.com", auth.Support)
	id := create(t, s, admin, url.Values{"title": {"Plans"}})
	post := "/admin/blog/" + strconv.FormatInt(id, 10)
	apptest.Status(t, s.Get(post, editor), http.StatusOK)

	form := url.Values{"title": {"Mine"}}
	for _, r := range []apptest.Req{
		{Path: "/admin/blog"}, {Path: "/admin/blog/new"}, {Path: post}, {Path: post + "/preview"},
		{Path: "/admin/blog", Form: form}, {Path: post, Form: form}, {Path: post + "/delete", Form: url.Values{}},
		{Path: "/admin/blog/preview", Form: url.Values{"body": {"x"}}},
	} {
		r.Cookie = support
		apptest.Status(t, s.Do(r), http.StatusForbidden)
		r.Cookie = nil
		if w := s.Do(r); w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/login") {
			t.Errorf("%s signed out: %d %s", r.Path, w.Code, w.Header().Get("Location"))
		}
	}
	if b := s.Get("/admin/blog", editor).Body.String(); !strings.Contains(b, `href="/admin/blog"`) {
		t.Error("no Blog link in the sidebar")
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	create(t, s, editor, url.Values{"title": {"Taken"}})
	id := create(t, s, editor, url.Values{"title": {"Mine"}})
	p := get(t, s, id)

	for _, tc := range []struct {
		name, msg string
		form      url.Values
	}{
		{"no title", "Enter a title.", url.Values{"title": {" "}, "slug": {"x"}}},
		{"bad slug", "Use lowercase letters", url.Values{"title": {"A"}, "slug": {"Not A Slug"}}},
		{"taken slug", "Another post has this slug.", url.Values{"title": {"A"}, "slug": {"taken"}}},
		{"no cover", "There is no image /media/ab12cd34 in Media.", url.Values{"title": {"A"}, "cover": {"/media/ab12cd34"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := s.Post("/admin/blog", editor, tc.form)
			apptest.Status(t, w, http.StatusUnprocessableEntity)
			if b := w.Body.String(); !strings.Contains(b, tc.msg) || !strings.Contains(b, "<html") {
				t.Errorf("plain create:\n%s", b)
			}
			w = s.Do(apptest.Req{Path: "/admin/blog", Cookie: editor, Form: tc.form, Target: "post-form"})
			apptest.Status(t, w, http.StatusUnprocessableEntity)
			if b := w.Body.String(); !strings.Contains(b, tc.msg) || strings.Contains(b, "<html") {
				t.Errorf("htmx create:\n%s", b)
			}
			w = edit(s, editor, p, tc.form, "post-form")
			apptest.Status(t, w, http.StatusUnprocessableEntity)
			if b := w.Body.String(); !strings.Contains(b, tc.msg) || strings.Contains(b, "<html") {
				t.Errorf("htmx edit:\n%s", b)
			}
			apptest.Status(t, edit(s, editor, p, tc.form, ""), http.StatusUnprocessableEntity)
		})
	}
	apptest.Status(t, edit(s, editor, db.Post{ID: id + 100, Title: "Gone"}, nil, ""), http.StatusNotFound)
	if got := get(t, s, id); got.Title != "Mine" {
		t.Errorf("saved %+v", got)
	}
}

func TestScheduled(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	soon := time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04")
	id := create(t, s, editor, url.Values{"title": {"Soon"}, "published_at": {soon}, "action": {"publish"}})
	if p := get(t, s, id); p.Status != "published" {
		t.Fatalf("scheduled %+v", p)
	}
	apptest.Status(t, s.Get("/blog/soon", nil), http.StatusNotFound)
	if b := s.Get("/blog/feed.xml", nil).Body.String(); strings.Contains(b, "Soon") {
		t.Errorf("feed lists a scheduled post:\n%s", b)
	}
	w := s.Do(apptest.Req{Path: "/admin/blog?state=scheduled", Cookie: editor, Target: "post-list"})
	if b := w.Body.String(); !strings.Contains(b, "Soon") || strings.Contains(b, "<html") {
		t.Errorf("scheduled list:\n%s", b)
	}

	if _, err := s.Pool.Exec(t.Context(), "UPDATE posts SET published_at = now() - interval '1 minute' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	apptest.Status(t, s.Get("/blog/soon", nil), http.StatusOK)
}

func TestRenamedSlugRedirects(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	id := create(t, s, editor, url.Values{"title": {"First name"}, "action": {"publish"}})
	apptest.Status(t, edit(s, editor, get(t, s, id), url.Values{"slug": {"second-name"}}, ""), http.StatusSeeOther)
	apptest.Status(t, edit(s, editor, get(t, s, id), url.Values{"slug": {"third-name"}}, ""), http.StatusSeeOther)

	for _, old := range []string{"/blog/first-name", "/blog/second-name"} {
		w := s.Get(old, nil)
		apptest.Status(t, w, http.StatusMovedPermanently)
		if loc := w.Header().Get("Location"); loc != "/blog/third-name" {
			t.Errorf("%s redirects to %q", old, loc)
		}
	}
	apptest.Status(t, s.Get("/blog/third-name", nil), http.StatusOK)

	// A new post may take an old slug, which stops redirecting.
	create(t, s, editor, url.Values{"title": {"First name"}, "action": {"publish"}})
	apptest.Status(t, s.Get("/blog/first-name", nil), http.StatusOK)

	apptest.Status(t, edit(s, editor, get(t, s, id), url.Values{"action": {"unpublish"}}, ""), http.StatusSeeOther)
	apptest.Status(t, s.Get("/blog/second-name", nil), http.StatusNotFound)
}

func TestConcurrentSave(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	id := create(t, s, editor, url.Values{"title": {"Shared"}})
	opened := get(t, s, id)
	apptest.Status(t, edit(s, editor, opened, url.Values{"body": {"Theirs"}}, ""), http.StatusSeeOther)

	w := edit(s, editor, opened, url.Values{"body": {"Mine"}}, "post-form")
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if b := w.Body.String(); !strings.Contains(b, "changed since you opened it") || !strings.Contains(b, "Mine") {
		t.Errorf("conflict:\n%s", b)
	}
	if p := get(t, s, id); p.Body != "Theirs" {
		t.Fatalf("body %q", p.Body)
	}
	apptest.Status(t, edit(s, editor, opened, url.Values{"body": {"Mine"}, "updated_at": {""}}, ""), http.StatusUnprocessableEntity)
	apptest.Status(t, edit(s, editor, get(t, s, id), url.Values{"body": {"Mine"}}, ""), http.StatusSeeOther)
}

func TestPreview(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	form := url.Values{"title": {"Draft"}, "body": {"Some **bold** text.\n\n{{< nope >}}"}}
	w := s.Do(apptest.Req{Path: "/admin/blog/preview", Cookie: editor, Form: form, Target: "post-preview"})
	apptest.Status(t, w, http.StatusOK)
	if b := w.Body.String(); !strings.Contains(b, "<strong>bold</strong>") || !strings.Contains(b, "line 3") || strings.Contains(b, "<html") {
		t.Errorf("preview fragment:\n%s", b)
	}
	w = s.Post("/admin/blog/preview", editor, form)
	apptest.Status(t, w, http.StatusOK)
	if b := w.Body.String(); !strings.Contains(b, "<strong>bold</strong>") || !strings.Contains(b, "noindex") || !strings.Contains(b, "src/public.css") {
		t.Errorf("preview page:\n%s", b)
	}
}

func TestFeedAndSitemap(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	create(t, s, editor, url.Values{"title": {"Live one"}, "summary": {"About it"}, "body": {"Hi <there> & all"}, "action": {"publish"}})
	create(t, s, editor, url.Values{"title": {"Draft one"}})

	w := s.Get("/blog/feed.xml", nil)
	apptest.Status(t, w, http.StatusOK)
	b := w.Body.String()
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/atom+xml") {
		t.Errorf("feed Content-Type %q", ct)
	}
	if !strings.Contains(b, `<link rel="alternate" type="text/html" href="http://quickstack.test/blog/live-one"></link>`) {
		t.Errorf("feed has no link to the post:\n%s", b)
	}
	if !strings.Contains(b, "<summary type=\"text\">About it</summary>") || strings.Contains(b, "Draft one") {
		t.Errorf("feed:\n%s", b)
	}

	w = s.Get("/sitemap.xml", nil)
	apptest.Status(t, w, http.StatusOK)
	b = w.Body.String()
	if !strings.Contains(b, "<loc>http://quickstack.test/blog/live-one</loc>") || strings.Contains(b, "draft-one") {
		t.Errorf("sitemap:\n%s", b)
	}

	w = s.Get("/blog", nil)
	apptest.Status(t, w, http.StatusOK)
	if b := w.Body.String(); !strings.Contains(b, "Live one") || strings.Contains(b, "Draft one") || !strings.Contains(b, `type="application/atom+xml"`) {
		t.Errorf("blog index:\n%s", b)
	}
}

func TestPostMedia(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	editor := staff(t, s, "eve@example.com", auth.Editor)
	q := db.New(s.Pool)
	var ids []int64
	for _, key := range []string{"frst2345", "secnd234"} {
		a, err := q.CreateAsset(t.Context(), db.CreateAssetParams{Key: key, ContentType: "image/png", Sha256: []byte{0}, Width: 1, Height: 1})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	used := func(postID int64) []int64 {
		t.Helper()
		rows, err := s.Pool.Query(t.Context(), "SELECT asset_id FROM post_media WHERE post_id = $1 ORDER BY asset_id", postID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	id := create(t, s, editor, url.Values{"title": {"Pictures"}, "body": {"![a](/media/frst2345)\n\n![b](/media/frst2345) ![gone](/media/zzzz2345)"}})
	if got := used(id); !slices.Equal(got, ids[:1]) {
		t.Errorf("post_media %v, want %v", got, ids[:1])
	}
	apptest.Status(t, edit(s, editor, get(t, s, id), url.Values{"body": {`{{< figure src="/media/secnd234" >}}`}}, ""), http.StatusSeeOther)
	if got := used(id); !slices.Equal(got, ids[1:]) {
		t.Errorf("post_media %v, want %v", got, ids[1:])
	}
}
