package web

import (
	"net/http"
	"slices"
)

// Mux is an http.ServeMux that remembers its patterns, so a test can list
// every route the app serves.
type Mux struct {
	// Name says which part of the app the mux serves, in the route listing.
	Name     string
	mux      *http.ServeMux
	patterns []string
}

func NewMux(name string) *Mux {
	return &Mux{Name: name, mux: http.NewServeMux()}
}

func (m *Mux) Get(path string, h HandlerFunc)  { m.Handle("GET "+path, Handle(h)) }
func (m *Mux) Post(path string, h HandlerFunc) { m.Handle("POST "+path, Handle(h)) }

// Handle registers h for a ServeMux pattern, for handlers that need
// middleware or aren't a HandlerFunc.
func (m *Mux) Handle(pattern string, h http.Handler) {
	m.mux.Handle(pattern, h)
	m.patterns = append(m.patterns, pattern)
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

// Handler returns the handler and pattern that would serve r.
func (m *Mux) Handler(r *http.Request) (http.Handler, string) { return m.mux.Handler(r) }

// Patterns lists what was registered, in order.
func (m *Mux) Patterns() []string { return slices.Clone(m.patterns) }
