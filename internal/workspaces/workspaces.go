// Package workspaces holds the tenant layer every workspace page sits in:
// the middleware that resolves /w/{ws} for a member, the workspace Layout,
// the list of a person's workspaces, settings, members and invitations.
package workspaces

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/slug"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

var ErrSlugTaken = errors.New("workspaces: slug taken")

// Middleware resolves the {ws} path value to a workspace the signed-in user
// is a member of, and puts it and their role in the request context. Anyone
// else gets a 404, so the URL doesn't reveal that the workspace exists.
// Platform staff are no exception: /admin shows workspaces without their
// projects.
func Middleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	q := db.New(pool)
	return func(next http.Handler) http.Handler {
		return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
			u := auth.From(r.Context())
			if u == nil {
				return web.ErrNotFound
			}
			m, err := q.GetMembership(r.Context(), db.GetMembershipParams{AccountID: u.ID, Slug: r.PathValue("ws")})
			if err != nil {
				return web.NotFoundIfNoRows(err)
			}
			ctx := tenant.WithWorkspace(r.Context(), &tenant.Workspace{ID: m.ID, Slug: m.Slug, Name: m.Name, Public: m.Public})
			ctx = auth.WithRole(ctx, auth.Role(m.Role))
			next.ServeHTTP(w, r.WithContext(ctx))
			return nil
		})
	}
}

// Create makes a workspace owned by ownerID. Run it in a transaction.
func Create(ctx context.Context, q *db.Queries, name, s string, ownerID int64) (db.Workspace, error) {
	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{Slug: s, Name: name})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return ws, ErrSlugTaken
	}
	if err != nil {
		return ws, err
	}
	if err := q.AddMember(ctx, db.AddMemberParams{WorkspaceID: ws.ID, AccountID: ownerID, Role: string(auth.Owner)}); err != nil {
		return ws, err
	}
	err = q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &ownerID, WorkspaceID: &ws.ID, Action: "workspace.created", Target: ws.Slug})
	return ws, err
}

type handler struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	jobs      *jobs.Client
	publicURL string
}

func newHandler(pool *pgxpool.Pool, jc *jobs.Client, publicURL string) handler {
	return handler{pool: pool, q: db.New(pool), jobs: jc, publicURL: publicURL}
}

// Routes adds the pages outside any one workspace, which need a signed-in
// user: the list of workspaces, and creating one.
func Routes(mux *http.ServeMux, pool *pgxpool.Pool, jc *jobs.Client, publicURL string) {
	h := newHandler(pool, jc, publicURL)
	mux.Handle("GET "+auth.HomePath, web.Handle(h.list))
	mux.Handle("POST "+auth.HomePath, web.Handle(h.create))
}

// InviteRoutes adds the invitation pages, which anyone with the link can
// open. Wrap mux in auth.OptionalLogin.
func InviteRoutes(mux *http.ServeMux, pool *pgxpool.Pool, jc *jobs.Client, publicURL string) {
	h := newHandler(pool, jc, publicURL)
	mux.Handle("GET /invite/{token}", web.Handle(h.invitation))
	mux.Handle("POST /invite/{token}", web.Handle(h.accept))
}

// WorkspaceRoutes adds a workspace's members and settings pages to mux,
// which serves behind Middleware.
func WorkspaceRoutes(mux *http.ServeMux, pool *pgxpool.Pool, jc *jobs.Client, publicURL string) {
	h := newHandler(pool, jc, publicURL)
	manage := auth.Require(auth.ManageMembers)
	mux.Handle("GET /w/{ws}/members", web.Handle(h.members))
	mux.Handle("POST /w/{ws}/members/invite", manage(web.Handle(h.invite)))
	mux.Handle("POST /w/{ws}/members/{id}/role", manage(web.Handle(h.changeRole)))
	// Removing someone needs ManageMembers, unless they remove themselves.
	mux.Handle("POST /w/{ws}/members/{id}/delete", web.Handle(h.removeMember))
	mux.Handle("POST /w/{ws}/invitations/{id}/delete", manage(web.Handle(h.revokeInvitation)))

	settings := auth.Require(auth.ManageWorkspace)
	mux.Handle("GET /w/{ws}/settings", settings(web.Handle(h.settings)))
	mux.Handle("POST /w/{ws}/settings", settings(web.Handle(h.saveSettings)))
}

