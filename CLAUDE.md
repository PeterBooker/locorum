# Locorum

Desktop app providing local WordPress dev environments via Docker. Pure-Go, immediate-mode GUI (Gio). Alternative to Local by Flywheel / DevKinsta.

## Quick Facts

| | |
|---|---|
| Language | Go 1.25 (see `go.mod`) |
| Module | `github.com/PeterBooker/locorum` |
| GUI | [Gio](https://gioui.org/) v0.9 (immediate-mode) |
| Docker | Go SDK (`github.com/docker/docker`), no Compose |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no CGO) |
| Migrations | `golang-migrate/migrate/v4`, embedded SQL |
| Data dir | `~/.locorum/` (config, SQLite, router state, mkcert certs) |
| Site files | `~/locorum/sites/{slug}/` (user-visible) |
| Build | `make build` → `build/bin/locorum` |
| Test | `go test ./...` |

CGO is required **only** for Gio's display backend. SQLite is pure Go.

## Build / Test / Run

```bash
go run .               # dev run
make build             # → build/bin/locorum
make test              # go test ./...
make linux-amd64       # cross-compile (see Makefile for all targets)
```

After code changes, **always** run `gofmt -w .`, `go vet ./...`, and `go test ./...` before reporting done. `gofmt` is non-negotiable — every `.go` file must be gofmt-clean (CI will reject otherwise). `build/` and a stray `locorum` binary at the repo root are both gitignored — a one-off `go build .` won't pollute `git status`.

**Use `go install` for testing UI changes.** The user runs the app from `~/go/bin/locorum` (on `$PATH`), so `go install .` is the only build step that updates the binary the user actually launches. `go build` to a temp path leaves the user looking at stale UI and wastes a feedback cycle. After every UI edit you want the user to see, run `go install .` — not `go build`.

Testing the GUI itself requires running the app; the test suite only covers storage, nginx templating, and utils. If you touch UI code you can't functionally verify without launching the app — **say so explicitly** rather than claiming the change works.

## Architecture

### Package Layout

```
main.go                     window + event loop + startup/shutdown
internal/app                 filesystem setup, global Docker infra
internal/docker              Engine interface + SDK implementation; ContainerSpec/NetworkSpec, healthchecks, retry, label-based ops
internal/docker/fake         in-memory Engine for unit tests
internal/orch                Plan / Step / Run — site-lifecycle orchestrator with rollback on failure
internal/router              Router interface + types (SiteRoute, ServiceRoute, Health)
internal/router/traefik      Traefik v3 implementation (file provider + admin API)
internal/router/fake         in-memory Router for tests
internal/tls                 TLS Provider interface + mkcert implementation
internal/storage             SQLite CRUD + embedded migrations (sites, site_hooks, settings)
internal/sites               SiteManager — core business logic, builds Plans for lifecycle methods
internal/sites/sitesteps     orch.Step implementations: ensure-network, pull-images, chown, create-containers, wait-ready, register-routes, …
internal/hooks               Hook engine: Runner interface, env builder, exec/host adapters, fake/ for tests
internal/ui                  Gio GUI (immediate-mode)
internal/types               shared data model (Site struct)
internal/utils               filesystem/WSL/platform helpers + streaming host-shell exec
internal/version             build-time identity + pinned image tags (incl. AlpineImage for chown helper)
config/router/               embedded Traefik static + dynamic YAML templates
config/nginx/                embedded per-site nginx config template (HTTP-only)
config/apache/               embedded per-site Apache config template (HTTP-only)
config/{db,php}/             embedded MySQL + PHP config
config/hooks/                embedded defaults pack (hook templates surfaced in the UI)
```

Dependency direction (strict):
```
main ─┬─ app    ─┬─ docker, utils, router (interface)
      │
      ├─ router/traefik ─┬─ docker, router, tls, version
      ├─ tls
      ├─ storage ─── types, hooks (Hook + Event types only)
      ├─ hooks  ─┬─ docker, utils, types, version (adapters in adapter.go)
      ├─ orch   ── (no internal deps — engine-agnostic Step orchestrator)
      ├─ sites/sitesteps ── docker, orch, router, types
      ├─ sites   ─┬─ docker, storage, types, utils, router, hooks, orch, sitesteps
      └─ ui      ─┴─ sites, types, hooks, orch, docker (orch.StepResult + docker.PullProgress only)
```

### Load-Bearing Invariants

