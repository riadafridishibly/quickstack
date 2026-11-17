Quickstack now has a blog. Posts are written in Markdown at `/admin/blog` by staff with the **editor** or **superadmin** role, and anyone can read them here, in the [feed](/blog/feed.xml), or through the sitemap.

![The sun setting behind three ridges](image:dusk)

A post goes live once it is published and its publish time has passed, so a time in the future schedules it. There is no job to run: the public pages compare the time with `now()`.

> [!NOTE]
> These posts come from `make seed`. Run `make seed args="--reset"` to put them back the way they were.

## What a post can hold

| Feature | Written as | Notes |
|:--------|:-----------|:------|
| Images | `![alt](/media/key)` | Resized to WebP at several widths |
| Callouts | `> [!TIP]` | Note, tip, important, warning, caution |
| Code | three backticks and a language | Highlighted on the server |
| Video | `{{< youtube id >}}` | Embedded without cookies |
| Footnotes | `[^1]` | Listed at the end of the post |

The [Markdown guide](/blog/writing-posts-in-markdown) shows each of them.

> [!TIP]
> Paste or drop an image into the editor to upload it. The Markdown for it goes in where the cursor is.
