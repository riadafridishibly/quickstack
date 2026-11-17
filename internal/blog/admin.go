package blog

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/markdown"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	adminPath     = "/admin/blog"
	previewPath   = adminPath + "/preview"
	listTarget    = "post-list"
	formTarget    = "post-form"
	previewTarget = "post-preview"
)

func editURL(id int64) string { return adminPath + "/" + strconv.FormatInt(id, 10) }

type adminHandler struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// AdminRoutes adds /admin/blog, where staff who may manage the blog write
// posts. Register it inside /admin.
func AdminRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	h := adminHandler{pool: pool, q: db.New(pool)}
	manage := auth.RequirePlatform(auth.ManageBlog)
	mux.Handle("GET "+adminPath, manage(web.Handle(h.list)))
	mux.Handle("GET "+adminPath+"/new", manage(web.Handle(h.newPost)))
	mux.Handle("POST "+adminPath, manage(web.Handle(h.create)))
	mux.Handle("POST "+previewPath, manage(web.Handle(h.previewDraft)))
	mux.Handle("GET "+adminPath+"/{id}", manage(web.Handle(h.edit)))
	mux.Handle("POST "+adminPath+"/{id}", manage(web.Handle(h.update)))
	mux.Handle("POST "+adminPath+"/{id}/delete", manage(web.Handle(h.delete)))
	mux.Handle("GET "+adminPath+"/{id}/preview", manage(web.Handle(h.preview)))
}

type listView struct {
	Rows    []db.ListPostsRow
	Query   string
	State   string
	Page    web.Page
	Now     time.Time
	Message string
}

func (h adminHandler) list(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	v := listView{Query: q.Get("q"), State: q.Get("state"), Page: web.PageFrom(r, web.PageSize), Now: time.Now()}
	switch v.State {
	case stateDraft, stateScheduled, statePublished:
	default:
		v.State = ""
	}
	var err error
	v.Rows, err = web.LoadPage(&v.Page, func(p db.ListPostsRow) int64 { return p.Total },
		func(limit, offset int32) ([]db.ListPostsRow, error) {
			return h.q.ListPosts(r.Context(), db.ListPostsParams{Query: db.EscapeLike(v.Query), State: v.State, Lim: limit, Off: offset})
		})
	if err != nil {
		return err
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, postList(v))
	}
	if q.Get("done") == "deleted" {
		v.Message = "Post deleted."
	}
	return web.Render(w, r, http.StatusOK, listPage(v))
}

// editView is the post form. Stored is the post as saved, or nil for a new
// one.
type editView struct {
	Stored  *db.Post
	Form    postForm
	Message string
	// Images are the newest in Media, to copy into the body.
	Images []db.ListAssetsRow
}

const pickerImages = 12

// render renders the whole edit page.
func (h adminHandler) render(w http.ResponseWriter, r *http.Request, status int, v editView) error {
	var err error
	if v.Images, err = h.q.ListAssets(r.Context(), db.ListAssetsParams{Lim: pickerImages}); err != nil {
		return err
	}
	return web.Render(w, r, status, editPage(v))
}

func (v editView) action() string {
	if v.Stored == nil {
		return adminPath
	}
	return editURL(v.Stored.ID)
}

func (h adminHandler) newPost(w http.ResponseWriter, r *http.Request) error {
	return h.render(w, r, http.StatusOK, editView{Form: postForm{Errors: map[string]string{}}})
}

func (h adminHandler) create(w http.ResponseWriter, r *http.Request) error {
	f := postFormFrom(r)
	if !f.validate() {
		return h.renderForm(w, r, editView{Form: f})
	}
	me := auth.From(r.Context())
	var p db.Post
	err := pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		p, err = save(r.Context(), tx, me.ID, nil, &f)
		return err
	})
	if errors.Is(err, errInvalid) {
		return h.renderForm(w, r, editView{Form: f})
	}
	if err != nil {
		return err
	}
	web.Redirect(w, r, editURL(p.ID)+"?done="+doneFor(f.Action))
	return nil
}

func doneFor(action string) string {
	switch action {
	case actionPublish:
		return "published"
	case actionUnpublish:
		return "unpublished"
	}
	return "saved"
}

