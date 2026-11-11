# CLAUDE.md

Quickstack is a template for server-rendered Go web apps with several dashboards: copy it, rename it with `scripts/rename`, delete the samples you don't need. One binary holds the public site, sign-in, and three dashboards: a workspace's (`/w/{ws}`), a person's account (`/account`), and the platform admin (`/admin`). Pages are server-rendered with templ, htmx handles server round-trips, and Preact islands handle client-side state. The frontend is embedded in the binary. PostgreSQL 18 holds the data, with row-level security per workspace, and River runs background jobs. Node runs only at build time.

Design notes and decisions live in the issue tracker, not in the repo. Keep code comments to what is necessary.

## Commands

- `make setup`: install pnpm packages, Playwright's Chromium, and the pre-push hook (once).
- `make db-setup`: create the roles and the `quickstack` database (once). `make db-up` starts Postgres in Docker instead.
- `make dev`: Vite + Go with live reload. Open http://localhost:7360 (the templ proxy), not :8360. Mail goes to http://localhost:7360/dev/mail.
- `make seed [args="--reset"]`: sample accounts (`owner@`, `manager@`, `member@`, `viewer@`, `admin@`, `support@seed.test`, password `seed-password`), two workspaces and 90 days of tasks. It refuses a database that isn't on this machine unless given `--allow-remote`.
- `make gen`: regenerate Go from `.templ` and `.sql` files. Run it after editing either.
- `make migration name=<name>`: new timestamped migration file.
- `make check`: gen, the dependency check, templ fmt, go vet, golangci-lint, go test, tsc, Biome, and the Vite build. **Work is done only when this passes.** The pre-push hook runs it.
- `make e2e`: Playwright against the built app. Run it when you change anything a browser does (htmx attributes, islands, forms).
- `make fmt`: format Go, templ, and TypeScript.
- `go run ./cmd/quickstack create-user|reset-password|set-platform-role ...`: manage accounts from the command line. `set-platform-role you@example.com superadmin` opens `/admin`.

There is no CI; run checks locally.

## Layout

```
cmd/quickstack/       serve (default), migrate, account commands
cmd/seed/             dev-only sample data command
internal/app/         New(cfg, pool): every route and middleware; Serve; Workers
internal/config/      QUICKSTACK_* environment
internal/db/          Connect, Migrate, roles.sql, migrations/, queries/, sqlc output, EscapeLike
internal/tenant/      Workspace, WithWorkspace/From(ctx), DB.Tx
internal/auth/        accounts, argon2id passwords, sessions, sign in/up, password reset, roles (Can, Require, RequirePlatform); authtest/
internal/workspaces/  the tenant layer: Middleware for /w/{ws}, the workspace Layout, the list at /w, settings, members, invitations
internal/account/     the personal dashboard: profile, password, sessions; its Layout frames /w too
internal/platform/    the staff dashboard at /admin: accounts, workspaces, audit log
internal/projects/    SAMPLE, the reference feature: list with live search, forms, htmx board, task-board island
internal/insights/    SAMPLE: a workspace's overview with stat tiles and charts
internal/public/      SAMPLE: landing page and public roadmaps, with their own layout and stylesheet
internal/chart/       line and column charts drawn as SVG on the server, with a table of the values
internal/mail/        Sender (SMTP, log, dev outbox), the send job, Enqueue
internal/jobs/        River client
internal/web/         Handle/Render/htmx helpers, middleware, Document/Layout/ShellLayout, form components, embedded dist/
internal/slug/        URL slugs
internal/seed/        what cmd/seed writes, through the app's own write functions
internal/testdb/      per-test Postgres databases
internal/apptest/     the whole app against a test database, for handler tests; Mail and Link read queued mail
frontend/src/         main.ts (htmx config, island loader), app.css (dashboards), public.css (public site), islands/
e2e/                  Playwright specs; import test/expect from ./fixtures
```

`cmd/quickstack` must never import `internal/seed`; `make check` enforces it.

## Two kinds of role

- **Workspace roles** (`owner`, `admin`, `member`, `viewer`) live in `memberships`. `workspaces.Middleware` puts the user's role in the request context; `auth.Require(action)` enforces it on routes and `auth.CanIn(ctx, action)` decides what a template shows. Add actions in `internal/auth/roles.go`.
- **Platform roles** (`superadmin`, `support`) live on `accounts.platform_role` and open `/admin`: `auth.RequirePlatform(action)`, `auth.CanPlatform(role, action)`. They give no access to any workspace; staff see that a workspace exists and who is in it, not its data.
- Ownership is the query: a row of another workspace isn't forbidden, it's not found.

