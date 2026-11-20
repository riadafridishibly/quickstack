package media

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func svgDoc(inner string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="100" height="50">` + inner + `</svg>`
}

func TestLooksLikeSVG(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		`<svg/>`: true,
		"\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- made by hand -->\n<!DOCTYPE svg PUBLIC \"-//W3C//DTD SVG 1.1//EN\" \"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd\">\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>": true,
		`<html><svg/></html>`: false,
		`hello <svg/>`:        false,
		"\x89PNG\r\n":         false,
		"":                    false,
	} {
		if got := looksLikeSVG([]byte(in)); got != want {
			t.Errorf("looksLikeSVG(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSanitizeSVGRemoves(t *testing.T) {
	t.Parallel()
	png := base64.StdEncoding.EncodeToString(encodePNG(t, testImage(2, 2)))
	for _, tt := range []struct {
		name, in string
		// gone must not be in the output; kept must be.
		gone, kept []string
	}{
		{"script", `<script>alert(1)</script><rect width="1" height="1"/>`, []string{"script", "alert"}, []string{"<rect"}},
		{"script in another namespace", `<x:script xmlns:x="http://www.w3.org/2000/svg">alert(1)</x:script>`, []string{"alert"}, nil},
		{"event handler", `<rect onclick="alert(1)" onload="alert(2)" width="1" height="1"/>`, []string{"alert", "onclick", "onload"}, []string{`<rect width="1" height="1"/>`}},
		{"event handler on root", ``, []string{"onload"}, nil},
		{"foreignObject", `<foreignObject><iframe xmlns="http://www.w3.org/1999/xhtml" src="https://example.com"/></foreignObject>`, []string{"foreignObject", "iframe", "example.com"}, nil},
		{"iframe", `<iframe src="https://example.com"/>`, []string{"iframe", "example.com"}, nil},
		{"javascript href", `<a href="javascript:alert(1)"><rect width="1" height="1"/></a>`, []string{"javascript", "alert", "<a"}, []string{"<g><rect"}},
		{"external use", `<use xlink:href="https://example.com/sprite.svg#icon"/>`, []string{"example.com"}, []string{"<use/>"}},
		{"external image", `<image href="https://example.com/x.png" width="1" height="1"/>`, []string{"example.com"}, nil},
		{"file image", `<image href="file:///etc/passwd" width="1" height="1"/>`, []string{"passwd"}, nil},
		{"svg data image", `<image href="data:image/svg+xml;base64,PHN2Zy8+" width="1" height="1"/>`, []string{"data:"}, nil},
		{"data image that isn't the declared type", `<image href="data:image/png;base64,PHN2Zy8+" width="1" height="1"/>`, []string{"data:"}, nil},
		{"data href on use", `<use href="data:image/png;base64,` + png + `"/>`, []string{"data:"}, nil},
		{"animate sets href", `<a><animate attributeName="href" values="javascript:alert(1)"/><rect width="1" height="1"/></a>`, []string{"animate", "javascript"}, []string{"<rect"}},
		{"set", `<rect width="1" height="1"><set attributeName="fill" to="red"/></rect>`, []string{"<set", "red"}, nil},
		{"style import", `<style>@import url(https://example.com/x.css); rect{fill:red}</style>`, []string{"import", "example.com", "<style"}, nil},
		{"style external url", `<style>rect{fill:url(https://example.com/x.svg#g)}</style>`, []string{"example.com", "<style"}, nil},
		{"style escape", `<style>rect{fill:\75 rl(https://example.com/x)}</style>`, []string{"example.com"}, nil},
		{"style comment joins words", `<style>rect{background:ur/**/l(https://example.com/x)}</style>`, []string{"example.com"}, nil},
		{"style comment in a string", `<style>rect{font-family:"/*"; fill:url(https://example.com/x); font-family:"*/"}</style>`, []string{"example.com"}, nil},
		{"element inside style", `<style><x:a xmlns:x="http://www.w3.org/2000/svg">rect{}</x:a></style>`, []string{"<a", "rect{}"}, nil},
		{"style attribute url", `<rect style="fill:url(https://example.com/x)" width="1" height="1"/>`, []string{"example.com", "style="}, nil},
		{"presentation attribute url", `<rect fill="url(https://example.com/x#g)" mask="url('//example.com/m')" width="1" height="1"/>`, []string{"example.com"}, nil},
		{"expression", `<rect style="width:expression(alert(1))" width="1" height="1"/>`, []string{"alert"}, nil},
		{"html style element", `<h:style xmlns:h="http://www.w3.org/1999/xhtml">rect{fill:red}</h:style>`, []string{"fill:red"}, nil},
		{"xml:base", `<g xml:base="https://example.com/"><use href="#a"/></g>`, []string{"example.com", "base"}, []string{`<use href="#a"/>`}},
		{"metadata", `<metadata><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">Jane Doe</rdf:RDF></metadata>`, []string{"Jane", "metadata", "rdf"}, nil},
		{"editor namespaces", `<sodipodi:namedview xmlns:sodipodi="http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd" docname="/home/jane/x.svg"/><g xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape" inkscape:label="Layer 1" i:extraneous="self"/>`, []string{"jane", "Layer", "inkscape", "sodipodi", "extraneous"}, []string{"<g/>"}},
		{"comment and processing instruction", `<!-- by jane --><?xml-stylesheet href="https://example.com/x.css"?>`, []string{"jane", "example.com"}, nil},
	} {
		in := svgDoc(tt.in)
		if tt.name == "event handler on root" {
			in = `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1" onload="alert(1)"/>`
		}
		out, _, _, err := sanitizeSVG([]byte(in))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		for _, s := range tt.gone {
			if bytes.Contains(out, []byte(s)) {
				t.Errorf("%s: %q is in the output: %s", tt.name, s, out)
			}
		}
		for _, s := range tt.kept {
			if !bytes.Contains(out, []byte(s)) {
				t.Errorf("%s: %q isn't in the output: %s", tt.name, s, out)
			}
		}
	}
}

