# Locorum

*Note: This is a very early prototype. It is not yet ready for use.*

## About

Locorum is a simple yet powerful local development environment for WordPress projects. It uses Docker to create isolated WordPress environments with nginx, PHP, MySQL, and Redis containers.

The desktop UI is built with [Gio](https://gioui.org/), a pure Go immediate-mode GUI framework.

## Prerequisites

- **Go** 1.23+
- **Docker** (running and accessible)
- **GCC** and C development tools

### Linux / WSL2

Gio requires system libraries for its display backends:

```bash
sudo apt install gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev \
    libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev \
    libxcursor-dev libvulkan-dev
```

### macOS

Install Xcode command line tools:

```bash
xcode-select --install
```

### Windows

Install [TDM-GCC](https://jmeubank.github.io/tdm-gcc/) or use MSYS2.

## Building

```bash
go build -o build/bin/locorum .
```

## Running

```bash
go run .
```

Or after building:

```bash
./build/bin/locorum
```

The application will:

1. Set up configuration files in `~/.locorum/`
2. Create a global Docker network and containers (nginx proxy, MailHog)
3. Open the desktop window

## Cross-Compilation

### Linux

```bash
GOOS=linux GOARCH=amd64 go build -o build/bin/locorum-linux .
```

### Windows (from Linux, requires mingw)

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc go build -o build/bin/locorum.exe .
```

### macOS App Bundle

Install the `gogio` tool:

```bash
go install gioui.org/cmd/gogio@latest
gogio -target macos -o build/bin/Locorum.app .
```

## Developing with Gio

### Architecture

Gio is an **immediate-mode** GUI framework. Unlike retained-mode frameworks (React, Qt), the entire UI is redrawn every frame by calling layout functions. There is no virtual DOM or widget tree that persists between frames.

Key concepts:

- **`app.Window`** creates the OS window and delivers events
- **`app.FrameEvent`** triggers a redraw; you respond by laying out the entire UI
- **`layout.Context` (gtx)** carries constraints, operations list, and dimensions
- **`op.Ops`** accumulates drawing operations for the frame
- **`widget.*`** types hold persistent state (click state, editor text, scroll position)

### Project Structure

```
main.go                    Entry point: window creation, event loop, startup/shutdown
internal/ui/
    ui.go                  Root UI struct, wires backend callbacks, defines root layout
    state.go               Shared UI state with mutex for thread-safe access
    theme.go               Color palette and font setup
    sidebar.go             Left sidebar: app title, search, site list, "New Site" button
    sitedetail.go          Right panel: site metadata, start/stop, versions, DB info
    newsite.go             Modal form for creating a new site
    modal.go               Generic modal overlay component
    widgets.go             Reusable widgets: buttons, inputs, dropdowns
```

### Event Loop

The main event loop in `main.go` is the core of Gio:

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

### Background Operations

Long-running operations (Docker container management, file dialogs) must run in goroutines to avoid blocking the UI. After completion, update `UIState` and call `state.Invalidate()` to trigger a redraw:

```go
go func() {
    _ = sm.StartSite(siteID)
    state.mu.Lock()
    state.SiteToggling[siteID] = false
    state.mu.Unlock()
    state.Invalidate()  // Wakes the event loop
}()
```

### Backend Communication

The backend (`SiteManager`) communicates with the UI through callback functions:

- `sm.OnSitesUpdated` is called when the site list changes
- `sm.OnSiteUpdated` is called when a single site's state changes

These callbacks update `UIState` and invalidate the window to trigger a redraw.

### Adding New UI Components

1. Create a new file in `internal/ui/`
2. Define a struct with persistent widget state (`widget.Clickable`, `widget.Editor`, etc.)
3. Implement a `Layout(gtx layout.Context, th *material.Theme) layout.Dimensions` method
4. Use `layout.Flex`, `layout.Stack`, `layout.Inset` for positioning
5. Use `material.*` functions for styled widgets
6. Wire it into `ui.go`

### Useful Gio Resources

- [Gio documentation](https://gioui.org/)
- [Gio API reference](https://pkg.go.dev/gioui.org)
- [Gio examples](https://git.sr.ht/~eliasnaur/gio-example)
- [Gio extended widgets](https://pkg.go.dev/gioui.org/x)

## Migrations

You first need to install the migrations tool:

```bash
go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

Then you can create migrations with:

```bash
migrate create -ext sql -dir internal/storage/migrations create_{table_name}_table
```
