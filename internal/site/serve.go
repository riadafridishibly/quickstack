package site

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/riadafridishibly/quickstack/internal/blob"
	"github.com/riadafridishibly/quickstack/internal/media"
	"github.com/riadafridishibly/quickstack/internal/web"
)

const (
	manifestPath = "/manifest.webmanifest"
	// rootCache is for files at fixed addresses, which change with the icon.
	rootCache = "public, max-age=3600"
)

// Routes serves the icon at the addresses clients ask for without reading
// a page, and the web app manifest. Register it outside the session
// middleware.
func Routes(mux *web.Mux, c *Cache, store blob.Store) {
	mux.Get("/favicon.ico", rootIcon(c, store, media.FaviconFile))
	mux.Get("/apple-touch-icon.png", rootIcon(c, store, media.AppleIconFile))
	mux.Get(manifestPath, func(w http.ResponseWriter, r *http.Request) error {
		s, err := c.Get(r.Context())
		if err != nil {
			return err
		}
		b, err := json.Marshal(manifestOf(s))
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", rootCache)
		_, _ = w.Write(b)
		return nil
	})
}

// rootIcon serves the live icon's file name, and a plain 404 when there
// is none: clients that ask for it want an image, not a page.
func rootIcon(c *Cache, store blob.Store, name string) web.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		s, err := c.Get(r.Context())
		if err != nil {
			return err
		}
		if s.Icon != nil {
			err = media.ServeIconFile(w, r, store, s.Icon.Rev, name, rootCache)
		}
		if s.Icon == nil || errors.Is(err, web.ErrNotFound) {
			http.NotFound(w, r)
			return nil
		}
		return err
	}
}

type manifest struct {
	Name       string         `json:"name"`
	StartURL   string         `json:"start_url"`
	Display    string         `json:"display"`
	ThemeColor string         `json:"theme_color"`
	Icons      []manifestIcon `json:"icons"`
}

type manifestIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes"`
	Type  string `json:"type"`
}

func manifestOf(s Settings) manifest {
	m := manifest{Name: "Quickstack", StartURL: "/", Display: "standalone", ThemeColor: s.Background, Icons: []manifestIcon{}}
	if s.Icon != nil {
		for _, size := range media.AppIconSizes {
			n := strconv.Itoa(size)
			m.Icons = append(m.Icons, manifestIcon{Src: media.IconURL(s.Icon.Rev, media.AppIconFile(size)), Sizes: n + "x" + n, Type: "image/png"})
		}
	}
	return m
}
