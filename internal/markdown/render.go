package markdown

import (
	"bytes"
	"context"

	"github.com/a-h/templ"
	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// Code is highlighted with classes, not inline styles, which the CSP
// blocks. frontend/src/chroma.css has the colors.
var codeFormatter = chromahtml.New(chromahtml.WithClasses(true))

type nodeRenderer struct{}

func (r nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindShortcode, r.shortcode)
	reg.Register(kindAlert, r.alert)
	reg.Register(kindImage, r.image)
	reg.Register(kindTableWrap, r.tableWrap)
	reg.Register(ast.KindFencedCodeBlock, r.code)
	reg.Register(ast.KindCodeBlock, r.code)
}

// children renders n's children, for a component to wrap.
func children(source []byte, n ast.Node) (templ.Component, error) {
	var buf bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if err := md.Renderer().Render(&buf, source, c); err != nil {
			return nil, err
		}
	}
	return templ.Raw(buf.String()), nil
}

func (nodeRenderer) shortcode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*shortcode)
	c := Call{Args: n.args, Image: n.image, Group: n.group, First: n.first}
	if shortcodes[n.name].inner {
		var err error
		if c.Inner, err = children(source, n); err != nil {
			return ast.WalkStop, err
		}
	}
	if err := shortcodes[n.name].render(c).Render(context.Background(), w); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}

func (nodeRenderer) alert(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	inner, err := children(source, node)
	if err != nil {
		return ast.WalkStop, err
	}
	if err := alert(node.(*alertNode).kind, inner).Render(context.Background(), w); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}

func (nodeRenderer) image(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	return ast.WalkContinue, img(node.(*imageNode).image).Render(context.Background(), w)
}

func (nodeRenderer) tableWrap(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString(`<div class="overflow-x-auto">` + "\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

func (nodeRenderer) code(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var lang string
	if n, ok := node.(*ast.FencedCodeBlock); ok {
		lang = string(n.Language(source))
	}
	var code bytes.Buffer
	lines := node.Lines()
	for i := range lines.Len() {
		seg := lines.At(i)
		code.Write(seg.Value(source))
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code.String())
	if err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, codeFormatter.Format(w, styles.Fallback, it)
}
