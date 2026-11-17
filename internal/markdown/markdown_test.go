package markdown

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

var assets = []db.Asset{
	{Key: "ready234", Status: media.Ready, Width: 1200, Height: 800, Widths: []int32{320, 640, 1024, 1200}, Alt: "Stored alt", Credit: "Stored credit"},
	{Key: "bzzy2345", Status: media.Processing, Width: 900, Height: 600},
	{Key: "fa772345", Status: media.Failed, Error: "not an image"},
}

func resolver(calls *[][]string) Resolver {
	return func(_ context.Context, keys []string) ([]db.Asset, error) {
		*calls = append(*calls, keys)
		var out []db.Asset
		for _, a := range assets {
			if slices.Contains(keys, a.Key) {
				out = append(out, a)
			}
		}
		return out, nil
	}
}

// TestGolden renders each testdata/*.md and compares the HTML and the
// problems with the .golden file next to it. go test -update rewrites them.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*.md")
	if err != nil || len(files) == 0 {
		t.Fatal("no testdata", err)
	}
	for _, f := range files {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".md"), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			html, problems, err := Render(context.Background(), string(src), resolver(&calls))
			if err != nil {
				t.Fatal(err)
			}
			if len(calls) > 1 {
				t.Errorf("resolver called %d times, want at most once", len(calls))
			}
			var b strings.Builder
			b.WriteString(string(html))
			if len(problems) > 0 {
				b.WriteString("\n<!-- problems\n")
				for _, p := range problems {
					b.WriteString(p.String() + "\n")
				}
				b.WriteString("-->\n")
			}
			golden := strings.TrimSuffix(f, ".md") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err, "(run go test -update)")
			}
			if b.String() != string(want) {
				t.Errorf("output differs from %s (go test -update rewrites it):\n%s", golden, b.String())
			}
		})
	}
}

func TestUnsafeIsLeftOut(t *testing.T) {
	src, err := os.ReadFile("testdata/unsafe.md")
	if err != nil {
		t.Fatal(err)
	}
	html, problems, err := Render(context.Background(), string(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.ToLower(string(html))
	for _, bad := range []string{"<div", "<b>", "<script", `href="javascript`, `href="data`, "onclick", "style="} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%s", bad, html)
		}
	}
	if len(problems) == 0 {
		t.Error("no problems reported")
	}
}

func TestResolverError(t *testing.T) {
	boom := errors.New("boom")
	_, _, err := Render(context.Background(), "![x](/media/ready234)", func(context.Context, []string) ([]db.Asset, error) {
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestResolverSkippedWithoutImages(t *testing.T) {
	var calls [][]string
	if _, _, err := Render(context.Background(), "No images here.", resolver(&calls)); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Errorf("resolver called for a post with no images")
	}
}

func TestDraftShowsProcessingImages(t *testing.T) {
	var calls [][]string
	src := "![Still going](/media/bzzy2345)\n\n![Broken](/media/fa772345)"
	html, problems, pending, err := Draft(context.Background(), src, resolver(&calls))
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Error("pending = false, want true")
	}
	if !strings.Contains(string(html), `viewBox="0 0 900 600"`) || !strings.Contains(string(html), `aria-label="Still going (processing)"`) {
		t.Errorf("no placeholder for the processing image:\n%s", html)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "couldn’t be processed") {
		t.Errorf("problems = %v, want only the failed image", problems)
	}

	if _, _, pending, _ := Draft(context.Background(), "![x](/media/ready234)", resolver(&calls)); pending {
		t.Error("pending = true for a ready image")
	}
}
