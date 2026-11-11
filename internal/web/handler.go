// Package web holds the HTTP plumbing shared by features: the error-returning
// handler type, htmx helpers, middleware, the page layout, shared form
// components, and the embedded frontend build.
package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrNotFound makes Handle render the 404 page.
	ErrNotFound = errors.New("not found")
	// ErrForbidden makes Handle render the 403 page.
	ErrForbidden = errors.New("forbidden")
)

// HandlerFunc is an http.HandlerFunc that returns an error instead of writing
// error responses itself.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle adapts h to http.Handler. ErrNotFound and ErrForbidden render the
// 404 and 403 pages; any other error is logged and renders the 500 page,
// unless the client went away, as htmx does when it aborts a request.
func Handle(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil || errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return
		}
		if errors.Is(err, ErrNotFound) {
			_ = Render(w, r, http.StatusNotFound, NotFoundPage())
			return
		}
		if errors.Is(err, ErrForbidden) {
			_ = Render(w, r, http.StatusForbidden, ForbiddenPage())
			return
		}
		slog.ErrorContext(r.Context(), "handler failed", "method", r.Method, "path", r.URL.Path, "err", err)
		_ = Render(w, r, http.StatusInternalServerError, ErrorPage())
	})
}

// Render renders c into a buffer, then writes it with the given status, so a
// template error never leaves a half-written page.
func Render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// The same URL returns a fragment or a full page depending on these headers.
	h.Add("Vary", "HX-Request, HX-Target")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// IsHTMX reports whether r came from htmx and expects a fragment rather than
// a full page.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}

// HXTarget returns the id of the element an htmx request will swap into, or
// "" for non-htmx requests and targets without an id. Use it when one URL
// feeds several targets. htmx sends the target as tag#id.
func HXTarget(r *http.Request) string {
	if !IsHTMX(r) {
		return ""
	}
	_, id, _ := strings.Cut(r.Header.Get("HX-Target"), "#")
	return id
}

// Redirect sends the browser to url after a successful POST: an HX-Redirect
// header for htmx requests, a 303 otherwise.
func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// PathID parses the path value name as a positive id, or returns
// ErrNotFound.
func PathID(r *http.Request, name string) (int64, error) { return ParseID(r.PathValue(name)) }

// ParseID parses s as a positive id, or returns ErrNotFound.
func ParseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotFound
	}
	return id, nil
}

// NotFoundIfNoRows turns pgx.ErrNoRows into ErrNotFound and returns other
// errors unchanged.
func NotFoundIfNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// WantsNoContent reports whether the client asked for no response body
// (Prefer: return=minimal, RFC 7240). Islands that post a change and keep
// their own state send it; handlers answer 204.
func WantsNoContent(r *http.Request) bool {
	for _, v := range r.Header.Values("Prefer") {
		for p := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(p) == "return=minimal" {
				return true
			}
		}
	}
	return false
}

// SafeNext returns next if it is a path on this site, and fallback
// otherwise, so a ?next= parameter can't send the browser elsewhere.
func SafeNext(next, fallback string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return fallback
}
