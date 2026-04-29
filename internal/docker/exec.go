package docker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecOptions configures a streaming exec invocation.
type ExecOptions struct {
	// Cmd is the argv to execute. The first element must be an absolute path
	// or available on PATH inside the container. Required.
	Cmd []string
	// Env is a list of "KEY=VALUE" strings appended to the container's env.
	// Already-set keys in the image are overridden.
	Env []string
	// User is the optional "uid[:gid]" to run as. Empty preserves the
	// container's default user.
	User string
	// WorkingDir overrides the container's working directory. Empty preserves
	// the default.
	WorkingDir string
}

// ExecLineHandler receives a single line of stdout (stderr=false) or stderr
// (stderr=true) from a streaming exec. The trailing newline is stripped.
//
// Implementations must be cheap; they are invoked from the streaming goroutine
// while output is still arriving. Heavy work should be deferred to another
// goroutine via a buffered channel.
type ExecLineHandler func(line string, stderr bool)

// ExecInContainerWriter runs cmd inside containerName and pipes the
// container's stdout into stdoutW and stderr into stderrW byte-for-byte.
// It is the streaming counterpart of ExecInContainerStream for callers that
// need raw bytes (database dumps, file extracts) rather than line-oriented
// output.
//
// Returns the command's exit code. Plumbing failures (failed to create exec,
// attach, write) are returned as err. A non-zero exit code is NOT reported
// as an error — the caller decides how to interpret it.
//
// Either writer may be nil; nil means "discard". stdin is not attached;
// callers needing to feed input use ExecInContainerWriterStdin below.
func (d *Docker) ExecInContainerWriter(ctx context.Context, containerName string, opts ExecOptions, stdoutW, stderrW io.Writer) (int, error) {
	return d.execWriter(ctx, containerName, opts, nil, stdoutW, stderrW)
}

// ExecInContainerWriterStdin is ExecInContainerWriter plus a stdin source
// piped into the container. Used by Restore implementations that pipe a
// dump file into `mysql` / `psql`.
func (d *Docker) ExecInContainerWriterStdin(ctx context.Context, containerName string, opts ExecOptions, stdin io.Reader, stdoutW, stderrW io.Writer) (int, error) {
	return d.execWriter(ctx, containerName, opts, stdin, stdoutW, stderrW)
}

func (d *Docker) execWriter(ctx context.Context, containerName string, opts ExecOptions, stdin io.Reader, stdoutW, stderrW io.Writer) (int, error) {
	if len(opts.Cmd) == 0 {
		return -1, errors.New("exec: empty command")
	}
	if stdoutW == nil {
		stdoutW = io.Discard
	}
	if stderrW == nil {
		stderrW = io.Discard
	}

	execCfg := container.ExecOptions{
		Cmd:          opts.Cmd,
		Env:          opts.Env,
		User:         opts.User,
		WorkingDir:   opts.WorkingDir,
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
	created, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return -1, fmt.Errorf("creating exec in %q: %w", containerName, err)
	}

	attach, err := d.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return -1, fmt.Errorf("attaching to exec in %q: %w", containerName, err)
	}
	defer attach.Close()

	// Pump stdin first, in a goroutine so it interleaves with stdout demux.
	stdinDone := make(chan error, 1)
	if stdin != nil {
		go func() {
			_, copyErr := io.Copy(attach.Conn, stdin)
			// CloseWrite signals EOF on stdin to the remote command — without
			// it `mysql` or `psql` reading stdin will hang forever.
			if cw, ok := attach.Conn.(closeWriter); ok {
				_ = cw.CloseWrite()
			}
			stdinDone <- copyErr
		}()
	} else {
		close(stdinDone)
	}

	demuxDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(stdoutW, stderrW, attach.Reader)
		demuxDone <- copyErr
	}()

	select {
	case <-demuxDone:
	case <-ctx.Done():
		attach.Close()
		<-demuxDone
	}
	// Drain stdin pump regardless — even if demux finished first.
	<-stdinDone

	if ctx.Err() != nil {
		return -1, ctx.Err()
	}

	inspect, err := d.cli.ContainerExecInspect(context.Background(), created.ID)
	if err != nil {
		return -1, fmt.Errorf("inspecting exec in %q: %w", containerName, err)
	}
	return inspect.ExitCode, nil
}

