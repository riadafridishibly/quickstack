package markdown

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// A Call is one use of a shortcode, as its component sees it.
type Call struct {
	Args  map[string]string
	Inner templ.Component // the rendered content, for a shortcode that takes it
	Image image           // the image of a figure
	Group string          // the radio group of a tab
	First bool            // the first tab of its tabs
}

type spec struct {
	params     []string
	required   []string
	positional string // the param a lone unnamed argument sets, as Hugo allows
	inner      bool   // takes content up to {{< /name >}}
	check      func(args map[string]string) string
	render     func(c Call) templ.Component
}

var (
	youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	vimeoID   = regexp.MustCompile(`^[0-9]{1,12}$`)
)

// shortcodes are the shortcodes a post may use. To add one, add an entry
// and a component in components.templ.
var shortcodes = map[string]spec{
	"figure": {
		params:   []string{"src", "alt", "caption", "credit"},
		required: []string{"src"},
		render:   func(c Call) templ.Component { return figure(c.Image, c.Args["caption"], c.Args["credit"]) },
	},
	"youtube": {
		params:     []string{"id", "title"},
		required:   []string{"id"},
		positional: "id",
		check: func(args map[string]string) string {
			if !youtubeID.MatchString(args["id"]) {
				return "id must be a YouTube video ID, the 11 characters after v= in its URL"
			}
			return ""
		},
		render: func(c Call) templ.Component { return youtube(c.Args["id"], or(c.Args["title"], "YouTube video")) },
	},
	"vimeo": {
		params:     []string{"id", "title"},
		required:   []string{"id"},
		positional: "id",
		check: func(args map[string]string) string {
			if !vimeoID.MatchString(args["id"]) {
				return "id must be a Vimeo video ID, the number at the end of its URL"
			}
			return ""
		},
		render: func(c Call) templ.Component { return vimeo(c.Args["id"], or(c.Args["title"], "Vimeo video")) },
	},
	"details": {
		params:     []string{"summary"},
		positional: "summary",
		inner:      true,
		render:     func(c Call) templ.Component { return details(or(c.Args["summary"], "Details"), c.Inner) },
	},
	"tabs": {
		inner:  true,
		render: func(c Call) templ.Component { return tabs(c.Inner) },
	},
	"tab": {
		params:     []string{"label"},
		required:   []string{"label"},
		positional: "label",
		inner:      true,
		render:     func(c Call) templ.Component { return tab(c.Group, c.Args["label"], c.First, c.Inner) },
	},
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

type shortcode struct {
	ast.BaseBlock
	name    string
	args    map[string]string
	unnamed []string
	argErr  string
	closing bool // {{< /name >}} with no opening tag
	selfEnd bool // {{< name />}}
	ended   bool // its closing tag was found
	image   image
	group   string
	first   bool
}

var kindShortcode = ast.NewNodeKind("Shortcode")

func (n *shortcode) Kind() ast.NodeKind { return kindShortcode }

func (n *shortcode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"name": n.name}, nil)
}

func (n *shortcode) tag() string { return "{{< " + n.name + " >}}" }

func (n *shortcode) container() bool { return shortcodes[n.name].inner && !n.selfEnd && !n.closing }

var tagRE = regexp.MustCompile(`^\{\{<\s*(/?)\s*([A-Za-z][A-Za-z0-9_-]*)(.*?)(/?)\s*>\}\}\s*$`)

// shortcodeParser parses a shortcode tag on a line of its own. Content up
// to the closing tag is parsed as Markdown, so shortcodes nest.
type shortcodeParser struct{}

func (shortcodeParser) Trigger() []byte { return []byte{'{'} }

func (shortcodeParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 {
		return nil, parser.NoChildren
	}
	m := tagRE.FindSubmatch(line[pos:])
	if m == nil {
		return nil, parser.NoChildren
	}
	n := &shortcode{name: string(m[2]), closing: len(m[1]) > 0, selfEnd: len(m[4]) > 0}
	n.args, n.unnamed, n.argErr = parseArgs(string(m[3]))
	reader.AdvanceToEOL()
	if n.container() {
		return n, parser.HasChildren
	}
	return n, parser.NoChildren
}

func (shortcodeParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	n := node.(*shortcode)
	if !n.container() {
		return parser.Close
	}
	line, _ := reader.PeekLine()
	m := tagRE.FindSubmatch(bytesTrimIndent(line))
	if m != nil && len(m[1]) > 0 && string(m[2]) == n.name && !innerHolds(n, pc) {
		reader.AdvanceToEOL()
		n.ended = true
		return parser.Close
	}
	return parser.Continue | parser.HasChildren
}

