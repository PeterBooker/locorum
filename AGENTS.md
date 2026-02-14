# AGENTS.md — Locorum

Locorum is a desktop application providing local WordPress development environments via Docker. It is a professional-grade alternative to tools like Local by Flywheel and DevKinsta, written in pure Go with a native GUI.

## Quick Reference

| Item | Detail |
|---|---|
| Language | Go 1.23+ (toolchain 1.24) |
| Module | `github.com/PeterBooker/locorum` |
| GUI framework | [Gio](https://gioui.org/) v0.9 (immediate-mode) |
| Docker | Go SDK (`github.com/docker/docker`), no Compose |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO for DB) |
| Migrations | `golang-migrate/migrate/v4` with embedded SQL |
| Build | `make build` / `go build -o build/bin/locorum .` |
| Test | `make test` / `go test ./...` |
| Data directory | `~/.locorum/` (config, SQLite DB, site files) |

## Architecture

### Package Dependency Graph

```
main.go ──┬── internal/app         Application lifecycle, Docker init
           │     ├── internal/docker    Docker Engine API wrapper
           │     └── internal/utils     Filesystem helpers, WSL detection
           │
           ├── internal/storage    SQLite persistence (sites table)
           │     └── internal/types     Site struct definition
           │
           ├── internal/sites      Business logic: site CRUD, start/stop, WP download, export
           │     ├── internal/docker
           │     ├── internal/storage
           │     ├── internal/types
           │     └── internal/utils
           │
           └── internal/ui         Gio GUI layer
                 ├── internal/sites     All operations go through SiteManager
                 └── internal/types     Site struct for display
```

**Critical rule**: The UI layer never calls Docker or Storage directly. All operations go through `SiteManager` in `internal/sites/`.

### Package Responsibilities

- **`main.go`** — Window creation, Gio event loop, startup/shutdown orchestration. Embeds all config files via `//go:embed all:config`.
- **`internal/app/`** — `Initialize()` sets up filesystem (`~/.locorum/`), extracts embedded configs, creates global Docker infrastructure. `Shutdown()` tears down all locorum containers and networks.
- **`internal/docker/`** — Thin wrapper over the Docker Go SDK. Manages containers, networks, volumes, and exec commands. All container names are prefixed `locorum-`.
- **`internal/sites/`** — `SiteManager` is the central business logic layer. Handles site CRUD, start/stop lifecycle, WordPress download/extraction, nginx config generation, WP-CLI execution, log streaming, and site export.
- **`internal/storage/`** — SQLite CRUD for the `sites` table. Migrations are embedded SQL files applied automatically on startup.
- **`internal/types/`** — `Site` struct shared across all packages. This is the core data model.
- **`internal/ui/`** — All GUI code. Immediate-mode layout with Gio. Thread-safe state management via `UIState` with mutex.
- **`internal/utils/`** — Filesystem helpers (`EnsureDir`), WSL2 detection, directory opening (platform-aware).

### UI Architecture (Gio Immediate-Mode)

Gio redraws the entire UI every frame. There is no widget tree or virtual DOM.

**Key concepts:**
- `app.FrameEvent` triggers a full re-layout via `ui.Layout(gtx)`
- `layout.Context` (gtx) carries constraints and accumulates draw operations
- Widget state (`widget.Clickable`, `widget.Editor`, `widget.List`) persists across frames in Go structs
- `material.*` functions render styled widgets from persistent state

**State management:** `UIState` (`internal/ui/state.go`) is the single source of truth for all UI state, protected by `sync.Mutex`. It holds: site list, selected site, search term, modal visibility, loading/toggling states, error messages, log output, WP-CLI output.

**Background operations:** All Docker and long-running operations must run in goroutines. After completion, lock `UIState`, update fields, unlock, and call `state.Invalidate()` to wake the event loop:

```go
go func() {
    err := sm.StartSite(siteID)
    state.mu.Lock()
    state.SiteToggling[siteID] = false
    state.mu.Unlock()
    state.Invalidate()
}()
```

**Backend-to-UI communication:** `SiteManager` has callback fields (`OnSitesUpdated`, `OnSiteUpdated`) that the UI sets during initialization. These update `UIState` and invalidate the window.

**Adding new UI components:**
1. Create a new file in `internal/ui/`
2. Define a struct with persistent widget state
3. Implement `Layout(gtx layout.Context, th *material.Theme) layout.Dimensions`
4. Use `layout.Flex`, `layout.Stack`, `layout.Inset` for positioning
5. Wire it into `ui.go`

### UI File Breakdown

| File | Purpose |
|---|---|
| `ui.go` | Root `UI` struct, layout orchestration, delete confirmation dialog |
| `state.go` | Thread-safe `UIState` (mutex-protected shared state) |
| `theme.go` | Tailwind CSS-inspired color palette, spacing constants, typography |
| `sidebar.go` | Left panel: app title, search box, scrollable site list, new-site button |
| `sitedetail.go` | Right panel: site header, info sections, version display, DB credentials |
| `sitecontrols.go` | Start/Stop/View Files/Export action bar |
| `newsite.go` | Modal form for creating a new site with version dropdowns |
| `modal.go` | Generic modal overlay with backdrop and pointer event blocking |
| `widgets.go` | Reusable primitives: buttons, inputs, dropdowns, badges, sections, KV rows, output areas, loader, confirm dialog |
| `toast.go` | Toast notification system (error/success/info with auto-dismiss) |
| `logviewer.go` | Container log viewer with service selector tabs |
| `wpcli.go` | WP-CLI command input and output panel |
| `dbcredentials.go` | Database credentials display with copy-to-clipboard |

## Docker Architecture

Docker is used programmatically via the Go SDK — there are no docker-compose files.

### Global Infrastructure (created on app startup)

