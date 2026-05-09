package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/op"
	"gioui.org/unit"

	application "github.com/PeterBooker/locorum/internal/app"
	"github.com/PeterBooker/locorum/internal/applog"
	settings "github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/health"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/platform"
	"github.com/PeterBooker/locorum/internal/router/traefik"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/telemetry"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
	"github.com/PeterBooker/locorum/internal/ui"
	"github.com/PeterBooker/locorum/internal/updatecheck"
	"github.com/PeterBooker/locorum/internal/utils"
	"github.com/PeterBooker/locorum/internal/version"
)

//go:embed all:config
var config embed.FS

func main() {
	// CLI clients are dispatched before any Gio / Docker setup. The CLI
	// process is a thin IPC wrapper — it must not duplicate
	// app.Initialize, must not bring up the Gio window, and must exit
	// quickly enough for shell scripts that loop over `locorum site
	// list`. See internal/cli/.
	if code, handled := dispatchCLI(); handled {
		os.Exit(code)
	}

	// Hard-fail before Gio loads if we're an amd64 binary translated by
	// Rosetta on Apple Silicon. See preflight_darwin.go.
	runPreflight()

	// daemonMode is set when the binary was started as `locorum daemon`
	// or auto-spawned by a CLI client. The init flow is identical to
	// GUI mode minus the window event loop.
	daemonMode := isDaemonMode()

	slog.Info("starting Locorum", "version", version.Version, "commit", version.Commit, "date", version.Date, "daemon", daemonMode)

	// Identify the host once. platform.Get() is then safe to call from
	// any goroutine for the rest of the process lifetime.
	plat := platform.Init(context.Background())

	// On WSL2, force the X11 backend. WSLg's Wayland compositor does not
	// fully support window-management actions (minimize/maximize). The
	// canonical signal lives in platform.Get().WSL.Active — we still
	// branch on it before Gio touches the display. Skip in daemon mode:
	// no Gio, no need to disturb display env.
	if plat.WSL.Active && !daemonMode {
		_ = os.Unsetenv("WAYLAND_DISPLAY")
		_ = os.Setenv("GSETTINGS_BACKEND", "memory")
		if _, ok := os.LookupEnv("DBUS_SESSION_BUS_ADDRESS"); !ok {
			_ = os.Setenv("DBUS_SESSION_BUS_ADDRESS", "disabled:")
		}
	}

	homeDir, err := utils.GetUserHomeDir()
	if err != nil {
		log.Fatalln("Error getting home dir:", err)
	}

	// Install the structured-log handler before any other startup work so
	// every record (including the "starting Locorum" line above is missed,
	// which is fine — that one is a single banner, not a debug source).
	logCloser, logErr := applog.Init(filepath.Join(homeDir, ".locorum", "logs"))
	if logErr != nil {
		// Stderr-only handler is already installed by Init's fallback;
		// surface the file-open failure once and continue.
		slog.Warn("applog: file logging disabled", "err", logErr.Error())
	}
	defer func() { _ = logCloser.Close() }()

	d := docker.New()

	st, err := storage.NewSQLiteStorage(context.Background())
	if err != nil {
		_ = logCloser.Close()
		log.Fatalln("Error:", err) //nolint:gocritic // exitAfterDefer: logCloser.Close() called explicitly above; no other cleanup needed before fatal exit
	}
	defer func() { _ = st.Close() }()

	cfg, err := settings.New(st)
	if err != nil {
		log.Fatalln("Error loading settings:", err)
	}

	// Apply the persisted Debug Mode toggle to the slog handler. Cheap;
	// safe to call before or after applog.Init.
	applog.SetDebug(cfg.DebugLogging())

	// Telemetry: noop sink today (UX.md §5.1, Phase A). The real
	// transport lands once the privacy doc + vendor decision do — Phase
	// B swaps SetDefault here for a PostHog (or self-rolled) sink, all
	// without touching the call sites.
	telemetry.SetDefault(telemetry.Noop{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = telemetry.Flush(ctx)
	}()

	mkcert := tlspkg.NewMkcert(
		filepath.Join(homeDir, ".locorum", "certs"),
		filepath.Join(homeDir, ".locorum", "bin"),
	)

	rtr, err := traefik.New(traefik.Config{
		HomeDir:    homeDir,
		AppVersion: version.Version,
		LogLevel:   os.Getenv("LOCORUM_LOG_LEVEL"),
		HTTPPort:   cfg.RouterHTTPPort(),
		HTTPSPort:  cfg.RouterHTTPSPort(),
	}, d, mkcert, config)
	if err != nil {
		log.Fatalln("Error initializing router:", err)
	}

	a := application.New(config, d, homeDir, rtr)

	hookLogsDir := filepath.Join(homeDir, ".locorum", "hooks", "runs")
	if err := utils.EnsureDir(hookLogsDir); err != nil {
		slog.Warn("hooks: could not create logs dir", "err", err.Error())
	}
	// Best-effort sweep of stale run logs at startup. Errors are
	// informational; the app comes up either way.
	if err := hooks.SweepLogs(hookLogsDir, hooks.DefaultLogMaxAge, hooks.DefaultMaxLogsPerSite); err != nil {
		slog.Warn("hooks: log sweep failed", "err", err.Error())
	}

	hookRunner, err := hooks.NewRunner(hooks.Config{
		Lister:      st,
		Container:   hooks.DockerContainerExecer{D: d},
		Host:        hooks.UtilsHostExecer{},
		Settings:    st,
		LogsBaseDir: hookLogsDir,
	})
	if err != nil {
		log.Fatalln("Error initializing hooks runner:", err)
	}

	sm := sites.NewSiteManager(st, a.GetClient(), d, rtr, hookRunner, config, homeDir, cfg)

	if daemonMode {
		runDaemonMode(homeDir, sm, a, d)
		_ = st.Close()
		return
	}

	userInterface := ui.New(sm)

	// Wire the Diagnostics → Reset Infrastructure card. The closure
	// captures `a` so the UI doesn't import internal/app — invariant
	// 1 (UI's strict diet) stays intact.
	if dp := userInterface.Settings.DiagnosticsPanel(); dp != nil {
		dp.SetResetInfraCard(ui.NewResetInfraCard(
			userInterface.State, sm, userInterface.Toasts,
			func(ctx context.Context) error { return a.ResetInfrastructure(ctx) },
		))
		dp.SetUpdateBanner(ui.NewUpdateBannerCard(userInterface.State, cfg))
	}

	// Hydrate the update-banner state from settings before the first
	// frame so the dot doesn't flicker on launch.
	userInterface.State.SetUpdateAvailable(cfg.UpdateLastAvailable(), "", "")
	userInterface.State.SetUpdateDismissed(cfg.UpdateDismissedVersion())

	// Hydrate the toast-suppression set from the persisted JSON so we
	// don't re-toast findings the user already saw on a previous run.
	if raw := cfg.HealthLastSeen(); raw != "" {
		var keys []string
		if err := json.Unmarshal([]byte(raw), &keys); err == nil {
			userInterface.State.HealthHydrateSeen(keys)
		}
	}

	// System Health runner. Wire it now so the UI can subscribe — the
	// runner's own loop won't tick until Start, which we defer until
	// Initialize completes.
	mkcertInstaller := func(ctx context.Context) error {
		return mkcert.InstallCA(ctx)
	}
	runner := newHealthRunner(plat, d, mkcert, mkcertInstaller, sm, cfg, homeDir)
	defer func() { _ = runner.Close() }()

	if cfg.HealthEnabled() {
		// Subscribe before Start so the very first publication propagates
		// to the UI. The callback runs on the runner goroutine; it
		// pushes the snapshot into UIState (atomic) and triggers an
		// invalidate.
		runner.Subscribe(func(snap health.Snapshot) {
			userInterface.State.SetHealthSnapshot(snap)
			// Toasts: fire one per never-seen-before warning or blocker.
			// Info-level findings appear in the panel only — toast spam
			// from the steady-state "your provider is Docker Desktop"
			// row would burn out the user's attention.
			for _, f := range snap.Findings {
				if f.Severity == health.SeverityInfo {
					continue
				}
				key := f.ID + "|" + f.DedupKey
				if !userInterface.State.HealthShouldToast(key) {
					continue
				}
				userInterface.Toasts.Add(f.Title, toastVariantFor(f.Severity))
			}
			// Push the latest disk-free reading into the top-bar
			// segment. Cheap; runs on every publish.
			userInterface.State.SetDiskFreeBytes(runner.DiskFreeBytes())
		})
		// Wire the System Health panel into Settings and the blocker
		// modal into the root UI.
		submit := func(id string, a health.Action) error {
			return runner.SubmitAction(context.Background(), id, a, nil)
		}
		runNow := func() { runner.RunNow(context.Background()) }
		userInterface.Settings.SetHealthPanel(ui.NewHealthPanel(userInterface.State, submit, runNow))
		userInterface.HealthBlocker = ui.NewHealthBlockerModal(userInterface.State, runNow, func() {
			os.Exit(0)
		})
	}

	// daemonHandle holds the lock + IPC server we acquire after
	// a.Initialize succeeds. They survive for the lifetime of the GUI
	// process; the eventLoop teardown releases them.
	var daemonLock *daemon.Lock
	var daemonServer *daemon.Server
	defer func() {
		if daemonServer != nil {
			daemonServer.Shutdown(2 * time.Second)
		}
		if daemonLock != nil {
			_ = daemonLock.Release()
		}
	}()

	initFunc := func() {
		d.SetClient(a.GetClient())

		ctx := context.Background()
		if err := a.Initialize(ctx); err != nil {
			slog.Error("Error initializing: " + err.Error())
			userInterface.State.SetInitError(err.Error())
			return
		}

		// Bind the IPC server now that Docker is up. Failure here
		// (e.g. another GUI is already running) does NOT block the
		// rest of startup — the user still gets a usable window, just
		// without CLI/MCP wiring. The daemon owner can be inspected
		// via the lock-error log.
		if daemonLock == nil {
			lock, srv, err := startDaemonServices(context.Background(), homeDir, sm)
			if err != nil {
				reportLockError(err)
			} else {
				daemonLock, daemonServer = lock, srv
			}
		}

		if err := sm.ReconcileState(); err != nil {
			slog.Error("Error reconciling site state: " + err.Error())
		}

		// Best-effort snapshot retention sweep. Logs counts; failures
		// don't block startup.
		if _, err := sm.SweepSnapshots(sm.LoadRetentionPolicy()); err != nil {
			slog.Warn("snapshot: retention sweep failed", "err", err.Error())
		}

		// Defensive activity-feed sweep. AppendActivity already enforces
		// retention on every insert; this guards against drift if the cap
		// is reduced or rows arrived from a process running an older
		// schema.
		if err := sm.SweepActivity(); err != nil {
			slog.Warn("activity: retention sweep failed", "err", err.Error())
		}

		refreshTLSNotice(mkcert, userInterface.State)

		// Start the runner once the docker client is ready. Pre-init
		// startup would have most checks fail loudly (Ping, ProviderInfo)
		// and pollute the UI on first frame.
		if cfg.HealthEnabled() {
			runner.Start(context.Background())
			// First-fire window: the runner's initial snapshot is
			// suppressed for toasts. After it lands, normal toast
			// behaviour resumes.
			go func() {
				time.Sleep(2 * time.Second)
				userInterface.State.HealthClearFirstFire()
				persistHealthSeen(cfg, userInterface.State)
			}()
		}

		userInterface.State.SetInitDone()
	}

	userInterface.State.SetRetryInit(func() {
		userInterface.State.ClearInitError()
		initFunc()
	})

	go initFunc()

	go func() {
		loadedSites, err := sm.GetSites()
		if err == nil {
			for i := range loadedSites {
				loadedSites[i].Started = false
			}
			userInterface.State.SetSites(loadedSites)
		}
	}()

	go pollServicesHealth(d, userInterface.State)

	if cfg.UpdateCheckEnabled() {
		go runUpdateCheck(homeDir, cfg, userInterface.State)
	}

	go func() {
		w := &app.Window{}
		w.Option(
			app.Title("Locorum"),
			app.Size(unit.Dp(1024), unit.Dp(768)),
		)
		userInterface.State.SetWindow(w)

		if err := eventLoop(w, userInterface); err != nil {
			slog.Error("Window error: " + err.Error())
		}

		// Tear down daemon services BEFORE app.Shutdown so a CLI
		// client mid-call gets a clean "connection closed" rather
		// than racing the Docker label-wipe.
		if daemonServer != nil {
			daemonServer.Shutdown(2 * time.Second)
			daemonServer = nil
		}
		if daemonLock != nil {
			_ = daemonLock.Release()
			daemonLock = nil
		}

		_ = a.Shutdown(context.Background())
		// Persist the seen-keys set so the next process doesn't re-toast
		// findings the user already dismissed. Best effort.
		persistHealthSeen(cfg, userInterface.State)
		_ = st.Close()
		os.Exit(0)
	}()

	app.Main()
}

// newHealthRunner builds the production runner with the bundled checks.
// Cadence and thresholds come from the user's config; missing keys fall
// back to documented defaults.
func newHealthRunner(plat *platform.Info, d *docker.Docker, mkcert *tlspkg.Mkcert, mkInstaller func(context.Context) error, sm *sites.SiteManager, cfg *settings.Config, homeDir string) *health.Runner {
	cadence := time.Duration(cfg.HealthCadenceMinutes()) * time.Minute
	if cadence <= 0 {
		cadence = 5 * time.Minute
	}
	const gb = int64(1024 * 1024 * 1024)
	checks := health.Bundled(health.BundledOpts{
		Platform:            plat,
		Engine:              d,
		Mkcert:              mkcert,
		MkcertInstaller:     mkInstaller,
		Sites:               sm,
		HostStatfsPath:      homeDir,
		RouterContainerName: traefik.ContainerName,
		DiskWarnBytes:       int64(cfg.HealthDiskWarnGB()) * gb,
		DiskBlockerBytes:    int64(cfg.HealthDiskBlockerGB()) * gb,
	})
	return health.NewRunner(health.Options{
		MinCadence: cadence,
		Logger:     slog.With("subsys", "health"),
	}, checks...)
}

// toastVariantFor maps a finding severity onto the existing notification
// type. The notifications system has Error / Success / Info; Warn maps to
// Error so warnings catch the user's eye but don't stick as long as a
// blocker (the blocker modal handles persistence). Info-level findings
// don't toast at all (filtered above).
func toastVariantFor(s health.Severity) ui.NotificationType {
	switch s {
	case health.SeverityBlocker, health.SeverityWarn:
		return ui.NotificationError
	}
	return ui.NotificationInfo
}

// persistHealthSeen writes the current toast-suppression set to the
// persistent settings table. Idempotent; failures are logged at warn.
func persistHealthSeen(cfg *settings.Config, state *ui.UIState) {
	if cfg == nil || state == nil {
		return
	}
	keys := state.HealthSeenKeys()
	body, err := json.Marshal(keys)
	if err != nil {
		slog.Warn("health: marshal last_seen", "err", err.Error())
		return
	}
	if err := cfg.SetHealthLastSeen(string(body)); err != nil {
		slog.Warn("health: persist last_seen", "err", err.Error())
	}
}

// refreshTLSNotice reads the current mkcert status and updates the banner.
// When the local CA isn't trusted, the banner gets an action button that
// downloads mkcert (if needed) and runs `mkcert -install` in a goroutine,
// then re-reads the status. Re-entrant: callers may invoke after every
// successful or failed install attempt.
func refreshTLSNotice(mkcert *tlspkg.Mkcert, state *ui.UIState) {
	status, err := mkcert.Available(context.Background())
	if err != nil || status.CATrusted {
		state.SetNotice("")
		return
	}
	state.SetNoticeWithAction(status.Message, "Set up trusted HTTPS", func() {
		go func() {
			defer state.SetNoticeBusy(false)
			if err := mkcert.InstallCA(context.Background()); err != nil {
				slog.Warn("mkcert install failed", "err", err.Error())
				state.ShowError("Could not set up trusted HTTPS: " + err.Error())
			}
			refreshTLSNotice(mkcert, state)
		}()
	})
}

// pollServicesHealth refreshes the rolled-up health of Locorum's global
// services (router, mail, adminer) on a 5-second cadence and pushes the
// result into UIState for the top status bar. Runs forever; stopped by
// process exit.
func pollServicesHealth(d *docker.Docker, state *ui.UIState) {
	requiredRoles := []string{docker.RoleRouter, docker.RoleMail, docker.RoleAdminer}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		state.SetServicesHealth(currentServicesHealth(d, requiredRoles))
		<-tick.C
	}
}

