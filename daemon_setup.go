package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	application "github.com/PeterBooker/locorum/internal/app"
	"github.com/PeterBooker/locorum/internal/cli"
	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/version"
)

// dispatchCLI is called at the top of main(). When the args declare a
// CLI subcommand (other than `daemon`, which still runs the GUI-less
// daemon main), we run the CLI dispatcher and exit with its code.
//
// Returns true when main() should exit; the caller is responsible for
// os.Exit so deferred cleanup in main() is allowed to fire (today there
// is none, but the contract keeps the door open).
func dispatchCLI() (int, bool) {
	args := os.Args
	if len(args) < 2 {
		return 0, false
	}
	if args[1] == "daemon" {
		// daemon mode is handled by the normal main path; signal to
		// the caller that we want to keep going but tag ourselves as
		// "this is a daemon, no GUI."
		return 0, false
	}
	if !cli.IsCLIInvocation(args) {
		return 0, false
	}
	env := cli.NewEnv(args, version.Version)
	code, _ := cli.Dispatch(args[1:], env)
	return int(code), true
}

// isDaemonMode reports whether this process is meant to come up
// without a GUI window. Two paths get here: the user explicitly typed
// `locorum daemon`, and the auto-spawn from a CLI client (which sets
// LOCORUM_DAEMON_MODE=1).
func isDaemonMode() bool {
	if daemon.IsDaemonAutoSpawn() {
		return true
	}
	if len(os.Args) >= 2 && os.Args[1] == "daemon" {
		return true
	}
	return false
}

// startDaemonServices acquires the owner-lock and binds the IPC
// listener. Returns the lock + server so the caller can defer their
// shutdown. Errors here are non-fatal in GUI mode (we still want the
// window to open if the user can't run multiple Locorum at once on a
// shared machine), but are fatal in daemon mode (no IPC = nothing for
// the CLI to talk to).
func startDaemonServices(ctx context.Context, homeDir string, sm *sites.SiteManager) (*daemon.Lock, *daemon.Server, error) {
	if err := daemon.EnsureStateDir(homeDir); err != nil {
		return nil, nil, err
	}

	lock, err := daemon.Acquire(daemon.Path(homeDir), version.Version)
	if err != nil {
		return nil, nil, err
	}

	ln, err := daemon.Listen(daemon.SocketPath(homeDir))
	if err != nil {
		_ = lock.Release()
		return nil, nil, err
	}

	srv := daemon.NewServer(ln, slog.With("subsys", "ipc"))
	daemon.RegisterMethods(srv, sm)

	go func() {
		if err := srv.Serve(ctx); err != nil {
			slog.Warn("ipc server stopped", "err", err.Error())
		}
	}()

	slog.Info("daemon ipc bound", "socket", ln.Addr())
	return lock, srv, nil
}

// runHeadlessDaemon blocks until SIGTERM/SIGINT or ctx cancel. Used in
// daemon mode where we have no Gio window event loop to keep the
// process alive.
func runHeadlessDaemon(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case sig := <-ch:
		slog.Info("daemon received signal, shutting down", "signal", sig.String())
	}
}

// runDaemonMode is the main loop for `locorum daemon` and the
// auto-spawn path. It mirrors initFunc / app.Main from the GUI flow
// without the Gio window: acquire lock, run app.Initialize, bind IPC,
// block on signal, tear down.
//
// Errors during init are fatal here — the daemon has nothing to fall
// back on, unlike the GUI which still has a usable window if Docker is
// down.
func runDaemonMode(homeDir string, sm *sites.SiteManager, a *application.App, d *docker.Docker) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock, srv, err := startDaemonServices(ctx, homeDir, sm)
	if err != nil {
		reportLockError(err)
		cancel()
		os.Exit(2) //nolint:gocritic // exitAfterDefer: cancel() called explicitly above; no other cleanup needed before fatal exit
	}
	defer func() {
		srv.Shutdown(2 * time.Second)
		_ = lock.Release()
	}()

	d.SetClient(a.GetClient())
	if err := a.Initialize(ctx); err != nil {
		slog.Error("daemon initialize failed", "err", err.Error())
		os.Exit(1)
	}
	if err := sm.ReconcileState(); err != nil {
		slog.Warn("reconcile state failed", "err", err.Error())
	}

	slog.Info("daemon ready")
	runHeadlessDaemon(ctx)

	if err := a.Shutdown(context.Background()); err != nil {
		slog.Warn("daemon shutdown error", "err", err.Error())
	}
}

// reportLockError logs whichever specific error a lock acquisition
// failure produced. The CLI wants the precise daemon owner (pid +
// start time); GUI mode logs and continues without IPC.
func reportLockError(err error) {
	var lockErr *daemon.ErrLocked
	if errors.As(err, &lockErr) {
		slog.Warn("another locorum daemon is running",
			"pid", lockErr.Owner.PID,
			"started_at", lockErr.Owner.StartedAt,
			"version", lockErr.Owner.Version)
		return
	}
	slog.Error("failed to acquire daemon lock", "err", err.Error())
}
