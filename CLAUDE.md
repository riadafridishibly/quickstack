# CLAUDE.md

Quickstack is a template for server-rendered Go web apps with several dashboards: copy it, rename it with `scripts/rename`, delete the samples you don't need. One binary holds the public site and its blog, sign-in, and three dashboards: a workspace's (`/w/{ws}`), a person's account (`/account`), and the platform admin (`/admin`). Pages are server-rendered with templ, htmx handles server round-trips, and Preact islands handle client-side state. The frontend is embedded in the binary. PostgreSQL 18 holds the data, with row-level security per workspace, and River runs background jobs. Node runs only at build time.

Design notes and decisions live in the issue tracker, not in the repo. Keep code comments to what is necessary.

## Commands

- `make setup`: install pnpm packages, Playwright's Chromium, and the pre-push hook (once).
- `make db-setup`: create the roles and the `quickstack` database (once). `make db-up` starts Postgres in Docker instead.
- `make dev`: Vite + Go with live reload. Open http://localhost:7360 (the templ proxy), not :8360. Mail goes to http://localhost:7360/dev/mail.
- `make s3-up`: SeaweedFS in Docker, to try the S3 blob store. Its tests run when `QUICKSTACK_TEST_S3_URL` is set (see `.env.example`).
- `make seed [args="--reset"]`: sample accounts (`owner@`, `manager@`, `member@`, `viewer@`, `admin@`, `support@`, `editor@seed.test`, password `seed-password`), two workspaces, 90 days of tasks, and four blog posts with images (two published, one scheduled for next week, one draft). The images go to `./tmp/media`, where `make dev` serves them. It refuses a database that isn't on this machine unless given `--allow-remote`.
- `make gen`: regenerate Go from `.templ` and `.sql` files, and `frontend/src/chroma.css` for code highlighting. Run it after editing either.
- `make migration name=<name>`: new timestamped migration file.
- `make check`: gen, the dependency check, templ fmt, go vet, golangci-lint, go test, tsc, Biome, and the Vite build. **Work is done only when this passes.** The pre-push hook runs it.
- `make e2e`: Playwright against the built app. Run it when you change anything a browser does (htmx attributes, islands, forms).
- `make fmt`: format Go, templ, and TypeScript.
- `go run ./cmd/quickstack create-user|reset-password|set-platform-role ...`: manage accounts from the command line. `set-platform-role you@example.com superadmin` opens `/admin`.

Building needs libvips 8.16+ (`brew install vips`, `apt install libvips-dev`), which comes in through cgo. Releases don't use it: `make dist TARGET=linux-amd64|linux-arm64|freebsd-amd64` cross-compiles with zig against a libvips kit from quickstack-vips (`scripts/vips-kit` pins the version) and packs the libraries next to the binary; `make docker` builds an image the same way.

There is no CI; run checks locally.

## Layout

```
cmd/quickstack/       serve (default), migrate, account commands
cmd/seed/             dev-only sample data command
internal/app/         New(cfg, pool): every route and middleware; Serve; Workers
internal/config/      QUICKSTACK_* environment
internal/db/          Connect, Migrate, roles.sql, migrations/, queries/, sqlc output, EscapeLike
internal/tenant/      Workspace, WithWorkspace/From(ctx), DB.Tx
internal/auth/        accounts, argon2id passwords, sessions, sign in/up, password reset; Policy and Router (policy.go), this app's roles (roles.go); authtest/
internal/workspaces/  the tenant layer: Middleware for /w/{ws}, the workspace Layout, the list at /w, settings, members, invitations
internal/account/     the personal dashboard: profile, password, sessions; its Layout frames /w too
internal/platform/    the staff dashboard at /admin: accounts, workspaces, audit log
internal/projects/    SAMPLE, the reference feature: list with live search, forms, htmx board, task-board island
internal/insights/    SAMPLE: a workspace's overview with stat tiles and charts
internal/public/      SAMPLE: landing page and public roadmaps, with their own layout and stylesheet
internal/blog/        the site's blog: writing at /admin/blog, reading at /blog, tags, the Atom feed and the sitemap
internal/markdown/    Markdown to HTML for posts: GFM, alerts, images from Media, shortcodes drawn by components.templ
internal/chart/       line and column charts drawn as SVG on the server, with a table of the values
internal/mail/        Sender (SMTP, log, dev outbox), the send job, Enqueue
internal/blob/        Store: files by key on local disk (file://) or in an S3-compatible bucket (s3://)
internal/media/       image uploads: Check, Create, Delete, the libvips job, /media/{key}/{file}, /admin/media
internal/jobs/        River client
internal/web/         Handle/Render/htmx helpers, middleware, Document/Layout/ShellLayout, form components, embedded dist/
internal/slug/        URL slugs
internal/seed/        what cmd/seed writes, through the app's own write functions; sample images and post bodies in testdata/
internal/testdb/      per-test Postgres databases
internal/apptest/     the whole app against a test database, for handler tests; Mail and Link read queued mail
frontend/src/         main.ts (htmx config, island loader), app.css (dashboards), public.css (public site), islands/
e2e/                  Playwright specs; import test/expect from ./fixtures
```

