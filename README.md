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
| Platform admin | `/admin` | platform roles, accounts, workspaces, audit log, media uploads |
| Public site | `/`, `/p/{ws}` | its own layout and stylesheet, cached pages |

## Run it

It needs Go, Node, PostgreSQL 18 (or Docker) and libvips for development: `brew install vips`, or `apt install libvips-dev`.

```sh
make setup        # pnpm packages, Playwright's Chromium, pre-push hook
make db-setup     # or: make db-up for Postgres in Docker
make seed         # sample accounts and data; password seed-password
make dev          # open http://localhost:7360
```

Sign in as `owner@seed.test`, or `admin@seed.test` for the platform admin. Mail goes to http://localhost:7360/dev/mail in development.

`make check` runs everything that must pass; `make e2e` runs the browser tests. `make build` puts the app in `bin/quickstack` for this machine.

## Deploy

Releases carry their own libvips, so servers don't need it installed. `make dist` cross-compiles with [zig](https://ziglang.org) (`brew install zig`) against a prebuilt libvips kit from [quickstack-vips](https://github.com/riadafridishibly/quickstack-vips), from any machine:

```sh
make dist TARGET=linux-amd64     # or linux-arm64, freebsd-amd64
```

`dist/quickstack-linux-amd64.tar.gz` holds `bin/quickstack`, the libraries in `lib/`, and their licences in `share/licenses/`. On the server, unpack it and run its `install` script as root: it copies the release into `/opt/quickstack` owned by root, and creates the `quickstack` user with `/var/lib/quickstack` for uploads. Run the app as that user. The binary loads its libraries from `/opt/quickstack/lib`, so only root may write there. Linux servers need glibc 2.28 or newer (RHEL 8, Debian 10, Ubuntu 20.04 and later); FreeBSD servers need 14.0 or newer.

`make docker` builds a Docker image with the same layout. Moving to a newer kit means changing the version and checksums in `scripts/vips-kit`.

## Start a new project

```sh
cp -R quickstack myapp && cd myapp && rm -rf .git node_modules && git init
scripts/rename github.com/you/myapp myapp
make setup db-setup check
```

Then delete the samples you don't need (`internal/projects`, `internal/insights`, `internal/public`). `CLAUDE.md` says how, and how to add features.

## License

MIT, see [LICENSE](LICENSE).
