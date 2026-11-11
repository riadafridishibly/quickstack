// Package auth signs people in: accounts, password hashing, sessions, the
// sign-in, sign-up and password reset pages, and the workspace and platform
// roles.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	// CookieName uses the __Host- prefix, so browsers only accept it with
	// Secure, Path=/ and no Domain: other hosts can't set or read it.
	CookieName = "__Host-session"
	sessionTTL = 30 * 24 * time.Hour
	touchEvery = time.Minute

	LoginPath  = "/login"
	LogoutPath = "/logout"
	SignupPath = "/signup"
	// HomePath is where signing in leads: the list of the user's workspaces.
	HomePath = "/w"
)

var (
	ErrEmailTaken      = errors.New("auth: email already has an account")
	ErrWrongPassword   = errors.New("auth: wrong password")
	ErrTooManyAttempts = errors.New("auth: too many attempts")
	ErrNoAccount       = errors.New("auth: no account with that email")
)

type User struct {
	ID           int64
	Email        string
	Name         string
	PlatformRole PlatformRole
	SessionID    int64
	tokenHash    []byte
}

type ctxKey struct{}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// From returns the signed-in user, or nil.
func From(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

type Auth struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	jobs      *jobs.Client
	publicURL string
	// A single IP can try one email at most once a minute after a short
	// burst, slower than byEmail refills, so one client can't lock an
	// account out. byEmail is the backstop against guessing from many IPs.
	byIP      *web.Limiter
	byIPEmail *web.Limiter
	byEmail   *web.Limiter
	byAccount *web.Limiter
	signups   *web.Limiter
}

// New returns an Auth. publicURL is the origin that links in email point to.
func New(pool *pgxpool.Pool, jc *jobs.Client, publicURL string) *Auth {
	return &Auth{
		pool:      pool,
		q:         db.New(pool),
		jobs:      jc,
		publicURL: publicURL,
		byIP:      web.NewLimiter(10*time.Second, 30),
		byIPEmail: web.NewLimiter(time.Minute, 5),
		byEmail:   web.NewLimiter(10*time.Second, 50),
		byAccount: web.NewLimiter(time.Minute, 5),
		signups:   web.NewLimiter(time.Minute, 10),
	}
}

func (a *Auth) Routes(mux *http.ServeMux) {
	mux.Handle("GET "+LoginPath, web.Handle(a.loginForm))
	mux.Handle("POST "+LoginPath, web.Handle(a.login))
	mux.Handle("POST "+LogoutPath, web.Handle(a.logout))
	mux.Handle("GET "+SignupPath, web.Handle(a.signupForm))
	mux.Handle("POST "+SignupPath, web.Handle(a.signup))
	mux.Handle("GET "+forgotPath, web.Handle(a.forgotForm))
	mux.Handle("POST "+forgotPath, web.Handle(a.forgot))
	mux.Handle("GET "+resetPath, web.Handle(a.resetForm))
	mux.Handle("POST "+resetPath, web.Handle(a.reset))
}

// RequireLogin puts the signed-in user in the request context, or sends the
// browser to the sign-in page.
func (a *Auth) RequireLogin(next http.Handler) http.Handler {
	return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.Load(w, r)
		if err != nil {
			return err
		}
		if u == nil {
			target := LoginPath
			if r.Method == http.MethodGet {
				target += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			web.Redirect(w, r, target)
			return nil
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
		return nil
	})
}

// OptionalLogin puts the signed-in user in the request context, if there is
// one, for pages anyone can see.
func (a *Auth) OptionalLogin(next http.Handler) http.Handler {
	return web.Handle(func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.Load(w, r)
		if err != nil {
			return err
		}
		if u != nil {
			r = r.WithContext(WithUser(r.Context(), u))
		}
		next.ServeHTTP(w, r)
		return nil
	})
}

