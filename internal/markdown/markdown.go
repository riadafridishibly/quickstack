// Package markdown turns a blog post into HTML: goldmark with GFM,
// footnotes, GitHub-style alerts, images from the media library, and
// Hugo-style block shortcodes.
package markdown

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/riadafridishibly/quickstack/internal/db"
	"github.com/riadafridishibly/quickstack/internal/media"
)

// A Problem is a part of the source that was left out of the HTML.
type Problem struct {
	Line int
	Msg  string
}

func (p Problem) String() string { return fmt.Sprintf("line %d: %s", p.Line, p.Msg) }

// Resolver looks up the assets with the given keys. Keys without an asset
// are left out. (*db.Queries).ListAssetsByKeys is one.
type Resolver func(ctx context.Context, keys []string) ([]db.Asset, error)

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
			extension.Strikethrough,
			extension.Linkify,
			extension.TaskList,
			extension.Footnote,
			extension.DefinitionList,
			extension.Typographer,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithAttribute(),
			parser.WithBlockParsers(util.Prioritized(shortcodeParser{}, 100)),
		),
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(nodeRenderer{}, 100)),
		),
	)
}

// Render turns src into HTML. Each part it leaves out, such as an unknown
// shortcode or an image that isn't ready, is reported as a Problem. The
// error is only from assets.
func Render(ctx context.Context, src string, assets Resolver) (template.HTML, []Problem, error) {
	source := []byte(src)
	doc := md.Parser().Parse(text.NewReader(source))
	p := newPass(source)
	p.walk(doc)
	if err := p.resolve(ctx, assets); err != nil {
		return "", nil, err
	}
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, source, doc); err != nil {
		return "", nil, err
	}
	sort.SliceStable(p.problems, func(i, j int) bool { return p.problems[i].Line < p.problems[j].Line })
	return template.HTML(buf.String()), slices.Compact(p.problems), nil
}

// pass walks the parsed document once, changes it into what the renderer
// expects, and records what needs an asset.
type pass struct {
	source     []byte
	lineStarts []int
	problems   []Problem
	keys       []string
	images     []*imageNode
	figures    []*shortcode
	tabs       int
}