## Adding a feature

Copy `internal/projects/`. It has a list with live search and a filter, create and edit forms with validation, real deletes, a board that htmx updates in place, and an island.

1. Migration: `make migration name=create_<things>`. Never edit a migration that has shipped; add a new one. A workspace-owned table needs:
   - `workspace_id bigint NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE`, with `PRIMARY KEY (workspace_id, id)`
   - foreign keys to other workspace tables as `(workspace_id, x_id) REFERENCES xs (workspace_id, id)`
   - indexes that start with `workspace_id`
   - `ENABLE` and `FORCE ROW LEVEL SECURITY`, plus the `workspace_isolation` policy from `20261111120200_create_projects.sql`

   `TestEveryWorkspaceTableHasRLS` fails if a table with `workspace_id` lacks the policy.
2. Queries: `internal/db/queries/<feature>.sql` with sqlc annotations, then `make gen`. Every query still filters `WHERE workspace_id = @workspace_id`; RLS is the backstop, not the filter. Go code calls the generated methods and never builds SQL strings. Pass user search text through `db.EscapeLike` before `ILIKE`.
3. Package `internal/<feature>/`:
   - `handlers.go`: `Routes(mux, tdb *tenant.DB)` plus the handlers. Workspace routes are `/w/{ws}/<feature>...`, each wrapped in `auth.Require(action)` unless every member may use it
   - `form.go`: parse, normalize, and validate
   - `views.templ`, with pages in `@workspaces.Layout(title)`
   - tests (see Tests)
4. Register the routes in `internal/app/app.go` on the `ws` mux (inside a workspace), `signedIn` (any signed-in user), `adm` (platform staff) or `open` (anyone), and add a sidebar entry in the dashboard's `shell` function (`internal/workspaces/views.templ` for a workspace).

To drop a sample, delete its package, its routes in `app.go`, its sidebar entry, and (before the first deploy) its migration and queries.

## Database access

- Workspace data goes through `tdb.Tx(ctx, func(tx tenant.Tx) error {...})`. It sets `app.workspace_id` for the workspace in ctx, transaction-locally. Pass `tx.WorkspaceID` to queries. A query outside `Tx` fails instead of returning another workspace's rows.
- Platform tables (`accounts`, `sessions`, `password_resets`, `workspaces`, `memberships`, `invitations`, `audit_log`) have no RLS and use `db.New(pool)`, or `db.New(tx)` inside `pgx.BeginTxFunc`. Queries about one workspace's members still filter by `workspace_id`.
- Record changes people should be able to trace with `RecordAudit` in the same transaction; `/admin/audit` lists them.
- Send mail with `mail.Enqueue(ctx, jc, tx, msg)` inside the transaction, so it goes out only if the change commits. Links in mail start with `cfg.PublicURL`.
- Map `pgx.ErrNoRows` to `web.ErrNotFound` (`web.NotFoundIfNoRows`).

## Handlers

- Write `func(w, r) error` and wrap it in `web.Handle`. Return `web.ErrNotFound` for a 404 and `web.ErrForbidden` for a 403. Any other error is logged and renders the 500 page.
- `auth.From(ctx)` is the signed-in user; `tenant.From(ctx)` the workspace, and `ws.URL("/path")` a link inside it.
- Render with `web.Render(w, r, status, component)`. It buffers, so a template error never sends half a page.
- One URL serves both the page and its fragment. Render the fragment when `web.IsHTMX(r)` is true, or when `web.HXTarget(r) == "<element id>"` if one URL feeds several targets. Otherwise render the full page.
- If validation fails, return status 422 and re-render the form: the fragment for htmx, the full page otherwise. main.ts configures htmx to swap 422 responses.
- After a successful POST, call `web.Redirect`. It sends HX-Redirect to htmx and a 303 to plain form posts. An island that keeps its own state sends `Prefer: return=minimal`; answer 204 when `web.WantsNoContent(r)`.
- Forms use POST only, so they work without JS: `POST /things` creates, `POST /things/{id}` updates, `POST /things/{id}/delete` deletes.
- CSRF is handled by `http.CrossOriginProtection` in app.go. No tokens are needed.

## Choosing client behavior, in this order

