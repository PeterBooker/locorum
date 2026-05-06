// Package daemon owns the process-coordination machinery for Locorum:
// the owner.lock that arbitrates which process owns Docker lifecycle,
// the local IPC transport (Unix domain socket on POSIX, named pipe on
// Windows), the JSON-RPC server bound to a SiteManager, and the matching
// client that CLI / MCP processes shell over.
//
// The split exists because Locorum's GUI and CLI are the same binary.
// Two processes both calling app.Initialize would race-delete each
// other's Docker resources (they share the io.locorum.platform=locorum
// label). Instead, exactly one process holds the lock and runs the
// daemon goroutines; every other invocation is an IPC client.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LockFilename is the basename of the owner-lock under
// ~/.locorum/state/. The full path is built by Path().
const LockFilename = "owner.lock"

// SocketFilename is the basename of the IPC socket on POSIX. Windows
// uses a named pipe and ignores this value (see transport_windows.go).
const SocketFilename = "locorum.sock"

// stateDir is the relative path under the home dir where daemon state
// files live. Mirrors the pre-existing convention used by the health
// runner ("state/").
const stateDir = ".locorum/state"

// Path returns the absolute owner-lock path under homeDir. Callers are
// responsible for ensuring the directory exists; Acquire will create it
// if missing.
func Path(homeDir string) string {
	return filepath.Join(homeDir, stateDir, LockFilename)
}

// SocketPath returns the absolute IPC-socket path under homeDir. Same
// directory as the lock so a single chmod 0700 protects both.
func SocketPath(homeDir string) string {
	return filepath.Join(homeDir, stateDir, SocketFilename)
}

// StateDir returns the absolute parent directory holding the lock and
// socket. EnsureStateDir creates it with 0700 perms so other users on
// the host cannot connect to or steal the lock.
func StateDir(homeDir string) string {
	return filepath.Join(homeDir, stateDir)
}

// EnsureStateDir creates ~/.locorum/state with 0700 perms (owner-only
// rwx). Idempotent. Tightening perms on an existing dir is best-effort
// because a directory created by an earlier release with 0755 should
// still work for the lock; callers may log a warning if the OS-reported
// mode is wider than 0700.
func EnsureStateDir(homeDir string) error {
	dir := StateDir(homeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	// Best-effort tighten. Failures (e.g. read-only filesystem in a
	// hardened sandbox) are logged by the caller, not surfaced here —
	// the dir exists, which is the only invariant this function
	// promises.
	_ = os.Chmod(dir, 0o700)
	return nil
}

// Owner is the JSON payload written into the lock file. PID + StartedAt
// together identify a specific process even after PID rollover, which
// matters on long-running hosts that recycle the daemon weekly.
type Owner struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	Version   string    `json:"version,omitempty"`
}

// Lock is an acquired owner.lock. Release exactly once on shutdown to
// remove the file and release the OS-level advisory lock.
//
// Lock is not safe for concurrent use; callers should hold one Lock per
// process. flock() releases automatically when the process exits, so a
// crash leaks at most a stale file (handled by stale-detection in
// Acquire).
type Lock struct {
	path string
	f    *os.File
	// owner is the payload we wrote on acquire. Surfaced via Owner()
	// so tests and the CLI's "daemon is already running" diagnostic
	// can read what we promised without a second file read.
	owner Owner
}

// ErrLocked indicates that a live Locorum daemon already owns the lock.
// Wraps the on-disk Owner so the CLI can print a useful diagnostic.
type ErrLocked struct {
	Path  string
	Owner Owner
}

func (e *ErrLocked) Error() string {
	if e == nil {
		return "lock held"
	}
	return fmt.Sprintf("locorum daemon already running (pid=%d, since %s)",
		e.Owner.PID, e.Owner.StartedAt.Format(time.RFC3339))
}

// Acquire takes the owner-lock at path with stale-detection. Three
// possible outcomes:
//
//   - Success: returns a *Lock holding the OS-level exclusive lock plus
//     a freshly-written Owner record on disk. Caller releases via
//     Lock.Release.
//   - Held by a live process: returns *ErrLocked. CLI clients translate
//     this into "talk to that pid over IPC instead."
//   - Held but stale (pid gone, or pid recycled to a different process):
//     takes over. The takeover is OS-atomic via flock(LOCK_EX|LOCK_NB),
//     so two simultaneous takeovers can't both succeed.
//
// version is stamped into the lock payload so the CLI can warn when an
// older daemon version is holding the lock (useful after a binary
// upgrade where the user forgot to restart the GUI).
func Acquire(path, version string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	// O_RDWR|O_CREATE so the same fd works for read-existing-PID,
	// truncate-and-rewrite, and the OS-level advisory lock applied
	// next. 0600 guards against world-readable PIDs leaking process
	// identity to other local users.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}

	// Try the OS-level exclusive non-blocking lock. On unix this is
	// flock(LOCK_EX|LOCK_NB); on windows LockFileEx with
	// LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY.
	if err := platformLock(f); err != nil {
		// We didn't get the OS lock — read the existing payload to
		// decide whether the holder is actually alive (live → return
		// ErrLocked, dead → re-attempt after taking over).
		owner, _ := readOwnerFromFile(f)
		_ = f.Close()
		if processAlive(owner.PID) {
			return nil, &ErrLocked{Path: path, Owner: owner}
		}
		// Stale. Removing the file then trying again gives any
		// concurrent takeover candidate exactly one chance — flock
		// will then arbitrate. We deliberately do NOT recurse forever:
		// one retry is enough; persistent failure means something
		// bigger is broken and the caller should see it.
		_ = os.Remove(path)
		return acquireOnce(path, version)
	}

	return finishAcquire(f, path, version)
}

