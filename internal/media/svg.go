package media

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	svgContentType = "image/svg+xml"
	// SVGFile is the name of an SVG asset's sanitized file.
	SVGFile = "image.svg"

	// MaxSVGBytes is the largest SVG an upload may be.
	MaxSVGBytes = 2 << 20
	// maxSVGElements bounds the elements an SVG has, and the elements it
	// draws once each <use> is replaced by what it refers to.
	maxSVGElements = 50_000
	maxSVGDepth    = 100
	// maxSVGSide bounds the declared width and height.
	maxSVGSide = 100_000
)

const (
	svgNS   = "http://www.w3.org/2000/svg"
	xlinkNS = "http://www.w3.org/1999/xlink"
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
)

var (
	errSVGTooLarge   = &Rejected{"This SVG is larger than 2 MB. Simplify it and try again."}
	errSVGUnreadable = &Rejected{"This SVG couldn’t be read. It may be damaged or cut off; try exporting it again."}
	errSVGDoctype    = &Rejected{"This SVG declares entities in its DOCTYPE, which isn’t allowed. Export it again as plain SVG."}
	errSVGNoSize     = &Rejected{"This SVG has no size we can use. Give its svg element a width and height in pixels, or a viewBox."}
	errSVGHuge       = &Rejected{"This SVG is wider or taller than 100,000 pixels."}
	errSVGComplex    = &Rejected{"This SVG has too many elements. Simplify it and try again."}
	errSVGUseCycle   = &Rejected{"This SVG has a <use> element that refers to itself or to an element that contains it."}
)

var bom = []byte("\xef\xbb\xbf")

// looksLikeSVG reports whether data is XML whose root element is named svg.
// Whether it is an SVG we accept is for sanitizeSVG to say.
func looksLikeSVG(data []byte) bool {
	data = bytes.TrimPrefix(data, bom)
	if t := bytes.TrimLeft(data, " \t\r\n"); len(t) == 0 || t[0] != '<' {
		return false
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.RawToken()
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t.Name.Local == "svg"
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return false
			}
		}
	}
}

// svgElem is an element that passed the allowlists. Its name and its
// attributes' names are as they are written out.
type svgElem struct {
	name     string
	attrs    []svgAttr
	children []svgNode
}

type svgAttr struct{ name, value string }

// svgNode is an element or, when elem is nil, text.
type svgNode struct {
	elem *svgElem
	text string
}

func (e *svgElem) attr(name string) string {
	for _, a := range e.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

// sanitizeSVG parses data and writes it out again with only the elements
// and attributes that can't run script or load anything outside the file.
// width and height are the size it declares, in pixels. It returns a
// *Rejected error for an SVG it refuses.
func sanitizeSVG(data []byte) (out []byte, width, height int, err error) {
	if len(data) > MaxSVGBytes {
		return nil, 0, 0, errSVGTooLarge
	}
	root, err := parseSVG(data)
	if err != nil {
		return nil, 0, 0, err
	}
	if err := checkUses(root); err != nil {
		return nil, 0, 0, err
	}
	width, height, err = svgSize(root)
	if err != nil {
		return nil, 0, 0, err
	}
	var b bytes.Buffer
	root.write(&b, true)
	return b.Bytes(), width, height, nil
}

func parseSVG(data []byte) (*svgElem, error) {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, bom)))
	var (
		root  *svgElem
		open  []*svgElem
		depth int
		count int
		// skip is the depth of the dropped element being skipped, or 0.
		skip int
	)
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errSVGUnreadable
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			count++
			switch {
			case depth > maxSVGDepth || count > maxSVGElements:
				return nil, errSVGComplex
			case skip > 0:
				continue
			case root != nil && len(open) == 0:
				// encoding/xml allows more than one root element.
				return nil, errSVGUnreadable
			case root == nil:
				if t.Name.Local != "svg" || !inSVG(t.Name) {
					return nil, errFormat
				}
			case open[len(open)-1].name == "style":
				skip = depth
				continue
			}
			e := cleanElement(t)
			if e == nil {
				skip = depth
				continue
			}
			if root == nil {
				root = e
			} else {
				p := open[len(open)-1]
				p.children = append(p.children, svgNode{elem: e})
			}
			open = append(open, e)
		case xml.EndElement:
			switch skip {
			case depth:
				skip = 0
			case 0:
				e := open[len(open)-1]
				open = open[:len(open)-1]
				if e.name == "style" && !cleanStyleElement(e) && len(open) > 0 {
					p := open[len(open)-1]
					p.children = p.children[:len(p.children)-1]
				}
			}
			depth--
		case xml.CharData:
			if skip == 0 && len(open) > 0 {
				p := open[len(open)-1]
				p.children = append(p.children, svgNode{text: string(t)})
			}
		case xml.Directive:
			// A DOCTYPE that only names a DTD declares nothing, and nothing
			// loads the DTD. Entities are declared in an internal subset.
			if !bytes.HasPrefix(t, []byte("DOCTYPE")) || bytes.ContainsAny(t, "[<") {
				return nil, errSVGDoctype
			}
		}
	}
	if root == nil {
		return nil, errFormat
	}
	return root, nil
}

