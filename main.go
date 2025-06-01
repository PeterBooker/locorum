package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/PeterBooker/locorum/internal/app"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed all:config
var config embed.FS

func main() {
	d := docker.New()

	// Create an instance of the app structure.
	app := app.New(config, d)

	t := types.NewType()

	st, err := storage.NewSQLiteStorage(app.GetContext())
	if err != nil {
		println("Error:", err.Error())
		return
	}

	defer st.Close()

	sm := sites.NewSiteManager(st, app.GetClient(), d, config, app.GetHomeDir())

	// Create application.
	err = wails.Run(&options.App{
		Title:     "Locorum",
		Width:     1024,
		Height:    768,
		Frameless: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Menu:             app.ApplicationMenu(),
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup: func(ctx context.Context) {
			app.SetContext(ctx)
			d.SetContext(ctx)
			sm.SetContext(ctx)
			d.SetClient(app.GetClient())
			err := sm.RegenerateGlobalNginxMap(false)
			if err != nil {
				println("Error:", err.Error())
				return
			}
			err = app.Initialize()
			if err != nil {
				println("Error:", err.Error())
				return
			}
		},
		OnShutdown: func(ctx context.Context) {
			app.Shutdown()
			st.Close()
		},
		Bind: []interface{}{
			t,
			sm,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
