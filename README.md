# quickstack

A starting point for Go web apps with several dashboards: one binary, PostgreSQL, server-rendered pages, and as little JavaScript as each screen needs.

- **Go** with `net/http`, **templ** for HTML, **sqlc** for queries, **goose** migrations, **River** jobs
- **htmx 4** for server round-trips, **Preact** islands for client state, **Tailwind 4** with **daisyUI 5**, built by **Vite** and embedded
- **PostgreSQL 18** with row-level security per workspace
- **libvips** (8.16+, through cgo) for image uploads, stored on disk or in any S3-compatible bucket

It comes with:

| | Where | What it shows |
|---|---|---|
| Sign in | `/login`, `/signup`, `/forgot-password` | argon2id passwords, sessions in Postgres, rate limits, reset by email |
| Workspace dashboard | `/w/{ws}` | tenant middleware, workspace roles, overview charts, projects with live search, a task board (htmx + a drag-and-drop island), members and invitations |
| Account dashboard | `/account` | profile, password, sessions on other devices |
| Platform admin | `/admin` | platform roles (superadmin, support, editor), accounts, workspaces, audit log, media uploads |
| Public site | `/`, `/p/{ws}` | its own layout and stylesheet, cached pages |
| Blog | `/blog`, `/admin/blog` | posts in Markdown with callouts and Hugo-style shortcodes, scheduling, a CodeMirror editor with live preview, image uploads resized by libvips, an Atom feed and the sitemap |

## Run it

It needs Go, Node, PostgreSQL 18 (or Docker) and libvips 8.16 or newer for development: `brew install vips`, or `apt install libvips-dev`. libvips comes in through cgo, so it is needed to build, not only to run.

```sh
make setup        # pnpm packages, Playwright's Chromium, pre-push hook
make db-setup     # or: make db-up for Postgres in Docker
make seed         # sample accounts, workspaces and blog posts; password seed-password
make dev          # open http://localhost:7360
```

Sign in as `owner@seed.test`, `admin@seed.test` for the platform admin, or `editor@seed.test` to write blog posts. Mail goes to http://localhost:7360/dev/mail in development.

Uploaded images go to `./tmp/media` in development. To try an S3-compatible bucket instead, run `make s3-up` (SeaweedFS in Docker) and set `QUICKSTACK_BLOB_URL` as `.env.example` shows.

`make check` runs everything that must pass; `make e2e` runs the browser tests. `make build` puts the app in `bin/quickstack` for this machine.

## Deploy

Releases carry their own libvips, so servers don't need it installed. `make dist` cross-compiles with [zig](https://ziglang.org) (`brew install zig`) against a prebuilt libvips kit from [quickstack-vips](https://github.com/riadafridishibly/quickstack-vips), from any machine:

```sh
make dist TARGET=linux-amd64     # or linux-arm64, freebsd-amd64
```

`dist/quickstack-linux-amd64.tar.gz` holds `bin/quickstack`, the libraries in `lib/`, and their licences in `share/licenses/`. On the server, unpack it and run its `install` script as root: it copies the release into `/opt/quickstack` owned by root, and creates the `quickstack` user with `/var/lib/quickstack` for uploads. Run the app as that user. The binary loads its libraries from `/opt/quickstack/lib`, so only root may write there. Linux servers need glibc 2.28 or newer (RHEL 8, Debian 10, Ubuntu 20.04 and later); FreeBSD servers need 14.0 or newer.

`make docker` builds a Docker image with the same layout, so the host needs no libvips either:

```sh
make docker
docker run -p 8360:8360 \
  -e QUICKSTACK_DATABASE_URL=postgres://quickstack_app:…@db/quickstack \
  -e QUICKSTACK_MIGRATE_URL=postgres://quickstack_owner:…@db/quickstack \
  -e QUICKSTACK_PUBLIC_URL=https://example.com \
  -e QUICKSTACK_SECRET_KEY=$(openssl rand -base64 32) \
  -v quickstack-media:/var/lib/quickstack/media \
  quickstack
```

Generate `QUICKSTACK_SECRET_KEY` once and keep it: it seals the keys of people's authenticator apps, which are lost without it. The server applies migrations as it starts. Uploads go to `/var/lib/quickstack/media`, or to a bucket when `QUICKSTACK_BLOB_URL` is set:

```sh
QUICKSTACK_BLOB_URL=s3://key:secret@bucket/prefix?endpoint=s3.example.com&region=eu-central-1
```

Without `key:secret`, the `AWS_*` variables, `~/.aws/credentials` or the instance role apply. Files are served through the app, so the bucket can stay private.

Moving to a newer kit means changing the version and checksums in `scripts/vips-kit`.

## Start a new project

```sh
cp -R quickstack myapp && cd myapp && rm -rf .git node_modules && git init
scripts/rename github.com/you/myapp myapp
make setup db-setup check
```

Then delete the samples you don't need (`internal/projects`, `internal/insights`, `internal/public`). `CLAUDE.md` says how, and how to add features.

## License

MIT, see [LICENSE](LICENSE).
