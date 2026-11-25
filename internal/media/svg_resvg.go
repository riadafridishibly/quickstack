//go:build resvg

package media

// #cgo pkg-config: resvg
// #include <stdlib.h>
// #include <resvg.h>
import "C"

import (
	_ "embed"
	"fmt"
	"image"
	"runtime"
	"sync"
	"unsafe"

	vips "github.com/cshum/vipsgen/vips816"
)

// SVGRaster reports whether an SVG also gets a social card, og.jpg, drawn
// with resvg. It is true in builds with the resvg tag.
const SVGRaster = true

// font draws all text: an SVG can't load fonts, and the server's aren't
// used.
//
//go:embed fonts/DejaVuSans.ttf
var font []byte

var resvgOptions = sync.OnceValue(func() *C.resvg_options {
	opt := C.resvg_options_create()
	C.resvg_options_load_font_data(opt, (*C.char)(unsafe.Pointer(&font[0])), C.uintptr_t(len(font)))
	// resvg falls back to the serif family for a family it doesn't have,
	// so this makes every family the one font.
	family := C.CString("DejaVu Sans")
	defer C.free(unsafe.Pointer(family))
	C.resvg_options_set_serif_family(opt, family)
	return opt
})

// rasterizeSVG draws a sanitized SVG as the social card, cropped around its
// centre. Pages show the SVG itself.
func rasterizeSVG(svg []byte) ([]file, error) {
	t, err := parseSVGTree(svg)
	if err != nil {
		return nil, err
	}
	defer t.close()
	startVips()

	f, err := t.drawImage(image.Pt(CardWidth, CardHeight), true, cardFile)
	if err != nil {
		return nil, err
	}
	return []file{f}, nil
}

type svgTree struct {
	tree *C.resvg_render_tree
	size C.resvg_size
}

func parseSVGTree(svg []byte) (*svgTree, error) {
	if len(svg) == 0 {
		return nil, errSVGUnreadable
	}
	var tree *C.resvg_render_tree
	if code := C.resvg_parse_tree_from_data((*C.char)(unsafe.Pointer(&svg[0])), C.uintptr_t(len(svg)), resvgOptions(), &tree); code != C.RESVG_OK {
		return nil, fmt.Errorf("resvg: parse error %d", code)
	}
	return &svgTree{tree: tree, size: C.resvg_get_image_size(tree)}, nil
}

func (t *svgTree) close() { C.resvg_tree_destroy(t.tree) }

// draw renders the image p in size, scaled evenly to cover it, or to fit
// inside it when cover is false, and centred, as RGBA that isn't
// premultiplied. Only p's pixels are drawn, however thin the image.
func (t *svgTree) draw(p image.Point, cover bool) []byte {
	px := make([]byte, p.X*p.Y*4)
	w, h := float64(t.size.width), float64(t.size.height)
	s := min(float64(p.X)/w, float64(p.Y)/h)
	if cover {
		s = max(float64(p.X)/w, float64(p.Y)/h)
	}
	tr := C.resvg_transform_identity()
	tr.a, tr.d = C.float(s), C.float(s)
	tr.e = C.float((float64(p.X) - w*s) / 2)
	tr.f = C.float((float64(p.Y) - h*s) / 2)
	C.resvg_render(t.tree, tr, C.uint32_t(p.X), C.uint32_t(p.Y), (*C.char)(unsafe.Pointer(&px[0])))
	for i := 0; i < len(px); i += 4 {
		if a := uint32(px[i+3]); a != 0 && a != 255 {
			for j := i; j < i+3; j++ {
				px[j] = uint8((uint32(px[j])*255 + a/2) / a)
			}
		}
	}
	return px
}

// drawImage draws the image at p, as draw does, and passes it to save as an
// sRGB libvips image.
func (t *svgTree) drawImage(p image.Point, cover bool, save func(*vips.Image) (file, error)) (file, error) {
	px := t.draw(p, cover)
	// libvips reads px without copying it.
	defer runtime.KeepAlive(px)
	mem, err := vips.NewImageFromMemory(px, p.X, p.Y, 4)
	if err != nil {
		return file{}, err
	}
	defer mem.Close()
	img, err := mem.Copy(&vips.CopyOptions{Interpretation: vips.InterpretationSrgb})
	if err != nil {
		return file{}, err
	}
	defer img.Close()
	return save(img)
}

type svgIcon struct{ *svgTree }

func newSVGIcon(svg []byte) (iconSource, error) {
	t, err := parseSVGTree(svg)
	if err != nil {
		return nil, err
	}
	return svgIcon{t}, nil
}

func (s svgIcon) square(size int, save func(*vips.Image) (file, error)) (file, error) {
	return s.drawImage(image.Pt(size, size), false, save)
}