| Container | Image | Purpose |
|---|---|---|
| `locorum-global-webserver` | `nginx:1.28` | Reverse proxy with SNI routing (ports 80/443) |
| `locorum-global-mail` | MailHog | Email capture at `mail.localhost` |
| `locorum-global-adminer` | Adminer | Database management at `db.localhost` |

All connected to the `locorum-global` bridge network.

### Per-Site Containers (created on site start)

| Container | Image | Purpose |
|---|---|---|
| `locorum-{slug}-web` | `nginx:1.28-alpine` | Per-site HTTPS reverse proxy |
| `locorum-{slug}-php` | `wodby/php:{version}` | PHP-FPM with WP-CLI |
| `locorum-{slug}-database` | `mysql:{version}` | MySQL with persistent named volume |
| `locorum-{slug}-redis` | `redis:{version}-alpine` | Object cache |

Each site gets its own `locorum-{slug}` bridge network. All site containers connect to both their site network and `locorum-global`.

### Lifecycle

1. **Startup:** `app.Initialize()` removes all existing `locorum-*` containers/networks, then recreates global infrastructure. `ReconcileState()` marks all sites as stopped in the DB.
2. **Site start:** Creates site network, all four containers, generates nginx config, reloads global webserver. Downloads WordPress on first start if needed.
3. **Site stop:** Stops and removes site containers and network. Updates DB state.
4. **Shutdown:** `app.Shutdown()` removes all `locorum-*` containers and networks. Database volumes persist.

### Naming Convention

All Docker resources use the prefix `locorum-`:
- Networks: `locorum-global`, `locorum-{slug}`
- Containers: `locorum-global-*`, `locorum-{slug}-*`
- Volumes: `locorum-{slug}-dbdata`

## Embedded Configuration

All files under `config/` are embedded into the binary at compile time (`//go:embed all:config` in `main.go`) and extracted to `~/.locorum/config/` on startup.

| Path | Purpose |
|---|---|
| `config/nginx/global.conf` | Global nginx: mail/adminer proxies, HTTP→HTTPS redirect, SNI stream routing |
| `config/nginx/site.tmpl` | Go template: per-site nginx HTTPS config with PHP-FPM proxy |
| `config/nginx/map.tmpl` | Go template: SNI routing map (`slug.localhost` → site web container) |
| `config/db/db.cnf` | MySQL UTF-8 character set defaults |
| `config/php/php.ini` | PHP config: 1GB memory, 100MB uploads, Xdebug, MailHog SMTP |
| `config/certs/wildcard.localhost.*` | Self-signed `*.localhost` TLS certificate (mkcert) |

## Data Model

The `Site` struct (`internal/types/types.go`) is the core data type:

```go
type Site struct {
    ID, Name, Slug, Domain      string
    FilesDir, PublicDir          string
    Started                      bool
    PHPVersion, MySQLVersion     string
    RedisVersion, DBPassword     string
    CreatedAt, UpdatedAt         string
}
```

Sites are persisted in SQLite at `~/.locorum/storage.db`. Site files live in `~/.locorum/sites/{slug}/`.

## Database & Migrations

SQLite database at `~/.locorum/storage.db` using the pure Go driver (`modernc.org/sqlite`).

Migrations live in `internal/storage/migrations/` as embedded SQL files, applied automatically on startup via `golang-migrate`.

**Creating new migrations:**

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate create -ext sql -dir internal/storage/migrations add_{description}
```

Each migration requires both `.up.sql` and `.down.sql` files.

## Building & Testing

### Prerequisites

- Go 1.23+
- Docker (running and accessible)
- GCC and C development tools (required by Gio)
- Linux/WSL2: `sudo apt install gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev`
- macOS: `xcode-select --install`

### Build Commands

```bash
make build              # Default: build/bin/locorum
make linux-amd64        # Cross-compile for Linux
make darwin-amd64       # Cross-compile for macOS Intel
make darwin-arm64       # Cross-compile for macOS Apple Silicon
make windows-amd64      # Cross-compile for Windows (requires mingw)
make all                # All platforms
make clean              # Remove build/
make test               # go test ./...
```

### Running

```bash
go run .                    # Development
./build/bin/locorum         # After building
```

### Testing

```bash
go test ./...
```

Tests use the standard `testing` package with table-driven tests. No external test frameworks.

Test files:
- `internal/storage/storage_test.go` — SQLite CRUD with in-memory databases
- `internal/sites/files_test.go` — Nginx config template functions
- `internal/utils/utils_test.go` — Filesystem helper functions

Test conventions:
- `setupTestDB` helpers with `:memory:` SQLite for isolation
- `t.TempDir()` for filesystem tests
- `t.Helper()` for setup functions
- `t.Cleanup()` for teardown

## Platform Notes

### WSL2

The application detects WSL2 at runtime (`internal/utils/utils.go`) and:
- Forces X11 backend by unsetting `WAYLAND_DISPLAY`
- Sets `GSETTINGS_BACKEND=memory`
- Converts WSL paths to Windows paths for file explorer operations

### Window

Default window size: 1024x768. Dark sidebar (256dp wide), white content panel.

## Code Conventions

- **Error handling:** Errors are returned up the call stack. The UI layer displays errors via toast notifications or error banners in `UIState`.
- **Concurrency:** All shared state access goes through `UIState.mu` mutex. Background goroutines must lock before writing and call `Invalidate()` after.
- **Naming:** Docker resources prefixed `locorum-`. Site slugs generated via `gosimple/slug`. UUIDs via `google/uuid`.
- **Templates:** Go `text/template` for nginx config generation (`internal/sites/files.go`).
- **No CGO for SQLite:** Uses `modernc.org/sqlite` (pure Go). CGO is only needed for Gio's display backend.