// acquireOnce is the post-stale-removal retry path. Same body as
// Acquire's happy path but without the recursion guard.
func acquireOnce(path, version string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock (retry): %w", err)
	}
	if err := platformLock(f); err != nil {
		owner, _ := readOwnerFromFile(f)
		_ = f.Close()
		// A second contender beat us to it after we removed the stale
		// file. Treat their claim as authoritative.
		return nil, &ErrLocked{Path: path, Owner: owner}
	}
	return finishAcquire(f, path, version)
}

// finishAcquire writes our Owner payload and returns the held Lock.
// Truncates whatever stale bytes survived the previous holder's crash.
func finishAcquire(f *os.File, path, version string) (*Lock, error) {
	owner := Owner{
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		Version:   version,
	}
	body, err := json.Marshal(owner)
	if err != nil {
		_ = platformUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("marshal lock owner: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		_ = platformUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("truncate lock: %w", err)
	}
	if _, err := f.WriteAt(body, 0); err != nil {
		_ = platformUnlock(f)
		_ = f.Close()
		return nil, fmt.Errorf("write lock: %w", err)
	}
	if err := f.Sync(); err != nil {
		// Sync failure is informational — the OS lock and in-memory
		// payload are still correct. A reader that sees an empty file
		// here would briefly miss our Owner, but processAlive() will
		// still return true because we hold the flock.
		_ = err
	}
	return &Lock{path: path, f: f, owner: owner}, nil
}

// Owner returns the payload we wrote on acquire. Stable across the
// lifetime of the Lock; never touches the file again.
func (l *Lock) Owner() Owner { return l.owner }

// Release drops the OS-level lock and removes the file. Idempotent: a
// second Release after the first is a no-op. Always returns nil so
// `defer lock.Release()` can be used without an error trail at every
// call site; serious releases also fall through process-exit cleanup of
// flock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Order matters: remove the file BEFORE unlock so a concurrent
	// Acquire+takeover doesn't grab a fresh lock on the file we then
	// remove out from under them.
	_ = os.Remove(l.path)
	_ = platformUnlock(l.f)
	_ = l.f.Close()
	l.f = nil
	return nil
}

// ReadOwner reads the lock payload at path without taking the OS lock.
// Used by the CLI to print a "daemon is running, pid=X" message before
// it dials the socket. Returns a zero Owner and nil error when the file
// does not exist (i.e. no daemon has ever started).
func ReadOwner(path string) (Owner, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Owner{}, nil
		}
		return Owner{}, fmt.Errorf("read lock: %w", err)
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return Owner{}, nil
	}
	var owner Owner
	if err := json.Unmarshal(body, &owner); err != nil {
		// Tolerate a legacy "pid only" file (single decimal int) so
		// upgrades from a future where someone wrote bare PIDs don't
		// hard-fail at startup.
		if pid, perr := strconv.Atoi(string(body)); perr == nil {
			return Owner{PID: pid}, nil
		}
		return Owner{}, fmt.Errorf("parse lock: %w", err)
	}
	return owner, nil
}

// readOwnerFromFile is the same as ReadOwner but reuses an open file.
// Returns the zero Owner on any error; callers handle "the holder may
// be dead" via processAlive(0) → false.
func readOwnerFromFile(f *os.File) (Owner, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return Owner{}, err
	}
	stat, err := f.Stat()
	if err != nil || stat.Size() == 0 {
		return Owner{}, err
	}
	body := make([]byte, stat.Size())
	if _, err := f.ReadAt(body, 0); err != nil {
		return Owner{}, err
	}
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return Owner{}, nil
	}
	var owner Owner
	if err := json.Unmarshal(body, &owner); err != nil {
		if pid, perr := strconv.Atoi(string(body)); perr == nil {
			return Owner{PID: pid}, nil
		}
		return Owner{}, err
	}
	return owner, nil
}
