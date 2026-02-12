package main

import (
	"context"
	"embed"
	"log"
	"log/slog"
	"os"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	application "github.com/PeterBooker/locorum/internal/app"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/ui"
)

//go:embed all:config
var config embed.FS

func main() {
	// On WSL2, force the X11 backend. WSLg's Wayland compositor does not
	// fully support window-management actions (minimize/maximize).
	// Unsetting WAYLAND_DISPLAY makes Gio fall back to X11 via XWayland,
	// where these actions work correctly.
	if _, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok {
		os.Unsetenv("WAYLAND_DISPLAY")
	}

	d := docker.New()

	// Create an instance of the app structure.
	a := application.New(config, d)

	st, err := storage.NewSQLiteStorage(context.Background())
	if err != nil {
		log.Fatalln("Error:", err)
	}
	defer st.Close()

	sm := sites.NewSiteManager(st, a.GetClient(), d, config, a.GetHomeDir())

	// Initialize Docker infrastructure in background.
	go func() {
		d.SetContext(context.Background())
		d.SetClient(a.GetClient())

		if err := a.Initialize(); err != nil {
			slog.Error("Error initializing: " + err.Error())
		}

		// All containers were cleaned up during Initialize, so mark all
		// sites as stopped to match actual Docker state.
		if err := sm.ReconcileState(); err != nil {
			slog.Error("Error reconciling site state: " + err.Error())
		}

		if err := sm.RegenerateGlobalNginxMap(false); err != nil {
			slog.Error("Error regenerating nginx map: " + err.Error())
		}
	}()

	// Create UI.
	userInterface := ui.New(sm)

	// Load initial sites.
	go func() {
		loadedSites, err := sm.GetSites()
		if err == nil {
			userInterface.State.Sites = loadedSites
			userInterface.State.Invalidate()
		}
	}()

	// Create and run window.
	go func() {
		w := &app.Window{}
		w.Option(
			app.Title("Locorum"),
			app.Size(unit.Dp(1024), unit.Dp(768)),
		)

		userInterface.State.Window = w

		if err := eventLoop(w, userInterface); err != nil {
			slog.Error("Window error: " + err.Error())
		}

		// Window closed — shut down.
		_ = a.Shutdown()
		st.Close()
		os.Exit(0)
	}()

	app.Main()
}

func eventLoop(w *app.Window, u *ui.UI) error {
	var ops op.Ops

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			u.Layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}
