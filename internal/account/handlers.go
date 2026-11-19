// Package account is the signed-in person's own dashboard: their profile,
// password, second factors and sessions. Its Layout also frames the list of workspaces.
package account

import (
	"errors"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	profilePath  = "/account"
	securityPath = auth.SecurityPath
)

type handler struct {
	auth *auth.Auth
	q    *db.Queries
}

func Routes(mux *web.Mux, a *auth.Auth, pool *pgxpool.Pool) {
	h := handler{auth: a, q: db.New(pool)}
	mux.Get("/account", h.profile)
	mux.Post("/account", h.updateProfile)
	mux.Post("/account/verify-email", h.sendVerification)
	mux.Get("/account/security", h.security)
	mux.Post("/account/security/password", h.changePassword)
	mux.Post("/account/security/sessions/{id}/delete", h.deleteSession)
	mux.Post("/account/security/sign-out-others", h.signOutOthers)
}

func (h handler) profile(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	v := profileView{Name: u.Name, Email: u.Email, Verified: u.EmailVerified}
	switch r.URL.Query().Get("done") {
	case "saved":
		v.Message = "Profile saved."
	case "sent":
		v.Message = "We sent a verification link to " + u.Email + "."
	}
	return web.Render(w, r, http.StatusOK, profilePage(v))
}

func (h handler) updateProfile(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	v := profileView{Name: strings.TrimSpace(r.PostFormValue("name")), Email: u.Email, Verified: u.EmailVerified}
	if v.Error = auth.CheckName(v.Name); v.Error != "" {
		return web.Render(w, r, http.StatusUnprocessableEntity, profilePage(v))
	}
	if _, err := h.q.UpdateAccountName(r.Context(), db.UpdateAccountNameParams{ID: u.ID, Name: v.Name}); err != nil {
		return err
	}
	web.Redirect(w, r, profilePath+"?done=saved")
	return nil
}

func (h handler) sendVerification(w http.ResponseWriter, r *http.Request) error {
	u := auth.From(r.Context())
	err := h.auth.SendVerification(r.Context(), u)
	switch {
	case errors.Is(err, auth.ErrEmailVerified):
		web.Redirect(w, r, profilePath)
	case errors.Is(err, auth.ErrTooManyAttempts):
		v := profileView{Name: u.Name, Email: u.Email, VerifyError: "We sent several links already. Wait a few minutes and try again."}
		return web.Render(w, r, http.StatusTooManyRequests, profilePage(v))
	case err != nil:
		return err
	default:
		web.Redirect(w, r, profilePath+"?done=sent")
	}
	return nil
}

type securityView struct {
	Email string
	// HasPassword is false for an account that signs in only another way.
	HasPassword bool
	Form        passwordForm
	// Factors are the sections second factors add.
	Factors  []templ.Component
	Sessions []db.ListSessionsRow
	Current  int64
	Message  string
}

func (h handler) loadSecurity(r *http.Request, v *securityView) error {
	u := auth.From(r.Context())
	v.Email, v.Current = u.Email, u.SessionID
	acc, err := h.q.GetAccount(r.Context(), u.ID)
	if err != nil {
		return err
	}
	v.HasPassword = acc.PasswordHash != nil
	if v.Factors, err = h.auth.SecuritySections(r.Context(), u.ID); err != nil {
		return err
	}
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