// Load returns the user for the session cookie, or nil if there is no valid
// session. It extends a session that is in use.
func (a *Auth) Load(w http.ResponseWriter, r *http.Request) (*User, error) {
	c, err := r.Cookie(CookieName)
	if errors.Is(err, http.ErrNoCookie) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hash := hashToken(c.Value)
	row, err := a.q.GetSession(r.Context(), hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Since(row.LastSeenAt) > touchEvery {
		if err := a.q.TouchSession(r.Context(), db.TouchSessionParams{TokenHash: hash, ExpiresAt: time.Now().Add(sessionTTL)}); err != nil {
			return nil, err
		}
		SetCookie(w, c.Value)
	}
	u := &User{ID: row.ID, Email: row.Email, Name: row.Name, SessionID: row.SessionID, tokenHash: hash}
	if row.PlatformRole != nil {
		u.PlatformRole = PlatformRole(*row.PlatformRole)
	}
	return u, nil
}

func (a *Auth) loginForm(w http.ResponseWriter, r *http.Request) error {
	next := web.SafeNext(r.URL.Query().Get("next"), HomePath)
	u, err := a.Load(w, r)
	if err != nil {
		return err
	}
	if u != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return nil
	}
	return web.Render(w, r, http.StatusOK, loginPage(loginView{Next: next, Email: r.URL.Query().Get("email")}))
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) error {
	v := loginView{Email: NormalizeEmail(r.PostFormValue("email")), Next: web.SafeNext(r.PostFormValue("next"), HomePath)}
	password := r.PostFormValue("password")

	ip := web.ClientKey(r)
	if !a.byIP.Allow(ip) || !a.byIPEmail.Allow(ip+" "+v.Email) || !a.byEmail.Allow(v.Email) {
		v.Error = "Too many sign-in attempts. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, loginPage(v))
	}
	acc, err := a.q.GetAccountByEmail(r.Context(), v.Email)
	ok := false
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		spendHashTime(password)
	case err != nil:
		return err
	default:
		if ok, err = CheckPassword(acc.PasswordHash, password); err != nil {
			return err
		}
	}
	if !ok {
		v.Error = "Email or password is incorrect."
		return web.Render(w, r, http.StatusUnprocessableEntity, loginPage(v))
	}
	token, err := NewSession(r.Context(), a.q, acc.ID, r.UserAgent())
	if err != nil {
		return err
	}
	SetCookie(w, token)
	http.Redirect(w, r, v.Next, http.StatusSeeOther)
	return nil
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := a.q.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	web.Redirect(w, r, LoginPath)
	return nil
}

func (a *Auth) signupForm(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	return web.Render(w, r, http.StatusOK, signupPage(signupView{
		Email: NormalizeEmail(q.Get("email")),
		Next:  web.SafeNext(q.Get("next"), HomePath),
	}))
}

func (a *Auth) signup(w http.ResponseWriter, r *http.Request) error {
	v := signupView{
		Name:   strings.TrimSpace(r.PostFormValue("name")),
		Email:  NormalizeEmail(r.PostFormValue("email")),
		Next:   web.SafeNext(r.PostFormValue("next"), HomePath),
		Errors: map[string]string{},
	}
	password := r.PostFormValue("password")
	if !a.signups.Allow(web.ClientKey(r)) {
		v.Error = "Too many sign-ups from your network. Wait a few minutes and try again."
		return web.Render(w, r, http.StatusTooManyRequests, signupPage(v))
	}
	if msg := CheckName(v.Name); msg != "" {
		v.Errors["name"] = msg
	}
	if !ValidEmail(v.Email) {
		v.Errors["email"] = "Enter an email address such as ana@example.com."
	}
	if msg := CheckNewPassword(password); msg != "" {
		v.Errors["password"] = msg
	}
	if len(v.Errors) > 0 {
		return web.Render(w, r, http.StatusUnprocessableEntity, signupPage(v))
	}
	var token string
	err := pgx.BeginTxFunc(r.Context(), a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		acc, err := CreateAccount(r.Context(), q, v.Email, v.Name, password)
		if err != nil {
			return err
		}
		if err := q.RecordAudit(r.Context(), db.RecordAuditParams{ActorID: &acc.ID, Action: "account.signup", Target: acc.Email}); err != nil {
			return err
		}
		token, err = NewSession(r.Context(), q, acc.ID, r.UserAgent())
		return err
	})
	if errors.Is(err, ErrEmailTaken) {
		v.Errors["email"] = "This email already has an account. Sign in instead."
		return web.Render(w, r, http.StatusUnprocessableEntity, signupPage(v))
	}
	if err != nil {
		return err
	}
	SetCookie(w, token)
	web.Redirect(w, r, v.Next)
	return nil
}