These are the rules that hold the app together. Don't violate them without discussing first.

1. **UI never calls Docker or Storage directly.** Everything goes through `SiteManager` in `internal/sites/`. The UI only touches `sites.SiteManager` and `internal/types` (and `orch.StepResult` + `docker.PullProgress` for progress callbacks).
2. **All Docker resources carry the `io.locorum.platform=locorum` label.** Startup and shutdown wipe everything matching this label (`app.Initialize` / `app.Shutdown`). The label is the source of truth — never use name-prefix matching in new code; use `Engine.RemoveContainersByLabel` / `Engine.NetworksByLabel` / `Engine.RemoveVolumesByLabel`.
3. **Every `ContainerSpec` carries `LabelConfigHash`.** `Engine.EnsureContainer` recreates a container only when the hash on the live container differs from the freshly-computed one. The hash deliberately excludes `LabelVersion` and `EnvSecret` *values* so a Locorum upgrade or password rotation alone never forces a recreate.
4. **Routing is owned by `router.Router`.** `internal/sites/` and `internal/app/` only depend on the interface. The Traefik implementation in `internal/router/traefik/` is the only thing that knows about Traefik config files, the admin API, or the global router container. Don't add routing-engine specifics anywhere else.
5. **Shared UI state is mutex-protected.** Every read/write of `UIState` fields goes through `s.mu`. Background goroutines lock → mutate → unlock → `state.Invalidate()` to wake the event loop.
6. **UI is redrawn every frame.** There is no widget tree. Persistent state lives in Go structs (`widget.Clickable`, `widget.Editor`, `widget.List`). `Layout()` is called on every `FrameEvent`.
7. **Long-running ops run in goroutines.** Docker calls, WP downloads, file dialogs, link checks — never call these from `Layout()`. Spawn a goroutine and invalidate when done.
8. **SiteManager → UI via callbacks.** `sm.OnSitesUpdated`, `sm.OnSiteUpdated`, and the lifecycle/hook callbacks (`OnStepStart`, `OnStepDone`, `OnPlanDone`, `OnPullProgress`, `OnHook*`) are set by the UI layer in `ui.New()`. The backend never imports `internal/ui`.
9. **Every Engine method takes `context.Context`.** There is NO shared `ctx` field. `git grep "SetContext\|d\.ctx"` must return empty.
10. **Lifecycle methods take `context.Context`.** `StartSite`, `StopSite`, `DeleteSite`, `CloneSite`, `ExportSite`, `UpdateSiteVersions`, `UpdatePublicDir` all take `ctx` as their first arg. The same `ctx` flows into `runHooks` and any router/Docker calls — propagate, never replace with `context.Background()` mid-call.
11. **Hooks fire pre/post around every lifecycle method.** `internal/sites/sites.go` calls `sm.runHooks(ctx, hooks.PreX, site)` before the work and `sm.runHooks(ctx, hooks.PostX, site)` after. The runner returns the task error in fail-strict mode and `nil` in fail-warn mode — propagate verbatim.
12. **Lifecycle methods are Plans.** `StartSite`, `StopSite`, `DeleteSite` build an `orch.Plan` of named steps and run it via `orch.Run`. A failing step rolls back every prior succeeded step in reverse order. Step `Apply` must be idempotent; `Rollback` is best-effort and never aborts the rollback chain on its own error.
13. **Per-site mutex serialises lifecycle calls.** `SiteManager.siteMutex(siteID)` returns a per-site `*sync.Mutex` from a `sync.Map`. Every lifecycle method locks it for the duration. Different sites still run in parallel.
14. **Spec builders bake in security defaults.** `WebSpec`, `PHPSpec`, `DatabaseSpec`, `RedisSpec`, `MailSpec`, `AdminerSpec` produce containers with `CapDrop=ALL`, `NoNewPrivileges=true`, `Init=true`, log size capped at `10m × 3`, and per-role `CapAdd`. Don't hand-roll a `ContainerSpec` for a role that already has a builder.
15. **DB passwords flow through `EnvSecret`.** Engine never logs the value; error strings are scrubbed via `redactErrSpec` before propagating. `docker inspect` still shows the value (a documented limit).

### Background Ops Pattern

```go
state.SetSiteToggling(siteID, true)
go func() {
    err := sm.StartSite(siteID)
    state.SetSiteToggling(siteID, false) // internally locks + Invalidate()s
    if err != nil {
        state.ShowError("Failed to start site: " + err.Error())
    }
}()
```

