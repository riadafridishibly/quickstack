// Package public is the site anyone can see: the landing page and each
// public workspace's roadmap. It has its own layout and stylesheet, as a
// storefront or a marketing site would.
package public

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
)

type handler struct {
	q   *db.Queries
	tdb *tenant.DB
}

// Routes adds the public pages. Wrap mux in auth.OptionalLogin so the
// landing page knows who is signed in.
func Routes(mux *http.ServeMux, pool *pgxpool.Pool, tdb *tenant.DB) {
	h := handler{q: db.New(pool), tdb: tdb}
	mux.Handle("GET /{$}", web.Handle(h.landing))
	mux.Handle("GET /p/{slug}", web.Handle(h.roadmap))
}

func (h handler) landing(w http.ResponseWriter, r *http.Request) error {
	return web.Render(w, r, http.StatusOK, landingPage())
}

type roadmapProject struct {
	Name        string
	Description string
	Now, Next   []db.ListRoadmapRow
	Done        []db.ListRoadmapRow
}

func (h handler) roadmap(w http.ResponseWriter, r *http.Request) error {
	row, err := h.q.GetPublicWorkspace(r.Context(), r.PathValue("slug"))
	if err != nil {
		return web.NotFoundIfNoRows(err)
	}
	ws := &tenant.Workspace{ID: row.ID, Slug: row.Slug, Name: row.Name, Public: true}
	var rows []db.ListRoadmapRow
	err = h.tdb.Tx(tenant.WithWorkspace(r.Context(), ws), func(tx tenant.Tx) (err error) {
		rows, err = tx.ListRoadmap(r.Context(), tx.WorkspaceID)
		return err
	})
	if err != nil {
		return err
	}
	var projects []*roadmapProject
	for i, t := range rows {
		if i == 0 || rows[i-1].ProjectID != t.ProjectID {
			projects = append(projects, &roadmapProject{Name: t.ProjectName, Description: t.Description})
		}
		p := projects[len(projects)-1]
		switch t.Status {
		case "doing":
			p.Now = append(p.Now, t)
		case "todo":
			p.Next = append(p.Next, t)
		default:
			p.Done = append(p.Done, t)
		}
	}
	// Anyone may cache the page briefly; it holds nothing private.
	w.Header().Set("Cache-Control", "public, max-age=60")
	return web.Render(w, r, http.StatusOK, roadmapPage(ws.Name, projects))
}