func TestSanitizeSVGRejects(t *testing.T) {
	t.Parallel()
	nest := func(n int) string {
		return svgDoc(strings.Repeat("<g>", n) + strings.Repeat("</g>", n))
	}
	// Ten levels of ten uses each draw 10^10 rects.
	var fan strings.Builder
	fan.WriteString(`<defs><rect id="l0" width="1" height="1"/>`)
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&fan, `<g id="l%d">%s</g>`, i, strings.Repeat(fmt.Sprintf(`<use href="#l%d"/>`, i-1), 10))
	}
	fan.WriteString(`</defs>`)

	for _, tt := range []struct {
		name, in string
		want     error
	}{
		{"too large", svgDoc(strings.Repeat(" ", MaxSVGBytes)), errSVGTooLarge},
		{"entity", `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY a "aaaaaaaa">]><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">&a;</svg>`, errSVGDoctype},
		{"external entity", `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><text>&x;</text></svg>`, errSVGDoctype},
		{"undeclared entity", svgDoc(`<text>&nbsp;</text>`), errSVGUnreadable},
		{"cut off", `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><g>`, errSVGUnreadable},
		{"mismatched tags", svgDoc(`<g></rect>`), errSVGUnreadable},
		{"two roots", svgDoc(``) + `<svg/>`, errSVGUnreadable},
		{"not utf-8", `<?xml version="1.0" encoding="ISO-8859-1"?><svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>`, errSVGUnreadable},
		{"root in another namespace", `<svg xmlns="http://www.w3.org/1999/xhtml" width="1" height="1"/>`, errFormat},
		{"deep nesting", nest(maxSVGDepth), errSVGComplex},
		{"too many elements", svgDoc(strings.Repeat("<g/>", maxSVGElements)), errSVGComplex},
		{"use expands", svgDoc(fan.String()), errSVGComplex},
		{"use refers to itself", svgDoc(`<use id="u" href="#u"/>`), errSVGUseCycle},
		{"use refers to its parent", svgDoc(`<g id="g"><use href="#g"/></g>`), errSVGUseCycle},
		{"use refers to the root", `<svg xmlns="http://www.w3.org/2000/svg" id="r" width="1" height="1"><use href="#r"/></svg>`, errSVGUseCycle},
		{"use cycle", svgDoc(`<defs><g id="a"><use href="#b"/></g><g id="b"><use href="#a"/></g></defs><use href="#a"/>`), errSVGUseCycle},
		{"no size", `<svg xmlns="http://www.w3.org/2000/svg"/>`, errSVGNoSize},
		{"relative size", `<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%"/>`, errSVGNoSize},
		{"empty viewBox", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 0 10"/>`, errSVGNoSize},
		{"huge", `<svg xmlns="http://www.w3.org/2000/svg" width="1e9" height="10"/>`, errSVGHuge},
	} {
		_, _, _, err := sanitizeSVG([]byte(tt.in))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, err, tt.want)
		}
	}

	if _, _, _, err := sanitizeSVG([]byte(nest(maxSVGDepth - 1))); err != nil {
		t.Errorf("nesting one less than the limit: %v", err)
	}
}

