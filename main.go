package main

import (
	"context"
	"embed"
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
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/router/traefik"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
	"github.com/PeterBooker/locorum/internal/ui"
	"github.com/PeterBooker/locorum/internal/utils"
	"github.com/PeterBooker/locorum/internal/version"
)

//go:embed all:config
var config embed.FS

func main() {
	slog.Info("starting Locorum", "version", version.Version, "commit", version.Commit, "date", version.Date)

	// On WSL2, force the X11 backend. WSLg's Wayland compositor does not
	// fully support window-management actions (minimize/maximize).
	if _, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok {
		os.Unsetenv("WAYLAND_DISPLAY")
		os.Setenv("GSETTINGS_BACKEND", "memory")
		if _, ok := os.LookupEnv("DBUS_SESSION_BUS_ADDRESS"); !ok {
			os.Setenv("DBUS_SESSION_BUS_ADDRESS", "disabled:")
		}
	}

	homeDir, err := utils.GetUserHomeDir()
	if err != nil {
		log.Fatalln("Error getting home dir:", err)
	}

	d := docker.New()

	mkcert := tlspkg.NewMkcert(
		filepath.Join(homeDir, ".locorum", "certs"),
		filepath.Join(homeDir, ".locorum", "bin"),
	)

	rtr, err := traefik.New(traefik.Config{
		HomeDir:    homeDir,
		AppVersion: version.Version,
		LogLevel:   os.Getenv("LOCORUM_LOG_LEVEL"),
	}, d, mkcert, config)
	if err != nil {
		log.Fatalln("Error initializing router:", err)
	}

	a := application.New(config, d, homeDir, rtr)

	st, err := storage.NewSQLiteStorage(context.Background())
	if err != nil {
		log.Fatalln("Error:", err)
	}
	defer st.Close()

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

	sm := sites.NewSiteManager(st, a.GetClient(), d, rtr, hookRunner, config, homeDir)

	userInterface := ui.New(sm)

	initFunc := func() {
		d.SetClient(a.GetClient())

		ctx := context.Background()
		if err := a.Initialize(ctx); err != nil {
			slog.Error("Error initializing: " + err.Error())
			userInterface.State.SetInitError(err.Error())
			return
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

		_ = a.Shutdown(context.Background())
		st.Close()
		os.Exit(0)
	}()

	app.Main()
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
