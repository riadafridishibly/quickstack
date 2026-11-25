package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"slices"
	"testing"

	"github.com/riadafridishibly/quickstack/internal/db"
)

func solidPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodePNG(t *testing.T, name string, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s isn't a PNG: %v", name, err)
	}
	return img
}

func rgba(img image.Image, x, y int) [4]uint8 {
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return [4]uint8{c.R, c.G, c.B, c.A}
}

func near(a, b [4]uint8) bool {
	for i := range a {
		if d := int(a[i]) - int(b[i]); d < -8 || d > 8 {
			return false
		}
	}
	return true
}

var (
	red         = [4]uint8{255, 0, 0, 255}
	green       = [4]uint8{0, 255, 0, 255}
	transparent = [4]uint8{0, 0, 0, 0}
)

// checkIcons checks the icons drawn from a red image twice as wide as it is
// tall, on a green background: each file's format and size, the image
// centred with transparent margins above and below, and the Apple icon's
// margins green.
func checkIcons(t *testing.T, files []file, svg bool) {
	t.Helper()
	byName := map[string][]byte{}
	var names []string
	for _, f := range files {
		byName[f.name] = f.data
		names = append(names, f.name)
	}
	want := []string{FaviconFile, AppleIconFile, AppIconFile(192), AppIconFile(512)}
	if svg {
		want = append(want, IconSVGFile)
	}
	if !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}

	pngs := map[string]int{AppleIconFile: appleIconSize, AppIconFile(192): 192, AppIconFile(512): 512}
	ico := byName[FaviconFile]
	if len(ico) < 22 || binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) != 1 ||
		ico[6] != faviconSize || ico[7] != faviconSize ||
		int(binary.LittleEndian.Uint32(ico[14:])) != len(ico)-22 || binary.LittleEndian.Uint32(ico[18:]) != 22 {
		t.Fatalf("%s header % x", FaviconFile, ico[:min(len(ico), 22)])
	}
	byName["favicon PNG"] = ico[22:]
	pngs["favicon PNG"] = faviconSize

	for name, size := range pngs {
		img := decodePNG(t, name, byName[name])
		if got := img.Bounds().Size(); got != image.Pt(size, size) {
			t.Errorf("%s is %v, want %d square", name, got, size)
			continue
		}
		margin := transparent
		if name == AppleIconFile {
			margin = green
			for y := range size {
				for x := range size {
					if a := rgba(img, x, y)[3]; a != 255 {
						t.Fatalf("%s has alpha %d at %d,%d", name, a, x, y)
					}
				}
			}
		}
		for _, p := range []image.Point{{size / 2, 0}, {size / 2, size - 1}} {
			if got := rgba(img, p.X, p.Y); !near(got, margin) {
				t.Errorf("%s at %v is %v, want %v", name, p, got, margin)
			}
		}
		for _, p := range []image.Point{{size / 2, size / 2}, {1, size / 2}, {size - 2, size / 2}} {
			if got := rgba(img, p.X, p.Y); !near(got, red) {
				t.Errorf("%s at %v is %v, want red", name, p, got)
			}
		}
	}
}

func TestRenderIconRaster(t *testing.T) {
	t.Parallel()
	files, err := renderIcon(solidPNG(t, 1024, 512, color.NRGBA{255, 0, 0, 255}), false, []float64{0, 255, 0})
	if err != nil {
		t.Fatal(err)
	}
	checkIcons(t, files, false)
}

func TestRenderIconGrey(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 600, 600))); err != nil {
		t.Fatal(err)
	}
	files, err := renderIcon(b.Bytes(), false, []float64{255, 255, 255})
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, files[2].name, files[2].data)
	if _, ok := img.(*image.NRGBA); !ok {
		t.Errorf("%s from a grey PNG decodes as %T, want RGBA", files[2].name, img)
	}
}

const wideRedSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20"><rect width="40" height="20" fill="#f00"/></svg>`

func TestRenderIconSVG(t *testing.T) {
	t.Parallel()
	files, err := renderIcon([]byte(wideRedSVG), true, []float64{0, 255, 0})
	if !SVGRaster {
		if err == nil {
			t.Fatal("an SVG icon was drawn without the resvg tag")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	checkIcons(t, files, true)
	if got := files[len(files)-1].data; string(got) != wideRedSVG {
		t.Errorf("%s is %q, want the sanitized SVG", IconSVGFile, got)
	}
}

func TestIconProblem(t *testing.T) {
	t.Parallel()
	ready := func(ct string, w, h int32) db.Asset {
		return db.Asset{Status: Ready, ContentType: ct, Width: w, Height: h}
	}
	for _, tt := range []struct {
		name string
		a    db.Asset
		ok   bool
	}{
		{"square", ready("image/png", 512, 512), true},
		{"wide", ready("image/jpeg", 2000, 512), true},
		{"short", ready("image/png", 2000, 511), false},
		{"processing", db.Asset{Status: Processing, ContentType: "image/png", Width: 512, Height: 512}, false},
		{"failed", db.Asset{Status: Failed, ContentType: "image/png", Width: 512, Height: 512}, false},
		{"small svg", ready(svgContentType, 16, 16), SVGRaster},
	} {
		if got := IconProblem(tt.a); (got == "") != tt.ok {
			t.Errorf("%s: IconProblem = %q", tt.name, got)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	t.Parallel()
	if got, err := parseHexColor("#0a80ff"); err != nil || !slices.Equal(got, []float64{10, 128, 255}) {
		t.Errorf("#0a80ff = %v, %v", got, err)
	}
	for _, s := range []string{"", "0a80ff", "#0a80f", "#0a80fff", "#0a80fg", "#+a80ff"} {
		if _, err := parseHexColor(s); err == nil {
			t.Errorf("%q was accepted", s)
		}
	}
}
