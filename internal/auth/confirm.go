package auth

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	ConfirmPath = "/confirm"
	recentFor   = 15 * time.Minute
)

// Recent reports whether u proved who they are in the last few minutes, by
// signing in or on the confirm page.
func (u *User) Recent() bool { return time.Since(u.AuthenticatedAt) < recentFor }

// RequireRecent sends a user who hasn't proved who they are lately to the
// confirm page first. Put it inside RequireLogin, on the pages and posts that
// change how someone signs in. A post comes back to the page it was sent
// from, to be sent again.
func RequireRecent(next http.Handler) http.Handler {
	return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
		u := From(r.Context())
		if u == nil {
			return web.ErrForbidden
		}
		if u.Recent() {
			next.ServeHTTP(w, r)
			return nil
		}
		back := r.URL.RequestURI()
		if r.Method != http.MethodGet {
			back = HomePath
			if ref, err := url.Parse(r.Referer()); err == nil && ref.Host == r.Host {
				back = web.SafeNext(ref.RequestURI(), HomePath)
			}
		}
		web.Redirect(w, r, ConfirmPath+"?next="+url.QueryEscape(back))
		return nil
	})
}

type confirmView struct {
	Next        string
	HasPassword bool
	Error       string
}

func (a *Auth) confirmForm(w http.ResponseWriter, r *http.Request) error {
	acc, err := a.q.GetAccount(r.Context(), From(r.Context()).ID)
	if err != nil {
		return err
	}
	v := confirmView{Next: web.SafeNext(r.URL.Query().Get("next"), HomePath), HasPassword: acc.PasswordHash != nil}
	return web.Render(w, r, http.StatusOK, confirmPage(v))
}

func (a *Auth) confirm(w http.ResponseWriter, r *http.Request) error {
	u := From(r.Context())
	acc, err := a.q.GetAccount(r.Context(), u.ID)
	if err != nil {
		return err
	}
	v := confirmView{Next: web.SafeNext(r.PostFormValue("next"), HomePath), HasPassword: acc.PasswordHash != nil}
	if !a.byAccount.Allow(strconv.FormatInt(u.ID, 10)) {
		v.Error = "Too many attempts. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, confirmPage(v))
	}
	ok := false
	if acc.PasswordHash != nil {
		if ok, err = CheckPassword(*acc.PasswordHash, r.PostFormValue("password")); err != nil {
			return err
		}
	}
	if !ok {
		v.Error = "That isn’t your password."
		return web.Render(w, r, http.StatusUnprocessableEntity, confirmPage(v))
	}
	if err := a.q.ConfirmSession(r.Context(), u.tokenHash); err != nil {
		return err
	}
	web.Redirect(w, r, v.Next)
	return nil
}
