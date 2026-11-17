Posts are Markdown with a few additions borrowed from GitHub and Hugo. This post uses each of them, so it doubles as a test page: open it in the editor to see the source.

## Text

Write *emphasis*, **strong**, ~~struck~~ and `code` as usual. Quotes turn "curly" and dashes -- like these -- turn typographic. Links such as https://go.dev are found on their own.

- Lists nest
  - like this
- [x] and tasks can be ticked
- [ ] or not

Term
: A definition list, for glossaries.

Footnotes keep asides out of the way.[^aside]

[^aside]: They are numbered and listed at the end.

## Tables

Colons in the divider row align a column.

| Width | Used for | Bytes, roughly |
|------:|:---------|---------------:|
| 320 | phones | 18 KB |
| 640 | phones with dense screens | 52 KB |
| 1024 | tablets | 110 KB |
| 1600 | wide screens | 240 KB |

## Callouts

> [!NOTE]
> Something readers should know.

> [!TIP]
> A better way to do it.

> [!IMPORTANT]
> Something they must not miss.

> [!WARNING]
> Something that may go wrong.

> [!CAUTION]
> Something that can't be undone.

## Code

```go
func greet(name string) string {
	return "Hello, " + name
}
```

```sh
make seed args="--reset"
```

```sql
SELECT slug, title FROM posts
WHERE status = 'published' AND published_at <= now()
ORDER BY published_at DESC;
```

## Images

An image from Media is plain Markdown. Its alt text comes from the Markdown, or from Media if the Markdown has none.

![Dunes under a high sun](image:dunes)

The `figure` shortcode adds a caption and a credit:

{{< figure src="image:dusk" caption="Ridges at dusk." credit="Drawn for the Quickstack seed." >}}

## Shortcodes

Shortcodes go on a line of their own. Those that wrap content close with a matching tag, and the content between is Markdown.

```text
{{< details summary="Show more" >}}
Hidden until opened.
{{< /details >}}
```

### Video

{{< youtube id="f6kdp27TYZs" title="Go Concurrency Patterns, Google I/O 2012" >}}

{{< vimeo id="76979871" title="A sample Vimeo video" >}}

### Details

{{< details summary="Why shortcodes and not HTML?" >}}
The renderer drops raw HTML, so there is nothing to sanitize, and each shortcode is a templ component the theme can restyle.

Shortcodes nest, and a code block inside one is left alone:

```text
{{< /details >}}
```
{{< /details >}}

### Tabs

{{< tabs >}}
{{< tab label="macOS" >}}
```sh
brew install vips
```
{{< /tab >}}
{{< tab label="Debian and Ubuntu" >}}
```sh
apt install libvips-dev
```
{{< /tab >}}
{{< tab label="Docker" >}}
Nothing to install: `make docker` builds against a libvips kit.
{{< /tab >}}
{{< /tabs >}}

Tabs are radio buttons, so they work without JavaScript.
