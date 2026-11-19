package auth_test

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/jobs"
	"github.com/riadafridishibly/quickstack/internal/testdb"
	"github.com/riadafridishibly/quickstack/internal/web"
)

// codeFactor is a second factor that accepts "123456" from enrolled
// accounts.
type codeFactor struct{ enrolled map[int64]bool }

func (codeFactor) Name() string { return "code" }

func (f codeFactor) Enrolled(_ context.Context, _ *db.Queries, id int64) (bool, error) {
	return f.enrolled[id], nil
}

func (codeFactor) Form(errMsg string) templ.Component {
	return templ.Raw(`<input name="code">` + errMsg)
}

func (codeFactor) Verify(_ context.Context, _ *db.Queries, _ int64, r *http.Request) (bool, error) {
	return r.PostFormValue("code") == "123456", nil
}

// server is auth on its own, with a factor and a page behind RequireRecent.
type server struct {
	t    *testing.T
	pool *pgxpool.Pool
	h    http.Handler
}

func newServer(t *testing.T, f auth.Factor) *server {
	pool := testdb.New(t)
	jc, err := jobs.New(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(pool, jc, "http://quickstack.test")
	a.AddFactor(f)
	mux := web.NewMux("test")
	a.Routes(mux)
	ok := web.Handle(func(w http.ResponseWriter, r *http.Request) error {
		_, err := w.Write([]byte("aal " + strconv.Itoa(int(auth.From(r.Context()).AAL))))
		return err
	})
	mux.Handle("/w", a.RequireLogin(ok))
	mux.Handle("/sensitive", a.RequireLogin(auth.RequireRecent(ok)))
	return &server{t: t, pool: pool, h: mux}
}

func (s *server) do(method, path string, form url.Values, header http.Header, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		r.Header[k] = v
	}
	for _, c := range cookies {
		if c != nil {
			r.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int, location string) {
	t.Helper()
	if w.Code != code || w.Header().Get("Location") != location {
		t.Fatalf("got %d to %q, want %d to %q; body:\n%.1000s", w.Code, w.Header().Get("Location"), code, location, w.Body)
	}
}

func TestSecondFactor(t *testing.T) {
	t.Parallel()
	f := codeFactor{enrolled: map[int64]bool{}}
	s := newServer(t, f)
	id, _ := authtest.SignIn(t, s.pool, "ana@example.com")
	f.enrolled[id] = true

	w := s.do("POST", "/login", login("ana@example.com", authtest.Password, "/w"), nil)
	wantStatus(t, w, http.StatusSeeOther, "/login/verify")
	if cookie(w, auth.CookieName) != nil {
		t.Fatal("a session before the second factor")
	}
	ch := cookie(w, "__Host-login")
	if body := s.do("GET", "/login/verify", nil, nil, ch).Body.String(); !strings.Contains(body, `<input name="code">`) {
		t.Fatalf("challenge page doesn't show the factor:\n%s", body)
	}

	w = s.do("POST", "/login/verify", url.Values{"factor": {"code"}, "code": {"000000"}}, nil, ch)
	wantStatus(t, w, http.StatusUnprocessableEntity, "")
	w = s.do("POST", "/login/verify", url.Values{"factor": {"code"}, "code": {"123456"}}, nil, ch)
	wantStatus(t, w, http.StatusSeeOther, "/w")
	if body := s.do("GET", "/w", nil, nil, cookie(w, auth.CookieName)).Body.String(); body != "aal 2" {
		t.Errorf("session after two factors: %q", body)
	}
	// The challenge is used up.
	wantStatus(t, s.do("POST", "/login/verify", url.Values{"factor": {"code"}, "code": {"123456"}}, nil, ch), http.StatusSeeOther, "/login?expired=1")

	// Five wrong codes end a challenge.
	ch = cookie(s.do("POST", "/login", login("ana@example.com", authtest.Password, "/w"), nil), "__Host-login")
	for range 5 {
		s.do("POST", "/login/verify", url.Values{"factor": {"code"}, "code": {"000000"}}, nil, ch)
	}
	wantStatus(t, s.do("POST", "/login/verify", url.Values{"factor": {"code"}, "code": {"123456"}}, nil, ch), http.StatusSeeOther, "/login?expired=1")

	// A password reset link doesn't skip the second factor.
	token := rand.Text()
	err := db.New(s.pool).CreatePasswordReset(t.Context(), db.CreatePasswordResetParams{TokenHash: auth.HashToken(token), AccountID: id, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	w = s.do("POST", "/reset-password", url.Values{"token": {token}, "password": {"a new password"}, "confirm_password": {"a new password"}}, nil)
	wantStatus(t, w, http.StatusSeeOther, "/login/verify")
	if cookie(w, auth.CookieName) != nil {
		t.Error("a session from a reset link before the second factor")
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()
	s := newServer(t, codeFactor{})
	id, c := authtest.SignIn(t, s.pool, "ana@example.com")

	wantStatus(t, s.do("GET", "/sensitive", nil, nil, c), http.StatusOK, "")
	if _, err := s.pool.Exec(t.Context(), "UPDATE sessions SET authenticated_at = now() - interval '1 hour' WHERE account_id = $1", id); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, s.do("GET", "/sensitive", nil, nil, c), http.StatusSeeOther, "/confirm?next=%2Fsensitive")
	// A post goes back to the page it came from.
	ref := http.Header{"Referer": {"http://example.com/sensitive?tab=1"}}
	wantStatus(t, s.do("POST", "/sensitive", url.Values{}, ref, c), http.StatusSeeOther, "/confirm?next=%2Fsensitive%3Ftab%3D1")

	wantStatus(t, s.do("POST", "/confirm", url.Values{"password": {"wrong"}, "next": {"/sensitive"}}, nil, c), http.StatusUnprocessableEntity, "")
	wantStatus(t, s.do("POST", "/confirm", url.Values{"password": {authtest.Password}, "next": {"/sensitive"}}, nil, c), http.StatusSeeOther, "/sensitive")
	wantStatus(t, s.do("GET", "/sensitive", nil, nil, c), http.StatusOK, "")
}
