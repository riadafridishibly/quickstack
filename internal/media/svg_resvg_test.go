//go:build resvg

package media

import (
	"encoding/base64"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	vips "github.com/cshum/vipsgen/vips816"
)

const redRect = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="12"><rect width="24" height="12" fill="#c00"/></svg>`

func TestRasterizeSVG(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		svg  string
	}{
		{"red", redRect},
		// Red edges that cover the card, scaled evenly and centred, leaves
		// out; scaled to fit, they would show at its sides.
		{"centred", `<svg xmlns="http://www.w3.org/2000/svg" width="30" height="10"><rect width="30" height="10" fill="#c00"/><rect x="5" width="20" height="10" fill="#0c0"/></svg>`},
		{"wide", `<svg xmlns="http://www.w3.org/2000/svg" width="2000" height="10"><rect width="2000" height="10" fill="#c00"/></svg>`},
		{"tall", `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="12000"><rect width="300" height="12000" fill="#c00"/></svg>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files, err := rasterizeSVG([]byte(tt.svg))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0].name != CardFile || files[0].width != 0 {
				t.Fatalf("files %+v, want only %s", files, CardFile)
			}
			img, err := vips.NewImageFromBuffer(files[0].data, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer img.Close()
			if got := image.Pt(img.Width(), img.Height()); got != image.Pt(CardWidth, CardHeight) {
				t.Fatalf("%s is %v", CardFile, got)
			}
			want := []float64{204, 0, 0}
			if tt.name == "centred" {
				want = []float64{0, 204, 0}
			}
			for _, x := range []int{5, CardWidth / 2, CardWidth - 6} {
				px, err := img.Getpoint(x, CardHeight/2, nil)
				if err != nil {
					t.Fatal(err)
				}
				for i := range want {
					if math.Abs(px[i]-want[i]) > 20 {
						t.Errorf("pixel at x %d is %v, want %v", x, px, want)
						break
					}
				}
			}
		})
	}
}

// painted counts the pixels of svg, drawn at p, that aren't transparent.
func painted(t *testing.T, svg string, p image.Point) int {
	t.Helper()
	tree, err := parseSVGTree([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.close()
	n := 0
	px := tree.draw(p, true)
	for i := 3; i < len(px); i += 4 {
		if px[i] != 0 {
			n++
		}
	}
	return n
}

func TestSVGText(t *testing.T) {
	t.Parallel()
	for _, family := range []string{"Arial", "sans-serif", "monospace", "DejaVu Sans"} {
		svg := `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="50"><text x="5" y="35" font-size="30" font-family="` + family + `">Hello</text></svg>`
		if n := painted(t, svg, image.Pt(200, 50)); n < 200 {
			t.Errorf("font-family %s: %d pixels painted", family, n)
		}
	}
}

// An <image> in an SVG that didn't pass the sanitizer still can't read a
// file.
func TestSVGReadsNoFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "red.svg")
	if err := os.WriteFile(path, []byte(redRect), 0o600); err != nil {
		t.Fatal(err)
	}
	withImage := func(href string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="12"><image width="24" height="12" href="` + href + `"/></svg>`
	}
	for _, href := range []string{path, "file://" + path, "red.svg"} {
		if n := painted(t, withImage(href), image.Pt(24, 12)); n != 0 {
			t.Errorf("%s: %d pixels painted", href, n)
		}
	}
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encodePNG(t, testImage(24, 12)))
	if n := painted(t, withImage(png), image.Pt(24, 12)); n != 24*12 {
		t.Errorf("data: PNG: %d pixels painted", n)
	}
}
