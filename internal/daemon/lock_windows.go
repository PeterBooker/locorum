//go:build windows

package daemon

import (
	"os"

	"golang.org/x/sys/windows"
)

// platformLock takes an exclusive non-overlapping byte-range lock on f.
// LockFileEx with LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY is
// the Windows analogue of flock(LOCK_EX|LOCK_NB). The lock spans the
// maximum file size (0xFFFFFFFF on each end of the range) so concurrent
// growth of the file does not leave a writable gap.
func platformLock(f *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		0xFFFFFFFF, 0xFFFFFFFF,
		overlapped,
	)
}

// platformUnlock releases the byte-range lock. UnlockFileEx requires
// the same range we passed to LockFileEx.
func platformUnlock(f *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		0xFFFFFFFF, 0xFFFFFFFF,
		overlapped,
	)
}

// processAlive reports whether the given PID currently exists.
// OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION) succeeds for any
// running PID we have permission to inspect, including those owned by
// other users on the same logon session. A process in the "zombie"
// state (signaled but not yet reaped) returns its exit code via
// GetExitCodeProcess; we filter that out to avoid treating a freshly-
// dead daemon as "alive."
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const flags = windows.PROCESS_QUERY_LIMITED_INFORMATION
	h, err := windows.OpenProcess(flags, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// We have a handle but can't read state — be conservative
		// and treat as alive so we don't accidentally seize a
		// running daemon's lock.
		return true
	}
	const stillActive = 259 // STILL_ACTIVE
	return code == stillActive
}
