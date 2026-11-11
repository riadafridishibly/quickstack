// Package account is the signed-in person's own dashboard: their profile,
// password and sessions. Its Layout also frames the list of workspaces.
package account

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	profilePath  = "/account"
	securityPath = "/account/security"
)

type handler struct {
	auth *auth.Auth
	q    *db.Queries
}

func Routes(mux *http.ServeMux, a *auth.Auth, pool *pgxpool.Pool) {
	h := handler{auth: a, q: db.New(pool)}
	mux.Handle("GET "+profilePath, web.Handle(h.profile))
	mux.Handle("POST "+profilePath, web.Handle(h.updateProfile))
	mux.Handle("GET "+securityPath, web.Handle(h.security))
	mux.Handle("POST "+securityPath+"/password", web.Handle(h.changePassword))
	mux.Handle("POST "+securityPath+"/sessions/{id}/delete", web.Handle(h.deleteSession))
	mux.Handle("POST "+securityPath+"/sign-out-others", web.Handle(h.signOutOthers))
}

func (h handler) profile(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	v := profileView{Name: u.Name, Email: u.Email, Saved: r.URL.Query().Get("saved") == "1"}
	return web.Render(w, r, http.StatusOK, profilePage(v))
}

func (h handler) updateProfile(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	v := profileView{Name: strings.TrimSpace(r.PostFormValue("name")), Email: u.Email}
	if v.Error = auth.CheckName(v.Name); v.Error != "" {
		return web.Render(w, r, http.StatusUnprocessableEntity, profilePage(v))
	}
	if _, err := h.q.UpdateAccountName(r.Context(), db.UpdateAccountNameParams{ID: u.ID, Name: v.Name}); err != nil {
		return err
	}
	web.Redirect(w, r, profilePath+"?saved=1")
	return nil
}

type securityView struct {
	Form     passwordForm
	Sessions []db.ListSessionsRow
	Current  int64
	Message  string
}

func (h handler) loadSecurity(r *http.Request, v *securityView) error {
	u := auth.From(r.Context())
	v.Current = u.SessionID
	var err error
	v.Sessions, err = h.q.ListSessions(r.Context(), u.ID)
	return err
}

func (h handler) security(w http.ResponseWriter, r *http.Request) error {
	v := securityView{Form: passwordForm{Errors: map[string]string{}}}
	switch r.URL.Query().Get("done") {
	case "password":
		v.Message = "Password changed. Your other sessions were signed out."
	case "others":
		v.Message = "Your other sessions were signed out."
	}
	if err := h.loadSecurity(r, &v); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, securityPage(v))
}

type passwordForm struct {
	Errors map[string]string
}

func (h handler) changePassword(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	f := passwordForm{Errors: map[string]string{}}
	current, next := r.PostFormValue("current_password"), r.PostFormValue("new_password")
	if current == "" {
		f.Errors["current_password"] = "Enter your current password."
	}
	if msg := auth.CheckNewPassword(next); msg != "" {
		f.Errors["new_password"] = msg
	} else if next != r.PostFormValue("confirm_password") {
		f.Errors["confirm_password"] = "The passwords don’t match."
	}
	if len(f.Errors) == 0 {
		err := h.auth.ChangePassword(r.Context(), u, current, next)
		switch {
		case errors.Is(err, auth.ErrWrongPassword):
			f.Errors["current_password"] = "That isn’t your current password."
		case errors.Is(err, auth.ErrTooManyAttempts):
			f.Errors["current_password"] = "Too many attempts. Wait a few minutes and try again."
		case err != nil:
			return err
		default:
			web.Redirect(w, r, securityPath+"?done=password")
			return nil
		}
	}
	v := securityView{Form: f}
	if err := h.loadSecurity(r, &v); err != nil {
		return err
	}
	return web.Render(w, r, http.StatusUnprocessableEntity, securityPage(v))
}

// deleteSession signs out one of the user's other devices. htmx removes its
// row with the empty response.
func (h handler) deleteSession(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	id, err := web.PathID(r, "id")
	if err != nil {
		return err
	}
	if id == u.SessionID {
		return web.ErrForbidden
	}
	n, err := h.q.DeleteSessionByID(r.Context(), db.DeleteSessionByIDParams{AccountID: u.ID, ID: id})
	if err != nil {
		return err
	}
	if n == 0 {
		return web.ErrNotFound
	}
	if web.IsHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	web.Redirect(w, r, securityPath)
	return nil
}

func (h handler) signOutOthers(w http.ResponseWriter, r *http.Request) error {
	if _, err := h.auth.SignOutOthers(r.Context(), auth.From(r.Context())); err != nil {
		return err
	}
	web.Redirect(w, r, securityPath+"?done=others")
	return nil
}

// Device names a session by its browser and system, such as
// "Firefox on Linux".
func Device(userAgent string) string {
	browser := "Unknown browser"
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}, {"curl/", "curl"},
	} {
		if strings.Contains(userAgent, b.token) {
			browser = b.name
			break
		}
	}
	for _, s := range []struct{ token, name string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	} {
		if strings.Contains(userAgent, s.token) {
			return browser + " on " + s.name
		}
	}
	return browser
}