type workspaceForm struct {
	Name   string
	Slug   string
	Public bool
	Errors map[string]string
}

func workspaceFormFrom(r *http.Request) workspaceForm {
	f := workspaceForm{
		Name:   strings.TrimSpace(r.PostFormValue("name")),
		Slug:   strings.ToLower(strings.TrimSpace(r.PostFormValue("slug"))),
		Public: r.PostFormValue("public") == "on",
		Errors: map[string]string{},
	}
	if f.Slug == "" {
		f.Slug = slug.Make(f.Name)
	}
	return f
}

func (f *workspaceForm) validate() bool {
	switch {
	case f.Name == "":
		f.Errors["name"] = "Enter a name."
	case utf8.RuneCountInString(f.Name) > 80:
		f.Errors["name"] = "Use at most 80 characters."
	}
	if !slug.Valid(f.Slug) {
		f.Errors["slug"] = "Use lowercase letters, digits and single hyphens, such as acme-robotics."
	}
	return len(f.Errors) == 0
}

func (h handler) list(w http.ResponseWriter, r *http.Request) error {
	ws, err := h.q.ListMyWorkspaces(r.Context(), auth.From(r.Context()).ID)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, listPage(ws, workspaceForm{Errors: map[string]string{}}))
}

func (h handler) create(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	f := workspaceFormFrom(r)
	render := func() error {
		if web.IsHTMX(r) {
			return web.Render(w, r, http.StatusUnprocessableEntity, createForm(f))
		}
		ws, err := h.q.ListMyWorkspaces(r.Context(), u.ID)
		if err != nil {
			return err
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, listPage(ws, f))
	}
	if !f.validate() {
		return render()
	}
	var ws db.Workspace
	err := pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		ws, err = Create(r.Context(), db.New(tx), f.Name, f.Slug, u.ID)
		return err
	})
	if errors.Is(err, ErrSlugTaken) {
		f.Errors["slug"] = "Another workspace uses this URL. Choose another."
		return render()
	}
	if err != nil {
		return err
	}
	web.Redirect(w, r, "/w/"+ws.Slug)
	return nil
}

func (h handler) settings(w http.ResponseWriter, r *http.Request) error {
	ws := tenant.From(r.Context())
	f := workspaceForm{Name: ws.Name, Slug: ws.Slug, Public: ws.Public, Errors: map[string]string{}}
	return web.Render(w, r, http.StatusOK, settingsPage(f, r.URL.Query().Get("saved") == "1"))
}

func (h handler) saveSettings(w http.ResponseWriter, r *http.Request) error {
	ws := tenant.From(r.Context())
	u := auth.From(r.Context())
	f := workspaceFormFrom(r)
	if f.validate() {
		err := pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			q := db.New(tx)
			_, err := q.UpdateWorkspace(r.Context(), db.UpdateWorkspaceParams{ID: ws.ID, Name: f.Name, Slug: f.Slug, Public: f.Public})
			if err != nil {
				return err
			}
			return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &u.ID, WorkspaceID: &ws.ID, Action: "workspace.updated", Target: f.Slug})
		})
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
			f.Errors["slug"] = "Another workspace uses this URL. Choose another."
		} else if err != nil {
			return err
		} else {
			web.Redirect(w, r, "/w/"+f.Slug+"/settings?saved=1")
			return nil
		}
	}
	return web.Render(w, r, http.StatusUnprocessableEntity, settingsPage(f, false))
}
