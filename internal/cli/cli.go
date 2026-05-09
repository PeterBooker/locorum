// Package cli implements Locorum's command-line surface. The same
// binary that runs the GUI parses os.Args before app.Main() and
// dispatches a CLI subcommand here when one is recognised. Every
// mutating command in this package shells JSON-RPC over the IPC socket
// to the daemon — this package never talks to Docker / SQLite directly.
//
// Output rules:
//   - Tables go to stdout, errors to stderr.
//   - Exit codes are documented per command (0 = success, 1 = error,
//     2 = invalid usage, 3 = no daemon).
//   - --json prints JSON-encoded results, suitable for scripting.
//   - NO_COLOR and a non-TTY stdout both suppress ANSI sequences.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/utils"
)

// ExitCode classifies the outcome of a CLI invocation. Numeric values
// are part of the public contract — scripts depend on them.
type ExitCode int

const (
	ExitOK         ExitCode = 0
	ExitError      ExitCode = 1
	ExitUsage      ExitCode = 2
	ExitNoDaemon   ExitCode = 3
	ExitNotFound   ExitCode = 4
	ExitForbidden  ExitCode = 5
	ExitConflict   ExitCode = 6
	ExitNotStarted ExitCode = 7
)

// errToExit also maps CodeConflict.

// Env carries every dependency a CLI command needs. Constructed once
// per invocation by main.go; injected into every subcommand so tests
// can capture stdout/stderr without process-global state.
type Env struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Args    []string // arguments AFTER the subcommand keyword
	HomeDir string
	ExePath string // absolute path to the locorum binary, for auto-spawn
	Version string
}

// Command is the runtime contract every subcommand satisfies.
type Command interface {
	Name() string
	Synopsis() string
	Run(ctx context.Context, env *Env) ExitCode
}

// commands lists the recognised subcommands in display order. Adding a
// new one means appending here AND the corresponding switch in
// Dispatch.
var commands = []struct {
	name    string
	summary string
}{
	{"site", "list / describe / start / stop / wp"},
	{"snapshot", "list / create / restore"},
	{"hook", "list / run"},
	{"mcp", "MCP server (stdio) for AI agents"},
	{"daemon", "run a headless daemon (no GUI)"},
	{"version", "print build identity"},
	{"help", "show this help"},
}

// Dispatch is the entry point called by main.go when os.Args[1] looks
// like a CLI subcommand. Returns true if the args were consumed (the
// caller exits with the returned code); false means "no subcommand
// recognised, continue to GUI."
func Dispatch(args []string, env *Env) (ExitCode, bool) {
	if len(args) == 0 {
		return ExitOK, false
	}
	verb := args[0]
	if !isCLIVerb(verb) {
		return ExitOK, false
	}

	ctx, stop := signalContext()
	defer stop()

	subEnv := *env
	subEnv.Args = args[1:]

	switch verb {
	case "site":
		return runSite(ctx, &subEnv), true
	case "snapshot":
		return runSnapshot(ctx, &subEnv), true
	case "hook":
		return runHook(ctx, &subEnv), true
	case "mcp":
		return runMCP(ctx, &subEnv), true
	case "daemon":
		return ExitOK, false // daemon mode is handled in main.go
	case "version":
		return runVersion(&subEnv), true
	case "help", "-h", "--help":
		printHelp(env.Stdout, env.Version)
		return ExitOK, true
	default:
		_, _ = fmt.Fprintf(env.Stderr, "locorum: unknown command %q\n", verb)
		printHelp(env.Stderr, env.Version)
		return ExitUsage, true
	}
}

// isCLIVerb reports whether os.Args[1] looks like a CLI verb. Used by
// main.go to decide whether to skip Gio bring-up.
func isCLIVerb(verb string) bool {
	switch verb {
	case "site", "snapshot", "hook", "mcp", "daemon", "version", "help",
		"-h", "--help":
		return true
	}
	return false
}

// IsCLIInvocation is the public hook main.go uses. Same as isCLIVerb
// but exported so the package boundary is explicit.
func IsCLIInvocation(args []string) bool {
	if len(args) < 2 {
		return false
	}
	return isCLIVerb(args[1])
}

// IsDaemonVerb reports whether os.Args asks for an explicit daemon
// boot. Distinct from auto-spawn: the user can run `locorum daemon` to
// get a headless process for CI.
func IsDaemonVerb(args []string) bool {
	return len(args) >= 2 && args[1] == "daemon"
}

// printHelp renders top-level help.
func printHelp(w io.Writer, version string) {
	_, _ = fmt.Fprintf(w, "Locorum %s — local WordPress dev environments\n\n", version)
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  locorum                          Launch the GUI")
	_, _ = fmt.Fprintln(w, "  locorum <command> [args...]      Run a CLI command (talks to a running daemon)")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %-10s %s\n", c.name, c.summary)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Run `locorum <command> --help` for command-specific options.")
}

// signalContext returns a context cancelled by Ctrl-C / SIGTERM. The
// CLI uses it as the bound on every IPC call so a hung daemon doesn't
// strand the user.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, cancel
}

// dial opens an IPC connection, auto-spawning a daemon if needed. The
// returned client is closed by the caller on completion.
//
// hello.PeerKind defaults to "cli" when empty so all CLI traffic shows
// up uniformly in the daemon's telemetry.
func dial(ctx context.Context, env *Env, hello daemon.HelloOptions) (*daemon.Client, error) {
	if hello.PeerKind == "" {
		hello.PeerKind = "cli"
	}
	cli, err := daemon.EnsureDaemon(ctx, env.HomeDir, env.ExePath, hello)
	if err != nil {
		return nil, err
	}
	return cli, nil
}

// errToExit maps an IPC / dial error to a documented exit code so
// scripts can branch on numeric values without parsing strings.
func errToExit(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, daemon.ErrNoDaemon) {
		return ExitNoDaemon
	}
	var rpcErr *daemon.RPCError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Code {
		case daemon.CodeNotFound:
			return ExitNotFound
		case daemon.CodeForbidden:
			return ExitForbidden
		case daemon.CodeConflict:
			return ExitConflict
		}
	}
	return ExitError
}

// printJSON marshal-prints v with two-space indent.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// resolveExePath returns the absolute locorum binary path for spawning
// the daemon. os.Executable is the canonical answer; we resolve
// symlinks so a user-installed version (e.g. ~/go/bin/locorum) survives
// a $PATH update mid-session.
func resolveExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	return resolved
}

// NewEnv builds a default Env from the running process. CLI entry-point
// calls NewEnv to keep the wire-up in one place.
func NewEnv(args []string, version string) *Env {
	home, _ := utils.GetUserHomeDir()
	return &Env{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Args:    args,
		HomeDir: home,
		ExePath: resolveExePath(),
		Version: version,
	}
}
