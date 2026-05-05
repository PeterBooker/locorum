package app

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/PeterBooker/locorum/internal/assets"
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

	// provider is the cached Docker daemon identification, populated by
	// Initialize once Ping has succeeded. Reads via Provider() are
	// concurrency-safe — pmu in *Docker guards the underlying cache.
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
func (a *App) Initialize(ctx context.Context) error {
	if err := a.SetupFilesystem(); err != nil {
		return err
	}
	if err := a.d.CheckDockerAvailable(ctx); err != nil {
		return err
	}

	// Identify the daemon up front so health checks can read a cached
	// answer without each one re-issuing `docker info`. Failure to
	// identify is non-fatal — we'll fall back to a "name unknown"
	// finding rather than block startup.
	if pi, err := a.d.ProviderInfo(ctx); err == nil {
		slog.Info("docker daemon identified",
			"provider", pi.Name,
			"server_version", pi.ServerVersion,
			"os_type", pi.OSType,
			"arch", pi.Architecture,
			"rootless", pi.Rootless,
		)
	} else {
		slog.Warn("docker: ProviderInfo failed; health checks may be degraded", "err", err.Error())
	}

	// Wipe leftover Locorum-owned resources from prior sessions before
	// creating new ones. ReconcileNetworks is the explicit "remove orphans"
	// pass that prevents same-name network create from failing after a
	// daemon crash.
	if err := a.cleanupExistingResources(ctx); err != nil {
		return err
	}
	if err := a.d.ReconcileNetworks(ctx); err != nil {
		slog.Warn("reconcile networks: " + err.Error())
	}

	if _, err := a.d.EnsureNetwork(ctx, docker.GlobalNetworkSpec()); err != nil {
		return fmt.Errorf("create global network: %w", err)
	}

	if err := a.ensureGlobalContainer(ctx, docker.MailSpec()); err != nil {
		return fmt.Errorf("global mail: %w", err)
	}
	if err := a.ensureGlobalContainer(ctx, docker.AdminerSpec()); err != nil {
		return fmt.Errorf("global adminer: %w", err)
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

// ensureGlobalContainer pulls the image, ensures the container, and starts
// it. EnsureContainer is idempotent — if the container is already at the
// right config hash, this is a fast no-op.
func (a *App) ensureGlobalContainer(ctx context.Context, spec docker.ContainerSpec) error {
	if err := a.d.PullImage(ctx, spec.Image, nil); err != nil {
		return fmt.Errorf("pull %s: %w", spec.Image, err)
	}
	if _, err := a.d.EnsureContainer(ctx, spec); err != nil {
		return fmt.Errorf("ensure %s: %w", spec.Name, err)
	}
	if err := a.d.StartContainer(ctx, spec.Name); err != nil {
		return fmt.Errorf("start %s: %w", spec.Name, err)
	}
	return nil
}

// cleanupExistingResources wipes Locorum-owned containers and networks.
// Resources are matched by the io.locorum.platform label.
func (a *App) cleanupExistingResources(ctx context.Context) error {
	labels := map[string]string{docker.LabelPlatform: docker.PlatformValue}
	if err := a.d.RemoveContainersByLabel(ctx, labels); err != nil {
		return err
	}
	if err := a.d.RemoveNetworksByLabel(ctx, labels); err != nil {
		return err
	}
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "apache", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "router", "dynamic"))

	labels := map[string]string{docker.LabelPlatform: docker.PlatformValue}
	if err := a.d.RemoveContainersByLabel(ctx, labels); err != nil {
		return err
	}
	if err := a.d.RemoveNetworksByLabel(ctx, labels); err != nil {
		return err
	}
	return nil
}

// IsDockerAvailable checks if Docker is available and running.
func (a *App) IsDockerAvailable(ctx context.Context) error {
	if err := a.d.CheckDockerAvailable(ctx); err != nil {
		slog.Error("Docker is not running or not accessible: " + err.Error())
		return err
	}
	return nil
}

func (a *App) GetClient() *client.Client { return a.cli }
func (a *App) GetHomeDir() string        { return a.homeDir }

// Provider returns the cached Docker daemon identification. If Initialize
// has not yet run (or the ProviderInfo call failed), the returned
// ProviderInfo carries the engine-side default values plus an empty Name
// — callers should treat that as "unknown" and not branch on specifics.
func (a *App) Provider(ctx context.Context) docker.ProviderInfo {
	pi, err := a.d.ProviderInfo(ctx)
	if err != nil {
		// The cache is set in Initialize; an error here means
		// Initialize hasn't run successfully yet. Return zero so
		// callers don't need to check.
		return docker.ProviderInfo{}
	}
	return pi
}

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
		// pre-multi-engine MySQL config; superseded by
		// config/dbengine/{mysql,mariadb}/locorum.cnf.
		path.Join(a.homeDir, ".locorum", "config", "db", "db.cnf"),
	} {
		_ = os.Remove(p)
	}
	for _, d := range []string{
		path.Join(a.homeDir, ".locorum", "config", "certs"),
		// Empty out the legacy config/db dir on first run with the new layout.
		path.Join(a.homeDir, ".locorum", "config", "db"),
	} {
		_ = os.RemoveAll(d)
	}

	// Reconcile bundled config assets against disk. The Reconcile
	// pass walks the embedded FS, hashes each file, and uses the
	// previous-run hash table to distinguish "bundled default
	// changed" from "user edited" so we never silently overwrite a
	// hand-edited file. Files needing manual merge are logged at
	// warn level; the GUI's System Health panel will surface them
	// once that lands (LEARNINGS §6.5).
	statePath := assets.DefaultStatePath(a.homeDir)
	prevState, err := assets.LoadState(statePath)
	if err != nil {
		slog.Warn("assets: load state: " + err.Error())
	}
	report, nextState, err := assets.Reconcile(a.configFiles, "config", path.Join(a.homeDir, ".locorum", "config"), prevState, nil)
	if err != nil {
		// Walk-level error means the embed itself is unreadable
		// — fatal, refuse to start with half-extracted config.
		slog.Error("Failed to reconcile assets: " + err.Error())
		return err
	}
	for _, fr := range report.MergeNeeded() {
		slog.Warn("config: bundled default changed; user edit preserved",
			"path", fr.Path,
			"hint", "compare your file against the bundled default and merge by hand")
	}
	if err := assets.SaveState(statePath, nextState); err != nil {
		// Non-fatal: missing state just means next run sees
		// every file as "matches prev".
		slog.Warn("assets: save state: " + err.Error())
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