// ChangePassword sets u's password after checking the current one, and signs
// out u's other sessions.
func (a *Auth) ChangePassword(ctx context.Context, u *User, current, next string) error {
	if !a.byAccount.Allow(strconv.FormatInt(u.ID, 10)) {
		return ErrTooManyAttempts
	}
	acc, err := a.q.GetAccount(ctx, u.ID)
	if err != nil {
		return err
	}
	ok, err := CheckPassword(acc.PasswordHash, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	return pgx.BeginTxFunc(ctx, a.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := q.UpdateAccountPassword(ctx, db.UpdateAccountPasswordParams{ID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if _, err := q.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{AccountID: u.ID, TokenHash: u.tokenHash}); err != nil {
			return err
		}
		return q.RecordAudit(ctx, db.RecordAuditParams{ActorID: &u.ID, Action: "account.password_changed", Target: u.Email})
	})
}

// SignOutOthers ends u's sessions except the current one.
func (a *Auth) SignOutOthers(ctx context.Context, u *User) (int64, error) {
	return a.q.DeleteOtherSessions(ctx, db.DeleteOtherSessionsParams{AccountID: u.ID, TokenHash: u.tokenHash})
}

// ResetPassword gives the account a new random password, signs it out
// everywhere, and returns the password. It is for the command line.
func ResetPassword(ctx context.Context, pool *pgxpool.Pool, email string) (string, error) {
	password := rand.Text()
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	err = pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := db.New(tx)
		acc, err := q.GetAccountByEmail(ctx, NormalizeEmail(email))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return err
		}
		if _, err := q.UpdateAccountPassword(ctx, db.UpdateAccountPasswordParams{ID: acc.ID, PasswordHash: hash}); err != nil {
			return err
		}
		_, err = q.DeleteAccountSessions(ctx, acc.ID)
		return err
	})
	return password, err
}

// CreateAccount hashes password and inserts the account.
func CreateAccount(ctx context.Context, q *db.Queries, email, name, password string) (db.Account, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return db.Account{}, err
	}
	acc, err := q.CreateAccount(ctx, db.CreateAccountParams{Email: email, Name: name, PasswordHash: hash})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return acc, ErrEmailTaken
	}
	return acc, err
}

// NewSession stores a session for the account and returns its token for
// SetCookie.
func NewSession(ctx context.Context, q *db.Queries, accountID int64, userAgent string) (string, error) {
	if err := q.DeleteExpiredSessions(ctx, accountID); err != nil {
		return "", err
	}
	token := rand.Text()
	err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: hashToken(token),
		AccountID: accountID,
		UserAgent: truncate(userAgent, 300),
		ExpiresAt: time.Now().Add(sessionTTL),
	})
	return token, err
}

func SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// HashToken is how session, reset and invitation tokens are stored: the
// database holds only the hash, so a leaked table can't be used.
func HashToken(token string) []byte { return hashToken(token) }

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// ValidEmail reports whether s, already normalized, is a plain address.
func ValidEmail(s string) bool {
	if len(s) > 254 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && a.Name == ""
}

const (
	MinPasswordLen = 10
	maxPasswordLen = 200
	maxNameLen     = 100
)

// CheckNewPassword returns an error message, or "" if password is acceptable.
func CheckNewPassword(password string) string {
	switch n := utf8.RuneCountInString(password); {
	case n < MinPasswordLen:
		return "Use at least 10 characters."
	case n > maxPasswordLen:
		return "Use at most 200 characters."
	}
	return ""
}

// CheckName returns an error message, or "" if name, already trimmed, is
// acceptable.
func CheckName(name string) string {
	switch {
	case name == "":
		return "Enter your name."
	case utf8.RuneCountInString(name) > maxNameLen:
		return "Use at most 100 characters."
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
