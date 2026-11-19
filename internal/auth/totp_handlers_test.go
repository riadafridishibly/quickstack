package auth_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/riadafridishibly/quickstack/internal/apptest"
	"github.com/riadafridishibly/quickstack/internal/auth"
	"github.com/riadafridishibly/quickstack/internal/auth/authtest"
)

var (
	keyRE    = regexp.MustCompile(`id="totp-key"[^>]*>([A-Z2-7 ]+)<`)
	sealedRE = regexp.MustCompile(`name="secret" value="([^"]+)"`)
	codesRE  = regexp.MustCompile(`<li>([a-z2-7]{4}-[a-z2-7]{4}-[a-z2-7]{4})</li>`)
)

func match(t *testing.T, re *regexp.Regexp, body string) string {
	t.Helper()
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s in:\n%.3000s", re, body)
	}
	return m[1]
}

// enableTOTP sets up an authenticator app for the account c signs in as and
// returns its key and recovery codes.
func enableTOTP(t *testing.T, s *apptest.Server, c *http.Cookie) (string, []string) {
	t.Helper()
	w := s.Get("/account/security/totp", c)
	apptest.Status(t, w, http.StatusOK)
	key, sealed := match(t, keyRE, w.Body.String()), match(t, sealedRE, w.Body.String())

	apptest.Status(t, s.Post("/account/security/totp", c, url.Values{"secret": {sealed}, "totp_code": {"000000"}}), http.StatusUnprocessableEntity)
	w = s.Post("/account/security/totp", c, url.Values{"secret": {sealed}, "totp_code": {auth.TOTPCode(key, time.Now())}})
	apptest.Status(t, w, http.StatusOK)
	var codes []string
	for _, m := range codesRE.FindAllStringSubmatch(w.Body.String(), -1) {
		codes = append(codes, m[1])
	}
	if len(codes) != 10 {
		t.Fatalf("%d recovery codes in:\n%.3000s", len(codes), w.Body)
	}
	return key, codes
}

// challenge signs in with the password and returns the challenge cookie.
func challenge(t *testing.T, s *apptest.Server, email string) *http.Cookie {
	t.Helper()
	w := s.Post("/login", nil, login(email, authtest.Password, "/w"))
	apptest.Redirect(t, w, "/login/verify")
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-login" {
			return c
		}
	}
	t.Fatal("no challenge cookie")
	return nil
}

func TestTOTP(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	_, c := authtest.SignIn(t, s.Pool, "ana@example.com")
	if body := s.Get("/account/security", c).Body.String(); !strings.Contains(body, `href="/account/security/totp"`) || strings.Contains(body, "Recovery codes") {
		t.Fatalf("security page before setup:\n%s", body)
	}
	key, codes := enableTOTP(t, s, c)
	if mail := s.Mail(); !strings.Contains(mail[len(mail)-1].Text, "An authenticator app was set up") {
		t.Errorf("no notice of the new factor: %+v", mail[len(mail)-1])
	}
	apptest.Redirect(t, s.Get("/account/security/totp", c), "/account/security")
	if body := s.Get("/account/security", c).Body.String(); !strings.Contains(body, "10 of 10 codes left") {
		t.Errorf("security page after setup:\n%s", body)
	}

	// The code that set the app up is spent; the next one signs in.
	ch := challenge(t, s, "ana@example.com")
	body := s.Get("/login/verify", ch).Body.String()
	if !strings.Contains(body, `name="totp_code"`) || !strings.Contains(body, `name="recovery_code"`) {
		t.Fatalf("challenge page:\n%s", body)
	}
	apptest.Status(t, s.Post("/login/verify", ch, url.Values{"factor": {"totp"}, "totp_code": {auth.TOTPCode(key, time.Now())}}), http.StatusUnprocessableEntity)
	next := auth.TOTPCode(key, time.Now().Add(30*time.Second))
	apptest.Redirect(t, s.Post("/login/verify", ch, url.Values{"factor": {"totp"}, "totp_code": {next}}), "/w")
	ch = challenge(t, s, "ana@example.com")
	apptest.Status(t, s.Post("/login/verify", ch, url.Values{"factor": {"totp"}, "totp_code": {next}}), http.StatusUnprocessableEntity)

	// A recovery code works once, written any way.
	w := s.Post("/login/verify", ch, url.Values{"factor": {"recovery"}, "recovery_code": {strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))}})
	apptest.Redirect(t, w, "/w")
	c = apptest.Cookie(t, w)
	if mail := s.Mail(); !strings.Contains(mail[len(mail)-1].Text, "9 of your recovery codes are left") {
		t.Errorf("no notice of the recovery code: %+v", mail[len(mail)-1])
	}
	ch = challenge(t, s, "ana@example.com")
	apptest.Status(t, s.Post("/login/verify", ch, url.Values{"factor": {"recovery"}, "recovery_code": {codes[0]}}), http.StatusUnprocessableEntity)

	// Confirming asks for a factor, not the password.
	if _, err := s.Pool.Exec(t.Context(), "UPDATE sessions SET authenticated_at = now() - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	apptest.Redirect(t, s.Post("/account/security/totp/delete", c, url.Values{}), "/confirm?next=%2Fw")
	body = s.Get("/confirm?next=/account/security", c).Body.String()
	if strings.Contains(body, `name="password"`) || !strings.Contains(body, `name="totp_code"`) {
		t.Fatalf("confirm page:\n%s", body)
	}
	apptest.Status(t, s.Post("/confirm", c, url.Values{"password": {authtest.Password}, "next": {"/account/security"}}), http.StatusNotFound)
	apptest.Redirect(t, s.Post("/confirm", c, url.Values{"factor": {"recovery"}, "recovery_code": {codes[1]}, "next": {"/account/security"}}), "/account/security")

	// New codes replace the old ones.
	w = s.Post("/account/security/recovery-codes", c, url.Values{})
	apptest.Status(t, w, http.StatusOK)
	ch = challenge(t, s, "ana@example.com")
	apptest.Status(t, s.Post("/login/verify", ch, url.Values{"factor": {"recovery"}, "recovery_code": {codes[2]}}), http.StatusUnprocessableEntity)
	apptest.Redirect(t, s.Post("/login/verify", ch, url.Values{"factor": {"recovery"}, "recovery_code": {match(t, codesRE, w.Body.String())}}), "/w")

	// Removing the app removes the recovery codes, and signing in needs only
	// the password again.
	apptest.Redirect(t, s.Post("/account/security/totp/delete", c, url.Values{}), "/account/security")
	if body := s.Get("/account/security", c).Body.String(); strings.Contains(body, "Recovery codes") {
		t.Errorf("recovery codes without a factor:\n%s", body)
	}
	apptest.Redirect(t, s.Post("/login", nil, login("ana@example.com", authtest.Password, "/w")), "/w")
}

func TestTOTPSetupForgedSecret(t *testing.T) {
	t.Parallel()
	s := apptest.New(t)
	_, ana := authtest.SignIn(t, s.Pool, "ana@example.com")
	_, ben := authtest.SignIn(t, s.Pool, "ben@example.com")
	w := s.Get("/account/security/totp", ana)
	key, sealed := match(t, keyRE, w.Body.String()), match(t, sealedRE, w.Body.String())
	// Ana's setup form doesn't set up Ben's account.
	w = s.Post("/account/security/totp", ben, url.Values{"secret": {sealed}, "totp_code": {auth.TOTPCode(key, time.Now())}})
	apptest.Status(t, w, http.StatusNotFound)
}
