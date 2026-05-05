package platform

// HostFreeBytes returns the bytes available to the *unprivileged* user at
// path on the host filesystem. Cheap (single syscall) so callers may
// invoke it on every health-check cadence without performance impact.
//
// On Linux/macOS path may be any directory mounted on the relevant
// filesystem — typically the user's HomeDir or ~/.locorum. On Windows
// it's the drive letter (e.g. `C:\`); a directory inside that drive
// works equivalently because GetDiskFreeSpaceEx looks up the parent
// volume.
//
// Implementation lives in the OS-specific files (freespace_unix.go,
// freespace_windows.go); this declaration is just the contract.
//
// Errors propagate verbatim — they are diagnostic only; the caller
// should fall back to "unknown" rather than aborting.
//
// The returned value can briefly exceed the actual filesystem capacity
// on copy-on-write filesystems (btrfs, zfs) reporting compressed-block
// estimates. Treat as "approximate, monotonic enough for thresholding".
func HostFreeBytes(path string) (int64, error) {
	return hostFreeBytes(path)
}
