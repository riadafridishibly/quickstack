package web

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetsFromManifest(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{".vite/manifest.json": {Data: []byte(`{
		"src/app.css": {"file": "assets/app-000.css", "isEntry": true},
		"src/public.css": {"file": "assets/public-111.css", "isEntry": true},
		"src/main.ts": {"file": "assets/main-abc.js", "isEntry": true, "css": ["assets/main-abc.css"], "imports": ["_shared.js"]},
		"_shared.js": {"file": "assets/shared-def.js", "css": ["assets/shared-def.css"]},
		"src/islands/x.tsx": {"file": "assets/x-123.js", "isDynamicEntry": true}
	}`)}}

	a, err := assetsFromManifest(fsys, styles)
	if err != nil {
		t.Fatal(err)
	}
	script := `<link rel="stylesheet" href="/static/assets/main-abc.css">` +
		`<link rel="stylesheet" href="/static/assets/shared-def.css">` +
		`<script type="module" src="/static/assets/main-abc.js"></script>` +
		`<link rel="modulepreload" href="/static/assets/shared-def.js">`
	for style, file := range map[string]string{AppStyle: "app-000.css", PublicStyle: "public-111.css"} {
		want := `<link rel="stylesheet" href="/static/assets/` + file + `">` + script
		if a.tags[style] != want {
			t.Errorf("%s tags:\n got %s\nwant %s", style, a.tags[style], want)
		}
	}
}

func TestAssetsFromManifestMissing(t *testing.T) {
	t.Parallel()
	_, err := assetsFromManifest(fstest.MapFS{}, styles)
	if err == nil || !strings.Contains(err.Error(), "pnpm run build") {
		t.Errorf("err = %v, want hint to run pnpm run build", err)
	}
}

func TestDevAssets(t *testing.T) {
	t.Parallel()
	a, err := NewAssets("http://localhost:8361/")
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range styles {
		tags := a.tags[style]
		if !strings.HasPrefix(tags, `<link rel="stylesheet" href="http://localhost:8361/`+style+`">`) ||
			!strings.Contains(tags, `src="http://localhost:8361/@vite/client"`) ||
			!strings.Contains(tags, `src="http://localhost:8361/src/main.ts"`) {
			t.Errorf("dev tags for %s = %s", style, tags)
		}
	}
}