func currentServicesHealth(d *docker.Docker, requiredRoles []string) ui.ServicesHealth {
	if !d.HasClient() {
		return ui.ServicesHealth{Status: ui.ServicesHealthUnknown}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	containers, err := d.ContainersByLabel(ctx, map[string]string{docker.LabelPlatform: docker.PlatformValue})
	if err != nil {
		return ui.ServicesHealth{Status: ui.ServicesHealthDown, Detail: err.Error()}
	}

	roleStates := make(map[string]string, len(requiredRoles))
	for _, c := range containers {
		role := c.Labels[docker.LabelRole]
		if role == "" {
			continue
		}
		// Prefer the running entry if multiple labelled containers exist.
		if existing, ok := roleStates[role]; !ok || existing != "running" {
			roleStates[role] = strings.ToLower(c.State)
		}
	}

	missing, notRunning := 0, 0
	for _, role := range requiredRoles {
		state, ok := roleStates[role]
		switch {
		case !ok:
			missing++
		case state != "running":
			notRunning++
		}
	}

	switch {
	case missing == len(requiredRoles):
		return ui.ServicesHealth{Status: ui.ServicesHealthDown}
	case missing > 0 || notRunning > 0:
		return ui.ServicesHealth{Status: ui.ServicesHealthDegraded}
	default:
		return ui.ServicesHealth{Status: ui.ServicesHealthHealthy}
	}
}

// runUpdateCheck queries GitHub Releases (gated by the throttle file)
// and pushes the result into UIState. Failures are logged at warn — the
// app keeps working without the banner.
func runUpdateCheck(homeDir string, cfg *settings.Config, state *ui.UIState) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	statePath := filepath.Join(homeDir, ".locorum", "state", "last_update_check.json")
	res, err := updatecheck.Check(ctx, version.Version, updatecheck.Options{
		Channel:   updatecheck.Channel(cfg.UpdateCheckChannel()),
		StatePath: statePath,
	})
	if err != nil {
		if errors.Is(err, updatecheck.ErrThrottled) {
			return
		}
		slog.Warn("update check failed", "err", err.Error())
		return
	}
	if res.Latest == "" {
		return
	}
	state.SetUpdateAvailable(res.Latest, res.URL, res.Notes)
	if err := cfg.SetUpdateLastAvailable(res.Latest); err != nil {
		slog.Warn("update check: persist last_available", "err", err.Error())
	}
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
