package utils

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// HostExecOptions configures a host-shell command execution.
type HostExecOptions struct {
	// Command is a single shell-evaluated command line. It is passed to
	// `bash -c` on Unix/WSL or `cmd /C` on native Windows so the user can
	// use pipes, redirections, and ${VAR} expansion. Required.
	Command string
	// Cwd is the working directory the command runs in. Empty inherits the
	// parent process's cwd.
	Cwd string
	// Env is a list of "KEY=VALUE" strings prepended to the inherited
	// environment. Later entries override earlier ones (Go's os/exec
	// semantics).
	Env []string
}

// HostLineHandler receives a single line of stdout (stderr=false) or stderr
// (stderr=true) from a streaming host command. The trailing newline is
// stripped. The handler must be cheap; it runs on the streaming goroutine.
type HostLineHandler func(line string, stderr bool)

// RunHostStream executes a command on the host shell, streaming stdout and
// stderr line-by-line through onLine. It blocks until the command exits or
// ctx is cancelled. Returns the exit code (or -1 if the process never
// started) and an error for plumbing failures only — a non-zero exit is not
// returned as an error.
//
// On Unix and inside WSL, the command runs under `bash -c`. On native
// Windows, it runs under `cmd /C`. WSL detection is automatic: a Linux
// process running inside WSL takes the bash branch.
func RunHostStream(ctx context.Context, opts HostExecOptions, onLine HostLineHandler) (int, error) {
	if strings.TrimSpace(opts.Command) == "" {
		return -1, errors.New("host exec: empty command")
	}
	if onLine == nil {
		onLine = func(string, bool) {}
	}

	cmd := buildHostCommand(ctx, opts.Command)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	if len(opts.Env) > 0 {
		// Inherit the parent environment then layer the caller's vars on top.
		// os/exec's "later wins" semantics give the caller the final say.
		env := append([]string(nil), envOrEmpty()...)
		env = append(env, opts.Env...)
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	var emitMu sync.Mutex
	emit := func(line string, isStderr bool) {
		emitMu.Lock()
		defer emitMu.Unlock()
		onLine(line, isStderr)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go scanHostLines(&wg, stdout, false, emit)
	go scanHostLines(&wg, stderr, true, emit)
	wg.Wait()

	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return -1, ctx.Err()
	}

	if cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode(), nil
	}
	// cmd.Wait error, no ProcessState — surface the underlying error.
	if waitErr != nil {
		return -1, waitErr
	}
	return -1, nil
}

// buildHostCommand returns an *exec.Cmd that will run command via the host's
// shell. It uses bash on Unix/WSL, cmd.exe on native Windows. The cmd.exe
// branch sets the CREATE_NO_WINDOW flag so the GUI process does not flash a
// transient console window for each task.
func buildHostCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" && !isWSL() {
		cmd := exec.CommandContext(ctx, "cmd.exe", "/C", command)
		HideConsole(cmd)
		return cmd
	}
	return exec.CommandContext(ctx, "bash", "-c", command)
}

// scanHostLines reads from r line-by-line and emits each line.
func scanHostLines(wg *sync.WaitGroup, r io.Reader, isStderr bool, emit func(string, bool)) {
	defer wg.Done()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			emit(strings.TrimRight(line, "\r\n"), isStderr)
		}
		if err != nil {
			return
		}
	}
}

// envOrEmpty returns the parent process environment. Indirected through a
// variable so tests can stub it.
var envOrEmpty = os.Environ