// inSVG reports whether n is in the SVG namespace, or in none, as in SVG
// copied from an HTML page.
func inSVG(n xml.Name) bool { return n.Space == svgNS || n.Space == "" }

// cleanElement returns t with only the allowed attributes, or nil if t
// isn't allowed.
func cleanElement(t xml.StartElement) *svgElem {
	if !inSVG(t.Name) {
		return nil
	}
	name := t.Name.Local
	if name == "a" {
		// A link can't be followed from an image; its contents still draw.
		name = "g"
	} else if !svgElements[name] {
		return nil
	}
	e := &svgElem{name: name}
	if name == "style" || t.Name.Local == "a" {
		return e
	}
	seen := map[string]int{}
	for _, a := range t.Attr {
		n, v, ok := cleanAttr(name, a)
		if !ok {
			continue
		}
		if i, dup := seen[n]; dup {
			// href wins over xlink:href, as in SVG 2.
			if n == "href" && a.Name.Space == "" {
				e.attrs[i].value = v
			}
			continue
		}
		seen[n] = len(e.attrs)
		e.attrs = append(e.attrs, svgAttr{n, v})
	}
	return e
}

// cleanAttr returns the name an allowed attribute is written with and its
// value, made safe.
func cleanAttr(elem string, a xml.Attr) (name, value string, ok bool) {
	switch {
	case a.Name.Space == "" && svgAttrs[a.Name.Local]:
		name = a.Name.Local
	case a.Name.Space == "" && a.Name.Local == "href",
		(a.Name.Space == xlinkNS || a.Name.Space == "xlink") && a.Name.Local == "href":
		return "href", a.Value, safeHref(elem, a.Value)
	case a.Name.Space == xmlNS && (a.Name.Local == "space" || a.Name.Local == "lang"):
		return "xml:" + a.Name.Local, a.Value, true
	default:
		return "", "", false
	}
	value, ok = cleanCSS(a.Value)
	return name, value, ok
}

var (
	fragmentRE = regexp.MustCompile(`^#[\p{L}\p{N}_.:-]+$`)
	dataURIRE  = regexp.MustCompile(`^(?i:data:(image/(?:png|jpeg|webp));base64,)([A-Za-z0-9+/=\s]+)$`)
)

// safeHref allows a reference to an element of the same file, and for an
// <image>, a PNG, JPEG or WebP data URI.
func safeHref(elem, v string) bool {
	v = strings.TrimSpace(v)
	if fragmentRE.MatchString(v) {
		return true
	}
	m := dataURIRE.FindStringSubmatch(v)
	if elem != "image" || m == nil {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(m[2]), ""))
	return err == nil && http.DetectContentType(b) == strings.ToLower(m[1])
}

