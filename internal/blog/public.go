package blog

import (
	"context"
	"encoding/xml"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/markdown"
	"github.com/riadafridishibly/quickstack/internal/media"
	"github.com/riadafridishibly/quickstack/internal/slug"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	blogPath     = "/blog"
	feedPath     = blogPath + "/feed.xml"
	pageSize     = 10
	feedSize     = 20
	publicTarget = "posts"
)

func postURL(slug string) string { return blogPath + "/" + slug }
func tagURL(tag string) string   { return blogPath + "/tags/" + tag }

type publicHandler struct {
	q         *db.Queries
	publicURL string
}

// Routes adds the public blog, its feed, and the site's sitemap. Drafts and
// scheduled posts are 404 to everyone; staff see them through the preview
// in /admin/blog.
func Routes(mux *web.Mux, pool *pgxpool.Pool, publicURL string) {
	h := publicHandler{q: db.New(pool), publicURL: publicURL}
	mux.Get("/blog", h.list)
	mux.Get("/blog/{slug}", h.post)
	mux.Get("/blog/tags/{tag}", h.tag)
	mux.Get("/blog/feed.xml", h.feed)
	mux.Get("/sitemap.xml", h.sitemap)
}

// cache lets anyone keep a page for a minute, so a scheduled post shows
// within a minute of its time.
func cache(w http.ResponseWriter) { w.Header().Set("Cache-Control", "public, max-age=60") }

func (h publicHandler) meta(path, description string) web.Meta {
	return web.Meta{Description: description, Canonical: h.publicURL + path, Feed: h.publicURL + feedPath}
}

type indexView struct {
	Tag   string
	Rows  []db.ListLivePostsRow
	Page  web.Page
	Title string
}

func (h publicHandler) index(w http.ResponseWriter, r *http.Request, v indexView) error {
	v.Page = web.PageFrom(r, pageSize)
	var err error
	v.Rows, err = web.LoadPage(&v.Page, func(p db.ListLivePostsRow) int64 { return p.Total },
		func(limit, offset int32) ([]db.ListLivePostsRow, error) {
			return h.q.ListLivePosts(r.Context(), db.ListLivePostsParams{Tag: v.Tag, Lim: limit, Off: offset})
		})
	if err != nil {
		return err
	}
	if v.Tag != "" && len(v.Rows) == 0 {
		return web.ErrNotFound
	}
	cache(w)
	if web.HXTarget(r) == publicTarget {
		return web.Render(w, r, http.StatusOK, postIndex(v))
	}
	path := blogPath
	if v.Tag != "" {
		path = tagURL(v.Tag)
	}
	if v.Page.Number > 1 {
		path += "?page=" + strconv.Itoa(v.Page.Number)
	}
	return web.Render(w, r, http.StatusOK, indexPage(v, h.meta(path, "Posts from the Quickstack team.")))
}

func (h publicHandler) list(w http.ResponseWriter, r *http.Request) error {
	return h.index(w, r, indexView{Title: "Blog"})
}

func (h publicHandler) tag(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	if !slug.Valid(tag) {
		return web.ErrNotFound
	}
	return h.index(w, r, indexView{Tag: tag, Title: "Posts tagged " + tag})
}

// article is a post as the site shows it.
type article struct {
	Post   db.Post
	Author string
	// Cover is the post's cover image, if it is ready.
	Cover    *db.Asset
	Body     template.HTML
	Problems []markdown.Problem
	// Preview marks a page that only staff see.
	Preview bool
}

func loadArticle(ctx context.Context, q *db.Queries, p db.Post, author *string) (article, error) {
	a := article{Post: p}
	if author != nil {
		a.Author = *author
	}
	if p.CoverID != nil {
		cover, err := q.GetAsset(ctx, *p.CoverID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return a, err
		}
		if err == nil && media.Showable(cover) {
			a.Cover = &cover
		}
	}
	var err error
	a.Body, a.Problems, err = markdown.Render(ctx, p.Body, q.ListAssetsByKeys)
	return a, err
}

// readyAsset returns the asset with key if it is ready to show, or nil.
func readyAsset(ctx context.Context, q *db.Queries, key string) (*db.Asset, error) {
	a, err := q.GetAssetByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || !media.Showable(a) {
		return nil, err
	}
	return &a, nil
}

