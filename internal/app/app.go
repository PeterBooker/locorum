package app

import (
	"context"
	"embed"
	"log/slog"
	"os"
	"path"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/utils"

	"github.com/docker/docker/client"
)

// App owns process-wide initialisation: filesystem layout, Docker readiness,
// global containers, and router lifecycle.
type App struct {
	cli         *client.Client
	d           *docker.Docker
	rtr         router.Router
	homeDir     string
	configFiles embed.FS
}

func New(configFiles embed.FS, d *docker.Docker, homeDir string, rtr router.Router) *App {
	cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	return &App{
		cli:         cli,
		d:           d,
		rtr:         rtr,
		homeDir:     homeDir,
		configFiles: configFiles,
	}
}

// Initialize runs the startup sequence: filesystem, cleanup, networks,
// global services, router. Returns the first error encountered so the UI
// can surface it.
func (a *App) Initialize() error {
	ctx := context.Background()

	if err := a.SetupFilesystem(); err != nil {
		return err
	}
	if err := a.d.CheckDockerAvailable(); err != nil {
		return err
	}
	if err := a.cleanupExistingResources(); err != nil {
		return err
	}
	if err := a.d.CreateGlobalNetwork(); err != nil {
		return err
	}
	if err := a.d.CreateGlobalMailserver(); err != nil {
		return err
	}
	if err := a.d.CreateGlobalAdminer(); err != nil {
		return err
	}
	if err := a.rtr.EnsureRunning(ctx); err != nil {
		return err
	}
	if err := a.rtr.UpsertService(ctx, router.ServiceRoute{
		Name:      "mail",
		Hostnames: []string{"mail.localhost"},
		Backend:   "http://locorum-global-mail:8025",
	}); err != nil {
		return err
	}
	if err := a.rtr.UpsertService(ctx, router.ServiceRoute{
		Name:      "adminer",
		Hostnames: []string{"db.localhost"},
		Backend:   "http://locorum-global-adminer:8080",
	}); err != nil {
		return err
	}
	return nil
}

// cleanupExistingResources wipes Locorum-owned containers and networks.
// Resources are matched by the io.locorum.platform label.
func (a *App) cleanupExistingResources() error {
	labels := map[string]string{docker.LabelPlatform: docker.PlatformValue}
	if err := a.d.RemoveByLabel(labels); err != nil {
		return err
	}
	if err := a.d.RemoveNetworksByLabel(labels); err != nil {
		return err
	}
	return nil
}

func (a *App) Shutdown() error {
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "apache", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "router", "dynamic"))

	labels := map[string]string{docker.LabelPlatform: docker.PlatformValue}
	if err := a.d.RemoveByLabel(labels); err != nil {
		return err
	}
	if err := a.d.RemoveNetworksByLabel(labels); err != nil {
		return err
	}
	return nil
}

// IsDockerAvailable checks if Docker is available and running.
func (a *App) IsDockerAvailable() error {
	if err := a.d.CheckDockerAvailable(); err != nil {
		slog.Error("Docker is not running or not accessible: " + err.Error())
		return err
	}
	return nil
}

func (a *App) GetClient() *client.Client { return a.cli }
func (a *App) GetHomeDir() string        { return a.homeDir }

func (a *App) SetupFilesystem() error {
	for _, p := range []string{
		path.Join(a.homeDir, ".locorum"),
		path.Join(a.homeDir, "locorum", "sites"),
	} {
		if err := utils.EnsureDir(p); err != nil {
			slog.Error("Failed to create directory: " + err.Error())
			return err
		}
	}

	// Drop files left by pre-Traefik installs before extraction so stale
	// templates don't reappear on disk.
	for _, p := range []string{
		path.Join(a.homeDir, ".locorum", "config", "nginx", "global.conf"),
		path.Join(a.homeDir, ".locorum", "config", "nginx", "map.tmpl"),
		path.Join(a.homeDir, ".locorum", "config", "nginx", "map.conf"),
	} {
		_ = os.Remove(p)
	}
	for _, d := range []string{
		path.Join(a.homeDir, ".locorum", "config", "certs"),
	} {
		_ = os.RemoveAll(d)
	}

	if err := utils.ExtractAssetsToDisk(a.configFiles, ".", path.Join(a.homeDir, ".locorum")); err != nil {
		slog.Error("Failed to extract assets: " + err.Error())
		return err
	}

	for _, p := range []string{
		path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"),
		path.Join(a.homeDir, ".locorum", "config", "apache", "sites"),
		path.Join(a.homeDir, ".locorum", "router", "dynamic"),
		path.Join(a.homeDir, ".locorum", "certs"),
	} {
		if err := utils.EnsureDir(p); err != nil {
			slog.Error("Failed to create directory: " + err.Error())
			return err
		}
	}

	return nil
}
