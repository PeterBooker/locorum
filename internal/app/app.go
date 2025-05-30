package app

import (
	"context"
	"embed"
	"path"
	"runtime"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/utils"

	"github.com/docker/docker/client"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	cli         *client.Client
	d           *docker.Docker
	homeDir     string
	configFiles embed.FS
}

// New creates a new App application struct
func New(configFiles embed.FS, d *docker.Docker) *App {
	cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	homeDir, _ := utils.GetUserHomeDir()

	return &App{
		cli:         cli,
		d:           d,
		homeDir:     homeDir,
		configFiles: configFiles,
	}
}

// Initialize is called to setup the application.
func (a *App) Initialize() error {
	err := a.SetupFilesystem()
	if err != nil {
		return err
	}

	err = a.d.RemoveContainers("locorum")
	if err != nil {
		return err
	}

	err = a.d.RemoveNetworks("locorum")
	if err != nil {
		return err
	}

	err = a.d.CheckDockerAvailable()
	if err != nil {
		return err
	}

	err = a.d.CreateGlobalNetwork()
	if err != nil {
		return err
	}

	err = a.d.CreateGlobalWebserver(a.homeDir)
	if err != nil {
		return err
	}

	return nil
}

func (a *App) Shutdown() error {
	err := utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites-enabled"))
	if err != nil {
		return err
	}

	err = a.d.RemoveContainers("locorum")
	if err != nil {
		return err
	}

	err = a.d.RemoveNetworks("locorum")
	if err != nil {
		return err
	}

	return nil
}

// IsDockerAvailable checks if Docker is available and running.
func (a *App) IsDockerAvailable() error {
	err := a.d.CheckDockerAvailable()
	if err != nil {
		rt.LogError(a.ctx, "Docker is not running or not accessible: "+err.Error())
		return err
	}

	return nil
}

// SetContext sets the context for the application.
func (a *App) SetContext(ctx context.Context) {
	a.ctx = ctx
}

// GetContext returns the context for the application.
func (a *App) GetContext() context.Context {
	return a.ctx
}

// GetClient returns the Docker client for the application.
func (a *App) GetClient() *client.Client {
	return a.cli
}

// GetHomeDir returns the home directory for the application.
func (a *App) GetHomeDir() string {
	return a.homeDir
}

func (a *App) SetupFilesystem() error {
	err := utils.EnsureDir(path.Join(a.homeDir, ".locorum"))
	if err != nil {
		rt.LogError(a.ctx, "Failed to create directory: "+err.Error())
		return err
	}

	err = utils.EnsureDir(path.Join(a.homeDir, "locorum", "sites"))
	if err != nil {
		rt.LogError(a.ctx, "Failed to create directory: "+err.Error())
		return err
	}

	err = utils.ExtractAssetsToDisk(a.configFiles, ".", path.Join(a.homeDir, ".locorum"))
	if err != nil {
		rt.LogError(a.ctx, "Failed to extract assets: "+err.Error())
		return err
	}

	err = utils.EnsureDir(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites-enabled"))
	if err != nil {
		rt.LogError(a.ctx, "Failed to create directory: "+err.Error())
		return err
	}

	return nil
}

// ApplicationMenu creates the application menu.
func (a *App) ApplicationMenu() *menu.Menu {
	AppMenu := menu.NewMenu()
	if runtime.GOOS == "darwin" {
		// On macOS platform, this must be done right after `NewMenu()`.
		AppMenu.Append(menu.AppMenu())
	}

	FileMenu := AppMenu.AddSubmenu("File")
	FileMenu.AddText("Open", keys.CmdOrCtrl("o"), func(_ *menu.CallbackData) {
		// Action.
	})

	FileMenu.AddSeparator()
	FileMenu.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		rt.Quit(a.ctx)
	})

	if runtime.GOOS == "darwin" {
		// On macOS platform, EditMenu should be appended to enable Cmd+C, Cmd+V, Cmd+Z... shortcuts.
		AppMenu.Append(menu.EditMenu())
	}

	return AppMenu
}