var (
	cssCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssLocalURL  = regexp.MustCompile(`(?i)url\(\s*['"]?#[\p{L}\p{N}_.:-]+['"]?\s*\)`)
	cssBad       = []string{"url(", "@import", "expression", "image-set", "-moz-binding", "behavior", "javascript:", `\`, "/*"}
)

// cleanCSS removes comments from a style or a presentation attribute and
// refuses it if it could load another file. It checks for substrings, not
// tokens, so a backslash is refused: a CSS escape could spell any of them.
// The comments are removed from what is written out, not only from what is
// checked, so a comment can't split a word the check looks for.
func cleanCSS(s string) (string, bool) {
	s = cssCommentRE.ReplaceAllString(s, "")
	l := strings.ToLower(cssLocalURL.ReplaceAllString(s, ""))
	for _, bad := range cssBad {
		if strings.Contains(l, bad) {
			return "", false
		}
	}
	return s, true
}

// cleanStyleElement replaces a <style> element's text with its cleaned CSS.
// It reports false if no CSS is left to keep.
func cleanStyleElement(e *svgElem) bool {
	var b strings.Builder
	for _, c := range e.children {
		b.WriteString(c.text)
	}
	css, ok := cleanCSS(b.String())
	if !ok || strings.TrimSpace(css) == "" {
		return false
	}
	e.children = []svgNode{{text: css}}
	return true
}

// checkUses refuses an SVG whose <use> elements refer to an element that
// contains them, or would draw more than maxSVGElements elements once
// expanded. Each level of nested <use>s multiplies the count.
func checkUses(root *svgElem) error {
	ids := map[string]*svgElem{}
	var collect func(e *svgElem)
	collect = func(e *svgElem) {
		if id := e.attr("id"); id != "" {
			if _, dup := ids[id]; !dup {
				ids[id] = e
			}
		}
		for _, c := range e.children {
			if c.elem != nil {
				collect(c.elem)
			}
		}
	}
	collect(root)

	weights := map[*svgElem]int{}
	visiting := map[*svgElem]bool{}
	var weigh func(e *svgElem) (int, error)
	weigh = func(e *svgElem) (int, error) {
		if w, ok := weights[e]; ok {
			return w, nil
		}
		if visiting[e] {
			return 0, errSVGUseCycle
		}
		visiting[e] = true
		defer delete(visiting, e)
		w := 1
		add := func(c *svgElem) error {
			cw, err := weigh(c)
			w += cw
			if err == nil && w > maxSVGElements {
				err = errSVGComplex
			}
			return err
		}
		for _, c := range e.children {
			if c.elem != nil {
				if err := add(c.elem); err != nil {
					return 0, err
				}
			}
		}
		if e.name == "use" {
			if t := ids[strings.TrimPrefix(strings.TrimSpace(e.attr("href")), "#")]; t != nil {
				if err := add(t); err != nil {
					return 0, err
				}
			}
		}
		weights[e] = w
		return w, nil
	}
	_, err := weigh(root)
	return err
}

var (
	lengthRE = regexp.MustCompile(`^\s*(\+?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)\s*(px|pt|pc|mm|cm|in)?\s*$`)
	// Pixels per unit, at CSS's 96 pixels to the inch.
	lengthUnits = map[string]float64{"": 1, "px": 1, "pt": 96.0 / 72, "pc": 16, "mm": 96 / 25.4, "cm": 96 / 2.54, "in": 96}
)

// svgLength parses a width or height in absolute units. Relative ones, such
// as % and em, say nothing about the image's own size.
func svgLength(s string) (float64, bool) {
	m := lengthRE.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	v *= lengthUnits[m[2]]
	return v, err == nil && v > 0 && !math.IsInf(v, 0)
}

func viewBoxSize(s string) (w, h float64, ok bool) {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	if len(f) != 4 {
		return 0, 0, false
	}
	var v [4]float64
	for i, x := range f {
		n, err := strconv.ParseFloat(x, 64)
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return 0, 0, false
		}
		v[i] = n
	}
	return v[2], v[3], v[2] > 0 && v[3] > 0
}

// svgSize is the size the root declares: its width and height, with the
// viewBox's aspect ratio standing in for whichever is missing, or else the
// viewBox's size.
func svgSize(root *svgElem) (width, height int, err error) {
	w, wok := svgLength(root.attr("width"))
	h, hok := svgLength(root.attr("height"))
	vw, vh, vok := viewBoxSize(root.attr("viewBox"))
	switch {
	case wok && hok:
	case wok && vok:
		h = w * vh / vw
	case hok && vok:
		w = h * vw / vh
	case vok:
		w, h = vw, vh
	default:
		return 0, 0, errSVGNoSize
	}
	if w > maxSVGSide || h > maxSVGSide {
		return 0, 0, errSVGHuge
	}
	return max(1, int(math.Round(w))), max(1, int(math.Round(h))), nil
}

var (
	textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#9;", "\n", "&#10;", "\r", "&#13;")
)

func (e *svgElem) write(b *bytes.Buffer, root bool) {
	b.WriteByte('<')
	b.WriteString(e.name)
	if root {
		b.WriteString(` xmlns="` + svgNS + `"`)
	}
	for _, a := range e.attrs {
		b.WriteByte(' ')
		b.WriteString(a.name)
		b.WriteString(`="`)
		_, _ = attrEscaper.WriteString(b, a.value)
		b.WriteByte('"')
	}
	if len(e.children) == 0 {
		b.WriteString("/>")
		return
	}
	b.WriteByte('>')
	for _, c := range e.children {
		if c.elem != nil {
			c.elem.write(b, false)
		} else {
			_, _ = textEscaper.WriteString(b, c.text)
		}
	}
	b.WriteString("</")
	b.WriteString(e.name)
	b.WriteByte('>')
}

