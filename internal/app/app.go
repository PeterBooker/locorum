package app

import (
	"embed"
	"log"
	"log/slog"
	"path"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/utils"

	"github.com/docker/docker/client"
)

// App struct
type App struct {
	cli         *client.Client
	d           *docker.Docker
	homeDir     string
	configFiles embed.FS
}

// New creates a new App application struct
func New(configFiles embed.FS, d *docker.Docker) *App {
	cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	homeDir, err := utils.GetUserHomeDir()
	if err != nil {
		log.Fatalln("error getting home dir:", err)
	}

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

	err = a.d.CreateGlobalMailserver()
	if err != nil {
		return err
	}

	err = a.d.CreateGlobalAdminer()
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
	err := utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"))
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
		slog.Error("Docker is not running or not accessible: " + err.Error())
		return err
	}

	return nil
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
		slog.Error("Failed to create directory: " + err.Error())
		return err
	}

	err = utils.EnsureDir(path.Join(a.homeDir, "locorum", "sites"))
	if err != nil {
		slog.Error("Failed to create directory: " + err.Error())
		return err
	}

	err = utils.ExtractAssetsToDisk(a.configFiles, ".", path.Join(a.homeDir, ".locorum"))
	if err != nil {
		slog.Error("Failed to extract assets: " + err.Error())
		return err
	}

	err = utils.EnsureDir(path.Join(a.homeDir, ".locorum", "config", "nginx"))
	if err != nil {
		slog.Error("Failed to create directory: " + err.Error())
		return err
	}

	err = utils.EnsureDir(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"))
	if err != nil {
		slog.Error("Failed to create directory: " + err.Error())
		return err
	}

	return nil
}
