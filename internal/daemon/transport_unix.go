//go:build !windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
)

// unixListener is a thin wrapper around net.UnixListener that pins the
// chmod 0600 invariant: only the owner of the socket file can connect.
// On Linux this is enforced by the filesystem perms (peer SO_PEERCRED
// is also available but not relied on); on macOS the same perm scheme
// is the documented protection mechanism.
type unixListener struct {
	*net.UnixListener
	path string
}

func (u *unixListener) Addr() string { return u.path }

func (u *unixListener) Close() error {
	err := u.UnixListener.Close()
	// net.UnixListener.Close removes the socket file when SetUnlinkOnClose(true)
	// is set (Go default since 1.8). Belt-and-braces unlink in case a
	// future Go release changes the default — leaving the file behind
	// would block the next Acquire+Listen.
	_ = os.Remove(u.path)
	return err
}

// Listen creates a Unix domain socket listener at path with 0600 perms.
// Removes a stale socket file if its owner is dead (Acquire's
// stale-detection is the canonical place for that, but a stale .sock
// without a matching .lock is still possible if the lock was removed by
// hand).
func Listen(path string) (Listener, error) {
	if err := os.MkdirAll(parentDir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	// Best-effort cleanup. If a live daemon were listening on this
	// path, the lock would already have refused us, so a leftover file
	// here is necessarily stale.
	_ = os.Remove(path)

	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("resolve unix addr: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("listen unix: %w", err)
	}
	ln.SetUnlinkOnClose(true)

	// Tighten perms BEFORE returning. Linux respects umask on bind, so
	// without this chmod a permissive umask (002) leaves the socket
	// group-writable.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return &unixListener{UnixListener: ln, path: path}, nil
}

// Dial connects to the daemon socket at path. Returns a net.Conn ready
// for newline-delimited JSON-RPC. Honours ctx cancellation.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoDaemon
		}
		return nil, fmt.Errorf("stat socket: %w", err)
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("dial socket: %w", err)
	}
	return conn, nil
}

// parentDir returns the parent of p without pulling in path/filepath
// for a single split. Callers pass absolute paths only.
func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
