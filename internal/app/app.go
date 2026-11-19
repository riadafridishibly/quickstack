// Package app wires every feature into one http.Handler: the public site,
// sign-in, and the three dashboards (a workspace's, a person's account, and
// the platform admin).
package app

import (
	"errors"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/riadafridishibly/quickstack/internal/account"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/blog"
	"github.com/riadafridishibly/quickstack/internal/insights"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/media"
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
	// PublicURL is the site's origin, which links in email, the feed and
	// the sitemap start with.
	PublicURL string
	// Jobs enqueues background jobs. Nil uses an insert-only client.
	Jobs *jobs.Client
	// Outbox, when set, is served at /dev/mail.
	Outbox *mail.Outbox
	// Blob holds uploaded files.
	Blob blob.Store
	// SecretKey seals secrets the database holds, such as authenticator
	// app keys. It is auth.KeySize bytes.
	SecretKey []byte
}

func New(cfg Config, pool *pgxpool.Pool) (http.Handler, error) {
	h, _, err := build(cfg, pool)
	return h, err
}

// routes are the app's muxes, in the order a request reaches them, and the
// action each workspace and admin pattern requires.
type routes struct {
	muxes   []*web.Mux
	actions map[*web.Mux]map[string]string
}

func build(cfg Config, pool *pgxpool.Pool) (http.Handler, routes, error) {
	if cfg.Blob == nil {
		return nil, routes{}, errors.New("app: no blob store")
	}
	if len(cfg.SecretKey) != auth.KeySize {
		return nil, routes{}, errors.New("app: the secret key must be 32 bytes")
	}
	assets, err := web.NewAssets(cfg.ViteURL)
	if err != nil {
		return nil, routes{}, err
	}
	if cfg.Jobs == nil {
		if cfg.Jobs, err = jobs.New(pool, nil); err != nil {
			return nil, routes{}, err
		}
	}
	tdb := tenant.NewDB(pool)
	a := auth.New(pool, cfg.Jobs, cfg.PublicURL)
	a.Layout = account.Layout
	a.AddFactor(auth.NewTOTP(a, cfg.SecretKey))
	notFound := web.Handle(func(http.ResponseWriter, *http.Request) error { return web.ErrNotFound })

	// Inside one workspace, behind workspaces.Middleware.
	ws := web.NewMux("workspace: /w/{ws}, through workspaces.Middleware")
	wsr := auth.Workspace.Router(ws)
	insights.Routes(wsr, tdb)
	projects.Routes(wsr, tdb)
	workspaces.WorkspaceRoutes(wsr, pool, cfg.Jobs, cfg.PublicURL)
	ws.Handle("/", notFound)

	// The platform admin, for staff only.
	adm := web.NewMux("admin: /admin, through open_admin")
	admr := auth.Platform.Router(adm)
	platform.Routes(admr, pool)
	blog.AdminRoutes(admr, pool)
	media.AdminRoutes(admr, pool, cfg.Blob, cfg.Jobs)
	adm.Handle("/", notFound)

	// Everything that needs a signed-in user.
	signedIn := web.NewMux("signed in: /account, /w and /admin, through auth.RequireLogin")
	account.Routes(signedIn, a, pool)
	a.SecurityRoutes(signedIn)
	workspaces.Routes(signedIn, pool, cfg.Jobs, cfg.PublicURL)
	inWorkspace := workspaces.Middleware(pool)(ws)
	signedIn.Handle("/w/{ws}", inWorkspace)
	signedIn.Handle("/w/{ws}/", inWorkspace)
	staff := auth.Platform.Require(auth.OpenAdmin)(adm)
	signedIn.Handle("/admin", staff)
	signedIn.Handle("/admin/", staff)
	signedIn.Handle("/", notFound)
	requireLogin := a.RequireLogin(signedIn)

	// Pages anyone can open, which show more to a signed-in user.
	open := web.NewMux("open: the rest of root, through auth.OptionalLogin")
	public.Routes(open, pool, tdb)
	blog.Routes(open, pool, cfg.PublicURL)
	workspaces.InviteRoutes(open, pool, cfg.Jobs, cfg.PublicURL)
	open.Handle("/", notFound)

	mux := web.NewMux("root")
	mux.Handle("GET /static/assets/", web.StaticHandler())
	media.Routes(mux, cfg.Blob)
	mux.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	if cfg.Outbox != nil {
		mux.Handle("GET /dev/mail", cfg.Outbox.Handler())
	}
	a.Routes(mux)
	for _, p := range []string{"/account", "/account/", "/w", "/w/", "/admin", "/admin/"} {
		mux.Handle(p, requireLogin)
	}
	mux.Handle("/", a.OptionalLogin(open))

	muxes := []*web.Mux{mux, open, signedIn, ws, adm}
	var h http.Handler = mux
	h = web.WithPath(h)
	h = assets.Middleware(h)
	h = http.NewCrossOriginProtection().Handler(h)
	h = web.SecureHeaders(cfg.ViteURL)(h)
	h = web.Recoverer(h)
	h = web.Logger(h)
	h = web.RealIP(cfg.TrustedProxies)(h)
	h = web.Trace(func(r *http.Request) string {
		for _, m := range muxes {
			if _, p := m.Handler(r); p != "" && p != "/" && !isMount(p) {
				return p
			}
		}
		return r.Method + " (unrouted)"
	})(h)
	return h, routes{muxes: muxes, actions: map[*web.Mux]map[string]string{ws: wsr.Actions(), adm: admr.Actions()}}, nil
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
func Workers(pool *pgxpool.Pool, sender mail.Sender, store blob.Store) *jobs.Setup {
	w := river.NewWorkers()
	mail.AddWorker(w, sender)
	media.AddWorkers(w, pool, store)
	periodic := auth.AddWorkers(w, pool)
	periodic = append(periodic, workspaces.AddWorkers(w, pool)...)
	return &jobs.Setup{Workers: w, Periodic: periodic}
}
