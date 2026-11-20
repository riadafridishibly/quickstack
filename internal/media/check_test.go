package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"slices"
	"strings"
	"testing"

	vips "github.com/cshum/vipsgen/vips816"
)

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func encodeJPEG(t testing.TB, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, testImage(w, h), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation adds an EXIF segment with orientation o to a JPEG.
func withOrientation(jpg []byte, o uint16) []byte {
	exif := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00")
	binary.BigEndian.PutUint16(exif[24:], o)
	seg := append([]byte{0xff, 0xe1, 0, 0}, exif...)
	binary.BigEndian.PutUint16(seg[2:], uint16(len(exif)+2))
	return slices.Concat(jpg[:2], seg, jpg[2:])
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// blackPNG is a w×h black PNG, made by libvips, which is quicker than
// image/png for large images.
func blackPNG(t testing.TB, w, h int) []byte {
	t.Helper()
	startVips()
	img, err := vips.NewBlack(w, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer img.Close()
	b, err := img.PngsaveBuffer(&vips.PngsaveBufferOptions{Compression: 1})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCheck(t *testing.T) {
	t.Parallel()
	var g bytes.Buffer
	if err := gif.Encode(&g, testImage(30, 20), nil); err != nil {
		t.Fatal(err)
	}
	startVips()
	pngImg, err := vips.NewImageFromBuffer(encodePNG(t, testImage(40, 30)), nil)
	if err != nil {
		t.Fatal(err)
	}
	webp, err := pngImg.WebpsaveBuffer(nil)
	pngImg.Close()
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name        string
		data        []byte
		contentType string
		w, h        int
	}{
		{"jpeg", encodeJPEG(t, 50, 40), "image/jpeg", 50, 40},
		{"jpeg turned", withOrientation(encodeJPEG(t, 50, 40), 6), "image/jpeg", 40, 50},
		{"png", encodePNG(t, testImage(10, 20)), "image/png", 10, 20},
		{"gif", g.Bytes(), "image/gif", 30, 20},
		{"webp", webp, "image/webp", 40, 30},
		{"svg", []byte(`<?xml version="1.0"?>` + "\n" + `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 12"/>`), "image/svg+xml", 24, 12},
	} {
		up, err := CheckData(tt.data)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if up.ContentType != tt.contentType || up.Width != tt.w || up.Height != tt.h {
			t.Errorf("%s: %s %d×%d, want %s %d×%d", tt.name, up.ContentType, up.Width, up.Height, tt.contentType, tt.w, tt.h)
		}
	}

	for _, tt := range []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "empty"},
		{"too large", make([]byte, MaxBytes+1), "larger than 20 MB"},
		{"text", []byte("hello, world"), "isn’t an image"},
		{"svg too large", []byte(svgDoc(strings.Repeat(" ", MaxSVGBytes))), "larger than 2 MB"},
		{"svg without a size", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "no size"},
		{"svg not in utf-8", []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`), "isn’t saved as UTF-8"},
		{"html", []byte(`<html><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/></html>`), "isn’t an image"},
		{"ppm", []byte("P6\n1 1\n255\n\x00\x00\x00"), "isn’t an image"},
		{"too many pixels", blackPNG(t, 8000, 7000), "56 megapixels"},
	} {
		_, err := CheckData(tt.data)
		if rej, ok := errors.AsType[*Rejected](err); !ok || !strings.Contains(rej.Msg, tt.want) {
			t.Errorf("%s: %v, want a rejection with %q", tt.name, err, tt.want)
		}
	}
}

func TestVariantWidths(t *testing.T) {
	t.Parallel()
	for w, want := range map[int][]int{
		100:  {100},
		320:  {320},
		800:  {320, 640, 800},
		1600: {320, 640, 1024, 1600},
		4000: {320, 640, 1024, 1600},
	} {
		if got := variantWidths(w); !slices.Equal(got, want) {
			t.Errorf("variantWidths(%d) = %v, want %v", w, got, want)
		}
	}
}

func TestOtherLoadersBlocked(t *testing.T) {
	t.Parallel()
	startVips()
	for name, data := range map[string]string{
		"svg": `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`,
		"ppm": "P6\n1 1\n255\n\x00\x00\x00",
	} {
		if img, err := vips.NewImageFromBuffer([]byte(data), nil); err == nil {
			img.Close()
			t.Errorf("libvips loaded %s", name)
		}
	}
}