`UIState` helpers (`SetSites`, `UpdateSite`, `SetSiteToggling`, `ShowError`, etc.) handle locking and invalidation internally — prefer them over touching `s.mu` directly from outside `state.go`.

## Docker Layout

No `docker-compose.yml`. Everything is created via the Go SDK in `internal/docker/`.

### Global (created at startup)

| Container | Image | Purpose |
|---|---|---|
| `locorum-global-router` | `traefik:v3.5` | TLS termination + hostname routing on host ports 80/443; admin API at `127.0.0.1:8888` (basic-auth, password generated per process) |
| `locorum-global-mail` | `mailhog/mailhog` | SMTP capture at `mail.localhost` |
| `locorum-global-adminer` | `adminer:latest` | DB UI at `db.localhost` |

All join the `locorum-global` bridge network. Image versions are pinned in `internal/version/images.go`.

### Per-site (created on start)

| Container | Image | Network alias |
|---|---|---|
| `locorum-{slug}-web` | `nginx:1.28-alpine` or `httpd:2.4-alpine` (HTTP only — no TLS) | `web` |
| `locorum-{slug}-php` | `wodby/php:{version}` | `php` |
| `locorum-{slug}-database` | `mysql:{version}` | `database` |
| `locorum-{slug}-redis` | `redis:{version}-alpine` | `redis` |

Each site has its own internal bridge network (`locorum-{slug}`). Web and PHP containers also join `locorum-global` so Traefik can route to them. DB data persists in named volume `locorum-{slug}-dbdata`.

### Routing

The router uses Traefik's file provider — dynamic configs live at `~/.locorum/router/dynamic/{site,svc}-*.yaml` and Traefik watches the directory via fsnotify. Per-site certificates issued by mkcert land at `~/.locorum/certs/{site,svc}-*/{cert,key}.pem` and are bind-mounted into the router. If mkcert is not installed (or `mkcert -install` has not been run), sites still serve over HTTPS — the browser just shows an untrusted-cert warning until the user installs mkcert. A persistent UI banner surfaces the install instructions.

### Lifecycle

- **Startup** (`app.Initialize`) — wipes all label-matched containers/networks, recreates the global network, brings up mailhog + adminer, calls `router.EnsureRunning` (creates Traefik), then `router.UpsertService` for `mail` and `adminer`. `ReconcileState` marks all sites as stopped. Embedded configs extracted to `~/.locorum/config/`; obsolete `nginx/global.conf`, `nginx/map.tmpl`, and `config/certs/` are scrubbed.
- **Start site** (`sm.StartSite`) — downloads WordPress if empty, renders per-site web server config (HTTP only), creates network + 4 containers (or starts existing ones), then `router.UpsertSite` writes the dynamic config and waits for the route to load. Multisite is configured if enabled.
- **Stop site** — stops containers (not removed), calls `router.RemoveSite`. Container state is preserved.
- **Delete site** — stops + removes containers, removes site network, removes per-site configs, calls `router.RemoveSite` (which also drops the cert), deletes DB row. **Volumes are kept** (so DB data survives deletion by design).
- **Shutdown** — clears `~/.locorum/router/dynamic/` and per-site configs, then removes everything labeled `io.locorum.platform=locorum`. Volumes persist.

## UI (Gio) Guide

Gio is immediate-mode: the entire UI is laid out on every `FrameEvent`. Read `internal/ui/ui.go` for the root layout.

### File Map

| File | Purpose |
|---|---|
| `ui.go` | Root `UI` struct, top-level `Layout`, delete-confirm dialog, error banner |
| `state.go` | Thread-safe `UIState` (mutex-protected) |
| `theme.go` | Dark palette (hacktoberfest-inspired: navy + neon cyan + gold), spacing, typography |
| `sidebar.go` | Left panel: logo, search, site list, new-site button |
| `sitedetail.go` | Right panel: site header, info sections, controls |
| `sitecontrols.go` | Start/Stop/View Files/Export action bar |
| `newsite.go` | New-site modal |
| `versioneditor.go` | Change PHP/MySQL/Redis versions on a stopped site |
| `clonemodal.go` | Clone site modal |
| `modal.go` | Generic modal overlay (backdrop + pointer blocking) |
| `widgets.go` | Reusable primitives (buttons, inputs, dropdowns, badges, KV rows, output areas, loader, confirm dialog, tab bar) |
| `toast.go` | Auto-dismissing toast notifications |
| `logviewer.go` | Container log viewer (service selector tabs) |
| `wpcli.go` | WP-CLI command input + output |
| `dbcredentials.go` | DB credentials panel with copy-to-clipboard |
| `linkchecker.go` | Link-crawl output panel |
| `logo.go` | Logo rendering |

