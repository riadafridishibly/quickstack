// Package platform is the dashboard for the people who run the service, at
// /admin: accounts, workspaces and the audit log. It reads platform tables
// only. Workspace data stays behind row-level security, so staff see that a
// workspace exists and who is in it, not its projects.
package platform

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/chart"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	listTarget = "admin-list"
	homePath   = "/admin"
)

var errOwnRole = errors.New("platform: own role")

type handler struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// Routes adds the admin pages to mux. Wrap it in auth.RequirePlatform(auth.ViewPlatform).
func Routes(mux *http.ServeMux, pool *pgxpool.Pool) {
	h := handler{pool: pool, q: db.New(pool)}
	mux.Handle("GET "+homePath, web.Handle(h.home))
	mux.Handle("GET /admin/accounts", web.Handle(h.accounts))
	mux.Handle("GET /admin/accounts/{id}", web.Handle(h.account))
	mux.Handle("POST /admin/accounts/{id}/role", auth.RequirePlatform(auth.ManagePlatformRoles)(web.Handle(h.setRole)))
	mux.Handle("POST /admin/accounts/{id}/sign-out", auth.RequirePlatform(auth.SignOutAccounts)(web.Handle(h.signOut)))
	mux.Handle("GET /admin/workspaces", web.Handle(h.workspaces))
	mux.Handle("GET /admin/workspaces/{id}", web.Handle(h.workspace))
	mux.Handle("GET /admin/audit", web.Handle(h.audit))
}

type homeView struct {
	Counts  db.PlatformCountsRow
	Signups chart.Data
}

func (h handler) home(w http.ResponseWriter, r *http.Request) error {
	var v homeView
	var err error
	if v.Counts, err = h.q.PlatformCounts(r.Context()); err != nil {
		return err
	}
	since := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -29)
	rows, err := h.q.DailySignups(r.Context(), since)
	if err != nil {
		return err
	}
	v.Signups = chart.Data{
		Title: "New accounts", Note: "Per day, last 30 days, UTC", Empty: "Nobody signed up in the last 30 days.",
		Kind: chart.Columns, Format: chart.CountFormat,
	}
	accounts := make([]*float64, len(rows))
	for i, row := range rows {
		v.Signups.Days = append(v.Signups.Days, row.Day)
		accounts[i] = chart.Value(row.Accounts)
	}
	v.Signups.Series = []chart.Series{chart.WithValues(chart.Slot1, "Accounts", accounts)}
	return web.Render(w, r, http.StatusOK, homePage(v))
}

type accountsView struct {
	Rows      []db.ListAccountsRow
	Query     string
	StaffOnly bool
	Page      web.Page
}

func (h handler) accounts(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	v := accountsView{Query: q.Get("q"), StaffOnly: q.Get("staff") == "1", Page: web.PageFrom(r, web.PageSize)}
	var err error
	v.Rows, err = web.LoadPage(&v.Page, func(a db.ListAccountsRow) int64 { return a.Total },
		func(limit, offset int32) ([]db.ListAccountsRow, error) {
			return h.q.ListAccounts(r.Context(), db.ListAccountsParams{Query: db.EscapeLike(v.Query), StaffOnly: v.StaffOnly, Lim: limit, Off: offset})
		})
	if err != nil {
		return err
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, accountList(v))
	}
	return web.Render(w, r, http.StatusOK, accountsPage(v))
}

type accountView struct {
	Account     db.Account
	Memberships []db.ListAccountMembershipsRow
	Sessions    []db.ListSessionsRow
	Message     string
	Error       string
}

func (h handler) loadAccount(r *http.Request, v *accountView) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	if v.Account, err = h.q.GetAccount(r.Context(), id); err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if v.Memberships, err = h.q.ListAccountMemberships(r.Context(), id); err != nil {
		return err
	}
	v.Sessions, err = h.q.ListSessions(r.Context(), id)
	return err
}

func (h handler) account(w http.ResponseWriter, r *http.Request) error {
	var v accountView
	if err := h.loadAccount(r, &v); err != nil {
		return err
	}
	switch r.URL.Query().Get("done") {
	case "role":
		v.Message = "Platform role saved."
	case "signed-out":
		v.Message = "Signed out everywhere."
	}
	return web.Render(w, r, http.StatusOK, accountPage(v))
}

