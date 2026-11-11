# quickstack

A starting point for Go web apps with several dashboards: one binary, PostgreSQL, server-rendered pages, and as little JavaScript as each screen needs.

- **Go** with `net/http`, **templ** for HTML, **sqlc** for queries, **goose** migrations, **River** jobs
- **htmx** for server round-trips, **Preact** islands for client state, **Tailwind 4** with **daisyUI 5**, built by **Vite** and embedded
- **PostgreSQL 17** with row-level security per workspace

It comes with:

| | Where | What it shows |
|---|---|---|
| Sign in | `/login`, `/signup`, `/forgot-password` | argon2id passwords, sessions in Postgres, rate limits, reset by email |
| Workspace dashboard | `/w/{ws}` | tenant middleware, workspace roles, overview charts, projects with live search, a task board (htmx + a drag-and-drop island), members and invitations |
| Account dashboard | `/account` | profile, password, sessions on other devices |
| Platform admin | `/admin` | platform roles, accounts, workspaces, audit log |
| Public site | `/`, `/p/{ws}` | its own layout and stylesheet, cached pages |

## Run it

```sh
make setup        # pnpm packages, Playwright's Chromium, pre-push hook
make db-setup     # or: make db-up for Postgres in Docker
make seed         # sample accounts and data; password seed-password
make dev          # open http://localhost:7360
```

Sign in as `owner@seed.test`, or `admin@seed.test` for the platform admin. Mail goes to http://localhost:7360/dev/mail in development.

`make check` runs everything that must pass; `make e2e` runs the browser tests. `make build` puts the app in `bin/quickstack`, and `make build-linux` makes a static Linux binary.

## Start a new project

```sh
cp -R quickstack myapp && cd myapp && rm -rf .git node_modules && git init
scripts/rename github.com/you/myapp myapp
make setup db-setup check
```

Then delete the samples you don't need (`internal/projects`, `internal/insights`, `internal/public`). `CLAUDE.md` says how, and how to add features.