### Theme Conventions

- **Dark theme only.** Background is navy (`ColorSidebarBg` / `ColorContentBg`). Text is light (`ColorTextPrimary`).
- **Accent colors:** neon cyan (`ColorBlue600`) for primary actions, gold (`ColorGold`) for branding, neon green (`ColorGreen600`) for success, hot pink (`ColorRed600`) for danger.
- **Spacing:** use the `SpaceXS`…`Space2XL` constants from `theme.go`, not raw `unit.Dp()`.
- **Text sizes:** minimum 18sp (accessibility). Use `TextXS`/`TextSM`/`TextBase`/`TextLG`.
- **Buttons:** use `PrimaryButton`, `SecondaryButton`, `DangerButton`, `SuccessButton`, `SmallButton` from `widgets.go` — don't hand-roll `material.Button` unless you need custom colors.
- **Sidebar width:** `SidebarWidth` (300dp). Modal width: `ModalWidth` (560dp).

### Responsive sizing in Gio

Gio is immediate-mode and constraint-based. Every widget receives `gtx.Constraints` (a `Min`/`Max` rectangle in *pixels*, not Dp), reports `layout.Dimensions{Size}`, and the parent decides where to place those dims. There is no CSS, no "fill: 100%", no implicit centering. Everything responsive in this codebase boils down to **what `Min`/`Max` does the child see, and what dims does it report back**.

Read this before writing anything that has to fill, center, or resize.

#### The constraint model

| | What this means |
|---|---|
| `Constraints.Max` | The largest size the parent allows. Children should not exceed it. |
| `Constraints.Min` | The smallest size the parent expects. `layout.Center` and many helpers use `Min` to know what box to center within — if `Min` is `(0,0)`, they treat the child's own dims as the whole world. |
| `Dimensions.Size` | What the child actually consumed. The parent positions other siblings around this. |

A child that returns dims smaller than `Min` will not be magically expanded — it will just be drawn at top-left of whatever box the parent assumed.

#### Parents that zero out `Min` on you

These are the two recurring traps. Both make `layout.Center` look broken.

1. **`layout.Flex` Rigid children get cross-axis `Min = 0`.** In a vertical Flex, Rigid children see `Min.X = 0`. A `layout.Center` placed inside such a Rigid can't center horizontally — it returns the child's own dims and the icon hugs the left edge of the rail. Setting `Flex.Alignment = layout.Middle` does *not* fix this: alignment positions the child *after* layout, but the child's reported width is still just its own content. Re-pin the cross-axis at the Rigid boundary:

   ```go
   layout.Rigid(func(gtx layout.Context) layout.Dimensions {
       gtx.Constraints.Min.X = gtx.Constraints.Max.X
       return layout.Center.Layout(gtx, child)
   })
   ```

   The `railRow` helper in `internal/ui/navrail.go` exists for exactly this. Reuse it (or copy the pattern) in any vertical-rail layout.

2. **`layout.Stack` Stacked children get `Min = (0, 0)` on both axes.** This bites when wrapping content in `RoundedFill` (which is `Stack` internally): the bg paints at the right size because `Expanded` reuses the maxSz of stacked dims, but the `Stacked` callback — typically a `layout.Center` — sees `Min = 0` and reports the child's own size, painting at the Stack's top-left. Re-pin inside the Stacked callback:

   ```go
   return RoundedFill(gtx, bg, th.Radii.R2, func(gtx layout.Context) layout.Dimensions {
       gtx.Constraints.Min = image.Pt(pillSz, pillSz)
       return layout.Center.Layout(gtx, child)
   })
   ```

When something looks "stuck top-left" in a sized container, the answer is almost always one of these two — not a bug in `layout.Center`.

#### Geometry: declared size = visible size

Custom-painted glyphs (icons, logos, marks) are usually described on a design grid (24×24 for icons, 32×32 for the brand). The grid almost always has padding — visible strokes occupy something like 3..21 of a 24-grid, never 0..24. If you naively scale by `px/24`, the rendered glyph is 60–75% of the requested size, and every caller ends up over-declaring sizes to "see" the glyph (e.g. asking for a 40dp icon to fill a 28dp space).

