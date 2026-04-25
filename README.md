# Locorum

*Early development (0.x) — expect rough edges and occasional breaking changes between releases. Don't point it at production data.*

## About

Locorum is a local development environment for WordPress. It uses Docker to spin up isolated WordPress sites, each with its own nginx, PHP, MySQL, and Redis containers, routed through a shared HTTPS reverse proxy.

The desktop UI is built with [Gio](https://gioui.org/), a pure-Go immediate-mode GUI framework.

---

## Install

Pre-built downloads for every release are on the [GitHub Releases page](https://github.com/PeterBooker/locorum/releases/latest). All platforms require Docker (Docker Desktop on macOS / Windows; Docker Engine or rootless Docker on Linux) to be installed and running.

### macOS

1. Download `Locorum-<version>-macos-universal.dmg` (one universal binary works on both Intel and Apple Silicon).
2. Open the DMG and drag Locorum to your Applications folder.
3. See **[First-Run Notes → macOS](#macos-gatekeeper)** before launching for the first time.

### Windows

1. Download the MSI for your CPU:
   - `Locorum-<version>-windows-amd64.msi` — Intel / AMD 64-bit (most PCs)
   - `Locorum-<version>-windows-arm64.msi` — Snapdragon / Surface Pro X
2. Double-click to install (admin prompt expected).
3. See **[First-Run Notes → Windows](#windows-smartscreen)** before launching.

### Linux

1. Download the tarball for your CPU:
   - `locorum-<version>-linux-amd64.tar.gz` — x86_64
   - `locorum-<version>-linux-arm64.tar.gz` — aarch64
2. Extract and install:
   ```bash
   tar -xzf locorum-<version>-linux-amd64.tar.gz
   cd locorum-<version>-linux-amd64
   sudo install -m755 locorum /usr/local/bin/locorum
   ```
3. (Optional) Add a menu entry:
   ```bash
   mkdir -p ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
   cp locorum.desktop ~/.local/share/applications/
   cp locorum.png ~/.local/share/icons/hicolor/256x256/apps/locorum.png
   ```
4. The binaries are built against glibc 2.35 — that covers Ubuntu 22.04+, Debian 12+, and current Arch / CachyOS / Fedora. Most distros also have the Gio runtime libs (`libwayland`, `libx11`, `libxkbcommon`, `libegl1`, `libvulkan`, `libxcursor`) installed by default; if Locorum complains about a missing library, install the matching `-dev`-less package via your distro's package manager.

---

## First-Run Notes

### macOS Gatekeeper

Locorum isn't notarized by Apple yet, so Gatekeeper blocks unsigned apps on the first launch. One-time workaround:

1. Open Finder → Applications.
2. **Right-click** (or Control-click) `Locorum.app` → **Open**.
3. In the dialog, click **Open** again.

After that, double-click works normally. macOS remembers the override per app.

### Windows SmartScreen

The MSI isn't code-signed yet, so Microsoft Defender SmartScreen warns on install:

> Microsoft Defender SmartScreen prevented an unrecognized app from starting.

Click **More info** → **Run anyway**. The warning won't reappear once the app is installed.

---

## Building from Source

### Prerequisites

- **Go** 1.25+
- **Docker** (running and accessible)

#### Linux / WSL2

Gio's Linux backend uses CGO. The native file-picker dialog (`sqweek/dialog`) needs GTK3 too. Install the system libraries:

```bash
sudo apt install gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev \
    libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev \
    libxcursor-dev libvulkan-dev libgtk-3-dev
```

Equivalent on Arch/CachyOS:

```bash
sudo pacman -S --needed pkgconf wayland libx11 libxcb libxkbcommon-x11 \
    mesa libxcursor vulkan-headers gtk3
```

#### macOS

Gio's macOS backend uses CGO. Install Xcode command-line tools:

```bash
xcode-select --install
```

#### Windows

No extra toolchain required — Gio v0.9 builds Windows targets in pure Go (no CGO, no MinGW).

### Build and run

```bash
make build       # → build/bin/locorum (with version ldflags)
go run .         # dev iteration
./build/bin/locorum
```

On first launch the app:

1. Sets up `~/.locorum/` (config, SQLite database, per-site nginx confs).
2. Wipes any leftover `locorum-*` Docker resources, then creates the global network and the proxy / mail / DB-admin containers.
3. Opens the desktop window.

### Release builds

Release artifacts (with embedded icon, manifest, version metadata, and installer wrapping) are built via `make`:

| Target | Output | Tools needed |
|---|---|---|
| `make tarball-linux-amd64` | `build/dist/locorum-<version>-linux-amd64.tar.gz` | `rsvg-convert` |
| `make tarball-linux-arm64` | `build/dist/locorum-<version>-linux-arm64.tar.gz` | aarch64 GCC + arm64 Gio headers (use the CI runner) |
| `make dist-windows` | `Locorum-<version>-windows-{amd64,arm64}.msi` | `gogio`, `wix` (.NET) |
| `make dist-macos` | `Locorum-<version>-macos-universal.dmg` | macOS host, `gogio`, `create-dmg` |

A real release happens automatically when you push a bare-semver tag:

```bash
git tag 0.1.0
git push origin 0.1.0
```

The [`Release` workflow](.github/workflows/release.yml) builds every artifact on its native runner, then GoReleaser publishes a **draft** GitHub Release with all five binaries + `SHA256SUMS` + auto-generated notes. Review the draft and click **Publish**.

### Cross-compilation quick-build

For a quick "does it compile" check across platforms (no icon, manifest, or version metadata — bare binary only):

```bash
GOOS=linux   GOARCH=arm64 go build -o build/bin/locorum-linux-arm64 .
GOOS=windows GOARCH=amd64 go build -o build/bin/locorum.exe .
GOOS=darwin  GOARCH=arm64 go build -o build/bin/locorum-macos .
```

Cross-builds that need CGO (Linux/macOS) require the matching cross toolchain on the host.

---

## Developing with Gio

Gio is **immediate-mode**: the entire UI is redrawn every frame by calling layout functions. There is no virtual DOM or persistent widget tree.

Key concepts:

- **`app.Window`** — creates the OS window and delivers events.
- **`app.FrameEvent`** — triggers a redraw; you respond by laying out the entire UI.
- **`layout.Context` (gtx)** — carries constraints, the operations list, and dimensions.
- **`op.Ops`** — accumulates drawing operations for the frame.
- **`widget.*`** — types that hold persistent state (click state, editor text, scroll position).

### Project structure

```
main.go                    Entry point: window creation, event loop, startup/shutdown
internal/
  app/                     Filesystem setup, global Docker infra
  docker/                  Thin wrapper over the Docker SDK
  storage/                 SQLite + embedded migrations
  sites/                   SiteManager — core business logic
  types/                   Shared data model
  utils/                   Filesystem / WSL / platform helpers
  version/                 Build-time identity (Version, Commit, Date)
  ui/
    ui.go                  Root UI struct, top-level Layout, error banner
    state.go               Mutex-protected shared state
    theme.go               Color palette, spacing, typography
    sidebar.go             Left panel (logo, search, site list)
    sitedetail.go          Right panel
    newsite.go             New-site modal
    widgets.go             Reusable primitives
    ...                    See CLAUDE.md for the full file map
```

### Event loop

```go
for {
    switch e := w.Event().(type) {
    case app.DestroyEvent:
        return e.Err
    case app.FrameEvent:
        gtx := app.NewContext(&ops, e)
        ui.Layout(gtx)       // Lay out the entire UI
        e.Frame(gtx.Ops)     // Submit the frame
    }
}
```

### Background operations

Long-running operations (Docker container management, file dialogs, link checks) must run in goroutines so they don't block the UI. After completion, update `UIState` via its locking helpers — they call `state.Invalidate()` internally to wake the event loop:

```go
state.SetSiteToggling(siteID, true)
go func() {
    err := sm.StartSite(siteID)
    state.SetSiteToggling(siteID, false)
    if err != nil {
        state.ShowError("Failed to start site: " + err.Error())
    }
}()
```

### Backend ↔ UI

`SiteManager` talks to the UI through callbacks — `sm.OnSitesUpdated` and `sm.OnSiteUpdated`, both wired by `ui.New()`. The backend never imports `internal/ui`.

### Adding a UI component

1. New file in `internal/ui/`.
2. Struct holds persistent widget state (`widget.Clickable`, `widget.Editor`, `widget.List`).
3. Implement `Layout(gtx layout.Context, th *material.Theme) layout.Dimensions`.
4. Wire it into `ui.go`.

See the `add-ui-component` skill in `.claude/skills/` for a fuller scaffold, and CLAUDE.md for the full architecture / invariants.

### Useful Gio resources

- [Gio documentation](https://gioui.org/)
- [Gio API reference](https://pkg.go.dev/gioui.org)
- [Gio examples](https://git.sr.ht/~eliasnaur/gio-example)
- [Gio extended widgets](https://pkg.go.dev/gioui.org/x)

---

## Migrations

Install the migrate CLI once:

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

Create a new migration:

```bash
migrate create -ext sql -dir internal/storage/migrations create_<table_name>_table
```

See the `add-migration` skill in `.claude/skills/` and `internal/storage/migrations/` for examples. Migrations are embedded into the binary at build time and applied automatically on startup.

---

## License

[MIT](LICENSE) © 2026 Peter Booker.