// innerHolds reports whether a block open inside n owns the current line:
// a fenced code block, whose text may show a closing tag, or a shortcode
// of the same name.
func innerHolds(n *shortcode, pc parser.Context) bool {
	blocks := pc.OpenedBlocks()
	i := slices.IndexFunc(blocks, func(b parser.Block) bool { return b.Node == n })
	for _, b := range blocks[i+1:] {
		switch b := b.Node.(type) {
		case *ast.FencedCodeBlock:
			return true
		case *shortcode:
			if b.name == n.name && b.container() && !b.ended {
				return true
			}
		}
	}
	return false
}

func bytesTrimIndent(line []byte) []byte {
	for i := 0; i < 3 && len(line) > 0 && line[0] == ' '; i++ {
		line = line[1:]
	}
	return line
}

func (shortcodeParser) Close(ast.Node, text.Reader, parser.Context) {}

func (shortcodeParser) CanInterruptParagraph() bool { return true }

func (shortcodeParser) CanAcceptIndentedLine() bool { return false }

// parseArgs reads k="v" pairs and unnamed values. Values may be quoted
// with "" (with \" escapes) or “, or bare.
func parseArgs(s string) (named map[string]string, unnamed []string, errMsg string) {
	named = map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return named, unnamed, ""
		}
		var key string
		if c := s[0]; c != '"' && c != '`' {
			i := strings.IndexAny(s, "= \t")
			if i < 0 || s[i] != '=' {
				if i < 0 {
					i = len(s)
				}
				unnamed = append(unnamed, s[:i])
				s = s[i:]
				continue
			}
			key, s = s[:i], s[i+1:]
		}
		v, rest, ok := value(s)
		if !ok {
			return named, unnamed, "a quoted value has no closing quote"
		}
		if key == "" {
			unnamed = append(unnamed, v)
		} else {
			named[key] = v
		}
		s = rest
	}
}

func value(s string) (v, rest string, ok bool) {
	switch {
	case strings.HasPrefix(s, "`"):
		i := strings.IndexByte(s[1:], '`')
		if i < 0 {
			return "", "", false
		}
		return s[1 : i+1], s[i+2:], true
	case strings.HasPrefix(s, `"`):
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				v, err := strconv.Unquote(s[:i+1])
				return v, s[i+1:], err == nil
			}
		}
		return "", "", false
	}
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		i = len(s)
	}
	return s[:i], s[i:], true
}

// shortcode checks n and reports whether to keep it.
func (p *pass) shortcode(n *shortcode) bool {
	sp, known := shortcodes[n.name]
	switch {
	case n.closing:
		p.report(n, "{{< /%s >}} closes nothing.", n.name)
		return false
	case !known:
		p.report(n, "There is no shortcode %s.", n.tag())
		return false
	case n.argErr != "":
		p.report(n, "%s: %s.", n.tag(), n.argErr)
		return false
	}
	if len(n.unnamed) > 0 {
		if sp.positional == "" || len(n.unnamed) > 1 || n.args[sp.positional] != "" {
			p.report(n, "%s: name each value, as in %s=\"…\".", n.tag(), or(sp.positional, firstOr(sp.params, "name")))
			return false
		}
		n.args[sp.positional] = n.unnamed[0]
	}
	for k := range n.args {
		if !slices.Contains(sp.params, k) {
			p.report(n, "%s has no parameter %q.", n.tag(), k)
			return false
		}
	}
	for _, k := range sp.required {
		if n.args[k] == "" {
			p.report(n, "%s needs %s=\"…\".", n.tag(), k)
			return false
		}
	}
	if sp.check != nil {
		if msg := sp.check(n.args); msg != "" {
			p.report(n, "%s: %s.", n.tag(), msg)
			return false
		}
	}
	if n.container() && !n.ended {
		p.report(n, "%s has no closing {{< /%s >}}, so it runs to the end.", n.tag(), n.name)
	}
	parent, inTabs := n.Parent().(*shortcode)
	inTabs = inTabs && parent.name == "tabs"
	switch n.name {
	case "tab":
		if !inTabs {
			p.report(n, "{{< tab >}} goes inside {{< tabs >}}.")
			return false
		}
		n.group = parent.group
		n.first = n.PreviousSibling() == nil
	case "tabs":
		p.tabs++
		n.group = "tabs-" + strconv.Itoa(p.tabs)
		var stray []ast.Node
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*shortcode); !ok || t.name != "tab" {
				stray = append(stray, c)
			}
		}
		for _, c := range stray {
			p.report(c, "Inside {{< tabs >}}, put content in a {{< tab label=\"…\" >}}.")
			n.RemoveChild(n, c)
		}
	case "figure":
		im, ok := p.image(n, n.args["src"], n.args["alt"])
		if !ok {
			return false
		}
		n.image = im
		if im.key != "" {
			p.figures = append(p.figures, n)
		}
	}
	return true
}

func firstOr(s []string, fallback string) string {
	if len(s) == 0 {
		return fallback
	}
	return s[0]
}