`cmd/quickstack` must never import `internal/seed`; `make check` enforces it.

## Permissions

Role-based. Each scope has roles, actions, and a table of which roles may perform each action, all in `internal/auth/roles.go`. Code asks about actions, never about role names.

- **Workspace roles** (`owner`, `admin`, `member`, `viewer`) live in `memberships`. `workspaces.Middleware` puts the user's role in the request context, and `auth.Workspace.CanIn(ctx, action)` decides what a template shows.
- **Platform roles** (`superadmin`, `support`, `editor`) live on `accounts.platform_role` and open `/admin`: `auth.Platform.CanIn(ctx, action)`. `OpenAdmin` lets a role in; each section also checks its own action (`ViewAccounts` for accounts, workspaces and the audit log, `ManageBlog` for the blog), and the sidebar lists only those the role may open (`sections` in `internal/platform/handlers.go`). An editor sees no accounts or workspaces. Platform roles give no access to any workspace; staff see that a workspace exists and who is in it, not its data.
- Routes inside a workspace or `/admin` go through an `auth.WorkspaceRouter` or `auth.PlatformRouter`: `r.Handle(pattern, action, h)`. Every route names its action; `auth.AnyMember` is for what every member may do.
- A rule about one record, such as "only owners manage owners", is a named function in the feature package that starts from an action, and both the handler and the template call it (`canManage` in `internal/workspaces/members.go`).
- `internal/app/testdata/permissions.golden` lists role × action and the action of every workspace and admin route. After changing either, run `go test ./internal/app -update` and read the diff.
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
   - `handlers.go`: `Routes(r *auth.WorkspaceRouter, tdb *tenant.DB)` plus the handlers. Workspace routes are `/w/{ws}/<feature>...`, each registered with `r.Handle(pattern, action, h)`; add actions in `internal/auth/roles.go`
   - `form.go`: parse, normalize, and validate
   - `views.templ`, with pages in `@workspaces.Layout(title)`
   - tests (see Tests)
4. Call `Routes` in `internal/app/app.go` with `wsr` (inside a workspace) or `admr` (platform staff), then run `go test ./internal/app -update`. Pages for any signed-in user go on the `signedIn` mux and public ones on `open`, with `mux.Handle`. Add a sidebar entry in the dashboard's `shell` function (`internal/workspaces/views.templ` for a workspace).

To drop a sample, delete its package, its routes in `app.go`, its sidebar entry, and (before the first deploy) its migration and queries.

## Copying auth

Auth is the part other projects most often take on its own and adapt:

- `internal/auth/`. `roles.go` holds this app's roles; the `platform_role` check in `create_auth` lists the platform roles too, so change both.
- the migrations `*_default_privileges.sql` and `*_create_auth.sql` (accounts, sessions, password resets, the audit log)
- the queries `auth.sql` and `audit.sql`

Its code uses only `db`, `web`, `mail` and `jobs` from this module, and `make check-deps` fails otherwise; keep workspace concerns such as memberships and invitations in `internal/workspaces`. Its handler tests run through `apptest`, the whole app, so they need adapting where that doesn't exist.

## Database access

