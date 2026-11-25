//go:build !resvg

package media

import "errors"

// SVGRaster reports whether an SVG also gets a social card, og.jpg, drawn
// with resvg. It is true in builds with the resvg tag.
const SVGRaster = false

func rasterizeSVG([]byte) ([]file, error) { return nil, nil }

func newSVGIcon([]byte) (iconSource, error) {
	return nil, errors.New("media: drawing an SVG icon needs the resvg tag")
}