func TestSanitizeSVGKeeps(t *testing.T) {
	t.Parallel()
	png := base64.StdEncoding.EncodeToString(encodePNG(t, testImage(2, 2)))
	for _, tt := range []struct {
		name, in string
		w, h     int
		kept     []string
	}{
		{
			"gradient", svgDoc(`<defs><linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#000"/></linearGradient></defs><rect width="100" height="50" fill="url(#g)"/>`), 100, 50,
			[]string{`<linearGradient id="g" x1="0" x2="1"><stop offset="0" stop-color="#fff"/>`, `fill="url(#g)"`},
		},
		{
			"inkscape style", svgDoc(`<path style="fill:url(#linearGradient12);stroke:#000;stroke-width:0.26" d="M 0,0 H 10 V 10 Z"/>`), 100, 50,
			[]string{`style="fill:url(#linearGradient12);stroke:#000;stroke-width:0.26"`, `d="M 0,0 H 10 V 10 Z"`},
		},
		{
			"illustrator classes", svgDoc(`<style type="text/css">.st0{fill:#FF0000;} /* red */ .st1{clip-path:url(#SVGID_2_);}</style><rect class="st0" width="10" height="10"/>`), 100, 50,
			[]string{`<style>.st0{fill:#FF0000;}  .st1{clip-path:url(#SVGID_2_);}</style>`, `class="st0"`},
		},
		{
			"use and symbol", svgDoc(`<symbol id="i" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></symbol><use xlink:href="#i" x="10" width="20" height="20"/>`), 100, 50,
			[]string{`<symbol id="i" viewBox="0 0 10 10"><circle cx="5" cy="5" r="4"/></symbol>`, `<use href="#i" x="10" width="20" height="20"/>`},
		},
		{
			"href wins over xlink:href", svgDoc(`<use xlink:href="#a" href="#b"/>`), 100, 50,
			[]string{`<use href="#b"/>`},
		},
		{
			"text", svgDoc(`<text x="1" y="20" font-family="'DejaVu Sans', sans-serif" xml:space="preserve">Tom &amp; Jerry &lt;3 <tspan dy="4">ok</tspan></text>`), 100, 50,
			[]string{`<text x="1" y="20" font-family="'DejaVu Sans', sans-serif" xml:space="preserve">Tom &amp; Jerry &lt;3 <tspan dy="4">ok</tspan></text>`},
		},
		{
			"title and desc", svgDoc(`<title>A chart</title><desc>Sales by month</desc>`), 100, 50,
			[]string{`<title>A chart</title><desc>Sales by month</desc>`},
		},
		{
			"filter and clip", svgDoc(`<filter id="f"><feGaussianBlur in="SourceGraphic" stdDeviation="2"/></filter><clipPath id="c"><rect width="5" height="5"/></clipPath><g filter="url(#f)" clip-path="url( '#c' )"><rect width="9" height="9"/></g>`), 100, 50,
			[]string{`<feGaussianBlur in="SourceGraphic" stdDeviation="2"/>`, `filter="url(#f)"`, `clip-path="url( '#c' )"`},
		},
		{
			"switch", svgDoc(`<switch><foreignObject requiredExtensions="http://ns.adobe.com/AdobeIllustrator/10.0/"/><g><rect width="1" height="1"/></g></switch>`), 100, 50,
			[]string{`<switch><g><rect width="1" height="1"/></g></switch>`},
		},
		{
			"png image", svgDoc(`<image width="2" height="2" href="data:image/png;base64,` + png + `"/>`), 100, 50,
			[]string{`href="data:image/png;base64,` + png + `"`},
		},
		{
			"uses within the limit", svgDoc(`<defs><rect id="r" width="1" height="1"/><g id="g"><use href="#r"/><use href="#r"/></g></defs><use href="#g"/><use href="#g"/><use href="#missing"/>`), 100, 50,
			[]string{`<use href="#missing"/>`},
		},
		{"viewBox without size", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>`, 24, 24, []string{`<path d="M0 0h24v24H0z"/>`}},
		{"viewBox with commas", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-5,-5,30.4,10.6"/>`, 30, 11, nil},
		{"width and viewBox", `<svg xmlns="http://www.w3.org/2000/svg" width="200" viewBox="0 0 100 50"/>`, 200, 100, nil},
		{"height and viewBox", `<svg xmlns="http://www.w3.org/2000/svg" height="10mm" viewBox="0 0 2 1"/>`, 76, 38, nil},
		{"units", `<svg xmlns="http://www.w3.org/2000/svg" width="1in" height="72pt"/>`, 96, 96, nil},
		{"px", `<svg xmlns="http://www.w3.org/2000/svg" width=" 12.4px " height="0.2"/>`, 12, 1, nil},
		{"no namespace", `<svg width="10" height="10"><rect width="10" height="10"/></svg>`, 10, 10, []string{`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`}},
		{
			"doctype naming the DTD", "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE svg PUBLIC \"-//W3C//DTD SVG 1.1//EN\" \"http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd\">\n<svg version=\"1.1\" xmlns=\"http://www.w3.org/2000/svg\" width=\"5\" height=\"5\"/>", 5, 5,
			[]string{`<svg xmlns="http://www.w3.org/2000/svg" width="5" height="5"/>`},
		},
	} {
		out, w, h, err := sanitizeSVG([]byte(tt.in))
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if w != tt.w || h != tt.h {
			t.Errorf("%s: %d×%d, want %d×%d", tt.name, w, h, tt.w, tt.h)
		}
		for _, s := range tt.kept {
			if !bytes.Contains(out, []byte(s)) {
				t.Errorf("%s: %q isn't in the output: %s", tt.name, s, out)
			}
		}
		again, _, _, err := sanitizeSVG(out)
		if err != nil || !bytes.Equal(again, out) {
			t.Errorf("%s: sanitizing the output again gives %s, %v; want it unchanged: %s", tt.name, again, err, out)
		}
	}
}

// FuzzSanitizeSVG checks that whatever the sanitizer writes, it would keep
// unchanged: nothing it lets through turns into something it drops once
// parsed again.
func FuzzSanitizeSVG(f *testing.F) {
	for _, s := range []string{
		svgDoc(`<style>.a{fill:url(#g)}</style><g id="g"><use href="#r"/><text>a &amp; b</text></g>`),
		svgDoc(`<a href="#x"><rect style="fill:url( '#g' )" width="1" height="1"/></a>`),
		`<!DOCTYPE svg PUBLIC "x" "y"><svg viewBox="0 0 1 1"><x:g xmlns:x="http://www.w3.org/2000/svg" xml:space="preserve"/></svg>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, w, h, err := sanitizeSVG(data)
		if err != nil {
			return
		}
		again, w2, h2, err := sanitizeSVG(out)
		if err != nil || !bytes.Equal(again, out) || w != w2 || h != h2 {
			t.Fatalf("sanitizing %q gave %q (%d×%d), and again %q (%d×%d), %v", data, out, w, h, again, w2, h2, err)
		}
	})
}
