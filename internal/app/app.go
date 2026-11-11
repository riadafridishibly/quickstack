// Package app wires every feature into one http.Handler: the public site,
// sign-in, and the three dashboards (a workspace's, a person's account, and
// the platform admin).
package app

import (
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/account"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/insights"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/platform"
	"github.com/riadafridishibly/quickstack/internal/projects"
	"github.com/riadafridishibly/quickstack/internal/public"
	"github.com/riadafridishibly/quickstack/internal/tenant"
	"github.com/riadafridishibly/quickstack/internal/web"
	"github.com/riadafridishibly/quickstack/internal/workspaces"
)

type Config struct {
	// ViteURL is the Vite dev server origin. Empty serves the embedded build.
	ViteURL        string
	TrustedProxies []netip.Prefix
	// PublicURL is the origin links in email point to.
	PublicURL string
	// Jobs enqueues background jobs. Nil uses an insert-only client.
	Jobs *jobs.Client
	// Outbox, when set, is served at /dev/mail.
	Outbox *mail.Outbox
}

func New(cfg Config, pool *pgxpool.Pool) (http.Handler, error) {
	assets, err := web.NewAssets(cfg.ViteURL)
	if err != nil {
		return nil, err
	}
	if cfg.Jobs == nil {
		if cfg.Jobs, err = jobs.New(pool, nil); err != nil {
			return nil, err
		}
	}
	tdb := tenant.NewDB(pool)
	a := auth.New(pool, cfg.Jobs, cfg.PublicURL)
	notFound := web.Handle(func(http.ResponseWriter, *http.Request) error { return web.ErrNotFound })

	// Inside one workspace, behind workspaces.Middleware.
	ws := http.NewServeMux()
	insights.Routes(ws, tdb)
	projects.Routes(ws, tdb)
	workspaces.WorkspaceRoutes(ws, pool, cfg.Jobs, cfg.PublicURL)
	ws.Handle("/", notFound)

	// The platform admin, for staff only.
	adm := http.NewServeMux()
	platform.Routes(adm, pool)
	adm.Handle("/", notFound)

	// Everything that needs a signed-in user.
	signedIn := http.NewServeMux()
	account.Routes(signedIn, a, pool)
	workspaces.Routes(signedIn, pool, cfg.Jobs, cfg.PublicURL)
	inWorkspace := workspaces.Middleware(pool)(ws)
	signedIn.Handle("/w/{ws}", inWorkspace)
	signedIn.Handle("/w/{ws}/", inWorkspace)
	staff := auth.RequirePlatform(auth.ViewPlatform)(adm)
	signedIn.Handle("/admin", staff)
	signedIn.Handle("/admin/", staff)
	signedIn.Handle("/", notFound)
	requireLogin := a.RequireLogin(signedIn)

	// Pages anyone can open, which show more to a signed-in user.
	open := http.NewServeMux()
	public.Routes(open, pool, tdb)
	workspaces.InviteRoutes(open, pool, cfg.Jobs, cfg.PublicURL)
	open.Handle("/", notFound)

	mux := http.NewServeMux()
	mux.Handle("GET /static/assets/", web.StaticHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.Outbox != nil {
		mux.Handle("GET /dev/mail", cfg.Outbox.Handler())
	}
	a.Routes(mux)
	for _, p := range []string{"/account", "/account/", "/w", "/w/", "/admin", "/admin/"} {
		mux.Handle(p, requireLogin)
	}
	mux.Handle("/", a.OptionalLogin(open))

	var h http.Handler = mux
	h = web.WithPath(h)
	h = assets.Middleware(h)
	h = http.NewCrossOriginProtection().Handler(h)
	h = web.SecureHeaders(cfg.ViteURL)(h)
	h = web.Recoverer(h)
	h = web.Logger(h)
	h = web.RealIP(cfg.TrustedProxies)(h)
	h = web.Trace(func(r *http.Request) string {
		for _, m := range []*http.ServeMux{mux, signedIn, ws, adm, open} {
			if _, p := m.Handler(r); p != "" && p != "/" && !isMount(p) {
				return p
			}
		}
		return r.Method + " (unrouted)"
	})(h)
	return h, nil
}

// isMount reports whether p is a pattern that hands the request on to
// another mux, so Trace keeps looking for the pattern that serves it.
func isMount(p string) bool {
	switch p {
	case "/account", "/account/", "/w", "/w/", "/admin", "/admin/", "/w/{ws}", "/w/{ws}/":
		return true
	}
	return false
}

// Workers returns the background job workers and their schedules.
func Workers(pool *pgxpool.Pool, sender mail.Sender) *jobs.Setup {
	w := river.NewWorkers()
	mail.AddWorker(w, sender)
	periodic := auth.AddWorkers(w, pool)
	return &jobs.Setup{Workers: w, Periodic: periodic}
}
