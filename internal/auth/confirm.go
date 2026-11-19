package auth

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/riadafridishibly/quickstack/internal/db"
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
	// Factors, when the account has any, are asked for instead of the
	// password, so a password alone can't remove a second factor.
	Factors []Factor
	Failed  string
	Error   string
}

func (a *Auth) loadConfirm(r *http.Request, next string) (confirmView, db.Account, error) {
	u := From(r.Context())
	v := confirmView{Next: web.SafeNext(next, HomePath)}
	acc, err := a.q.GetAccount(r.Context(), u.ID)
	if err != nil {
		return v, acc, err
	}
	v.HasPassword = acc.PasswordHash != nil
	v.Factors, err = a.enrolled(r.Context(), a.q, u.ID)
	return v, acc, err
}

func (a *Auth) confirmForm(w http.ResponseWriter, r *http.Request) error {
	v, _, err := a.loadConfirm(r, r.URL.Query().Get("next"))
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, confirmPage(v))
}

func (a *Auth) confirm(w http.ResponseWriter, r *http.Request) error {
	u := From(r.Context())
	v, acc, err := a.loadConfirm(r, r.PostFormValue("next"))
	if err != nil {
		return err
	}
	aal := AAL1
	ok := false
	if len(v.Factors) > 0 {
		v.Failed = r.PostFormValue("factor")
		var f Factor
		for _, e := range v.Factors {
			if e.Name() == v.Failed {
				f = e
			}
		}
		if f == nil {
			return web.ErrNotFound
		}
		if !a.factorTries.Allow(strconv.FormatInt(u.ID, 10)) {
			v.Error = "Too many attempts. Wait a few minutes and try again."
			return web.Render(w, r, http.StatusTooManyRequests, confirmPage(v))
		}
		if ok, err = f.Verify(r.Context(), a.q, u.ID, r); err != nil {
			return err
		}
		aal = AAL2
		v.Error = "That didn’t work. Try again."
	} else {
		if !a.byAccount.Allow(strconv.FormatInt(u.ID, 10)) {
			v.Error = "Too many attempts. Wait a few minutes and try again."
			return web.Render(w, r, http.StatusTooManyRequests, confirmPage(v))
		}
		if acc.PasswordHash != nil {
			if ok, err = CheckPassword(*acc.PasswordHash, r.PostFormValue("password")); err != nil {
				return err
			}
		}
		v.Error = "That isn’t your password."
	}
	if !ok {
		return web.Render(w, r, http.StatusUnprocessableEntity, confirmPage(v))
	}
	if err := a.q.ConfirmSession(r.Context(), db.ConfirmSessionParams{TokenHash: u.tokenHash, Aal: int16(aal)}); err != nil {
		return err
	}
	web.Redirect(w, r, v.Next)
	return nil
}