// closeWriter mirrors net.TCPConn.CloseWrite — Docker's hijacked connection
// implements it, but the interface is not exported on attach.Conn directly.
type closeWriter interface {
	CloseWrite() error
}

// ExecInContainerStream runs cmd inside containerName with separated stdout
// and stderr streams, invoking onLine for each captured line. It blocks until
// the command completes or ctx is cancelled.
//
// Returns the command's exit code and an error. A non-zero exit code is NOT
// reported as an error — the caller decides how to interpret it. err is
// non-nil only for genuine plumbing failures (failed to create exec, attach,
// or connection broken before exit).
//
// Unlike ExecInContainer, this path uses Tty:false so stdout and stderr
// are demultiplexed independently. That makes it suitable for hooks where
// the runner must distinguish failure messages from normal output.
func (d *Docker) ExecInContainerStream(ctx context.Context, containerName string, opts ExecOptions, onLine ExecLineHandler) (int, error) {
	if len(opts.Cmd) == 0 {
		return -1, errors.New("exec: empty command")
	}
	if onLine == nil {
		onLine = func(string, bool) {}
	}

	execCfg := container.ExecOptions{
		Cmd:          opts.Cmd,
		Env:          opts.Env,
		User:         opts.User,
		WorkingDir:   opts.WorkingDir,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
	created, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return -1, fmt.Errorf("creating exec in %q: %w", containerName, err)
	}

	attach, err := d.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return -1, fmt.Errorf("attaching to exec in %q: %w", containerName, err)
	}
	defer attach.Close()

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	// stdcopy.StdCopy demuxes the multiplexed stream from Docker into
	// stdout/stderr writers. It blocks until the source is exhausted, so
	// run it in a goroutine and signal completion to the line scanners.
	demuxDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(stdoutW, stderrW, attach.Reader)
		// Closing the writers terminates the line-scanner goroutines.
		_ = stdoutW.Close()
		_ = stderrW.Close()
		demuxDone <- copyErr
	}()

	// Both scanners share a single mutex around onLine so writes from
	// stdout and stderr never interleave bytes.
	var emitMu sync.Mutex
	emit := func(line string, isStderr bool) {
		emitMu.Lock()
		defer emitMu.Unlock()
		onLine(line, isStderr)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go scanLines(&wg, stdoutR, false, emit)
	go scanLines(&wg, stderrR, true, emit)

	// Wait for ctx cancellation OR demux completion. On cancel we close the
	// attach connection so StdCopy returns and the scanners drain.
	select {
	case copyErr := <-demuxDone:
		// Source exhausted normally.
		_ = copyErr // io.EOF / "use of closed connection" are expected at end
	case <-ctx.Done():
		// Closing the attach connection unblocks StdCopy with an error.
		attach.Close()
		<-demuxDone
	}

	wg.Wait()

	// Drain pipes (defensive — should be closed already).
	_ = stdoutR.Close()
	_ = stderrR.Close()

	if ctx.Err() != nil {
		return -1, ctx.Err()
	}

	inspect, err := d.cli.ContainerExecInspect(context.Background(), created.ID)
	if err != nil {
		return -1, fmt.Errorf("inspecting exec in %q: %w", containerName, err)
	}
	return inspect.ExitCode, nil
}

// scanLines reads from r line-by-line and calls emit for each line. The
// trailing newline is stripped. Empty lines are reported as empty strings (not
// suppressed) so callers can preserve formatting.
func scanLines(wg *sync.WaitGroup, r io.Reader, isStderr bool, emit func(string, bool)) {
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