Fix it once, in the geometry primitive:

1. Scale by the **visible span**, not the grid: `scale = px / visibleSpan`. For our icons, `visibleSpan = 18`.
2. Push an `op.Offset` so the design-center (e.g. (12, 12) in a 24-grid) lands at the geometric center of the `px × px` box. For a content range centered on (12, 12) in a span-18 design, that's `op.Offset(image.Pt(-px/6, -px/6))`.
3. Return the `op.TransformStack` so the caller can `defer st.Pop()`.
4. Stroke width scales with the same `scale` so glyph weight stays proportional.

`internal/ui/icons.go:iconBase` and `internal/ui/logo.go:LayoutLogo` are the canonical implementations. New geometry primitives should follow the same contract: **a declared `unit.Dp` size always means the size of the visible content, never a padded grid.**

#### Centering: pick the right tool

| Want | Use |
|---|---|
| Center one widget in the available cross-axis of a Flex | `Flex.Alignment = layout.Middle` *and* re-pin Min cross-axis on each Rigid (see above) |
| Center within a fixed box (a pill, button, modal frame) | Set `Constraints.Min = Max = box size`, then `layout.Center.Layout(gtx, child)` |
| Pad uniformly inside a parent | `layout.UniformInset(dp)` — does not expand, only shrinks |
| Make a child fill a parent | Set `Constraints.Min = Constraints.Max` and have the child honour `Min` (most do) |

`layout.Center` is reliable — but only when both `Min` and `Max` describe the box you want to center within. If you set just `Max`, you'll fight it forever.

#### Things that won't help (but it's tempting to try)

- `layout.Spacer` between Flex children to "push centered" — works for a single sibling, not for centering a widget that has no symmetric counterpart.
- `Flex.Alignment` alone — see above; it positions known-size children, it doesn't tell the child what size to report.
- Pushing a hand-rolled `op.Offset` to "nudge" something into place — fragile under resize, breaks click-handling, almost always means you missed a `Min` somewhere upstream.

### Adding a UI Component

1. New file in `internal/ui/`.
2. Struct holds persistent widget state (`widget.Clickable`, `widget.Editor`, `widget.List`). Constructor takes `*UIState`, `*sites.SiteManager`, `*ToastManager` if needed.
3. Implement `Layout(gtx layout.Context, th *material.Theme) layout.Dimensions`.
4. Use `layout.Flex`, `layout.Stack`, `layout.Inset`; use theme constants for colors/sizing.
5. Wire into `ui.go` (`UI` struct field, construct in `New()`, call from `Layout()`).
6. For backend ops, spawn a goroutine and update `UIState` when done.

See the `add-ui-component` skill in `.claude/skills/` for a fuller scaffold.

## Database

SQLite at `~/.locorum/storage.db`, driver `modernc.org/sqlite`. Migrations embedded from `internal/storage/migrations/` and applied on startup.

### Adding a Migration

Filename pattern: `YYYYMMDDHHMMSS_short_description.{up,down}.sql` — `golang-migrate` orders strictly by the numeric prefix. Both `.up.sql` and `.down.sql` are required.

**SQLite gotcha:** `DROP COLUMN` isn't supported on older SQLite; the existing down-migrations recreate the table (see `20260215000002_add_multisite.down.sql`).

When adding a column:
1. Write up + down SQL files in `internal/storage/migrations/`.
2. Add field to `types.Site` in `internal/types/types.go` (with `json` tag).
3. Update all four queries in `internal/storage/storage.go` (GetSites, GetSite, AddSite, UpdateSite) — column list AND the Scan/Exec parameter list.
4. Add test coverage in `storage_test.go` if the field is non-trivial.
5. If the field is user-facing, wire it through `SiteManager` and `UIState`/UI.

See the `add-migration` skill for the full checklist.

## Hooks

User-defined commands attached to site lifecycle events. The engine lives in `internal/hooks/`; the GUI in `internal/ui/hookspanel.go`, `hookeditor.go`, `hookoutput.go`.

### Data flow

```
GUI ─Add/Update/Delete──► storage.{AddHook,UpdateHook,...}      (SiteManager pass-throughs)
                                ▼
                          site_hooks table
                                ▲
SiteManager.runHooks  ──Run────► hooks.Runner ──┬─► docker.ExecInContainerStream  (exec, wp-cli)
                                                └─► utils.RunHostStream            (exec-host)
                                                  via DockerContainerExecer / UtilsHostExecer
                                                  adapters in internal/hooks/adapter.go
```

