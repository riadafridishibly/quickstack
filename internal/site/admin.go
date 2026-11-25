// Package site holds the settings for the whole site, such as its icon, and
// the Site section of /admin where they are changed.
package site

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/media"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	adminPath    = "/admin/site"
	iconPath     = adminPath + "/icon"
	iconTarget   = "icon-form"
	statusTarget = "icon-status"
)

var errInvalid = errors.New("site: invalid")

type adminHandler struct {
	pool *pgxpool.Pool
	q    *db.Queries
	jc   *jobs.Client
}

// AdminRoutes adds /admin/site. Register it inside /admin.
func AdminRoutes(r *auth.PlatformRouter, pool *pgxpool.Pool, jc *jobs.Client) {
	h := adminHandler{pool: pool, q: db.New(pool), jc: jc}
	r.Get("/admin/site", auth.ManageSite, h.page)
	r.Post("/admin/site/icon", auth.ManageSite, h.saveIcon)
}

type pageView struct {
	Settings db.SiteSetting
	// Icon is the chosen icon, which may not be live yet.
	Icon    *db.Asset
	Form    iconForm
	Message string
}

// drawing reports whether the chosen icon's files are still being drawn,
// so pages show the previous icon.
func (v pageView) drawing() bool {
	s := v.Settings
	return s.IconID != nil && (s.LiveIconID == nil || *s.LiveIconID != *s.IconID || s.LiveIconRev < s.IconRev)
}

func (h adminHandler) load(ctx context.Context) (pageView, error) {
	row, err := h.q.GetSiteSettings(ctx)
	if err != nil {
		return pageView{}, err
	}
	v := pageView{Settings: row.SiteSetting, Form: iconFormOf(row)}
	if id := row.SiteSetting.IconID; id != nil {
		a, err := h.q.GetAsset(ctx, *id)
		if err != nil {
			return pageView{}, err
		}
		v.Icon = &a
	}
	return v, nil
}

func (h adminHandler) page(w http.ResponseWriter, r *http.Request) error {
	v, err := h.load(r.Context())
	if err != nil {
		return err
	}
	if web.HXTarget(r) == statusTarget {
		return web.Render(w, r, http.StatusOK, iconStatus(v))
	}
	if r.URL.Query().Get("done") == "saved" {
		v.Message = "Icon saved."
	}
	return web.Render(w, r, http.StatusOK, sitePage(v))
}

func (h adminHandler) saveIcon(w http.ResponseWriter, r *http.Request) error {
	f := iconFormFrom(r)
	if !f.validate() {
		return h.renderForm(w, r, f)
	}
	me := auth.From(r.Context())
	err := pgx.BeginTxFunc(r.Context(), h.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return saveIcon(r.Context(), tx, h.jc, me.ID, &f)
	})
	if errors.Is(err, errInvalid) {
		return h.renderForm(w, r, f)
	}
	if err != nil {
		return err
	}
	web.Redirect(w, r, adminPath+"?done=saved")
	return nil
}

// renderForm re-renders an icon form that wasn't saved: the form alone for
// htmx, the whole page otherwise.
func (h adminHandler) renderForm(w http.ResponseWriter, r *http.Request, f iconForm) error {
	v, err := h.load(r.Context())
	if err != nil {
		return err
	}
	v.Form = f
	if web.IsHTMX(r) {
		return web.Render(w, r, http.StatusUnprocessableEntity, iconFormEl(v))
	}
	return web.Render(w, r, http.StatusUnprocessableEntity, sitePage(v))
}

// saveIcon writes the validated form f as part of tx and queues the job
// that draws the icon's files. It returns errInvalid, with f.Errors set,
// for an image that isn't in Media or can't be an icon.
func saveIcon(ctx context.Context, tx pgx.Tx, jc *jobs.Client, actorID int64, f *iconForm) error {
	q := db.New(tx)
	old, err := q.LockSiteSettings(ctx)
	if err != nil {
		return err
	}
	var iconID *int64
	if f.iconKey != "" {
		a, err := q.GetAssetByKey(ctx, f.iconKey)
		if errors.Is(err, pgx.ErrNoRows) {
			f.Errors["icon"] = "There is no image " + f.Icon + " in Media."
			return errInvalid
		}
		if err != nil {
			return err
		}
		if msg := media.IconProblem(a); msg != "" {
			f.Errors["icon"] = msg
			return errInvalid
		}
		iconID = &a.ID
	}
	iconChanged := (old.IconID == nil) != (iconID == nil) || (iconID != nil && *old.IconID != *iconID)
	backgroundChanged := old.IconBackground != f.background
	if !iconChanged && !backgroundChanged {
		return nil
	}

	set, err := q.UpdateSiteIcon(ctx, db.UpdateSiteIconParams{IconID: iconID, IconBackground: f.background})
	if err != nil {
		return err
	}
	switch {
	case set.IconID != nil:
		_, err = jc.InsertTx(ctx, tx, media.IconArgs{Rev: set.IconRev}, nil)
	case old.LiveIconRev > 0:
		_, err = jc.InsertTx(ctx, tx, media.DeleteIconFiles(old.LiveIconRev), nil)
	}
	if err != nil {
		return err
	}

	audit := func(action, target string) error {
		return q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &actorID, Action: action, Target: target})
	}
	if iconChanged {
		if iconID == nil {
			err = audit("site.icon_removed", "")
		} else {
			err = audit("site.icon_set", media.URL(f.iconKey))
		}
		if err != nil {
			return err
		}
	}
	if backgroundChanged {
		return audit("site.icon_background_set", f.background)
	}
	return nil
}