- Workspace data goes through `tdb.Tx(ctx, func(tx tenant.Tx) error {...})`. It sets `app.workspace_id` for the workspace in ctx, transaction-locally. Pass `tx.WorkspaceID` to queries. A query outside `Tx` fails instead of returning another workspace's rows.
- Platform tables (`accounts`, `sessions`, `password_resets`, `workspaces`, `memberships`, `invitations`, `audit_log`) have no RLS and use `db.New(pool)`, or `db.New(tx)` inside `pgx.BeginTxFunc`. Queries about one workspace's members still filter by `workspace_id`.
- Record changes people should be able to trace with `RecordAudit` in the same transaction; `/admin/audit` lists them.
- Send mail with `mail.Enqueue(ctx, jc, tx, msg)` inside the transaction, so it goes out only if the change commits. Links in mail start with `cfg.PublicURL`.
- Map `pgx.ErrNoRows` to `web.ErrNotFound` (`web.NotFoundIfNoRows`).
- Images go through `internal/media` (see Media).

## Media

Images uploaded at `/admin/media` (or pasted into the post editor), for the blog or any other feature.

- Files live in a `blob.Store` named by `QUICKSTACK_BLOB_URL`: `file:///dir`, `file://./relative/dir`, or `s3://key:secret@bucket/prefix?endpoint=host:port&region=r&insecure=1` for any S3-compatible bucket. In development it is `./tmp/media`; `make s3-up` runs SeaweedFS to try a bucket.
- To accept an upload: `media.Check(r)` before the transaction (a `*media.Rejected` error is a 422 with its message; `media.CheckData` takes bytes you already have), then `media.Create(ctx, tx, store, jc, userID, up)` inside it, and `media.Discard` if it doesn't commit. `media.Delete` removes an asset and queues the deletion of its files.
- `Create` stores the original and queues `media_process` on the `media` queue. The job re-encodes it with libvips into WebP at `media.Widths` (never wider than the original) and an `og.jpg` social card, then marks the asset ready. `media.Process` does the same work directly, as the seed does. Only the JPEG, PNG, WebP, GIF and HEIF loaders of libvips may run.
- An asset is named by its 8-character key, never its id. `media.URL(key)` (`/media/{key}`) is the address Markdown uses; `/media/{key}/{640.webp|og.jpg}` serves the files with a year-long cache, since a key's files never change. The original is never served: it keeps metadata such as where a photo was taken.

## Blog

A site-wide blog, written at `/admin/blog` by platform roles with `ManageBlog` (superadmin and editor) and read at `/blog`.