// setRole gives an account a platform role, or takes it away. Nobody
// changes their own, so the last superadmin can't lock everyone out by
// accident.
func (h handler) setRole(w http.ResponseWriter, r *http.Request) error {
	me := auth.From(r.Context())
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	var role *string
	if s := r.PostFormValue("role"); s != "" {
		if !auth.PlatformRole(s).Valid() {
			return web.ErrNotFound
		}
		role = &s
	}
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if id == me.ID {
			return errOwnRole
		}
		q := db.New(tx)
		acc, err := q.GetAccount(r.Context(), id)
		if err != nil {
			return err
		}
		if _, err := q.SetPlatformRole(r.Context(), db.SetPlatformRoleParams{ID: id, PlatformRole: role}); err != nil {
			return err
		}
		target := acc.Email + " → none"
		if role != nil {
			target = acc.Email + " → " + *role
		}
		return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &me.ID, Action: "platform.role_changed", Target: target})
	})
	if errors.Is(err, errOwnRole) {
		v := accountView{Error: "You can’t change your own platform role. Ask another superadmin."}
		if err := h.loadAccount(r, &v); err != nil {
			return err
		}
		return web.Render(w, r, http.StatusUnprocessableEntity, accountPage(v))
	}
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	web.Redirect(w, r, accountURL(id)+"?done=role")
	return nil
}

func (h handler) signOut(w http.ResponseWriter, r *http.Request) error {
	me := auth.From(r.Context())
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	err = pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		acc, err := q.GetAccount(r.Context(), id)
		if err != nil {
			return err
		}
		if _, err := q.DeleteAccountSessions(r.Context(), id); err != nil {
			return err
		}
		return q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &me.ID, Action: "platform.signed_out", Target: acc.Email})
	})
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	if id == me.ID {
		web.Redirect(w, r, auth.LoginPath)
		return nil
	}
	web.Redirect(w, r, accountURL(id)+"?done=signed-out")
	return nil
}

type workspacesView struct {
	Rows  []db.ListWorkspacesRow
	Query string
	Page  web.Page
}

func (h handler) workspaces(w http.ResponseWriter, r *http.Request) error {
	v := workspacesView{Query: r.URL.Query().Get("q"), Page: web.PageFrom(r, web.PageSize)}
	var err error
	v.Rows, err = web.LoadPage(&v.Page, func(w db.ListWorkspacesRow) int64 { return w.Total },
		func(limit, offset int32) ([]db.ListWorkspacesRow, error) {
			return h.q.ListWorkspaces(r.Context(), db.ListWorkspacesParams{Query: db.EscapeLike(v.Query), Lim: limit, Off: offset})
		})
	if err != nil {
		return err
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, workspaceList(v))
	}
	return web.Render(w, r, http.StatusOK, workspacesPage(v))
}

func (h handler) workspace(w http.ResponseWriter, r *http.Request) error {
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	ws, err := h.q.GetWorkspace(r.Context(), id)
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	members, err := h.q.ListMembers(r.Context(), id)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, workspacePage(ws, members))
}

type auditView struct {
	Rows    []db.ListAuditRow
	Actions []string
	Action  string
	Page    web.Page
}

func (h handler) audit(w http.ResponseWriter, r *http.Request) error {
	v := auditView{Action: r.URL.Query().Get("action"), Page: web.PageFrom(r, web.PageSize)}
	var err error
	v.Rows, err = web.LoadPage(&v.Page, func(a db.ListAuditRow) int64 { return a.Total },
		func(limit, offset int32) ([]db.ListAuditRow, error) {
			return h.q.ListAudit(r.Context(), db.ListAuditParams{Action: v.Action, Lim: limit, Off: offset})
		})
	if err != nil {
		return err
	}
	if web.HXTarget(r) == listTarget {
		return web.Render(w, r, http.StatusOK, auditList(v))
	}
	if v.Actions, err = h.q.ListAuditActions(r.Context()); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, auditPage(v))
}
