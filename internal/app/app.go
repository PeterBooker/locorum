package app

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/PeterBooker/locorum/internal/assets"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/utils"
	"github.com/PeterBooker/locorum/internal/wpcli"

	"github.com/docker/docker/client"
)

// preLaunchProbeTimeout caps each TCP probe of host ports 80/443 issued
// before we attempt the router create. A listener-present case answers
// in microseconds; the timeout exists only to avoid an indefinite hang
// against a misbehaving local proxy that accepts the SYN but never
// replies.
const preLaunchProbeTimeout = 200 * time.Millisecond

// preLaunchProbePorts is the canonical pair of host-bound ports the
// router needs. Centralised here so a future port change (extremely
// unlikely — see F14 design note) is one constant edit, not a sweep.
var preLaunchProbePorts = []int{80, 443}

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

	// wp-cli is bind-mounted into every PHP container. Download +
	// SHA-512 verify happens at most once per pinned-version bump
	// (see internal/wpcli). Failure here is non-fatal so the user
	// can still inspect existing sites — but lifecycle methods that
	// invoke wp-cli will surface the missing-file error clearly.
	if pharPath, err := wpcli.EnsurePhar(a.homeDir); err != nil {
		slog.Warn("wp-cli phar unavailable; site lifecycle steps that invoke wp will fail",
			"err", err.Error())
	} else {
		slog.Info("wp-cli phar ready", "path", pharPath)
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
	if err := a.WipeLabelled(ctx); err != nil {
		return err
	}
	if err := a.d.ReconcileNetworks(ctx); err != nil {
		slog.Warn("reconcile networks: " + err.Error())
	}

	return a.BringUpGlobals(ctx)
}

// WipeLabelled removes every Locorum-owned container and network. Used
// at startup (to clear state from a crashed previous session) and from
// SiteManager.ResetInfrastructure (to give the user a one-click recovery
// path). Volumes are deliberately preserved — site DB data persists
// across both flows.
//
// Idempotent: safe to call when nothing is running.
func (a *App) WipeLabelled(ctx context.Context) error {
	labels := map[string]string{docker.LabelPlatform: docker.PlatformValue}
	if err := a.d.RemoveContainersByLabel(ctx, labels); err != nil {
		return fmt.Errorf("remove labelled containers: %w", err)
	}
	if err := a.d.RemoveNetworksByLabel(ctx, labels); err != nil {
		return fmt.Errorf("remove labelled networks: %w", err)
	}
	return nil
}

// BringUpGlobals creates the global network, brings up mail + adminer,
// and (re)starts the router with the canonical service routes pre-
// registered. Used at the end of Initialize and from
// SiteManager.ResetInfrastructure.
//
// Each step propagates the underlying error verbatim. The router itself
// returns sentinels (router.ErrPortInUse) the UI can branch on.
func (a *App) BringUpGlobals(ctx context.Context) error {
	// Pre-launch probe: if a foreign process already owns 80 or 443,
	// short-circuit with router.ErrPortInUse rather than letting the
	// Docker SDK surface a deeper, less-actionable "port is already
	// allocated" error after pulling the router image. F14 design
	// note: we deliberately do NOT fall back to ephemeral ports —
	// URL stability of https://<slug>.localhost is load-bearing for
	// browser cookie scope, mkcert SANs, hard-coded WP siteurl, and
	// the user's documentation. The user must free the port instead.
	//
	// At this moment in the lifecycle the router container is *not*
	// running (BringUpGlobals creates it below), so any existing
	// listener on the host port cannot be ours.
	if held := probeHeldRouterPort(ctx); held > 0 {
		err := fmt.Errorf("%w: port %d is held by another process", router.ErrPortInUse, held)
		slog.Warn("pre-launch port probe: foreign listener detected; refusing to create router",
			"port", held)
		return err
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

// probeHeldRouterPort dials each port in [preLaunchProbePorts] and
// returns the first one that already has a listener. Returns 0 when all
// ports look free. Used by [BringUpGlobals] to short-circuit before
// pulling the router image when a foreign process holds 80 or 443.
//
// Pure read-only TCP probe — never binds. A successful dial means a
// listener accepted the SYN; we close immediately. The probe is bounded
// by [preLaunchProbeTimeout] per port, which is several orders of
// magnitude faster than the BringUpGlobals image pull, so the cost is
// negligible.
func probeHeldRouterPort(ctx context.Context) int {
	for _, port := range preLaunchProbePorts {
		if isPortHeld(ctx, port) {
			return port
		}
	}
	return 0
}

// isPortHeld is the per-port half of [probeHeldRouterPort]. Factored
// out so a future caller (status bar, CLI sub-command) can reuse the
// dial without picking up the router-port iteration.
func isPortHeld(ctx context.Context, port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	d := net.Dialer{Timeout: preLaunchProbeTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (a *App) Shutdown(ctx context.Context) error {
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "nginx", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "config", "apache", "sites"))
	_ = utils.DeleteDirFiles(path.Join(a.homeDir, ".locorum", "router", "dynamic"))
	return a.WipeLabelled(ctx)
}

// ResetInfrastructure wipes every Locorum-owned container and network,
// then re-runs the global startup sequence (network + mail + adminer +
// router). Volumes are preserved — site DB data is untouched. Per-site
// containers are removed and the caller is expected to reconcile the
// "started" state in storage.
//
// User-facing flow: Settings → Diagnostics → "Reset Locorum
// Infrastructure" with a confirmation modal. Idempotent and safe to
// retry; failures bubble up verbatim so the UI banner shows what went
// wrong.
func (a *App) ResetInfrastructure(ctx context.Context) error {
	if err := a.WipeLabelled(ctx); err != nil {
		return fmt.Errorf("reset: wipe: %w", err)
	}
	if err := a.BringUpGlobals(ctx); err != nil {
		return fmt.Errorf("reset: bring-up: %w", err)
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
