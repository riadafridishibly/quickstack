package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"runtime/trace"
	"strings"
	"time"
)

// Logger logs one line per request, skipping static assets.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start),
			"htmx", r.Header.Get("HX-Request") == "true",
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Recoverer turns a panic in a handler into a logged 500.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				slog.ErrorContext(r.Context(), "panic", "method", r.Method, "path", r.URL.Path, "panic", v)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// SecureHeaders sets the Content-Security-Policy and related headers.
// viteURL is the Vite dev server origin, allowed in development only;
// pass "" in production.
//
// The policy allows no inline scripts, inline style attributes, or eval, so
// templates can't use <script> blocks, style="", onclick="", or hx-on:*.
func SecureHeaders(viteURL string) func(http.Handler) http.Handler {
	script, style, connect, font := "'self'", "'self'", "'self'", "'self'"
	if viteURL != "" {
		ws := "ws" + strings.TrimPrefix(viteURL, "http")
		script += " " + viteURL
		font += " " + viteURL
		// Vite injects CSS as <style> tags in dev.
		style += " " + viteURL + " 'unsafe-inline'"
		connect += " " + viteURL + " " + ws
	}
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"style-src " + style,
		"connect-src " + connect,
		"img-src 'self' data:",
		"font-src " + font,
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			next.ServeHTTP(w, r)
		})
	}
}

// RealIP sets r.RemoteAddr to the client address from X-Forwarded-For when
// the connection comes from a trusted proxy. It walks the header from the
// right and takes the first address that isn't a trusted proxy, because
// clients can put anything on the left.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(next http.Handler) http.Handler {
		if len(trusted) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, err := netip.ParseAddrPort(r.RemoteAddr)
			if err != nil || !isTrusted(peer.Addr().Unmap()) {
				next.ServeHTTP(w, r)
				return
			}
			hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
			for i := len(hops) - 1; i >= 0; i-- {
				a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
				if err != nil {
					break
				}
				a = a.Unmap()
				if !isTrusted(a) {
					r2 := r.Clone(r.Context())
					r2.RemoteAddr = netip.AddrPortFrom(a, 0).String()
					next.ServeHTTP(w, r2)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Trace runs each request in a runtime/trace task and region named by
// name(r), such as its route pattern, while a trace is being recorded.
func Trace(name func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !trace.IsEnabled() {
				next.ServeHTTP(w, r)
				return
			}
			n := name(r)
			ctx, task := trace.NewTask(r.Context(), n)
			defer task.End()
			trace.WithRegion(ctx, n, func() { next.ServeHTTP(w, r.WithContext(ctx)) })
		})
	}
}

type pathKey struct{}

// WithPath records the request path, so navigation can mark the current
// page.
func WithPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), pathKey{}, r.URL.Path)))
	})
}

// Path returns the path WithPath recorded.
func Path(ctx context.Context) string {
	p, _ := ctx.Value(pathKey{}).(string)
	return p
}