### Where to add a new lifecycle event

1. Declare the constant in `internal/hooks/events.go` (`Pre*` and `Post*`); add to `allEvents`. Add to `activeEvents` as soon as the firing site exists.
2. If the event fires while containers are down, list it in `Event.AllowsContainerTasks`.
3. Add `sm.runHooks(ctx, hooks.PreX, site)` before the work and `sm.runHooks(ctx, hooks.PostX, site)` after, in the relevant `internal/sites/` method. Hold the per-site mutex across both.

### Where to add a new task type

1. Add a `TaskType` constant in `internal/hooks/hooks.go`. Update `Valid()` and `AllTaskTypes()`.
2. Add a `case` in `taskFromHook` in `internal/hooks/tasks.go`. Implement the `task` interface.
3. Update `Hook.Validate` for any task-specific constraints (e.g. service column, run-as-user).
4. Surface the new type in `hookTaskTypeOptions` in `internal/ui/hookeditor.go` plus the related `hookTaskTypeAt` / `hookTaskTypeIndex` mapping.

### Run logs

Every Run writes a complete log to `~/.locorum/hooks/runs/<site-slug>/<event>-<timestamp>.log`. `hooks.SweepLogs` runs at startup with defaults `30 days OR 50 per site` (whichever is fewer). The runner doesn't depend on the sweep — failure to open the log file is non-fatal (warn + continue).

### Testing

- `internal/hooks/runner_test.go` covers the runner with `fake.ContainerExecer`, `fake.HostExecer`, `fake.Lister`, `fake.Settings` — no Docker required.
- `internal/storage/hooks_test.go` covers the CRUD on `:memory:` SQLite, including FK-cascade-on-delete.
- `internal/sites/sites_test.go` exercises `runHooks` and the per-site mutex via `internal/hooks/fake`.
- A test in `main_test.go` validates the embedded `config/hooks/defaults.json` so a packaging mistake fails CI rather than the GUI.

## Testing

Standard `testing` package, no external frameworks. Table-driven tests with `t.Run` subtests.

Covered:
- `internal/storage/storage_test.go` — CRUD against in-memory SQLite (`:memory:`)
- `internal/sites/files_test.go` — nginx template funcs
- `internal/utils/utils_test.go` — filesystem helpers

Conventions:
- `t.Helper()` in setup funcs
- `t.TempDir()` for filesystem tests
- `t.Cleanup()` for teardown
- `:memory:` SQLite for storage tests (isolated per test)

Pure-Go tests run without Docker. Don't add tests that require a Docker daemon; mock at the `SiteManager` boundary if you need to.

## Platform Notes

- **WSL2:** `main.go` unsets `WAYLAND_DISPLAY` and sets `GSETTINGS_BACKEND=memory` so Gio uses X11 via XWayland (WSLg Wayland is missing min/max). `utils.isWSL()` detects it. Windows-via-WSL path conversion is handled in `OpenDirectory`.
- **Windows without WSL:** file picker uses `sqweek/dialog`. With WSL, it shells into `wsl.exe zenity`.
- **PHP UID/GID:** on Windows `os.Getuid()` returns -1; falls back to `1000:1000` to match the wodby image default.

## Claude-Specific Guidance

- **Prefer `Edit` over `Write`** for existing files — Write asks for a full file and is wasteful for small changes.
- **Don't create new docs/READMEs** unless asked. The `README.md`, `CLAUDE.md`, and migration file headers are enough.
- **Don't add comments** that just describe what the code does; the code is already self-documenting. Only add comments for *why* (non-obvious constraint, workaround, invariant).
- **For tool use:** use `Bash` for `go build`/`go test`/`go vet`; use `Read` for files, `Edit` for modifications. Use the `Explore` agent for open-ended searches across the repo; use `grep`/`find` directly for specific lookups.
- **Don't touch `~/.locorum/`** in your working commands — that's runtime state for the user's real sites. Work only within the repo.
- **Migration files are immutable once merged.** If you need to change a shipped migration, write a *new* migration. Don't edit the old one.

## External Links

- Gio: <https://gioui.org/>, API: <https://pkg.go.dev/gioui.org>
- Docker SDK: <https://pkg.go.dev/github.com/docker/docker/client>
- golang-migrate: <https://github.com/golang-migrate/migrate>
