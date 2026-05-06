//go:build !windows

package daemon

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// platformLock takes an exclusive non-blocking advisory lock on f.
// Returns an error matching unix.EWOULDBLOCK when another process holds
// the lock. The lock is automatically released by the kernel if the
// process exits, so a daemon crash never leaks a permanent block.
func platformLock(f *os.File) error {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	return nil
}

// platformUnlock releases an advisory lock previously taken by
// platformLock. Errors are returned for completeness but the caller
// generally ignores them: f.Close() also releases the lock, and the
// kernel cleans up after process exit.
func platformUnlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// processAlive reports whether the given PID currently exists. Returns
// false for pid <= 0 (zero from a legacy lock file, negative from a
// malformed payload). On unix this uses kill(pid, 0): the kernel
// returns EPERM if the process exists but isn't owned by us (which still
// counts as "alive") and ESRCH if it's gone.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	// EPERM means the process exists but we don't own it — still
	// "alive" for the purposes of stale-lock detection. ESRCH means
	// gone.
	return errors.Is(err, syscall.EPERM)
}