1. **Plain HTML**: links, forms, `<details>`, `<dialog>`, `popover`. Every form must work with JavaScript off; `e2e/projects.spec.ts` has a no-JS test that shows how to check.
2. **htmx**, when the server should render the result: inline validation, search-as-you-type, partial updates, row deletes.
3. **A Preact island**, when the UI holds state the server doesn't need on every keystroke: drag and drop, editors, rich inputs.
4. **A whole client-side app** only for a screen that is mostly an editor. Build it with Vite as another entry, embed it, and call the same Go handlers with the same session cookie. No separate Node server.

## Templates

- Each dashboard has a Layout: `workspaces.Layout`, `account.Layout`, `platform.Layout`. They fill in a `web.Shell` (sidebar sections, user, links) for `web.ShellLayout`. Sign-in and other one-off pages use `web.Layout`; the public site has its own layout in `internal/public`. Pages start with `@web.PageHeader(title)`.
- Form controls go inside `@web.Field(label, name, errMsg)`, take `{ web.InputAttrs(name, errMsg)... }`, and use the `web.*Class` constants. A control that only makes sense without JS gets `[.js_&]:hidden`.
- Components are daisyUI 5 classes (`btn`, `input`, `badge`, `alert`, ...) on the `quickstack` theme in `app.css`. Use theme colors (`bg-base-100`, `text-error`), not raw Tailwind palette colors. Chart colors are `chart-1`..`chart-5` and `chart-other`.
- Charts: build a `chart.Data` and render `@chart.Card(d)`. They need no JavaScript; tooltips show on hover with CSS.
- Avoid daisyUI `radial-progress` and `countdown`: they need inline `style`, which the CSP blocks.
- The CSP blocks inline `<script>` blocks, `style="..."`, `onclick=`-style handlers, and `hx-on:*`. JavaScript goes in `frontend/src/`.
- htmx is version 4 (docs at four.htmx.org; most examples online are for 2). Attributes don't inherit: put `hx-target`, `hx-swap` and the rest on the element that sends the request, or mark a parent's with `:inherited` (`hx-target:inherited`). Events have colon names (`htmx:after:swap`).
- `app.css` scans `internal/**/*.{templ,go}` except `internal/public/`; `public.css` scans `internal/public/` and `internal/web/`. Both scan `frontend/src/**`. Write class names as complete literal strings; Tailwind can't see classes assembled at runtime.

## Islands

- One file per custom element: `frontend/src/islands/<tag-name>.tsx` calls `defineIsland("<tag-name>", mount)`. main.ts loads the file the first time `<tag-name>` appears, including after htmx swaps. There is no list to register in.
- Inside the element, server-render a fallback that works on its own, such as the board's columns with a Move form per card. `mount` reads values from the element and its JSON, then the island replaces the fallback.
- Pass larger data with `@templ.JSONScript(id, value)`, placed before the element, and read it with `readJSON(id)`.
- Island form fields live in light DOM and submit with the surrounding form. The server re-normalizes and re-validates everything.
- An island gets `data-mounted` once it renders. e2e tests wait for `<tag>[data-mounted]` before using it.

## Generated code: don't edit

- `*_templ.go` comes from `.templ`.
- Files in `internal/db/` that start with `// Code generated by sqlc` come from the SQL files.
- Commit both kinds, so `go build` works from a clean checkout.

## Tests

- Tests run against real Postgres, never mocks. `testdb.New(t)` gives each test its own database, copied from a migrated template and connected as `quickstack_app` so RLS applies. `testdb.CreateWorkspace` makes a workspace; `authtest.SignIn` and `authtest.Member` make accounts with session cookies.
- Handler tests go through the whole app: `s := apptest.New(t)`, then `s.Get`, `s.Post`, or `s.Do(apptest.Req{..., Target: "<id>"})` for an htmx request. `s.Link(email)` is the link in the last mail queued to that address.
- Call `t.Parallel()` in tests that use `testdb`. Keep the suite fast.
- Cover behavior that can break: each handler path (success, validation failure for htmx and plain posts, not found, forbidden, fragment vs. full page). Skip tests that only restate the code or re-test a library.
- e2e covers flows that need a browser: islands, htmx swaps, the no-JS fallback, and mail links via `/dev/mail` (`mailLink` in fixtures). The fixture fails a test on any console error, which includes CSP violations.
- `e2e/owner.setup.ts` signs the owner up and creates the workspace first, and saves the session, so specs start signed in. Use `test.use({ storageState: { cookies: [], origins: [] } })` for signed-out tests.

## Comments and prose

Keep a comment only when it says something the code does not: why, an invariant, or an outside constraint. Delete comments that restate the code, argue against alternatives nobody proposed, list what something doesn't do, or recount history. Write plain sentences. Commits, issues and PRs too.
