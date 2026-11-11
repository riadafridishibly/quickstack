package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"github.com/a-h/templ"
)

// dist is the Vite build output (`pnpm run build`). It holds only .gitkeep
// until the frontend has been built.
//
//go:embed all:dist
var dist embed.FS

// Vite entry points, relative to the Vite root (frontend/). Stylesheets are
// separate entries so both dev and production load them with a
// render-blocking <link>; imported from JS, Vite's dev server would apply
// them after first paint.
const (
	scriptEntry = "src/main.ts"
	// AppStyle is the dashboards' and the account pages' stylesheet.
	AppStyle = "src/app.css"
	// PublicStyle is the public pages' stylesheet.
	PublicStyle = "src/public.css"
)

var styles = []string{AppStyle, PublicStyle}

// Assets holds the <script> and <link> tags that load the frontend, one set
// per page stylesheet.
type Assets struct {
	tags map[string]string
}

// NewAssets points the tags at the Vite dev server when viteURL is set, and
// at the hashed files listed in the embedded Vite manifest otherwise.
func NewAssets(viteURL string) (*Assets, error) {
	if viteURL != "" {
		u := html.EscapeString(strings.TrimSuffix(viteURL, "/"))
		a := &Assets{tags: map[string]string{}}
		for _, style := range styles {
			a.tags[style] = fmt.Sprintf(
				`<link rel="stylesheet" href="%[1]s/%[2]s">`+
					`<script type="module" src="%[1]s/@vite/client"></script>`+
					`<script type="module" src="%[1]s/%[3]s"></script>`,
				u, style, scriptEntry,
			)
		}
		return a, nil
	}
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return assetsFromManifest(sub, styles)
}

type manifestChunk struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"`
}

func assetsFromManifest(fsys fs.FS, styles []string) (*Assets, error) {
	data, err := fs.ReadFile(fsys, ".vite/manifest.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("frontend not built: run `pnpm run build`, or `make dev` for the Vite dev server")
	}
	if err != nil {
		return nil, err
	}
	var manifest map[string]manifestChunk
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse vite manifest: %w", err)
	}
	for _, e := range slices.Concat([]string{scriptEntry}, styles) {
		if _, ok := manifest[e]; !ok {
			return nil, fmt.Errorf("vite manifest has no entry %q", e)
		}
	}

	// CSS the script's static imports pull in, and the imports to preload.
	var scriptCSS, preload []string
	seen := map[string]bool{}
	var walk func(key string)
	walk = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		chunk := manifest[key]
		for _, c := range chunk.CSS {
			if !seen["css:"+c] {
				seen["css:"+c] = true
				scriptCSS = append(scriptCSS, c)
			}
		}
		for _, imp := range chunk.Imports {
			preload = append(preload, manifest[imp].File)
			walk(imp)
		}
	}
	walk(scriptEntry)

	a := &Assets{tags: map[string]string{}}
	for _, style := range styles {
		var b strings.Builder
		fmt.Fprintf(&b, `<link rel="stylesheet" href="/static/%s">`, html.EscapeString(manifest[style].File))
		for _, c := range scriptCSS {
			if c != manifest[style].File {
				fmt.Fprintf(&b, `<link rel="stylesheet" href="/static/%s">`, html.EscapeString(c))
			}
		}
		fmt.Fprintf(&b, `<script type="module" src="/static/%s"></script>`, html.EscapeString(manifest[scriptEntry].File))
		for _, p := range preload {
			fmt.Fprintf(&b, `<link rel="modulepreload" href="/static/%s">`, html.EscapeString(p))
		}
		a.tags[style] = b.String()
	}
	return a, nil
}

type assetsKey struct{}

// Middleware makes the asset tags available to layouts through the request
// context.
func (a *Assets) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), assetsKey{}, a)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AssetTags renders the tags that load the script and the page stylesheet
// style, AppStyle or PublicStyle.
func AssetTags(style string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		a, ok := ctx.Value(assetsKey{}).(*Assets)
		if !ok {
			return nil
		}
		tags, ok := a.tags[style]
		if !ok {
			return fmt.Errorf("no stylesheet entry %q", style)
		}
		_, err := io.WriteString(w, tags)
		return err
	})
}

// StaticHandler serves the built frontend at /static/assets/. File names
// carry a content hash, so they can be cached forever.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(dist, "dist/assets")
	if err != nil {
		panic(err) // fs.Sub only fails on an invalid path
	}
	files := http.FileServerFS(sub)
	return http.StripPrefix("/static/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	}))
}
