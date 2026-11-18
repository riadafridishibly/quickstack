package media

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	adminPath     = "/admin/media"
	listTarget    = "media-list"
	uploadTarget  = "upload-form"
	textTarget    = "asset-text"
	previewTarget = "asset-preview"

	maxAltLen    = 500
	maxCreditLen = 200
)

var variantRE = regexp.MustCompile(`^[1-9][0-9]{0,4}\.webp$`)

// Routes serves asset files at /media/{key}/{file}: the WebP variants and
// og.jpg. The original is never served; it keeps metadata such as where a
// photo was taken. Register it outside the session middleware.
func Routes(mux *http.ServeMux, store blob.Store) {
	mux.Handle("GET /media/{key}/{file}", web.Handle(func(w http.ResponseWriter, r *http.Request) error {
		key, name := r.PathValue("key"), r.PathValue("file")
		contentType := "image/webp"
		switch {
		case !ValidKey(key):
			return web.ErrNotFound
		case name == CardFile:
			contentType = "image/jpeg"
		case !variantRE.MatchString(name):
			return web.ErrNotFound
		}
		f, err := store.Open(r.Context(), key+"/"+name)
		if errors.Is(err, blob.ErrNotFound) {
			return web.ErrNotFound
		}
		if err != nil {
			return err
		}
		defer f.Close()
		h := w.Header()
		h.Set("Content-Type", contentType)
		// A key's files never change: a new upload gets a new key.
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, name, time.Time{}, f)
		return nil
	}))
}

type handler struct {
	pool  *pgxpool.Pool
	q     *db.Queries
	store blob.Store
	jc    *jobs.Client
}

// AdminRoutes adds /admin/media, where staff who manage the blog upload
// images and edit their alt text and credit.
func AdminRoutes(r *auth.PlatformRouter, pool *pgxpool.Pool, store blob.Store, jc *jobs.Client) {
	h := handler{pool: pool, q: db.New(pool), store: store, jc: jc}
	r.Handle("GET "+adminPath, auth.ManageBlog, h.list)
	r.Handle("POST "+adminPath, auth.ManageBlog, h.upload)
	r.Handle("GET "+adminPath+"/{key}", auth.ManageBlog, h.show)
	r.Handle("POST "+adminPath+"/{key}", auth.ManageBlog, h.update)
	r.Handle("POST "+adminPath+"/{key}/delete", auth.ManageBlog, h.delete)
}

func assetURL(key string) string { return adminPath + "/" + key }

type listView struct {
	Rows        []db.ListAssetsRow
	Page        web.Page
	UploadError string
	Message     string
}

func (h handler) loadList(r *http.Request, v *listView) (err error) {
	v.Page = web.PageFrom(r, web.PageSize)
	v.Rows, err = web.LoadPage(&v.Page, func(a db.ListAssetsRow) int64 { return a.Total },
		func(limit, offset int32) ([]db.ListAssetsRow, error) {
			return h.q.ListAssets(r.Context(), db.ListAssetsParams{Lim: limit, Off: offset})
		})
	return err
}

func (h handler) list(w http.ResponseWriter, r *http.Request) error {
	var v listView
	if err := h.loadList(r, &v); err != nil {
		return err
	}
	if r.URL.Query().Get("done") == "deleted" {
		v.Message = "Image deleted."
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, assetList(v))
	}
	return web.Render(w, r, http.StatusOK, listPage(v))
}

// uploaded is the JSON answer to an upload from the editor.
type uploaded struct {
	Key      string `json:"key"`
	URL      string `json:"url"`
	Markdown string `json:"markdown"`
	Status   string `json:"status"`
}

func (h handler) upload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBytes+1<<20)
	up, err := Check(r)
	if rej, ok := errors.AsType[*Rejected](err); ok {
		return h.rejectUpload(w, r, rej.Msg)
	}
	if err != nil {
		return err
	}
	me := auth.From(r.Context())
	var a db.Asset
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		if a, err = Create(r.Context(), tx, h.store, h.jc, me.ID, up); err != nil {
			return err
		}
		return db.New(tx).RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &me.ID, Action: "media.uploaded", Target: a.Key})
	})
	if err != nil {
		Discard(r.Context(), h.store, a)
		return err
	}
	if wantsJSON(r) {
		return writeJSON(w, http.StatusCreated, uploaded{Key: a.Key, URL: URL(a.Key), Markdown: Markdown(a.Key, a.Alt), Status: a.Status})
	}
	web.Redirect(w, r, assetURL(a.Key))
	return nil
}

func (h handler) rejectUpload(w http.ResponseWriter, r *http.Request, msg string) error {
	if wantsJSON(r) {
		return writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
	}
	if web.IsHTMX(r) {
		return web.Render(w, r, http.StatusUnprocessableEntity, uploadForm(msg))
	}
	v := listView{UploadError: msg}
	if err := h.loadList(r, &v); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusUnprocessableEntity, listPage(v))
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

type assetView struct {
	Asset   db.Asset
	Form    textForm
	Message string
}

type textForm struct {
	Alt    string
	Credit string
	Errors map[string]string
}

func textFormFrom(r *http.Request) textForm {
	return textForm{
		Alt:    strings.Join(strings.Fields(r.PostFormValue("alt")), " "),
		Credit: strings.TrimSpace(r.PostFormValue("credit")),
		Errors: map[string]string{},
	}
}

func (f *textForm) validate() bool {
	if utf8.RuneCountInString(f.Alt) > maxAltLen {
		f.Errors["alt"] = "Use at most 500 characters."
	}
	if utf8.RuneCountInString(f.Credit) > maxCreditLen {
		f.Errors["credit"] = "Use at most 200 characters."
	}
	return len(f.Errors) == 0
}

func (h handler) show(w http.ResponseWriter, r *http.Request) error {
	a, err := h.q.GetAssetByKey(r.Context(), r.PathValue("key"))
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if web.HXTarget(r) == previewTarget {
		return web.Render(w, r, http.StatusOK, preview(a))
	}
	v := assetView{Asset: a, Form: textForm{Alt: a.Alt, Credit: a.Credit, Errors: map[string]string{}}}
	if r.URL.Query().Get("done") == "saved" {
		v.Message = "Saved."
	}
	return web.Render(w, r, http.StatusOK, assetPage(v))
}

func (h handler) update(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	a, err := h.q.GetAssetByKey(r.Context(), key)
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	f := textFormFrom(r)
	if !f.validate() {
		if web.IsHTMX(r) {
			return web.Render(w, r, http.StatusUnprocessableEntity, textFormEl(a, f))
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, assetPage(assetView{Asset: a, Form: f}))
	}
	n, err := h.q.UpdateAssetText(r.Context(), db.UpdateAssetTextParams{Key: key, Alt: f.Alt, Credit: f.Credit})
	if err != nil {
		return err
	}
	if n == 0 {
		return web.ErrNotFound
	}
	web.Redirect(w, r, assetURL(key)+"?done=saved")
	return nil
}

func (h handler) delete(w http.ResponseWriter, r *http.Request) error {
	key := r.PathValue("key")
	me := auth.From(r.Context())
	err := pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := Delete(r.Context(), tx, h.jc, key); err != nil {
			return err
		}
		return db.New(tx).RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &me.ID, Action: "media.deleted", Target: key})
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	web.Redirect(w, r, adminPath+"?done=deleted")
	return nil
}

// Markdown is how a post embeds the asset with key.
func Markdown(key, alt string) string {
	alt = strings.NewReplacer(`\`, `\\`, "[", `\[`, "]", `\]`).Replace(alt)
	return "![" + alt + "](" + URL(key) + ")"
}
