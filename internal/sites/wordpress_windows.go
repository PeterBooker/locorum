//go:build windows

package sites

// extraOpenFlags is a no-op on Windows. NTFS doesn't have Unix-style
// symlinks in the same sense; CreateFile semantics are configured at the
// Windows API layer rather than via O_NOFOLLOW. The tar-entry-type filter
// in extractTarGz already rejects symlinks, so the window for redirection
// is closed at the parser level.
func extraOpenFlags() int { return 0 }