func newPass(source []byte) *pass {
	starts := []int{0}
	for i, c := range source {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &pass{source: source, lineStarts: starts}
}

func (p *pass) report(n ast.Node, format string, args ...any) {
	p.problems = append(p.problems, Problem{Line: p.line(n), Msg: fmt.Sprintf(format, args...)})
}

func (p *pass) line(n ast.Node) int {
	pos := 0
	for x := n; x != nil; x = x.Parent() {
		if t, ok := x.(*ast.Text); ok {
			pos = t.Segment.Start
			break
		}
		if x.Pos() > 0 {
			pos = x.Pos()
			break
		}
		if x.Type() == ast.TypeBlock && x.Lines().Len() > 0 {
			pos = x.Lines().At(0).Start
			break
		}
	}
	return sort.Search(len(p.lineStarts), func(i int) bool { return p.lineStarts[i] > pos })
}

var keepAttrs = []string{"id", "class"}

func (p *pass) walk(doc ast.Node) {
	var remove, unwrap, images, alerts, tables []ast.Node
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		// {style=...} and the like would only trip the CSP.
		if attrs := n.Attributes(); len(attrs) > 0 {
			n.RemoveAttributes()
			for _, a := range attrs {
				if slices.Contains(keepAttrs, string(a.Name)) {
					n.SetAttribute(a.Name, a.Value)
				}
			}
		}
		switch n := n.(type) {
		case *ast.Heading:
			// The page title is the h1.
			n.Level = min(n.Level+1, 6)
		case *ast.HTMLBlock:
			if n.HTMLBlockType != ast.HTMLBlockType2 {
				p.report(n, "HTML isn’t allowed in posts; use Markdown or a shortcode.")
			}
			remove = append(remove, n)
		case *ast.RawHTML:
			if !bytes.HasPrefix(n.Segments.Value(p.source), []byte("<!--")) {
				p.report(n, "HTML isn’t allowed in posts; use Markdown or a shortcode.")
			}
			remove = append(remove, n)
		case *ast.Link:
			if !safeURL(string(n.Destination)) {
				p.report(n, "The link to %q was left out: links must be http, https, mailto or tel.", n.Destination)
				unwrap = append(unwrap, n)
			}
		case *ast.AutoLink:
			if !safeURL(string(n.URL(p.source))) {
				p.report(n, "The link to %q was left out: links must be http, https, mailto or tel.", n.URL(p.source))
				unwrap = append(unwrap, n)
			}
		case *ast.Image:
			images = append(images, n)
			return ast.WalkSkipChildren, nil
		case *ast.Blockquote:
			alerts = append(alerts, n)
		case *east.Table:
			tables = append(tables, n)
		case *shortcode:
			if !p.shortcode(n) {
				remove = append(remove, n)
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	for _, n := range images {
		img := n.(*ast.Image)
		im, ok := p.image(n, string(img.Destination), plainText(n, p.source))
		if !ok {
			remove = append(remove, n)
			continue
		}
		node := &imageNode{image: im}
		n.Parent().ReplaceChild(n.Parent(), n, node)
		if im.key != "" {
			p.images = append(p.images, node)
		}
	}
	for _, n := range remove {
		removeNode(n)
	}
	for _, n := range unwrap {
		parent := n.Parent()
		if a, ok := n.(*ast.AutoLink); ok {
			parent.ReplaceChild(parent, a, ast.NewString(a.Label(p.source)))
			continue
		}
		for c := n.FirstChild(); c != nil; {
			next := c.NextSibling()
			parent.InsertBefore(parent, n, c)
			c = next
		}
		parent.RemoveChild(parent, n)
	}
	for _, n := range alerts {
		p.alert(n.(*ast.Blockquote))
	}
	for _, n := range tables {
		n.SetAttributeString("class", []byte("table"))
		wrap := &tableWrap{}
		n.Parent().ReplaceChild(n.Parent(), n, wrap)
		wrap.AppendChild(wrap, n)
	}
}

// removeNode removes n, and its paragraph if n was all of it.
func removeNode(n ast.Node) {
	parent := n.Parent()
	parent.RemoveChild(parent, n)
	if para, ok := parent.(*ast.Paragraph); ok && para.ChildCount() == 0 {
		para.Parent().RemoveChild(para.Parent(), para)
	}
}

// safeURL allows relative URLs and the schemes a post links to.
func safeURL(u string) bool {
	i := strings.IndexAny(u, ":/?#")
	if i < 0 || u[i] != ':' {
		return true
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// image checks an image's source. An asset is resolved later; a local path
// that isn't an asset is shown as it is. The CSP blocks images from other
// sites.
func (p *pass) image(n ast.Node, src, alt string) (image, bool) {
	if key, ok := strings.CutPrefix(src, "/media/"); ok && !strings.Contains(key, "/") {
		if !media.ValidKey(key) {
			p.report(n, "There is no image %s in Media.", src)
			return image{}, false
		}
		p.keys = append(p.keys, key)
		return image{key: key, Alt: alt}, true
	}
	if strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "//") {
		return image{Src: src, Alt: alt}, true
	}
	p.report(n, "The image %q was left out: upload it to Media and use its Markdown.", src)
	return image{}, false
}

func (p *pass) resolve(ctx context.Context, assets Resolver) error {
	byKey := map[string]*db.Asset{}
	if len(p.keys) > 0 && assets != nil {
		slices.Sort(p.keys)
		rows, err := assets(ctx, slices.Compact(p.keys))
		if err != nil {
			return fmt.Errorf("markdown: assets: %w", err)
		}
		for i := range rows {
			byKey[rows[i].Key] = &rows[i]
		}
	}
	check := func(n ast.Node, im *image) bool {
		a := byKey[im.key]
		switch {
		case a == nil:
			p.report(n, "There is no image %s in Media.", media.URL(im.key))
		case a.Status == media.Processing:
			p.report(n, "The image %s is still being processed.", media.URL(im.key))
		case a.Status == media.Failed:
			p.report(n, "The image %s couldn’t be processed: %s", media.URL(im.key), a.Error)
		default:
			im.Asset = a
			if im.Alt == "" {
				im.Alt = a.Alt
			}
			return true
		}
		return false
	}
	for _, n := range p.images {
		if !check(n, &n.image) {
			removeNode(n)
		}
	}
	for _, n := range p.figures {
		if !check(n, &n.image) {
			removeNode(n)
			continue
		}
		if _, ok := n.args["credit"]; !ok {
			n.args["credit"] = n.image.Asset.Credit
		}
	}
	return nil
}

var alertRE = regexp.MustCompile(`(?i)^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$`)

// alert turns a blockquote that starts with a line such as [!NOTE] into an
// alert, as GitHub and Hugo do.
func (p *pass) alert(b *ast.Blockquote) {
	para, ok := b.FirstChild().(*ast.Paragraph)
	if !ok || para.Lines().Len() == 0 {
		return
	}
	first := para.Lines().At(0)
	m := alertRE.FindSubmatch(bytes.TrimSpace(first.Value(p.source)))
	if m == nil {
		return
	}
	for c := para.FirstChild(); c != nil; {
		t, ok := c.(*ast.Text)
		if !ok || t.Segment.Start >= first.Stop {
			break
		}
		next := c.NextSibling()
		para.RemoveChild(para, c)
		c = next
	}
	if para.ChildCount() == 0 {
		b.RemoveChild(b, para)
	}
	n := &alertNode{kind: strings.ToLower(string(m[1]))}
	b.Parent().ReplaceChild(b.Parent(), b, n)
	for c := b.FirstChild(); c != nil; {
		next := c.NextSibling()
		n.AppendChild(n, c)
		c = next
	}
}

func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			if n.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// image is an <img>: an asset, or a local path that isn't one.
type image struct {
	key   string
	Asset *db.Asset
	Src   string
	Alt   string
}

type imageNode struct {
	ast.BaseInline
	image
}

var kindImage = ast.NewNodeKind("MediaImage")

func (n *imageNode) Kind() ast.NodeKind { return kindImage }

func (n *imageNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

type alertNode struct {
	ast.BaseBlock
	kind string
}

var kindAlert = ast.NewNodeKind("Alert")

func (n *alertNode) Kind() ast.NodeKind { return kindAlert }

func (n *alertNode) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }

// tableWrap lets a wide table scroll instead of widening the page.
type tableWrap struct {
	ast.BaseBlock
}

var kindTableWrap = ast.NewNodeKind("TableWrap")

func (n *tableWrap) Kind() ast.NodeKind { return kindTableWrap }

func (n *tableWrap) Dump(source []byte, level int) { ast.DumpHelper(n, source, level, nil, nil) }