func (h publicHandler) post(w http.ResponseWriter, r *http.Request) error {
	s := r.PathValue("slug")
	row, err := h.q.GetLivePost(r.Context(), s)
	if errors.Is(err, pgx.ErrNoRows) {
		current, err := h.q.GetRedirectedSlug(r.Context(), s)
		if err != nil {
			return web.NotFoundIfNoRows(err)
		}
		cache(w)
		http.Redirect(w, r, postURL(current), http.StatusMovedPermanently)
		return nil
	}
	if err != nil {
		return err
	}
	a, err := loadArticle(r.Context(), h.q, row.Post, row.AuthorName)
	if err != nil {
		return err
	}
	m := h.meta(postURL(a.Post.Slug), a.Post.Summary)
	m.Article = true
	if a.Cover != nil {
		m.Image = h.publicURL + media.FileURL(a.Cover.Key, media.CardFile)
	}
	cache(w)
	return web.Render(w, r, http.StatusOK, postPage(a, m))
}

type atomFeed struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Author  atomPerson  `xml:"author"`
	Entries []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Links      []atomLink     `xml:"link"`
	Author     *atomPerson    `xml:"author,omitempty"`
	Categories []atomCategory `xml:"category"`
	Summary    *atomText      `xml:"summary,omitempty"`
	Content    atomText       `xml:"content"`
}

// feed is the Atom feed of the latest live posts, with their full text.
func (h publicHandler) feed(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.q.ListRecentPosts(r.Context(), feedSize)
	if err != nil {
		return err
	}
	host := h.publicURL
	if u, err := url.Parse(h.publicURL); err == nil {
		host = u.Hostname()
	}
	f := atomFeed{
		Title: "Quickstack blog",
		ID:    h.publicURL + blogPath,
		Links: []atomLink{
			{Rel: "self", Type: "application/atom+xml", Href: h.publicURL + feedPath},
			{Rel: "alternate", Type: "text/html", Href: h.publicURL + blogPath},
		},
		Author: atomPerson{Name: "Quickstack"},
	}
	var newest time.Time
	for _, row := range rows {
		p := row.Post
		body, _, err := markdown.Render(r.Context(), p.Body, h.q.ListAssetsByKeys)
		if err != nil {
			return err
		}
		updated := p.UpdatedAt
		if p.PublishedAt.After(updated) {
			updated = *p.PublishedAt
		}
		if updated.After(newest) {
			newest = updated
		}
		e := atomEntry{
			Title: p.Title,
			// Stays the same when the slug changes.
			ID:        "tag:" + host + "," + p.CreatedAt.UTC().Format(time.DateOnly) + ":blog/" + strconv.FormatInt(p.ID, 10),
			Published: p.PublishedAt.UTC().Format(time.RFC3339),
			Updated:   updated.UTC().Format(time.RFC3339),
			Links:     []atomLink{{Rel: "alternate", Type: "text/html", Href: h.publicURL + postURL(p.Slug)}},
			Content:   atomText{Type: "html", Body: string(body)},
		}
		if row.AuthorName != nil {
			e.Author = &atomPerson{Name: *row.AuthorName}
		}
		if p.Summary != "" {
			e.Summary = &atomText{Type: "text", Body: p.Summary}
		}
		for _, t := range p.Tags {
			e.Categories = append(e.Categories, atomCategory{Term: t})
		}
		f.Entries = append(f.Entries, e)
	}
	if newest.IsZero() {
		newest = time.Now()
	}
	f.Updated = newest.UTC().Format(time.RFC3339)
	cache(w)
	return writeXML(w, "application/atom+xml; charset=utf-8", f)
}

type urlSet struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// sitemap lists the site's pages for search engines: the home page, the
// blog, and every live post.
func (h publicHandler) sitemap(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.q.ListLivePostDates(r.Context())
	if err != nil {
		return err
	}
	s := urlSet{URLs: []sitemapURL{{Loc: h.publicURL + "/"}, {Loc: h.publicURL + blogPath}}}
	for _, p := range rows {
		s.URLs = append(s.URLs, sitemapURL{Loc: h.publicURL + postURL(p.Slug), LastMod: p.Modified.UTC().Format(time.RFC3339)})
	}
	cache(w)
	return writeXML(w, "application/xml; charset=utf-8", s)
}

func writeXML(w http.ResponseWriter, contentType string, v any) error {
	b, err := xml.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write([]byte(xml.Header))
	_, err = w.Write(b)
	return err
}