// renderForm re-renders a form that wasn't saved: the form alone for htmx,
// the whole page otherwise.
func (h adminHandler) renderForm(w http.ResponseWriter, r *http.Request, v editView) error {
	if web.IsHTMX(r) {
		return web.Render(w, r, http.StatusUnprocessableEntity, postFormEl(v))
	}
	return h.render(w, r, http.StatusUnprocessableEntity, v)
}

func (h adminHandler) edit(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	row, err := h.q.GetPost(r.Context(), id)
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	v := editView{Stored: &row.Post, Form: postFormOf(row.Post, row.CoverKey)}
	switch r.URL.Query().Get("done") {
	case "saved":
		v.Message = "Saved."
	case "published":
		if live(row.Post, time.Now()) {
			v.Message = "Published."
		} else {
			v.Message = "Scheduled."
		}
	case "unpublished":
		v.Message = "Unpublished. The post is a draft again."
	}
	return h.render(w, r, http.StatusOK, v)
}

func (h adminHandler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	f := postFormFrom(r)
	ok := f.validate()
	me := auth.From(r.Context())
	var old db.Post
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		// Checked first, so a missing post is a 404 even when the form is
		// invalid.
		if old, err = db.New(tx).LockPost(r.Context(), id); err != nil || !ok {
			return err
		}
		if !old.UpdatedAt.Equal(f.updatedAt) {
			return errConflict
		}
		_, err = save(r.Context(), tx, me.ID, &old, &f)
		return err
	})
	switch {
	case errors.Is(err, errConflict):
		// Saving again replaces the other change, now that the editor has
		// been told about it.
		f.Conflict = true
		f.UpdatedAt = old.UpdatedAt.Format(time.RFC3339Nano)
		fallthrough
	case errors.Is(err, errInvalid), err == nil && !ok:
		return h.renderForm(w, r, editView{Stored: &old, Form: f})
	case err != nil:
		return web.NotFoundIfNoRows(err)
	}
	web.Redirect(w, r, editURL(id)+"?done="+doneFor(f.Action))
	return nil
}

func (h adminHandler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	me := auth.From(r.Context())
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		slug, err := q.DeletePost(r.Context(), id)
		if err != nil {
			return err
		}
		return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &me.ID, Action: "blog.deleted", Target: slug})
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	web.Redirect(w, r, adminPath+"?done=deleted")
	return nil
}

type previewView struct {
	HTML     template.HTML
	Problems []markdown.Problem
	// Pending says an image is still processing.
	Pending bool
}

// previewDraft renders the body of the form as it is being written: the
// rendered body and its problems for htmx, the post in the public layout
// otherwise.
func (h adminHandler) previewDraft(w http.ResponseWriter, r *http.Request) error {
	f := postFormFrom(r)
	html, problems, pending, err := markdown.Draft(r.Context(), f.Body, h.q.ListAssetsByKeys)
	if err != nil {
		return err
	}
	if web.IsHTMX(r) {
		return web.Render(w, r, http.StatusOK, previewPane(previewView{HTML: html, Problems: problems, Pending: pending}))
	}
	f.validate()
	if f.Title == "" {
		f.Title = "Untitled"
	}
	a := article{
		Post: db.Post{Title: f.Title, Summary: f.Summary, Status: Draft, Tags: f.tags},
		Body: html, Problems: problems, Preview: true,
	}
	if f.coverKey != "" {
		if a.Cover, err = readyAsset(r.Context(), h.q, f.coverKey); err != nil {
			return err
		}
	}
	return web.Render(w, r, http.StatusOK, postPage(a, web.Meta{NoIndex: true}))
}

// preview shows a saved post as it will look on the site, whether or not it
// is live.
func (h adminHandler) preview(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	row, err := h.q.GetPost(r.Context(), id)
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	a, err := loadArticle(r.Context(), h.q, row.Post, row.AuthorName)
	if err != nil {
		return err
	}
	a.Preview = true
	return web.Render(w, r, http.StatusOK, postPage(a, web.Meta{NoIndex: true}))
}
