package auth_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
)

func login(email, password, next string) url.Values {
	return url.Values{"email": {email}, "password": {password}, "next": {next}}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	authtest.SignIn(t, s.Pool, "ana@example.com")

	w := s.Post("/login", nil, login("ana@example.com", "wrong password", "/w"))
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "Email or password is incorrect.") {
		t.Error("no error message for a wrong password")
	}
	// An unknown email gets the same answer as a wrong password.
	w = s.Post("/login", nil, login("nobody@example.com", "wrong password", "/w"))
	apptest.Status(t, w, http.StatusUnprocessableEntity)

	w = s.Post("/login", nil, login(" Ana@Example.com ", authtest.Password, "//evil.example/"))
	apptest.Redirect(t, w, "/w")
	c := apptest.Cookie(t, w)
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie %+v", c)
	}
	apptest.Status(t, s.Get("/w", c), http.StatusOK)

	w = s.Post("/logout", c, url.Values{})
	apptest.Redirect(t, w, "/login")
	apptest.Redirect(t, s.Get("/w", c), "/login?next=%2Fw")
}

func TestLoginWithoutPassword(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	if _, err := db.New(s.Pool).CreateAccount(t.Context(), db.CreateAccountParams{Email: "ana@example.com", Name: "Ana"}); err != nil {
		t.Fatal(err)
	}
	apptest.Status(t, s.Post("/login", nil, login("ana@example.com", "", "/w")), http.StatusUnprocessableEntity)
}

func TestLoginRateLimit(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	var w = s.Post("/login", nil, login("ana@example.com", "x", "/w"))
	for range 10 {
		w = s.Post("/login", nil, login("ana@example.com", "x", "/w"))
	}
	apptest.Status(t, w, http.StatusTooManyRequests)
}

func TestSignup(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	w := s.Post("/signup", nil, url.Values{"name": {""}, "email": {"nope"}, "password": {"short"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	for _, msg := range []string{"Enter your name.", "Enter an email address", "Use at least 10 characters."} {
		if !strings.Contains(w.Body.String(), msg) {
			t.Errorf("no %q", msg)
		}
	}

	form := url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "password": {"long enough password"}, "next": {"/invite/x"}}
	w = s.Post("/signup", nil, form)
	apptest.Redirect(t, w, "/invite/x")
	apptest.Status(t, s.Get("/w", apptest.Cookie(t, w)), http.StatusOK)
	if msgs := s.Mail(); len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "Verify your email") {
		t.Errorf("mail after sign-up: %+v", msgs)
	}

	w = s.Post("/signup", nil, form)
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "already has an account") {
		t.Error("no message for a taken email")
	}
}

func TestPasswordReset(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	_, old := authtest.SignIn(t, s.Pool, "ana@example.com")

	// The answer is the same whether or not the email has an account.
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		w := s.Post("/forgot-password", nil, url.Values{"email": {email}})
		apptest.Status(t, w, http.StatusOK)
		if !strings.Contains(w.Body.String(), "Check your email") {
			t.Errorf("%s: no confirmation", email)
		}
	}
	if n := len(s.Mail()); n != 1 {
		t.Fatalf("%d mails queued, want 1", n)
	}
	link := s.Link("ana@example.com")
	apptest.Status(t, s.Get(link, nil), http.StatusOK)

	token := strings.TrimPrefix(link, "/reset-password?token=")
	token, _ = url.QueryUnescape(token)
	w := s.Post("/reset-password", nil, url.Values{"token": {token}, "password": {"a new password"}, "confirm_password": {"another"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)

	w = s.Post("/reset-password", nil, url.Values{"token": {token}, "password": {"a new password"}, "confirm_password": {"a new password"}})
	apptest.Redirect(t, w, "/w")
	apptest.Status(t, s.Get("/w", apptest.Cookie(t, w)), http.StatusOK)
	// The reset signs out every other session, and works once.
	apptest.Redirect(t, s.Get("/w", old), "/login?next=%2Fw")
	apptest.Status(t, s.Get(link, nil), http.StatusNotFound)
	apptest.Redirect(t, s.Post("/login", nil, login("ana@example.com", "a new password", "/w")), "/w")
}

func TestVerifyEmail(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	w := s.Post("/signup", nil, url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "password": {"long enough password"}})
	apptest.Redirect(t, w, "/w")
	link := s.Link("ana@example.com")
	token, _ := url.QueryUnescape(strings.TrimPrefix(link, "/verify-email?token="))

	// Opening the link changes nothing; the button does.
	if body := s.Get(link, nil).Body.String(); !strings.Contains(body, "Verify email") {
		t.Fatalf("verification page:\n%s", body)
	}
	acc, err := db.New(s.Pool).GetAccountByEmail(t.Context(), "ana@example.com")
	if err != nil || acc.EmailVerifiedAt != nil {
		t.Fatalf("verified before the button: %+v, %v", acc, err)
	}
	apptest.Redirect(t, s.Post("/verify-email", nil, url.Values{"token": {token}}), "/verify-email?done=1")
	if acc, _ = db.New(s.Pool).GetAccountByEmail(t.Context(), "ana@example.com"); acc.EmailVerifiedAt == nil {
		t.Error("email not verified")
	}
	apptest.Status(t, s.Get(link, nil), http.StatusNotFound)
	apptest.Status(t, s.Post("/verify-email", nil, url.Values{"token": {token}}), http.StatusNotFound)
}

func TestPasswordResetVerifiesEmail(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	apptest.Redirect(t, s.Post("/signup", nil, url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "password": {"long enough password"}}), "/w")
	s.Post("/forgot-password", nil, url.Values{"email": {"ana@example.com"}})
	token, _ := url.QueryUnescape(strings.TrimPrefix(s.Link("ana@example.com"), "/reset-password?token="))
	w := s.Post("/reset-password", nil, url.Values{"token": {token}, "password": {"a new password"}, "confirm_password": {"a new password"}})
	apptest.Redirect(t, w, "/w")
	if body := s.Get("/account", apptest.Cookie(t, w)).Body.String(); !strings.Contains(body, ">Verified<") {
		t.Error("a reset from the email doesn't verify it")
	}
}