- **Posts** are a platform table, not a workspace one. A post is live when `status = 'published'` and `published_at <= now()`, so a future time schedules it without a job; public pages cache for a minute. Write posts through `blog.Create` or the admin handlers, which share `save`: it validates, checks the cover and the slug, records which assets the body shows in `post_media`, keeps a redirect when a published post's slug changes, and writes the audit log.
- **Markdown**: `markdown.Render(ctx, src, q.ListAssetsByKeys)` returns the HTML and a list of `Problem`s, the parts it left out (an unknown shortcode, an image that isn't ready). `markdown.Draft` shows images still processing as placeholders, for the preview. It is goldmark with GFM, footnotes, definition lists, heading IDs and GitHub-style alerts (`> [!NOTE]`, `TIP`, `IMPORTANT`, `WARNING`, `CAUTION`). Raw HTML is dropped. `![alt](/media/key)` becomes an `<img>` with a `srcset` of the variants. Code is highlighted with chroma classes, styled by `frontend/src/chroma.css`.
- **Shortcodes** are Hugo-style and sit on a line of their own: `figure`, `youtube`, `vimeo`, `details`, and `tabs` holding `tab`s. Those that wrap content close with `{{< /name >}}`, and the content between is Markdown. To add one, add an entry to `shortcodes` in `internal/markdown/shortcode.go` (its params, which are required, a `check` for their values, whether it takes inner content) and a component in `components.templ`, then cover it in `testdata/shortcodes.md` and its golden file. Restyle a shortcode by changing its component; posts keep their Markdown.
- **The editor** is `<markdown-editor>` (`frontend/src/islands/markdown-editor.tsx`), registered with `defineEnhancement` around the server-rendered textarea, which stays in the form so the post saves without JS. It runs CodeMirror in a shadow root (see Islands), sets the textarea's value, and fires `input`, so htmx posts the body to `/admin/blog/preview` and swaps the rendered preview and its problems in. Pasted and dropped images are posted to `/admin/media` with `Accept: application/json`, and the answer's Markdown replaces an "Uploading…" placeholder.

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

- Each dashboard has a Layout: `workspaces.Layout`, `account.Layout`, `platform.Layout`. They fill in a `web.Shell` (sidebar sections, user, links) for `web.ShellLayout`. Sign-in and other one-off pages use `web.Layout`; the public site and the blog use `public.Layout`, which passes a `web.Meta` (description, canonical URL, social image, feed, `noindex`) to `web.Document`. Pages start with `@web.PageHeader(title)`.
- Form controls go inside `@web.Field(label, name, errMsg)`, take `{ web.InputAttrs(name, errMsg)... }`, and use the `web.*Class` constants. A control that only makes sense without JS gets `[.js_&]:hidden`.
- Components are daisyUI 5 classes (`btn`, `input`, `badge`, `alert`, ...) on the `quickstack` theme in `app.css`. Use theme colors (`bg-base-100`, `text-error`), not raw Tailwind palette colors. Chart colors are `chart-1`..`chart-5` and `chart-other`.
- Charts: build a `chart.Data` and render `@chart.Card(d)`. They need no JavaScript; tooltips show on hover with CSS.
- Avoid daisyUI `radial-progress` and `countdown`: they need inline `style`, which the CSP blocks.
- The CSP blocks inline `<script>` blocks, `style="..."`, `onclick=`-style handlers, and `hx-on:*`. JavaScript goes in `frontend/src/`.
- htmx is version 4 (docs at four.htmx.org; most examples online are for 2). Attributes don't inherit: put `hx-target`, `hx-swap` and the rest on the element that sends the request, or mark a parent's with `:inherited` (`hx-target:inherited`). Events have colon names (`htmx:after:swap`).
- `app.css` scans `internal/**/*.{templ,go}` except `internal/public/`; `public.css` scans `internal/public/`, `internal/blog/`, `internal/markdown/` and `internal/web/`. Both scan `frontend/src/**`. Write class names as complete literal strings; Tailwind can't see classes assembled at runtime.

## Islands

- One file per custom element: `frontend/src/islands/<tag-name>.tsx` calls `defineIsland("<tag-name>", mount)`. main.ts loads the file the first time `<tag-name>` appears, including after htmx swaps. There is no list to register in.
- Inside the element, server-render a fallback that works on its own, such as the board's columns with a Move form per card. `mount` reads values from the element and its JSON, then the island replaces the fallback.
- Pass larger data with `@templ.JSONScript(id, value)`, placed before the element, and read it with `readJSON(id)`.
- Island form fields live in light DOM and submit with the surrounding form. The server re-normalizes and re-validates everything.
- An island gets `data-mounted` once it renders. e2e tests wait for `<tag>[data-mounted]` before using it.
- `<markdown-editor>` mounts CodeMirror in a shadow root. There CodeMirror's styles are constructed stylesheets, which the CSP allows; on the document it adds a `<style>` element, which the CSP blocks. Theme colors reach it as CSS variables.

## Generated code: don't edit

- `*_templ.go` comes from `.templ`.
- Files in `internal/db/` that start with `// Code generated by sqlc` come from the SQL files.
- Commit both kinds, so `go build` works from a clean checkout.

## Tests

- Tests run against real Postgres, never mocks. `testdb.New(t)` gives each test its own database, copied from a migrated template and connected as `quickstack_app` so RLS applies. `testdb.CreateWorkspace` makes a workspace; `authtest.SignIn` makes an account with a session cookie, and `testdb.Member` one that is also in a workspace.
- Handler tests go through the whole app: `s := apptest.New(t)`, then `s.Get`, `s.Post`, or `s.Do(apptest.Req{..., Target: "<id>"})` for an htmx request. `s.Link(email)` is the link in the last mail queued to that address.
- Call `t.Parallel()` in tests that use `testdb`. Keep the suite fast.
- Cover behavior that can break: each handler path (success, validation failure for htmx and plain posts, not found, forbidden, fragment vs. full page). Skip tests that only restate the code or re-test a library.
- e2e covers flows that need a browser: islands, htmx swaps, the no-JS fallback, and mail links via `/dev/mail` (`mailLink` in fixtures). The fixture fails a test on any console error, which includes CSP violations.
- `e2e/owner.setup.ts` signs the owner up and creates the workspace first, and saves the session, so specs start signed in. Use `test.use({ storageState: { cookies: [], origins: [] } })` for signed-out tests.

## Comments and prose

Keep a comment only when it says something the code does not: why, an invariant, or an outside constraint. Delete comments that restate the code, argue against alternatives nobody proposed, list what something doesn't do, or recount history. Write plain sentences. Commits, issues and PRs too.
