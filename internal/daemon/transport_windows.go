//go:build windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"

	"github.com/Microsoft/go-winio"
)

// windowsPipePrefix is the canonical Win32 named-pipe prefix. The full
// pipe path is `\\.\pipe\locorum-{user-sid}` so two users on the same
// machine cannot collide. SIDs (S-1-5-21-…) are unique per account and
// do not contain reserved pipe-name chars, so they go in raw.
const windowsPipePrefix = `\\.\pipe\locorum-`

// pipeListener wraps the Microsoft/go-winio listener. Addr() returns
// the full pipe path so the CLI dial path matches.
type pipeListener struct {
	net.Listener
	path string
}

func (p *pipeListener) Addr() string { return p.path }

// Listen opens a Windows named pipe at the per-user path. The path
// argument is honoured for compatibility with the unix transport but
// the daemon always rewrites it to the per-user pipe to avoid
// cross-user squatting.
func Listen(path string) (Listener, error) {
	pipe, err := pipePath()
	if err != nil {
		return nil, err
	}
	// Owner-only ACL: D:P(A;;GA;;;OW) — Discretionary ACL, Protected,
	// Allow Generic All to Owner. Mirrors the 0600 unix invariant.
	cfg := &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;OW)",
		MessageMode:        false, // byte mode, matches net.Conn semantics
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}
	ln, err := winio.ListenPipe(pipe, cfg)
	if err != nil {
		return nil, fmt.Errorf("listen pipe: %w", err)
	}
	return &pipeListener{Listener: ln, path: pipe}, nil
}

// Dial connects to the daemon's named pipe.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	pipe, err := pipePath()
	if err != nil {
		return nil, err
	}
	conn, err := winio.DialPipeContext(ctx, pipe)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoDaemon
		}
		return nil, fmt.Errorf("dial pipe: %w", err)
	}
	return conn, nil
}

// pipePath returns the per-user named-pipe path. SID is preferred over
// username because it survives renames and avoids issues with non-ASCII
// account names. Falls back to the username if SID lookup fails (rare).
func pipePath() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("identify user: %w", err)
	}
	id := u.Uid
	if id == "" {
		id = u.Username
	}
	if id == "" {
		return "", errors.New("could not identify current user")
	}
	return windowsPipePrefix + id, nil
}
