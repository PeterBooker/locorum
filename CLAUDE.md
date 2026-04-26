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

Testing the GUI itself requires running the app; the test suite only covers storage, nginx templating, and utils. If you touch UI code you can't functionally verify without launching the app — **say so explicitly** rather than claiming the change works.

## Architecture

### Package Layout

```
main.go                     window + event loop + startup/shutdown
internal/app                 filesystem setup, global Docker infra
internal/docker              thin wrapper over Docker SDK (labels, container/network/volume helpers)
internal/router              Router interface + types (SiteRoute, ServiceRoute, Health)
internal/router/traefik      Traefik v3 implementation (file provider + admin API)
internal/router/fake         in-memory Router for tests
internal/tls                 TLS Provider interface + mkcert implementation
internal/storage             SQLite CRUD + embedded migrations (sites, site_hooks, settings)
internal/sites               SiteManager — core business logic, fires hooks at every lifecycle event
internal/hooks               Hook engine: Runner interface, env builder, exec/host adapters, fake/ for tests
internal/ui                  Gio GUI (immediate-mode)
internal/types               shared data model (Site struct)
internal/utils               filesystem/WSL/platform helpers + streaming host-shell exec
internal/version             build-time identity + pinned image tags
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
      ├─ sites   ─┬─ docker, storage, types, utils, router, hooks (interface)
      └─ ui      ─┴─ sites, types, hooks (read types only — never the runner directly)
```

### Load-Bearing Invariants

These are the rules that hold the app together. Don't violate them without discussing first.

1. **UI never calls Docker or Storage directly.** Everything goes through `SiteManager` in `internal/sites/`. The UI only touches `sites.SiteManager` and `internal/types`.
2. **All Docker resources carry the `io.locorum.platform=locorum` label.** Startup and shutdown wipe everything matching this label (`app.Initialize` / `app.Shutdown`). The `locorum-` name prefix is also swept for migration from pre-label installs but the label is the source of truth — never use name-prefix matching in new code; use `docker.RemoveByLabel` / `docker.NetworksByLabel`.
3. **Routing is owned by `router.Router`.** `internal/sites/` and `internal/app/` only depend on the interface. The Traefik implementation in `internal/router/traefik/` is the only thing that knows about Traefik config files, the admin API, or the global router container. Don't add routing-engine specifics anywhere else.
4. **Shared UI state is mutex-protected.** Every read/write of `UIState` fields goes through `s.mu`. Background goroutines lock → mutate → unlock → `state.Invalidate()` to wake the event loop.
5. **UI is redrawn every frame.** There is no widget tree. Persistent state lives in Go structs (`widget.Clickable`, `widget.Editor`, `widget.List`). `Layout()` is called on every `FrameEvent`.
6. **Long-running ops run in goroutines.** Docker calls, WP downloads, file dialogs, link checks — never call these from `Layout()`. Spawn a goroutine and invalidate when done.
7. **SiteManager → UI via callbacks.** `sm.OnSitesUpdated` and `sm.OnSiteUpdated` are set by the UI layer in `ui.New()`. The backend never imports `internal/ui`.
8. **Lifecycle methods take `context.Context`.** `StartSite`, `StopSite`, `DeleteSite`, `CloneSite`, `ExportSite`, `UpdateSiteVersions`, `UpdatePublicDir` all take `ctx` as their first arg. The same `ctx` flows into `runHooks` and any router/Docker calls — propagate, never replace with `context.Background()` mid-call.
9. **Hooks fire pre/post around every lifecycle method.** `internal/sites/sites.go` calls `sm.runHooks(ctx, hooks.PreX, site)` before the work and `sm.runHooks(ctx, hooks.PostX, site)` after. The runner returns the task error in fail-strict mode and `nil` in fail-warn mode — propagate verbatim. New lifecycle methods get one new `Event` constant in `internal/hooks/events.go` plus two `runHooks` calls.
10. **Per-site mutex serialises lifecycle calls.** `SiteManager.siteMutex(siteID)` returns a per-site `*sync.Mutex`. Every lifecycle method locks it for the duration. Different sites still run in parallel.

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
