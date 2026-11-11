// Package apptest runs the whole app against a test database, for tests
// that go through routing, middleware and templates.
package apptest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/riadafridishibly/quickstack/internal/app"
	"github.com/riadafridishibly/quickstack/internal/mail"
	"github.com/riadafridishibly/quickstack/internal/testdb"
)

// PublicURL is the origin links in test mail point to.
const PublicURL = "http://quickstack.test"

type Server struct {
	t    testing.TB
	Pool *pgxpool.Pool
	h    http.Handler
}

func New(t testing.TB) *Server {
	t.Helper()
	pool := testdb.New(t)
	// The Vite URL makes the layout point at a dev server, so tests don't
	// need a frontend build.
	h, err := app.New(app.Config{ViteURL: "http://localhost:8361", PublicURL: PublicURL}, pool)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{t: t, Pool: pool, h: h}
}

// Req is a request to send: Form makes it a POST.
type Req struct {
	Path   string
	Cookie *http.Cookie
	Form   url.Values
	// Target makes it an htmx request that swaps into that element id; "-"
	// is an htmx request with no target.
	Target string
	Header http.Header
}

func (s *Server) Do(r Req) *httptest.ResponseRecorder {
	s.t.Helper()
	method, body := http.MethodGet, ""
	if r.Form != nil {
		method, body = http.MethodPost, r.Form.Encode()
	}
	req := httptest.NewRequest(method, r.Path, strings.NewReader(body))
	if r.Form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if r.Cookie != nil {
		req.AddCookie(r.Cookie)
	}
	if r.Target != "" {
		req.Header.Set("HX-Request", "true")
		if r.Target != "-" {
			req.Header.Set("HX-Target", "div#"+r.Target)
		}
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, req)
	return w
}

func (s *Server) Get(path string, c *http.Cookie) *httptest.ResponseRecorder {
	return s.Do(Req{Path: path, Cookie: c})
}

func (s *Server) Post(path string, c *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	return s.Do(Req{Path: path, Cookie: c, Form: form})
}

// Status fails the test unless w has status want.
func Status(t testing.TB, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d; body:\n%.2000s", w.Code, want, w.Body.String())
	}
}

// Redirect fails the test unless w is a 303 to want.
func Redirect(t testing.TB, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	Status(t, w, http.StatusSeeOther)
	if got := w.Header().Get("Location"); got != want {
		t.Fatalf("redirect to %q, want %q", got, want)
	}
}

// Mail returns the messages queued to be sent, oldest first.
func (s *Server) Mail() []mail.Message {
	s.t.Helper()
	rows, err := s.Pool.Query(s.t.Context(), "SELECT args FROM river_job WHERE kind = 'send_email' ORDER BY id")
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var out []mail.Message
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			s.t.Fatal(err)
		}
		var a mail.SendArgs
		if err := json.Unmarshal(raw, &a); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, a.Message)
	}
	return out
}

var linkRE = regexp.MustCompile(regexp.QuoteMeta(PublicURL) + `(/\S+)`)

// Link returns the path of the link in the last mail sent to email.
func (s *Server) Link(email string) string {
	s.t.Helper()
	msgs := s.Mail()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == email {
			if m := linkRE.FindStringSubmatch(msgs[i].Text); m != nil {
				return m[1]
			}
		}
	}
	s.t.Fatalf("no mail with a link to %s", email)
	return ""
}

// Cookie returns the session cookie w sets.
func Cookie(t testing.TB, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-session" && c.MaxAge > 0 {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}
