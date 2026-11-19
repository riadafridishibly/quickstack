package account_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/account"
	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
	"github.com/riadafridishibly/quickstack/internal/db"
)

func TestChangePassword(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	id, c := authtest.SignIn(t, s.Pool, "ana@example.com")
	token, err := auth.NewSession(t.Context(), db.New(s.Pool), id, auth.AAL1, "other device")
	if err != nil {
		t.Fatal(err)
	}
	other := &http.Cookie{Name: auth.CookieName, Value: token}

	w := s.Post("/account/security/password", c, url.Values{"current_password": {"wrong one"}, "new_password": {"a new password"}, "confirm_password": {"a new password"}})
	apptest.Status(t, w, http.StatusUnprocessableEntity)
	if !strings.Contains(w.Body.String(), "That isn’t your current password.") {
		t.Error("no message for a wrong current password")
	}
	apptest.Redirect(t, s.Post("/account/security/password", c, url.Values{
		"current_password": {authtest.Password}, "new_password": {"a new password"}, "confirm_password": {"a new password"},
	}), "/account/security?done=password")
	apptest.Status(t, s.Get("/account", c), http.StatusOK)
	apptest.Redirect(t, s.Get("/account", other), "/login?next=%2Faccount")
}

func TestSessions(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	id, c := authtest.SignIn(t, s.Pool, "ana@example.com")
	q := db.New(s.Pool)
	token, err := auth.NewSession(t.Context(), q, id, auth.AAL1, "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := q.ListSessions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var otherID int64
	for _, s := range sessions {
		if s.UserAgent != "test" {
			otherID = s.ID
		}
	}
	if !strings.Contains(s.Get("/account/security", c).Body.String(), "Firefox on Linux") {
		t.Error("sessions list doesn't name the other device")
	}
	w := s.Do(apptest.Req{Path: fmt.Sprintf("/account/security/sessions/%d/delete", otherID), Cookie: c, Target: "-", Form: url.Values{}})
	apptest.Status(t, w, http.StatusOK)
	apptest.Redirect(t, s.Get("/w", &http.Cookie{Name: auth.CookieName, Value: token}), "/login?next=%2Fw")
	// Another account's session id is not found.
	apptest.Status(t, s.Post(fmt.Sprintf("/account/security/sessions/%d/delete", otherID), c, url.Values{}), http.StatusNotFound)
}

func TestDevice(t *testing.T) {
	t.Parallel()
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15":                   "Safari on macOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36 Edg/140.0":                   "Edge on Windows",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1": "Safari on iPhone",
		"curl/8.7.1": "curl",
	} {
		if got := account.Device(ua); got != want {
			t.Errorf("Device(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestSendVerification(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	w := s.Post("/signup", nil, url.Values{"name": {"Ana"}, "email": {"ana@example.com"}, "password": {"long enough password"}})
	c := apptest.Cookie(t, w)
	if !strings.Contains(s.Get("/account", c).Body.String(), "Not verified") {
		t.Error("profile doesn't say the email isn't verified")
	}
	apptest.Redirect(t, s.Post("/account/verify-email", c, url.Values{}), "/account?done=sent")
	if n := len(s.Mail()); n != 2 {
		t.Errorf("%d mails, want 2", n)
	}
	s.Post("/account/verify-email", c, url.Values{})
	s.Post("/account/verify-email", c, url.Values{})
	w = s.Post("/account/verify-email", c, url.Values{})
	apptest.Status(t, w, http.StatusTooManyRequests)
	if strings.Contains(w.Body.String(), `aria-invalid="true"`) {
		t.Error("the rate-limit message marks a field invalid")
	}

	_, verified := authtest.SignIn(t, s.Pool, "bo@example.com")
	apptest.Redirect(t, s.Post("/account/verify-email", verified, url.Values{}), "/account")
}

func TestSecurityWithoutPassword(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	q := db.New(s.Pool)
	acc, err := q.CreateAccount(t.Context(), db.CreateAccountParams{Email: "ana@example.com", Name: "Ana"})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.NewSession(t.Context(), q, acc.ID, auth.AAL1, "test")
	if err != nil {
		t.Fatal(err)
	}
	body := s.Get("/account/security", &http.Cookie{Name: auth.CookieName, Value: token}).Body.String()
	if !strings.Contains(body, "Set a password") || strings.Contains(body, "Current password") {
		t.Errorf("security page of an account without a password:\n%s", body)
	}
}