// svgElements are the elements kept. The rest are dropped with everything
// inside them: script, foreignObject, animation, metadata, and anything an
// editor adds in its own namespace.
var svgElements = setOf(
	"svg", "g", "defs", "symbol", "use", "switch", "title", "desc", "style",
	"path", "rect", "circle", "ellipse", "line", "polyline", "polygon",
	"text", "tspan", "textPath",
	"linearGradient", "radialGradient", "stop",
	"clipPath", "mask", "pattern", "marker", "image",
	"filter", "feBlend", "feColorMatrix", "feComponentTransfer", "feComposite",
	"feConvolveMatrix", "feDiffuseLighting", "feDisplacementMap", "feDistantLight",
	"feDropShadow", "feFlood", "feFuncA", "feFuncB", "feFuncG", "feFuncR",
	"feGaussianBlur", "feMerge", "feMergeNode", "feMorphology", "feOffset",
	"fePointLight", "feSpecularLighting", "feSpotLight", "feTile", "feTurbulence",
)

// svgAttrs are the attributes kept, other than href, xml:space and
// xml:lang. Event handlers (on*) aren't among them.
var svgAttrs = setOf(
	// Core and structure.
	"id", "class", "style", "transform", "viewBox", "preserveAspectRatio",
	"x", "y", "width", "height",
	// Shapes.
	"d", "points", "pathLength", "x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry",
	// Text.
	"dx", "dy", "rotate", "textLength", "lengthAdjust", "startOffset", "method", "spacing", "side", "path",
	// Gradients, patterns, clip paths, masks, markers.
	"offset", "fx", "fy", "fr", "gradientUnits", "gradientTransform", "spreadMethod",
	"patternUnits", "patternContentUnits", "patternTransform", "clipPathUnits",
	"maskUnits", "maskContentUnits", "markerUnits", "markerWidth", "markerHeight", "refX", "refY", "orient",
	// Filters.
	"filterUnits", "primitiveUnits", "in", "in2", "result", "mode", "operator", "k1", "k2", "k3", "k4",
	"stdDeviation", "edgeMode", "values", "type", "tableValues", "slope", "intercept", "amplitude",
	"exponent", "offset", "order", "kernelMatrix", "divisor", "bias", "targetX", "targetY", "preserveAlpha",
	"kernelUnitLength", "surfaceScale", "diffuseConstant", "specularConstant", "specularExponent", "scale",
	"xChannelSelector", "yChannelSelector", "radius", "baseFrequency", "numOctaves", "seed", "stitchTiles",
	"azimuth", "elevation", "z", "pointsAtX", "pointsAtY", "pointsAtZ", "limitingConeAngle",
	// Presentation attributes.
	"alignment-baseline", "baseline-shift", "clip", "clip-path", "clip-rule", "color",
	"color-interpolation", "color-interpolation-filters", "color-rendering", "direction", "display",
	"dominant-baseline", "enable-background", "fill", "fill-opacity", "fill-rule", "filter", "flood-color",
	"flood-opacity", "font", "font-family", "font-size", "font-size-adjust", "font-stretch", "font-style",
	"font-variant", "font-weight", "image-rendering", "isolation", "letter-spacing", "lighting-color",
	"marker", "marker-start", "marker-mid", "marker-end", "mask", "mask-type", "mix-blend-mode", "opacity",
	"overflow", "paint-order", "shape-rendering", "stop-color", "stop-opacity", "stroke",
	"stroke-dasharray", "stroke-dashoffset", "stroke-linecap", "stroke-linejoin", "stroke-miterlimit",
	"stroke-opacity", "stroke-width", "text-anchor", "text-decoration", "text-rendering",
	"transform-origin", "unicode-bidi", "vector-effect", "visibility", "word-spacing", "writing-mode",
)

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
