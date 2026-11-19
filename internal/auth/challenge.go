package auth

import (
	"crypto/rand"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	challengePath   = "/login/verify"
	challengeCookie = "__Host-login"
	challengeTTL    = 10 * time.Minute
	// maxChallengeTries ends a challenge; factorTries limits the account.
	maxChallengeTries = 5
)

// signIn follows a first factor such as a password. It starts a session, or,
// when the account has a second factor, a challenge that asks for it.
func (a *Auth) signIn(w http.ResponseWriter, r *http.Request, accountID int64, next string) error {
	fs, err := a.enrolled(r.Context(), a.q, accountID)
	if err != nil {
		return err
	}
	if len(fs) == 0 {
		token, err := NewSession(r.Context(), a.q, accountID, AAL1, r.UserAgent())
		if err != nil {
			return err
		}
		SetCookie(w, token)
		web.Redirect(w, r, next)
		return nil
	}
	token := rand.Text()
	err = a.q.CreateLoginChallenge(r.Context(), db.CreateLoginChallengeParams{
		TokenHash: hashToken(token), AccountID: accountID, Next: next, ExpiresAt: time.Now().Add(challengeTTL),
	})
	if err != nil {
		return err
	}
	setChallengeCookie(w, token, int(challengeTTL/time.Second))
	web.Redirect(w, r, challengePath)
	return nil
}

func setChallengeCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: challengeCookie, Value: token, Path: "/", MaxAge: maxAge,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func challengeToken(r *http.Request) []byte {
	c, err := r.Cookie(challengeCookie)
	if err != nil {
		return nil
	}
	return hashToken(c.Value)
}

func (a *Auth) challengeForm(w http.ResponseWriter, r *http.Request) error {
	ch, err := a.q.GetLoginChallenge(r.Context(), db.GetLoginChallengeParams{TokenHash: challengeToken(r), MaxAttempts: maxChallengeTries})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Redirect(w, r, LoginPath, http.StatusSeeOther)
		return nil
	}
	if err != nil {
		return err
	}
	fs, err := a.enrolled(r.Context(), a.q, ch.AccountID)
	if err != nil {
		return err
	}
	return web.Render(w, r, http.StatusOK, challengePage(challengeView{Factors: fs}))
}

// errChallengeGone is a challenge that expired, ran out of attempts, or was
// used by another request.
var errChallengeGone = errors.New("auth: login challenge gone")

func (a *Auth) challenge(w http.ResponseWriter, r *http.Request) error {
	hash := challengeToken(r)
	expired := func() error {
		setChallengeCookie(w, "", -1)
		web.Redirect(w, r, LoginPath+"?expired=1")
		return nil
	}
	ch, err := a.q.GetLoginChallenge(r.Context(), db.GetLoginChallengeParams{TokenHash: hash, MaxAttempts: maxChallengeTries})
	if errors.Is(err, pgx.ErrNoRows) {
		return expired()
	}
	if err != nil {
		return err
	}
	fs, err := a.enrolled(r.Context(), a.q, ch.AccountID)
	if err != nil {
		return err
	}
	v := challengeView{Factors: fs, Failed: r.PostFormValue("factor")}
	var f Factor
	for _, e := range fs {
		if e.Name() == v.Failed {
			f = e
		}
	}
	if f == nil {
		return web.ErrNotFound
	}
	if !a.factorTries.Allow(strconv.FormatInt(ch.AccountID, 10)) {
		v.Error = "Too many attempts. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, challengePage(v))
	}
	// Only a code that gets checked counts against the challenge.
	if _, err := a.q.AttemptLoginChallenge(r.Context(), db.AttemptLoginChallengeParams{TokenHash: hash, MaxAttempts: maxChallengeTries}); errors.Is(err, pgx.ErrNoRows) {
		return expired()
	} else if err != nil {
		return err
	}
	ok, err := f.Verify(r.Context(), a.q, ch.AccountID, r)
	if err != nil {
		return err
	}
	if !ok {
		v.Error = "That didn’t work. Try again."
		return web.Render(w, r, http.StatusUnprocessableEntity, challengePage(v))
	}
	var token string
	err = pgx.BeginTxFunc(r.Context(), a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Deleting the row is what spends the challenge, so of two requests
		// that both verified, only one starts a session.
		n, err := q.DeleteLoginChallenge(r.Context(), hash)
		if err != nil {
			return err
		}
		if n == 0 {
			return errChallengeGone
		}
		token, err = NewSession(r.Context(), q, ch.AccountID, AAL2, r.UserAgent())
		return err
	})
	if errors.Is(err, errChallengeGone) {
		return expired()
	}
	if err != nil {
		return err
	}
	setChallengeCookie(w, "", -1)
	SetCookie(w, token)
	web.Redirect(w, r, ch.Next)
	return nil
}
