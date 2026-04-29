// Package fake provides an in-memory dbengine.Execer for tests so they
// can exercise Snapshot / Restore / marker-write code paths without a
// running Docker daemon.
package fake

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/PeterBooker/locorum/internal/docker"
)

// Execer is a recording, scriptable dbengine.Execer. Tests construct one,
// queue stdout / stderr bodies + exit codes for the next exec calls, then
// assert on the recorded calls afterwards.
type Execer struct {
	mu sync.Mutex

	// Calls records every exec invocation in order. Tests assert on it.
	Calls []Call

	// StdoutScript queues bodies to return as stdout from the *next* exec.
	// First call dequeues StdoutScript[0]; if the slice is empty, "" is
	// written.
	StdoutScript []string

	// StderrScript is the parallel stderr script.
	StderrScript []string

	// ExitScript queues exit codes. Empty → 0.
	ExitScript []int

	// Err is returned (instead of running) if non-nil.
	Err error

	// CapturedStdin records stdin payloads passed to ExecInContainerWriterStdin
	// in order. Useful for marker-write assertions.
	CapturedStdin [][]byte
}

// Call is one recorded exec invocation.
type Call struct {
	Container string
	Cmd       []string
	HasStdin  bool
}

// New returns an empty fake Execer.
func New() *Execer { return &Execer{} }

// ExecInContainerWriter pops the next stdout/stderr/exit triple, writes
// stdout to stdoutW, stderr to stderrW, returns the exit code.
func (e *Execer) ExecInContainerWriter(_ context.Context, name string, opts docker.ExecOptions, stdoutW, stderrW io.Writer) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.Calls = append(e.Calls, Call{Container: name, Cmd: copyStrings(opts.Cmd)})
	if e.Err != nil {
		return -1, e.Err
	}

	out, err := e.shift(&e.StdoutScript)
	if err == nil && stdoutW != nil && out != "" {
		if _, werr := io.WriteString(stdoutW, out); werr != nil {
			return -1, werr
		}
	}
	se, _ := e.shift(&e.StderrScript)
	if stderrW != nil && se != "" {
		if _, werr := io.WriteString(stderrW, se); werr != nil {
			return -1, werr
		}
	}
	return e.shiftInt(&e.ExitScript), nil
}

// ExecInContainerWriterStdin records the stdin payload, then runs the
// same script as ExecInContainerWriter.
func (e *Execer) ExecInContainerWriterStdin(_ context.Context, name string, opts docker.ExecOptions, stdin io.Reader, stdoutW, stderrW io.Writer) (int, error) {
	e.mu.Lock()
	if stdin != nil {
		buf, err := io.ReadAll(stdin)
		if err != nil {
			e.mu.Unlock()
			return -1, err
		}
		e.CapturedStdin = append(e.CapturedStdin, buf)
	}
	e.Calls = append(e.Calls, Call{Container: name, Cmd: copyStrings(opts.Cmd), HasStdin: true})
	if e.Err != nil {
		err := e.Err
		e.mu.Unlock()
		return -1, err
	}
	out, _ := e.shift(&e.StdoutScript)
	if stdoutW != nil && out != "" {
		if _, werr := io.WriteString(stdoutW, out); werr != nil {
			e.mu.Unlock()
			return -1, werr
		}
	}
	se, _ := e.shift(&e.StderrScript)
	if stderrW != nil && se != "" {
		if _, werr := io.WriteString(stderrW, se); werr != nil {
			e.mu.Unlock()
			return -1, werr
		}
	}
	exit := e.shiftInt(&e.ExitScript)
	e.mu.Unlock()
	return exit, nil
}

func (e *Execer) shift(s *[]string) (string, error) {
	if len(*s) == 0 {
		return "", errors.New("empty")
	}
	v := (*s)[0]
	*s = (*s)[1:]
	return v, nil
}

func (e *Execer) shiftInt(s *[]int) int {
	if len(*s) == 0 {
		return 0
	}
	v := (*s)[0]
	*s = (*s)[1:]
	return v
}

func copyStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
