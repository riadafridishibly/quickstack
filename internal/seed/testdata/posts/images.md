Every image on this blog goes through [libvips](https://www.libvips.org). An upload is checked, stored as it came, and a background job turns it into WebP copies at several widths and a 1200×630 JPEG for social cards.

{{< figure src="image:sea" caption="The original is 1600 pixels wide, so it gets all four widths." >}}

## What gets made

| File | Size | Why |
|------|------|-----|
| `320.webp` to `1600.webp` | up to four widths, never wider than the original | the browser picks one from `srcset` |
| `og.jpg` | 1200×630, cropped around what draws the eye | link previews |

The original is kept but never served. It still has its metadata, such as where a photo was taken.

> [!IMPORTANT]
> Only the JPEG, PNG, WebP, GIF and HEIF loaders of libvips may run. Anything else is refused before it is stored.

## Trying it

```sh
make s3-up
export QUICKSTACK_BLOB_URL='s3://quickstack:quickstack-secret@media?endpoint=localhost:8333&insecure=1'
make dev
```

{{< details summary="Where the files go" >}}
Each image has a key of eight characters, and its files live under that key: on disk in `./tmp/media` during development, or in a bucket. A key's files never change, so they are served with a year-long cache.
{{< /details >}}
