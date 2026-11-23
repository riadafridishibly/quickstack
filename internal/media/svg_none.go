//go:build !resvg

package media

// SVGRaster reports whether an SVG also gets a social card, og.jpg, drawn
// with resvg. It is true in builds with the resvg tag.
const SVGRaster = false

func rasterizeSVG([]byte) ([]file, error) { return nil, nil }
